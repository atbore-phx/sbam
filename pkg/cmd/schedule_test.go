package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	u "sbam/src/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schedule is a small test-only compatibility wrapper that constructs a
// Runner with the provided args and runs a single Tick. It mirrors the
// historical production helper but lives in tests to avoid exporting a
// test-facing helper from production code.
func schedule(apiKey, url, fronius_ip string, pw_consumption, max_charge, pw_batt_reserve float64,
	start_hr, end_hr, batt_reserve_start_hr, batt_reserve_end_hr string, pw_lwt, pw_upt float64,
	cache_forecast bool, cache_file_prefix string, cache_time int32, mqttClient mqtt.Client, mqttCfg mqtt.Config) {
	runnerCfg := RunnerConfig{
		APIKey:             apiKey,
		URL:                url,
		FroniusIP:          fronius_ip,
		PWConsumption:      pw_consumption,
		MaxCharge:          max_charge,
		PWBattReserve:      pw_batt_reserve,
		StartHR:            start_hr,
		EndHR:              end_hr,
		BattReserveStartHR: batt_reserve_start_hr,
		BattReserveEndHR:   batt_reserve_end_hr,
		PWLWT:              pw_lwt,
		PWUPT:              pw_upt,
		CacheForecast:      cache_forecast,
		CacheFilePrefix:    cache_file_prefix,
		CacheTime:          cache_time,
		MQTT:               mqttCfg,
		Now:                time.Now,
	}

	runner := NewRunner(runnerCfg, mqttClient)
	if err := runner.Tick(context.Background(), time.Now()); err != nil {
		u.Log.Error(err)
	}
}

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
func (f *fakeClient) IsConnected() bool   { return true }
func (f *fakeClient) OnConnect(cb func()) {}

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

func (f *fakeFroniusClient) Handler(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, forecast_charge_enabled bool, fronius_port ...string) (int16, fronius.Decision, fronius.Reason, fronius.PowerState, error) {
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

type fakeStorageClient struct {
	capacityToCharge float64
	capacityMax      float64
	socPct           float64
	err              error
	calls            int
}

func (f *fakeStorageClient) Handler(fronius_ip string) (float64, float64, float64, error) {
	_ = fronius_ip
	f.calls++
	return f.capacityToCharge, f.capacityMax, f.socPct, f.err
}

type fakePowerClient struct {
	forecastWh float64
	retrieved  bool
	err        error
	calls      int
}

func (f *fakePowerClient) Handler(apiKey string, url string, cache_forecast bool, cache_file_prefix string, cache_time int32, forecastHorizon string, now time.Time) (float64, bool, error) {
	_ = apiKey
	_ = url
	_ = cache_forecast
	_ = cache_file_prefix
	_ = cache_time
	_ = forecastHorizon
	_ = now
	f.calls++
	return f.forecastWh, f.retrieved, f.err
}

type trackingFroniusClient struct {
	err                  error
	decision             fronius.Decision
	reason               fronius.Reason
	chargePct            int16
	powerState           fronius.PowerState
	calls                int
	lastForecastWh       float64
	lastForecastEnabled  bool
	lastReserveWindowArg bool
}

func (f *trackingFroniusClient) Handler(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, forecast_charge_enabled bool, fronius_port ...string) (int16, fronius.Decision, fronius.Reason, fronius.PowerState, error) {
	_ = pw_batt2charge
	_ = pw_batt_max
	_ = pw_consumption
	_ = max_charge
	_ = pw_batt_reserve
	_ = start_hr
	_ = end_hr
	_ = fronius_ip
	_ = pw_lwt
	_ = pw_upt
	_ = fronius_port
	f.calls++
	f.lastForecastWh = pw_forecast
	f.lastForecastEnabled = forecast_charge_enabled
	f.lastReserveWindowArg = batt_reserve_charge_enabled
	if f.err != nil {
		return 0, fronius.DecisionSkip, "", fronius.PowerState{}, f.err
	}
	return f.chargePct, f.decision, f.reason, f.powerState, nil
}

func TestSchedule_OutsideWindowPublishesBatteryOnlyAndSkipsPowerFronius(t *testing.T) {
	client := newFakeClient()

	fakeStorage := &fakeStorageClient{capacityToCharge: 100, capacityMax: 10000, socPct: 50}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	oldPowerFactory := newPower
	newPower = func() powerClient {
		t.Fatalf("power handler should not be called when outside charge window")
		return nil
	}
	defer func() { newPower = oldPowerFactory }()

	oldFroniusFactory := newFronius
	newFronius = func() froniusClient {
		t.Fatalf("fronius handler should not be called when outside charge window")
		return nil
	}
	defer func() { newFronius = oldFroniusFactory }()

	// start_hr > end_hr forces CheckTimeRange to false in this implementation.
	schedule(
		"",
		"",
		"127.0.0.1",
		1000.0,
		3500.0,
		100.0,
		"23:59",
		"00:00",
		"23:59",
		"00:00",
		0.0,
		0.0,
		false,
		"",
		0,
		client,
		mqtt.Config{},
	)

	assert.Equal(t, 1, fakeStorage.calls)

	msgs := drainPublishes(client)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}

	var payload mqtt.StatePayload
	require.NoError(t, json.Unmarshal(msgs[0].payload, &payload))
	assert.Equal(t, fronius.DecisionIdle.String(), payload.LastDecision)
	assert.Equal(t, "current time outside configured charging window", payload.LastDecisionReason)
	require.NotNil(t, payload.BatterySOCPct)
	require.NotNil(t, payload.BatteryCapacityWh)
	assert.Equal(t, 50.0, *payload.BatterySOCPct)
	assert.Equal(t, 10000.0, *payload.BatteryCapacityWh)
}

func TestSchedule_ForecastErrorDisablesForecastButStillCallsFronius(t *testing.T) {
	client := newFakeClient()

	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	fakePower := &fakePowerClient{forecastWh: 9999, retrieved: true, err: errors.New("forecast unavailable")}
	oldPowerFactory := newPower
	newPower = func() powerClient { return fakePower }
	defer func() { newPower = oldPowerFactory }()

	fakeFronius := &trackingFroniusClient{
		decision:   fronius.DecisionIdle,
		reason:     "fallback without forecast",
		chargePct:  int16(15),
		powerState: fronius.PowerState{Net: 123.0},
	}
	oldFroniusFactory := newFronius
	newFronius = func() froniusClient { return fakeFronius }
	defer func() { newFronius = oldFroniusFactory }()

	schedule(
		"key",
		"https://example.test/forecast",
		"127.0.0.1",
		1200.0,
		3500.0,
		100.0,
		"00:00",
		"23:59",
		"00:00",
		"23:59",
		0.0,
		0.0,
		false,
		"",
		0,
		client,
		mqtt.Config{},
	)

	assert.Equal(t, 1, fakeStorage.calls)
	assert.Equal(t, 1, fakePower.calls)
	assert.Equal(t, 1, fakeFronius.calls)
	assert.Equal(t, 0.0, fakeFronius.lastForecastWh)
	assert.False(t, fakeFronius.lastForecastEnabled)

	msgs := drainPublishes(client)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(msgs))
	}

	var payload mqtt.StatePayload
	require.NoError(t, json.Unmarshal(msgs[0].payload, &payload))
	assert.Equal(t, fronius.DecisionIdle.String(), payload.LastDecision)
	assert.Equal(t, "fallback without forecast", payload.LastDecisionReason)
	require.NotNil(t, payload.ForecastTodayWh)
	assert.Equal(t, 0.0, *payload.ForecastTodayWh)
}

func TestMakeBasePayloadSetsCommonFields(t *testing.T) {
	p := makeBasePayload("idle", "outside window", true, false)

	assert.Equal(t, "idle", p.LastDecision)
	assert.Equal(t, "outside window", p.LastDecisionReason)
	if assert.NotNil(t, p.ChargeWindowActive) {
		assert.True(t, *p.ChargeWindowActive)
	}
	if assert.NotNil(t, p.ReserveWindowActive) {
		assert.False(t, *p.ReserveWindowActive)
	}
	assert.False(t, p.Paused)
	assert.False(t, p.Timestamp.IsZero())
	assert.Nil(t, p.BatterySOCPct)
	assert.Nil(t, p.BatteryCapacityWh)
	assert.Nil(t, p.ForecastTodayWh)
	assert.Nil(t, p.PwNetWh)
	assert.Nil(t, p.ChargePct)
}

// ---------------------------------------------------------------------------
// checkScheduleschedule validation tests
// ---------------------------------------------------------------------------

func TestCheckScheduleschedule_EmptyFroniusIP(t *testing.T) {
	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fronius_ip")
}

func TestCheckScheduleschedule_EmptyApiKey(t *testing.T) {
	err := checkScheduleschedule("0 0 * * *", "", "http://url", "127.0.0.1", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apikey")
}

func TestCheckScheduleschedule_EmptyURL(t *testing.T) {
	err := checkScheduleschedule("0 0 * * *", "key", "", "127.0.0.1", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url")
}

func TestCheckScheduleschedule_EmptyCrontab(t *testing.T) {
	err := checkScheduleschedule("", "key", "http://url", "127.0.0.1", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "crontab")
}

func TestCheckScheduleschedule_NegativePWConsumption(t *testing.T) {
	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", -1, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pw_consumption")
}

func TestCheckScheduleschedule_NegativeMaxCharge(t *testing.T) {
	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", 1000, -1, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_charge")
}

func TestCheckScheduleschedule_InvalidWindow(t *testing.T) {
	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", 1000, 3500, 100, "bad", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start_hr")
}

func TestCheckScheduleschedule_BattReserveNotContained(t *testing.T) {
	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", 1000, 3500, 100, "00:00", "06:00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "batt_reserve_start_hr")
}

func TestCheckScheduleschedule_InvalidCacheTime(t *testing.T) {
	oldBRStart, oldBREnd := batt_reserve_start_hr, batt_reserve_end_hr
	batt_reserve_start_hr = "00:00"
	batt_reserve_end_hr = "23:59"
	defer func() { batt_reserve_start_hr = oldBRStart; batt_reserve_end_hr = oldBREnd }()

	oldCacheTime := s_cache_time
	s_cache_time = -1
	defer func() { s_cache_time = oldCacheTime }()

	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache_time")
}

func TestCheckScheduleschedule_CacheTimeTooLarge(t *testing.T) {
	oldBRStart, oldBREnd := batt_reserve_start_hr, batt_reserve_end_hr
	batt_reserve_start_hr = "00:00"
	batt_reserve_end_hr = "23:59"
	defer func() { batt_reserve_start_hr = oldBRStart; batt_reserve_end_hr = oldBREnd }()

	oldCacheTime := s_cache_time
	s_cache_time = 90000
	defer func() { s_cache_time = oldCacheTime }()

	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache_time")
}

func TestCheckScheduleschedule_InvalidForecastHorizon(t *testing.T) {
	oldBRStart, oldBREnd := batt_reserve_start_hr, batt_reserve_end_hr
	batt_reserve_start_hr = "00:00"
	batt_reserve_end_hr = "23:59"
	oldCacheTime := s_cache_time
	s_cache_time = 3600
	defer func() {
		batt_reserve_start_hr = oldBRStart
		batt_reserve_end_hr = oldBREnd
		s_cache_time = oldCacheTime
	}()

	oldForecastHorizon := forecast_horizon
	forecast_horizon = "invalid_horizon"
	defer func() { forecast_horizon = oldForecastHorizon }()

	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forecast_horizon")
}

func TestCheckScheduleschedule_InvalidConsumptionHorizon(t *testing.T) {
	oldBRStart, oldBREnd := batt_reserve_start_hr, batt_reserve_end_hr
	batt_reserve_start_hr = "00:00"
	batt_reserve_end_hr = "23:59"
	oldCacheTime := s_cache_time
	s_cache_time = 3600
	defer func() {
		batt_reserve_start_hr = oldBRStart
		batt_reserve_end_hr = oldBREnd
		s_cache_time = oldCacheTime
	}()

	oldConsumptionHorizon := consumption_horizon
	consumption_horizon = "invalid_horizon"
	defer func() { consumption_horizon = oldConsumptionHorizon }()

	err := checkScheduleschedule("0 0 * * *", "key", "http://url", "127.0.0.1", 1000, 3500, 100, "00:00", "23:59")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consumption_horizon")
}

// ---------------------------------------------------------------------------
// isWindowContainedIn tests
// ---------------------------------------------------------------------------

func TestIsWindowContainedIn_FalseWhenNotContained(t *testing.T) {
	contained, err := isWindowContainedIn("12:00", "18:00", "00:00", "06:00")
	require.NoError(t, err)
	assert.False(t, contained)
}

func TestIsWindowContainedIn_CrossMidnightContained(t *testing.T) {
	contained, err := isWindowContainedIn("22:00", "02:00", "20:00", "06:00")
	require.NoError(t, err)
	assert.True(t, contained)
}

func TestIsWindowContainedIn_InvalidInnerStart(t *testing.T) {
	_, err := isWindowContainedIn("bad", "06:00", "00:00", "06:00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid inner_start")
}

func TestIsWindowContainedIn_InvalidOuterStart(t *testing.T) {
	_, err := isWindowContainedIn("00:00", "06:00", "bad", "06:00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid outer_start")
}

// ---------------------------------------------------------------------------
// subscribeScheduleCommands tests
// ---------------------------------------------------------------------------

func TestSubscribeScheduleCommands_NilMQTTClientWhenEnabled(t *testing.T) {
	runner := newRunnerForTests(newFakeClient())
	err := subscribeScheduleCommands(context.Background(), nil, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, runner)
	require.NoError(t, err)
}

func TestSubscribeScheduleCommands_NilContext(t *testing.T) {
	client := &recordingMQTTClient{connected: true}
	runner := newRunnerForTests(client)
	err := subscribeScheduleCommands(nil, client, mqtt.Config{Enabled: true, TopicPrefix: "sbam"}, runner)
	require.NoError(t, err)
	assert.Equal(t, 1, client.subscribeCalls)
}

// ---------------------------------------------------------------------------
// crontabSchedule tests
// ---------------------------------------------------------------------------

func TestCrontabSchedule_NilRunner(t *testing.T) {
	err := crontabSchedule(context.Background(), nil, "0 0 * * *", false, "00:00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runner must not be nil")
}

func TestCrontabSchedule_NilContext(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	// Nil context should use background, cron will start and wait until context is done.
	// We cancel via a separate mechanism — the test validates no panic.
	errCh := make(chan error, 1)
	go func() {
		errCh <- crontabSchedule(nil, runner, "*/1 * * * *", false, "23:59")
	}()
	// Wait a bit for cron to start, then submit shutdown to unblock.
	time.Sleep(50 * time.Millisecond)
	runner.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown})
	select {
	case <-errCh:
		// Expected: the crontabSchedule returns when context is done
	case <-time.After(3 * time.Second):
		// crontabSchedule blocks on ctx.Done() so nil ctx = context.Background() which never cancels.
		// This is expected behavior — the goroutine will leak but the test validates no panic.
	}
}

func TestCrontabSchedule_InvalidEndHour(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	err := crontabSchedule(context.Background(), runner, "0 0 * * *", false, "bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid end_hr")
}

func TestCrontabSchedule_DefaultsDisabled(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- crontabSchedule(ctx, runner, "*/1 * * * *", false, "23:59")
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("crontabSchedule did not return after cancel")
	}
}

// ---------------------------------------------------------------------------
// finalizeRunnerMode tests
// ---------------------------------------------------------------------------

func TestFinalizeRunnerMode_NilStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := NewRunner(RunnerConfig{Now: time.Now}, nil)
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	err := finalizeRunnerMode(false, runner, runDone, nil)
	require.NoError(t, err)
}
