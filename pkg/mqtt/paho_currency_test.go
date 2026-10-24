package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// currencyTestConfig is an optimizer-enabled discovery config against the
// in-process broker at address, with no currency known yet.
func currencyTestConfig(address, clientID string) Config {
	return Config{
		Enabled:           true,
		Broker:            "tcp://" + address,
		ClientID:          clientID,
		TopicPrefix:       "sbam",
		HADiscovery:       true,
		HADiscoveryPrefix: "homeassistant",
		Optimizer:         true,
	}
}

// expectedCostUnit decodes the unit_of_measurement of an opt_expected_cost
// discovery payload ("" when absent).
func expectedCostUnit(t *testing.T, payload []byte) string {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(payload, &doc))
	unit, _ := doc["unit_of_measurement"].(string)
	return unit
}

// T-0115 (UX O-3): after SetCurrency("DKK"), the discovery republished when
// Home Assistant comes online carries DKK as the Expected Cost unit, even
// though the client was constructed without a currency.
func TestPahoSetCurrency_RepublishCarriesUnit_T0115(t *testing.T) {
	address := reserveTCPAddress(t)
	srv := newTestBroker(t, address, nil)
	defer srv.Close()

	p, err := NewPaho(currencyTestConfig(address, "currency-client"))
	require.NoError(t, err)
	// OnConnect callbacks run after the homeassistant/status subscription,
	// so this signals that the HA-online republish path is armed.
	ready := make(chan struct{})
	var readyOnce sync.Once
	p.OnConnect(func() { readyOnce.Do(func() { close(ready) }) })

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	require.NoError(t, p.Connect(ctx))
	defer disconnectClient(t, p)
	select {
	case <-ready:
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for the on-connect handler")
	}

	observer := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "currency-observer"})
	defer disconnectClient(t, observer)
	messages := make(chan receivedMessage, 16)
	costTopic := discoveryConfigTopic("homeassistant", "sensor", "opt_expected_cost")
	require.NoError(t, observer.Subscribe(ctx, costTopic, qosAtLeastOnce, func(topic string, payload []byte) {
		messages <- receivedMessage{topic: topic, payload: append([]byte(nil), payload...)}
	}))

	// The connect-time discovery (retained) has no unit: currency unknown.
	first := waitForMessage(t, messages)
	assert.Equal(t, costTopic, first.topic)
	assert.Empty(t, expectedCostUnit(t, first.payload), "no unit before the currency is known")

	p.SetCurrency("DKK")
	require.NoError(t, observer.Publish(ctx, haStatusTopic(), qosAtLeastOnce, false, []byte("online")))

	// Any DKK payload can only come from a republish after SetCurrency.
	deadline := time.After(testTimeout)
	for {
		select {
		case msg := <-messages:
			if unit := expectedCostUnit(t, msg.payload); unit != "" {
				assert.Equal(t, "DKK", unit)
				assert.Contains(t, string(msg.payload), `"device_class":"monetary"`)
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for discovery republish with the DKK unit")
		}
	}
}

// T-0115 edge case: SetCurrency does not publish by itself; it only changes
// what the next discovery publish carries.
func TestPahoSetCurrency_DoesNotPublish_T0115(t *testing.T) {
	p, err := NewPaho(currencyTestConfig("127.0.0.1:1", "currency-nopublish"))
	require.NoError(t, err)
	fake := newFakePahoClient()
	p.client = fake

	p.SetCurrency("DKK")
	assert.Empty(t, fake.publishCallsSnapshot(), "SetCurrency must not publish")
	assert.Equal(t, "DKK", p.discoveryConfig().Currency)

	// Clearing the currency falls back to no unit (failure/unknown case).
	p.SetCurrency("")
	entity := requireEntity(t, BuildDiscoveryWithDeviceID(p.discoveryConfig(), "1.0.0", "sbam_test"), "sensor", "opt_expected_cost")
	assert.Empty(t, expectedCostUnit(t, entity.Payload))
}

// T-0115 (-race): concurrent SetCurrency calls racing with discovery builds
// (as the runner and the paho callbacks do) are data-race free, and the last
// write wins.
func TestPahoSetCurrency_Concurrent_T0115(t *testing.T) {
	p, err := NewPaho(currencyTestConfig("127.0.0.1:1", "currency-race"))
	require.NoError(t, err)

	const writers, readers, iterations = 8, 4, 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				p.SetCurrency(fmt.Sprintf("C%02d", w))
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// No require/t.FailNow off the test goroutine: assert only.
				cfg := p.discoveryConfig()
				var unit string
				for _, e := range BuildDiscoveryWithDeviceID(cfg, "1.0.0", "sbam_test") {
					if e.Component == "sensor" && e.ObjectID == "opt_expected_cost" {
						var doc map[string]any
						if assert.NoError(t, json.Unmarshal(e.Payload, &doc)) {
							unit, _ = doc["unit_of_measurement"].(string)
						}
					}
				}
				assert.Equal(t, cfg.Currency, unit)
			}
		}()
	}
	wg.Wait()

	p.SetCurrency("EUR")
	assert.Equal(t, "EUR", p.discoveryConfig().Currency)
}
