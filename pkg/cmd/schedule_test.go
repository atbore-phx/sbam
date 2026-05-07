package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"

	"github.com/stretchr/testify/assert"
)

type publishedMessage struct {
	topic   string
	payload []byte
}

type fakeClient struct {
	publishes chan publishedMessage
}

func newFakeClient() *fakeClient {
	return &fakeClient{publishes: make(chan publishedMessage, 32)}
}

func (f *fakeClient) Connect(ctx context.Context) error    { return nil }
func (f *fakeClient) Disconnect(ctx context.Context) error { return nil }
func (f *fakeClient) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	f.publishes <- publishedMessage{topic: topic, payload: payload}
	return nil
}
func (f *fakeClient) Subscribe(ctx context.Context, topic string, qos byte, handler mqtt.MessageHandler) error {
	return nil
}
func (f *fakeClient) IsConnected() bool { return true }

func drainPublishes(f *fakeClient) []publishedMessage {
	var out []publishedMessage
	for {
		select {
		case m := <-f.publishes:
			out = append(out, m)
		default:
			return out
		}
	}
}

// Storage failure should cause schedule to publish a DecisionSkip payload
// and return without attempting further work.
func TestSchedule_StorageFailurePublishesSkip(t *testing.T) {
	// Create a server and close it so the storage read fails deterministically.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	addr := ts.Listener.Addr().String()
	ts.Close()

	client := newFakeClient()

	// Call schedule with the closed server address so storage.Handler fails.
	schedule(
		"",      // apiKey
		"",      // url
		addr,    // fronius_ip -> closed server
		0.0,     // pw_consumption
		0.0,     // max_charge
		0.0,     // pw_batt_reserve
		"00:00", // start_hr
		"23:59", // end_hr
		"00:00", // batt_reserve_start_hr
		"23:59", // batt_reserve_end_hr
		0.0,     // pw_lwt
		0.0,     // pw_upt
		false,   // cache_forecast
		"",      // cache_file_prefix
		0,       // cache_time
		client,
		mqtt.Config{},
	)

	msgs := drainPublishes(client)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}

	var payload mqtt.StatePayload
	if err := json.Unmarshal(msgs[0].payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	assert.Equal(t, fronius.DecisionSkip.String(), payload.LastDecision)
	assert.Contains(t, payload.LastDecisionReason, "storage read failed")
}

// Fronius Modbus failure should cause schedule to publish a DecisionSkip payload
// even when storage read succeeds.
func TestSchedule_FroniusModbusFailurePublishesSkip(t *testing.T) {
	// Start a storage HTTP server that returns valid battery JSON so storage
	// read succeeds but Modbus connect (done later) will fail because the
	// fronius_ip contains a port (makes Modbus URL invalid/unreachable).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Body":{"Data":{"0":{"Controller":{"Enable":1,"DesignedCapacity":10000,"StateOfCharge_Relative":50}}}},"Head":{"Status":{"Code":0,"Reason":"","UserMessage":""},"Timestamp":""}}`))
	}))
	addr := ts.Listener.Addr().String()
	defer ts.Close()

	client := newFakeClient()

	// Call schedule with the HTTP server address as fronius_ip so storage
	// returns OK, but Modbus connection will fail during fronius.Handler.
	schedule(
		"",      // apiKey
		"",      // url
		addr,    // fronius_ip -> storage http server (host:port)
		0.0,     // pw_consumption
		3500.0,  // max_charge
		0.0,     // pw_batt_reserve
		"00:00", // start_hr
		"23:59", // end_hr
		"00:00", // batt_reserve_start_hr
		"23:59", // batt_reserve_end_hr
		0.0,     // pw_lwt
		0.0,     // pw_upt
		false,   // cache_forecast
		"",      // cache_file_prefix
		0,       // cache_time
		client,
		mqtt.Config{},
	)

	msgs := drainPublishes(client)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}

	var payload mqtt.StatePayload
	if err := json.Unmarshal(msgs[0].payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	assert.Equal(t, fronius.DecisionSkip.String(), payload.LastDecision)
	assert.Contains(t, payload.LastDecisionReason, "fronius handler failed")
}

// fakeFroniusClient implements the minimal Handler method used by
// schedule. It returns a non-skip decision so schedule should publish a
// non-skip payload.
type fakeFroniusClient struct{}

func (f *fakeFroniusClient) Handler(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, forecast_charge_enabled bool, fronius_port ...string) (int16, fronius.Decision, string, fronius.PowerState, error) {
	ps := fronius.PowerState{PvNet: 0.0, Batt: 0.0, Net: 0.0, BattReserveNet: 0.0}
	return int16(10), fronius.DecisionIdle, "fake-handler", ps, nil
}

// Verify that when Fronius operations succeed schedule publishes a
// non-skip payload. We inject a fake Fronius client to avoid starting a
// real Modbus server.
func TestSchedule_SuccessfulFroniusPublishesDecision(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Body":{"Data":{"0":{"Controller":{"Enable":1,"DesignedCapacity":10000,"StateOfCharge_Relative":50}}}},"Head":{"Status":{"Code":0,"Reason":"","UserMessage":""},"Timestamp":""}}`))
	}))
	defer ts.Close()

	addr := ts.Listener.Addr().String()

	client := newFakeClient()

	// Inject fake fronius client
	oldFactory := newFronius
	newFronius = func() froniusClient { return &fakeFroniusClient{} }
	defer func() { newFronius = oldFactory }()

	schedule(
		"",      // apiKey
		"",      // url
		addr,    // fronius_ip -> storage http server
		0.0,     // pw_consumption
		3500.0,  // max_charge
		0.0,     // pw_batt_reserve
		"00:00", // start_hr
		"23:59", // end_hr
		"00:00", // batt_reserve_start_hr
		"23:59", // batt_reserve_end_hr
		0.0,     // pw_lwt
		0.0,     // pw_upt
		false,   // cache_forecast
		"",      // cache_file_prefix
		0,       // cache_time
		client,
		mqtt.Config{},
	)

	msgs := drainPublishes(client)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}

	var payload mqtt.StatePayload
	if err := json.Unmarshal(msgs[0].payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	assert.NotEqual(t, fronius.DecisionSkip.String(), payload.LastDecision)
	if payload.ChargePct == nil {
		t.Fatalf("expected ChargePct to be present")
	}
	assert.Equal(t, int16(10), *payload.ChargePct)
}
