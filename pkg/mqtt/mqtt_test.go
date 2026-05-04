package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	broker "github.com/mochi-mqtt/server/v2"
	brokerauth "github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTimeout = 5 * time.Second

type testBroker struct {
	server  *broker.Server
	address string
}

type receivedMessage struct {
	topic   string
	payload []byte
}

func TestNewReturnsNoopWhenDisabled(t *testing.T) {
	client, err := New(Config{})
	require.NoError(t, err)

	noop, ok := client.(*Noop)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	assert.NoError(t, noop.Connect(ctx))
	assert.NoError(t, noop.Publish(ctx, stateTopic(""), qosAtLeastOnce, true, []byte("payload")))
	assert.NoError(t, noop.Subscribe(ctx, stateTopic(""), qosAtLeastOnce, func(topic string, payload []byte) {}))
	assert.NoError(t, noop.Disconnect(ctx))
	assert.False(t, noop.IsConnected())
}

func TestNormalizePrefix(t *testing.T) {
	assert.Equal(t, defaultTopicPrefix, normalizePrefix("   "))
	assert.Equal(t, "custom", normalizePrefix(" /custom/ "))
	assert.Equal(t, defaultTopicPrefix+"/state", stateTopic(" /// "))
	assert.Equal(t, "custom/error", errorTopic(" /custom/ "))
	assert.Equal(t, "custom/availability", availabilityTopic(" /custom/ "))
}

func TestPublishStateRoundTrip(t *testing.T) {
	address := reserveTCPAddress(t)
	broker := newTestBroker(t, address, nil)
	defer broker.Close()

	subscriber := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "state-subscriber"})
	defer disconnectClient(t, subscriber)

	messages := make(chan receivedMessage, 1)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	require.NoError(t, subscriber.Subscribe(ctx, stateTopic(" "), qosAtLeastOnce, func(topic string, payload []byte) {
		messages <- receivedMessage{topic: topic, payload: payload}
	}))

	publisher := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "state-publisher"})
	defer disconnectClient(t, publisher)

	PublishState(context.Background(), publisher, " / ", StatePayload{
		BatterySOCPct:      42.5,
		BatteryCapacityWh:  10000,
		ForecastTodayWh:    5100,
		LastDecision:       "charge",
		LastDecisionReason: "forecast shortfall",
		Paused:             true,
	})

	message := waitForMessage(t, messages)
	assert.Equal(t, stateTopic(""), message.topic)

	var payload StatePayload
	require.NoError(t, json.Unmarshal(message.payload, &payload))
	assert.Equal(t, 42.5, payload.BatterySOCPct)
	assert.Equal(t, 10000.0, payload.BatteryCapacityWh)
	assert.Equal(t, 5100.0, payload.ForecastTodayWh)
	assert.Equal(t, "charge", payload.LastDecision)
	assert.Equal(t, "forecast shortfall", payload.LastDecisionReason)
	assert.True(t, payload.Paused)
	assert.False(t, payload.Timestamp.IsZero())
}

func TestPublishAvailabilityRetained(t *testing.T) {
	address := reserveTCPAddress(t)
	broker := newTestBroker(t, address, nil)
	defer broker.Close()

	publisher := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "availability-publisher"})
	defer disconnectClient(t, publisher)

	PublishAvailability(context.Background(), publisher, " /custom/ ", true)

	subscriber := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "availability-subscriber"})
	defer disconnectClient(t, subscriber)

	messages := make(chan receivedMessage, 1)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	require.NoError(t, subscriber.Subscribe(ctx, availabilityTopic("custom"), qosAtLeastOnce, func(topic string, payload []byte) {
		messages <- receivedMessage{topic: topic, payload: payload}
	}))

	message := waitForMessage(t, messages)
	assert.Equal(t, availabilityTopic("custom"), message.topic)
	assert.Equal(t, "online", string(message.payload))
}

func TestReconnectAfterBrokerRestart(t *testing.T) {
	address := reserveTCPAddress(t)
	broker := newTestBroker(t, address, nil)
	defer broker.Close()

	client := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "reconnect-client"})
	defer disconnectClient(t, client)

	broker.Crash()

	assert.Eventually(t, func() bool {
		if !client.IsConnected() {
			return true
		}

		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		_ = client.Publish(ctx, stateTopic(""), qosAtLeastOnce, false, []byte("probe"))
		return !client.IsConnected()
	}, testTimeout, 100*time.Millisecond)

	broker = newTestBroker(t, address, nil)

	assert.Eventually(t, func() bool {
		return client.IsConnected()
	}, 10*time.Second, 100*time.Millisecond)

	subscriber := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "reconnect-subscriber"})
	defer disconnectClient(t, subscriber)

	messages := make(chan receivedMessage, 1)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	require.NoError(t, subscriber.Subscribe(ctx, stateTopic(""), qosAtLeastOnce, func(topic string, payload []byte) {
		messages <- receivedMessage{topic: topic, payload: payload}
	}))

	PublishState(context.Background(), client, "", StatePayload{
		BatterySOCPct:     55,
		BatteryCapacityWh: 8000,
		ForecastTodayWh:   3200,
		LastDecision:      "idle",
	})

	message := waitForMessage(t, messages)
	assert.Equal(t, stateTopic(""), message.topic)
}

func TestConnectFailsWithBadCredentials(t *testing.T) {
	address := reserveTCPAddress(t)
	ledger := &brokerauth.Ledger{
		Users: brokerauth.Users{
			"good": {
				Username: "good",
				Password: "secret",
				ACL: brokerauth.Filters{
					"#": brokerauth.ReadWrite,
				},
			},
		},
	}

	broker := newTestBroker(t, address, ledger)
	defer broker.Close()

	client, err := New(Config{
		Enabled:  true,
		Broker:   "tcp://" + address,
		ClientID: "bad-credentials",
		Username: "good",
		Password: "wrong",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	err = client.Connect(ctx)
	assert.Error(t, err)
	assert.False(t, client.IsConnected())
}

func TestNewPahoFailsWithInvalidTLSCAFile(t *testing.T) {
	client, err := NewPaho(Config{
		Enabled:   true,
		Broker:    "tls://127.0.0.1:8883",
		TLSCAFile: "/does/not/exist.pem",
	})
	assert.Nil(t, client)
	assert.Error(t, err)
}

func newTestBroker(t *testing.T, address string, ledger *brokerauth.Ledger) *testBroker {
	t.Helper()

	server := broker.New(nil)
	server.Options.Capabilities.Compatibilities.ObscureNotAuthorized = true
	server.Options.Capabilities.Compatibilities.PassiveClientDisconnect = true
	server.Options.Capabilities.Compatibilities.NoInheritedPropertiesOnAck = true

	var err error
	if ledger == nil {
		err = server.AddHook(new(brokerauth.AllowHook), nil)
	} else {
		err = server.AddHook(new(brokerauth.Hook), &brokerauth.Options{Ledger: ledger})
	}
	require.NoError(t, err)

	listener := listeners.NewTCP(listeners.Config{ID: "mqtt-test", Address: address})
	require.NoError(t, server.AddListener(listener))

	go func() {
		_ = server.Serve()
	}()

	assert.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, testTimeout, 50*time.Millisecond)

	return &testBroker{server: server, address: address}
}

func (b *testBroker) Close() {
	if b == nil || b.server == nil {
		return
	}
	_ = b.server.Close()
	b.server = nil
}

func (b *testBroker) Crash() {
	if b == nil || b.server == nil {
		return
	}

	for _, client := range b.server.Clients.GetAll() {
		client.Stop(errors.New("test broker crash"))
	}

	b.Close()
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	return listener.Addr().String()
}

func mustConnectClient(t *testing.T, cfg Config) Client {
	t.Helper()

	client, err := New(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	require.NoError(t, client.Connect(ctx))

	return client
}

func disconnectClient(t *testing.T, client Client) {
	t.Helper()
	if client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	assert.NoError(t, client.Disconnect(ctx))
}

func waitForMessage(t *testing.T, messages <-chan receivedMessage) receivedMessage {
	t.Helper()

	select {
	case message := <-messages:
		return message
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for MQTT message")
		return receivedMessage{}
	}
}
