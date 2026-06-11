package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	pw "sbam/pkg/power"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeBatteryWriter struct {
	forceChargeCalls int
	setDefaultsCalls int
	lastFroniusIP    string
	lastTargetPct    int16
	forceChargeErr   error
	setDefaultsErr   error
}

// stubFroniusClient is a test helper that implements the froniusClient
// interface and records the number of Handler calls.
type stubFroniusClient struct{ calls int }

func (s *stubFroniusClient) Handler(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, forecast_charge_enabled bool, fronius_port ...string) (int16, fronius.Decision, fronius.Reason, fronius.PowerState, error) {
	s.calls++
	return 0, fronius.DecisionIdle, "stub", fronius.PowerState{}, nil
}

func (f *fakeBatteryWriter) ForceCharge(froniusIP string, targetPct int16) error {
	f.forceChargeCalls++
	f.lastFroniusIP = froniusIP
	f.lastTargetPct = targetPct
	return f.forceChargeErr
}

func (f *fakeBatteryWriter) SetDefaults(froniusIP string) error {
	f.setDefaultsCalls++
	f.lastFroniusIP = froniusIP
	return f.setDefaultsErr
}

func newRunnerForTests(client mqtt.Client) *Runner {
	fixedNow := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	return NewRunner(RunnerConfig{
		APIKey:             "key",
		URL:                "https://example.test/forecast",
		FroniusIP:          "127.0.0.1",
		PWConsumption:      1000,
		MaxCharge:          3500,
		PWBattReserve:      100,
		StartHR:            "00:00",
		EndHR:              "23:59",
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "23:59",
		PWLWT:              0,
		PWUPT:              0,
		CacheForecast:      false,
		CacheFilePrefix:    "cached_forecast",
		CacheTime:          7200,
		MQTT: mqtt.Config{
			Enabled:     true,
			TopicPrefix: "sbam",
		},
		Now: func() time.Time {
			return fixedNow
		},
	}, client)
}

func findPublishedBySuffix(msgs []publishedMessage, suffix string) (publishedMessage, bool) {
	for _, msg := range msgs {
		if strings.HasSuffix(msg.topic, suffix) {
			return msg, true
		}
	}
	return publishedMessage{}, false
}

func decodeAckPayload(t *testing.T, body []byte) mqtt.AckPayload {
	t.Helper()
	var ack mqtt.AckPayload
	require.NoError(t, json.Unmarshal(body, &ack))
	return ack
}

func decodeStatePayload(t *testing.T, body []byte) mqtt.StatePayload {
	t.Helper()
	var state mqtt.StatePayload
	require.NoError(t, json.Unmarshal(body, &state))
	return state
}

func TestRunner_HandleCommandPauseEmptyPayload(t *testing.T) {
	runner := newRunnerForTests(newFakeClient())

	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/pause", nil)
	require.True(t, accepted)

	select {
	case intent := <-runner.intents:
		assert.Equal(t, mqtt.IntentPause, intent.Kind)
		assert.Nil(t, intent.PauseUntil)
		assert.Equal(t, "sbam/cmd/pause", intent.CommandTopic)
	default:
		t.Fatal("expected pause intent to be enqueued")
	}
}

func TestRunner_HandleIntentPausePublishesStateAndAck(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentPause,
		CommandTopic: "sbam/cmd/pause",
	})

	msgs := drainPublishes(client)
	require.NotEmpty(t, msgs)

	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok, "expected state publish")
	state := decodeStatePayload(t, stateMsg.payload)
	assert.True(t, state.Paused)
	assert.Equal(t, "paused", state.LastDecision)

	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "pause", ack.Command)
	assert.Empty(t, ack.Error)
}

func TestRunner_ForceChargeCommandExecutesWriterAndPublishesAck(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":42}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, fakeStorage.calls)
	assert.Equal(t, 1, fakeWriter.forceChargeCalls)
	assert.Equal(t, "127.0.0.1", fakeWriter.lastFroniusIP)
	assert.Equal(t, int16(35), fakeWriter.lastTargetPct)

	msgs := drainPublishes(client)
	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok, "expected state publish")
	state := decodeStatePayload(t, stateMsg.payload)
	require.NotNil(t, state.ChargePct)
	assert.Equal(t, int16(35), *state.ChargePct)

	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
}

func TestRunner_ForceChargeCommandBelowCapKeepsRequestedPct(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":20}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, fakeStorage.calls)
	assert.Equal(t, 1, fakeWriter.forceChargeCalls)
	assert.Equal(t, int16(20), fakeWriter.lastTargetPct)
}

func TestRunner_ForceChargeCommandWithZeroMaxChargeResolvesToZero(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	runner.cfg.MaxCharge = 0

	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":80}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, fakeStorage.calls)
	assert.Equal(t, 1, fakeWriter.forceChargeCalls)
	assert.Equal(t, int16(0), fakeWriter.lastTargetPct)
}

func TestRunner_ForceChargeCommandAt100UsesMaxChargeCap(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":100}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, fakeStorage.calls)
	assert.Equal(t, 1, fakeWriter.forceChargeCalls)
	assert.Equal(t, "127.0.0.1", fakeWriter.lastFroniusIP)
	assert.Equal(t, int16(35), fakeWriter.lastTargetPct)

	msgs := drainPublishes(client)
	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok, "expected state publish")
	state := decodeStatePayload(t, stateMsg.payload)
	require.NotNil(t, state.ChargePct)
	assert.Equal(t, int16(35), *state.ChargePct)

	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
}

func TestRunner_ForceChargeCommandZeroSetsDefaults(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":0}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 0, fakeStorage.calls)
	assert.Equal(t, 1, fakeWriter.forceChargeCalls)
	assert.Equal(t, int16(0), fakeWriter.lastTargetPct)

	msgs := drainPublishes(client)
	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok, "expected state publish")
	state := decodeStatePayload(t, stateMsg.payload)
	require.NotNil(t, state.ChargePct)
	assert.Equal(t, int16(0), *state.ChargePct)

	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
}

func TestRunner_ForceChargeCommandIgnoreMaxChargeBypassesCap(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":100,"ignore_max_charge":true}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 0, fakeStorage.calls)
	assert.Equal(t, 1, fakeWriter.forceChargeCalls)
	assert.Equal(t, int16(100), fakeWriter.lastTargetPct)

	msgs := drainPublishes(client)
	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok, "expected state publish")
	state := decodeStatePayload(t, stateMsg.payload)
	require.NotNil(t, state.ChargePct)
	assert.Equal(t, int16(100), *state.ChargePct)

	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
}

func TestRunner_ForceChargeRejectsInvalidOverrideIntent(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:            mqtt.IntentForceCharge,
		TargetPct:       50,
		IgnoreMaxCharge: true,
		CommandTopic:    "sbam/cmd/force_charge",
	})

	assert.Equal(t, 0, fakeStorage.calls)
	assert.Equal(t, 0, fakeWriter.forceChargeCalls)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, "ignore_max_charge requires target_pct 100")
}

func TestRunner_ForceChargeCommandAt100RejectedWhenCapacityUnavailable(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	fakeStorage := &fakeStorageClient{err: errors.New("storage unavailable")}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":100}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, fakeStorage.calls)
	assert.Equal(t, 0, fakeWriter.forceChargeCalls)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, "unable to resolve force_charge target")
}

func TestRunner_SetDefaultsCommandExecutesWriterAndPublishesAck(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/set_defaults", []byte("{}"))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, fakeWriter.setDefaultsCalls)
	assert.Equal(t, "127.0.0.1", fakeWriter.lastFroniusIP)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "set_defaults", ack.Command)
}

func TestRunner_HandleCommandQueueFullPublishesRejectedAck(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	for i := 0; i < runnerIntentQueueSize; i++ {
		require.True(t, runner.Submit(mqtt.Intent{Kind: mqtt.IntentTick}))
	}

	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/resume", nil)
	require.False(t, accepted)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "resume", ack.Command)
	assert.Contains(t, ack.Error, errRunnerIntentQueueFull.Error())
}

func TestRunner_ForceChargeRejectedWhilePaused(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	runner := newRunnerForTests(client)
	runner.setPause(nil)

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentForceCharge,
		TargetPct:    55,
		CommandTopic: "sbam/cmd/force_charge",
	})

	assert.Equal(t, 0, fakeWriter.forceChargeCalls)
	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, errRunnerPaused.Error())
}

func TestRunner_HandleSetReserveRejected(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentSetReserve,
		CommandTopic: "sbam/cmd/set_reserve",
	})

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "set_reserve", ack.Command)
	assert.Contains(t, ack.Error, errSetReserveUnsupported.Error())
}

func TestRunner_HandleCommandParseErrorPublishesRejectedAck(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":"bad"}`))
	require.False(t, accepted)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, mqtt.ErrInvalidPayload.Error())
}

func TestRunner_HandleCommandQueueFullKeepsExistingItems(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	for i := 0; i < runnerIntentQueueSize; i++ {
		require.True(t, runner.Submit(mqtt.Intent{Kind: mqtt.IntentTick}))
	}

	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/trigger_now", nil)
	require.False(t, accepted)

	count := 0
	for {
		select {
		case <-runner.intents:
			count++
		default:
			assert.Equal(t, runnerIntentQueueSize, count)
			return
		}
	}
}

func TestRunner_HandleCommandUnknownTopicRejected(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/not_known", nil)
	require.False(t, accepted)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "not_known", ack.Command)
	assert.Equal(t, mqtt.ErrUnknownCommand.Error(), ack.Error)
}

func TestRunner_TickSkipsForecastAndChargeWhenPaused(t *testing.T) {
	client := newFakeClient()

	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	fakePower := &fakePowerClient{forecastWh: 9999, retrieved: true}
	oldPowerFactory := newPower
	newPower = func() powerClient { return fakePower }
	defer func() { newPower = oldPowerFactory }()

	stub := &stubFroniusClient{}
	oldFroniusFactory := newFronius
	newFronius = func() froniusClient { return stub }
	defer func() { newFronius = oldFroniusFactory }()

	runner := newRunnerForTests(client)
	runner.setPause(nil) // indefinite pause

	require.NoError(t, runner.Tick(context.Background(), runner.now()))

	// storage should be called, but power and fronius should not be called
	assert.Equal(t, 1, fakeStorage.calls)
	assert.Equal(t, 0, fakePower.calls)
	assert.Equal(t, 0, stub.calls)

	msgs := drainPublishes(client)
	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok)
	state := decodeStatePayload(t, stateMsg.payload)
	assert.True(t, state.Paused)
	assert.Equal(t, "paused", state.LastDecision)
}

func TestRunner_NewCommandPayloadUsesLocalWallClockWindow(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)

	tests := []struct {
		name         string
		now          time.Time
		wantInCharge bool
		wantReserve  bool
	}{
		{
			name:         "00:00 local inside window",
			now:          time.Date(2026, time.May, 16, 0, 0, 0, 0, loc),
			wantInCharge: true,
			wantReserve:  true,
		},
		{
			name:         "01:00 local inside window",
			now:          time.Date(2026, time.May, 16, 1, 0, 0, 0, loc),
			wantInCharge: true,
			wantReserve:  true,
		},
		{
			name:         "06:00 local inclusive end boundary",
			now:          time.Date(2026, time.May, 16, 6, 0, 0, 0, loc),
			wantInCharge: true,
			wantReserve:  true,
		},
		{
			name:         "07:00 local outside window",
			now:          time.Date(2026, time.May, 16, 7, 0, 0, 0, loc),
			wantInCharge: false,
			wantReserve:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewRunner(RunnerConfig{
				StartHR:            "00:00",
				EndHR:              "06:00",
				BattReserveStartHR: "00:00",
				BattReserveEndHR:   "06:00",
				Now: func() time.Time {
					return tt.now
				},
			}, nil)

			payload := runner.newCommandPayload("decision", "reason", runner.now())

			require.NotNil(t, payload.ChargeWindowActive)
			require.NotNil(t, payload.ReserveWindowActive)
			assert.Equal(t, tt.wantInCharge, *payload.ChargeWindowActive)
			assert.Equal(t, tt.wantReserve, *payload.ReserveWindowActive)
		})
	}
}

func TestRunner_NewCommandPayloadUsesLocalWallClockWindowCrossMidnight(t *testing.T) {
	loc := time.FixedZone("CEST", 2*60*60)

	tests := []struct {
		name         string
		now          time.Time
		wantInCharge bool
		wantReserve  bool
	}{
		{
			name:         "23:00 local inside cross-midnight windows",
			now:          time.Date(2026, time.May, 16, 23, 0, 0, 0, loc),
			wantInCharge: true,
			wantReserve:  true,
		},
		{
			name:         "02:00 local inside cross-midnight windows",
			now:          time.Date(2026, time.May, 16, 2, 0, 0, 0, loc),
			wantInCharge: true,
			wantReserve:  true,
		},
		{
			name:         "12:00 local outside cross-midnight windows",
			now:          time.Date(2026, time.May, 16, 12, 0, 0, 0, loc),
			wantInCharge: false,
			wantReserve:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewRunner(RunnerConfig{
				StartHR:            "22:00",
				EndHR:              "06:00",
				BattReserveStartHR: "23:00",
				BattReserveEndHR:   "05:00",
				Now: func() time.Time {
					return tt.now
				},
			}, nil)

			payload := runner.newCommandPayload("decision", "reason", runner.now())

			require.NotNil(t, payload.ChargeWindowActive)
			require.NotNil(t, payload.ReserveWindowActive)
			assert.Equal(t, tt.wantInCharge, *payload.ChargeWindowActive)
			assert.Equal(t, tt.wantReserve, *payload.ReserveWindowActive)
		})
	}
}

func TestIsInCooldown_SameDayWindow(t *testing.T) {
	loc := time.UTC
	tests := []struct {
		name         string
		now          time.Time
		startHR      string
		endHR        string
		wantCooldown bool
		errSubstr    string
	}{
		{
			name:         "4 min before end — in cooldown",
			now:          time.Date(2026, time.June, 11, 6, 51, 0, 0, loc),
			startHR:      "00:00",
			endHR:        "06:55",
			wantCooldown: true,
		},
		{
			name:         "6 min before end — not in cooldown",
			now:          time.Date(2026, time.June, 11, 6, 49, 0, 0, loc),
			startHR:      "00:00",
			endHR:        "06:55",
			wantCooldown: false,
		},
		{
			name:         "at cooldown boundary (exactly 5 min before end)",
			now:          time.Date(2026, time.June, 11, 6, 50, 0, 0, loc),
			startHR:      "00:00",
			endHR:        "06:55",
			wantCooldown: true,
		},
		{
			name:         "at end boundary — not in cooldown",
			now:          time.Date(2026, time.June, 11, 6, 55, 0, 0, loc),
			startHR:      "00:00",
			endHR:        "06:55",
			wantCooldown: false,
		},
		{
			name:      "invalid endHR returns error",
			now:       time.Date(2026, time.June, 11, 6, 50, 0, 0, loc),
			startHR:   "00:00",
			endHR:     "not-a-time",
			errSubstr: "end_hr",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			inCooldown, err := isInCooldown(tt.now, tt.startHR, tt.endHR, chargeCooldownMinutes)
			if tt.errSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCooldown, inCooldown)
		})
	}
}

func TestIsInCooldown_CrossMidnightWindow(t *testing.T) {
	loc := time.UTC
	tests := []struct {
		name         string
		now          time.Time
		startHR      string
		endHR        string
		wantCooldown bool
	}{
		{
			name:         "before midnight far from end — not in cooldown",
			now:          time.Date(2026, time.June, 11, 23, 0, 0, 0, loc),
			startHR:      "22:00",
			endHR:        "06:00",
			wantCooldown: false,
		},
		{
			name:         "after midnight 2 min to end — in cooldown",
			now:          time.Date(2026, time.June, 12, 5, 58, 0, 0, loc),
			startHR:      "22:00",
			endHR:        "06:00",
			wantCooldown: true,
		},
		{
			name:         "after midnight 7 min to end — not in cooldown",
			now:          time.Date(2026, time.June, 12, 5, 53, 0, 0, loc),
			startHR:      "22:00",
			endHR:        "06:00",
			wantCooldown: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			inCooldown, err := isInCooldown(tt.now, tt.startHR, tt.endHR, chargeCooldownMinutes)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCooldown, inCooldown)
		})
	}
}

func TestRunner_TickCooldownSuppressesCharge(t *testing.T) {
	cooldownNow := time.Date(2026, time.June, 11, 6, 51, 0, 0, time.UTC)

	originalNewFronius := newFronius
	originalNewStorage := newStorage
	originalNewPower := newPower
	defer func() {
		newFronius = originalNewFronius
		newStorage = originalNewStorage
		newPower = originalNewPower
	}()

	newFronius = func() froniusClient {
		return &stubFroniusClient{}
	}

	newStorage = func() storageClient {
		return &fakeStorageClient{
			capacityToCharge: 5000,
			capacityMax:      13824,
			socPct:           36.0,
		}
	}

	powerCalled := false
	newPower = func() powerClient {
		powerCalled = true
		return &fakePowerClient{forecastWh: 15000, retrieved: true}
	}

	cfg := RunnerConfig{
		APIKey:             "key",
		URL:                "https://example.test/forecast",
		FroniusIP:          "127.0.0.1",
		PWConsumption:      1000,
		MaxCharge:          3500,
		PWBattReserve:      5000,
		StartHR:            "00:00",
		EndHR:              "06:55",
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "06:55",
		PWLWT:              0,
		PWUPT:              0,
		CacheForecast:      false,
		CacheTime:          7200,
		MQTT: mqtt.Config{
			Enabled:     true,
			TopicPrefix: "sbam",
		},
		Now: func() time.Time {
			return cooldownNow
		},
	}

	client := newFakeClient()
	runner := NewRunner(cfg, client)
	runner.writer = &fakeBatteryWriter{}

	err := runner.Tick(context.Background(), cooldownNow)
	require.NoError(t, err)

	assert.False(t, powerCalled, "power forecast should not be fetched during cooldown")

	msgs := drainPublishes(client)
	stateMsg, found := findPublishedBySuffix(msgs, "/state")
	require.True(t, found, "expected a state payload during cooldown")

	state := decodeStatePayload(t, stateMsg.payload)
	assert.Equal(t, "cooldown", state.LastDecision)
	assert.Contains(t, state.LastDecisionReason, "suppressed")
	assert.NotNil(t, state.BatterySOCPct)
	assert.NotNil(t, state.BatteryCapacityWh)
}

func TestRunner_TickOutsideCooldownProceedsNormally(t *testing.T) {
	normalNow := time.Date(2026, time.June, 11, 6, 49, 0, 0, time.UTC)

	originalNewFronius := newFronius
	originalNewStorage := newStorage
	originalNewPower := newPower
	defer func() {
		newFronius = originalNewFronius
		newStorage = originalNewStorage
		newPower = originalNewPower
	}()

	froniusCalls := 0
	newFronius = func() froniusClient {
		froniusCalls++
		return &stubFroniusClient{}
	}

	newStorage = func() storageClient {
		return &fakeStorageClient{
			capacityToCharge: 5000,
			capacityMax:      13824,
			socPct:           36.0,
		}
	}

	powerCalled := false
	newPower = func() powerClient {
		powerCalled = true
		return &fakePowerClient{forecastWh: 15000, retrieved: true}
	}

	cfg := RunnerConfig{
		APIKey:             "key",
		URL:                "https://example.test/forecast",
		FroniusIP:          "127.0.0.1",
		PWConsumption:      1000,
		MaxCharge:          3500,
		PWBattReserve:      5000,
		StartHR:            "00:00",
		EndHR:              "06:55",
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "06:55",
		PWLWT:              0,
		PWUPT:              0,
		CacheForecast:      false,
		CacheTime:          7200,
		MQTT: mqtt.Config{
			Enabled:     true,
			TopicPrefix: "sbam",
		},
		Now: func() time.Time {
			return normalNow
		},
	}

	client := newFakeClient()
	runner := NewRunner(cfg, client)
	runner.writer = &fakeBatteryWriter{}

	err := runner.Tick(context.Background(), normalNow)
	require.NoError(t, err)

	assert.True(t, powerCalled, "power forecast should be fetched outside cooldown")
	assert.Equal(t, 1, froniusCalls, "fronius handler should be called outside cooldown")
}

func TestRunner_HandleIntentShutdown(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind: mqtt.IntentShutdown,
	})

	msgs := drainPublishes(client)
	assert.Empty(t, msgs, "shutdown intent should not publish anything")
}

func TestRunner_HandleIntentResumeClearsPauseAndPublishes(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	runner.setPause(nil) // first pause

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentResume,
		CommandTopic: "sbam/cmd/resume",
	})

	msgs := drainPublishes(client)

	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok, "expected state publish")
	state := decodeStatePayload(t, stateMsg.payload)
	assert.False(t, state.Paused)
	assert.Equal(t, "resume", state.LastDecision)

	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "resume", ack.Command)
}

func TestRunner_HandleIntentUnknownKind(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         "unknown_kind",
		CommandTopic: "sbam/cmd/unknown",
	})

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "unknown", ack.Command)
	assert.Contains(t, ack.Error, errUnsupportedIntent.Error())
}

func TestRunner_HandleForceChargeNegativeTargetPct(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentForceCharge,
		TargetPct:    -1,
		CommandTopic: "sbam/cmd/force_charge",
	})

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, "target_pct must be between 0 and 100")
}

func TestRunner_HandleForceChargeWriterError(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{forceChargeErr: errors.New("modbus write failed")}
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}

	oldWriterFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldWriterFactory }()

	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(client)
	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentForceCharge,
		TargetPct:    50,
		CommandTopic: "sbam/cmd/force_charge",
	})

	assert.Equal(t, 1, fakeWriter.forceChargeCalls)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, "modbus write failed")
}

func TestRunner_HandleForceChargePaused(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	runner.setPause(nil) // indefinite pause

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentForceCharge,
		TargetPct:    50,
		CommandTopic: "sbam/cmd/force_charge",
	})

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
	assert.Contains(t, ack.Error, errRunnerPaused.Error())
}

func TestRunner_HandleSetDefaultsPaused(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	runner.setPause(nil) // indefinite pause

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentSetDefaults,
		CommandTopic: "sbam/cmd/set_defaults",
	})

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "set_defaults", ack.Command)
	assert.Contains(t, ack.Error, errRunnerPaused.Error())
}

func TestRunner_HandleSetDefaultsWriterError(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{setDefaultsErr: errors.New("modbus write failed")}

	oldWriterFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldWriterFactory }()

	runner := newRunnerForTests(client)
	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentSetDefaults,
		CommandTopic: "sbam/cmd/set_defaults",
	})

	assert.Equal(t, 1, fakeWriter.setDefaultsCalls)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "set_defaults", ack.Command)
	assert.Contains(t, ack.Error, "modbus write failed")
}

func TestRunner_ResolveForceChargeNegativeMaxCharge(t *testing.T) {
	runner := newRunnerForTests(newFakeClient())
	runner.cfg.MaxCharge = -1

	_, err := runner.resolveForceChargeTargetPct(50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid max_charge")
}

func TestRunner_ResolveForceChargeZeroMaxCapacity(t *testing.T) {
	fakeStorage := &fakeStorageClient{capacityToCharge: 0, capacityMax: 0, socPct: 0}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(newFakeClient())
	_, err := runner.resolveForceChargeTargetPct(50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid battery capacity")
}

func TestRunner_ResolveForceChargeNegativeCapacity(t *testing.T) {
	fakeStorage := &fakeStorageClient{capacityToCharge: 0, capacityMax: -1, socPct: 0}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(newFakeClient())
	_, err := runner.resolveForceChargeTargetPct(50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid battery capacity")
}

func TestRunner_ResolveForceChargeStorageError(t *testing.T) {
	fakeStorage := &fakeStorageClient{err: errors.New("storage down")}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	runner := newRunnerForTests(newFakeClient())
	_, err := runner.resolveForceChargeTargetPct(50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to resolve force_charge target")
}

func TestRunner_PublishErrorNilErrorSkips(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	runner.publishError(context.Background(), "source", nil)

	msgs := drainPublishes(client)
	assert.Empty(t, msgs, "nil error should not publish anything")
}

func TestRunner_PublishErrorMQTTDisabledSkips(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	runner.cfg.MQTT.Enabled = false
	runner.publishError(context.Background(), "source", errors.New("some error"))

	msgs := drainPublishes(client)
	assert.Empty(t, msgs, "MQTT disabled should not publish error")
}

func TestRunner_PublishErrorNilContext(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	// Should not panic with nil context
	runner.publishError(nil, "source", errors.New("some error"))

	msgs := drainPublishes(client)
	errMsg, ok := findPublishedBySuffix(msgs, "/error")
	require.True(t, ok, "expected error publish")
	assert.Contains(t, string(errMsg.payload), "some error")
}

func TestRunner_PublishIntentAckEmptyTopicSkips(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	intent := mqtt.Intent{
		Kind:         mqtt.IntentResume,
		CommandTopic: "",
	}
	runner.publishIntentAck(context.Background(), intent, nil)

	msgs := drainPublishes(client)
	assert.Empty(t, msgs, "empty topic should skip ack publish")
}

func TestRunner_PublishIntentAckNilContext(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	// Should not panic with nil context
	runner.publishIntentAck(nil, mqtt.Intent{
		Kind:         mqtt.IntentResume,
		CommandTopic: "sbam/cmd/resume",
	}, nil)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish with nil context fallback")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
}

func TestRunner_SetPauseTimed(t *testing.T) {
	runner := newRunnerForTests(newFakeClient())
	future := runner.now().Add(1 * time.Hour)
	runner.setPause(&future)

	paused, until := runner.pauseStateAt(runner.now())
	assert.True(t, paused)
	require.NotNil(t, until)
	assert.True(t, until.After(runner.now()))
}

func TestRunner_PauseStateAtExpired(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	past := runner.now().Add(-1 * time.Hour)
	runner.setPause(&past)

	paused, until := runner.pauseStateAt(runner.now())
	assert.False(t, paused, "expired pause should report not paused")
	assert.Nil(t, until)
}

func TestRunner_NewCommandPayloadInvalidWindows(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR:            "bad",
		EndHR:              "also_bad",
		BattReserveStartHR: "bad",
		BattReserveEndHR:   "also_bad",
		Now:                time.Now,
	}, nil)

	// Should not panic; should handle errors gracefully.
	payload := runner.newCommandPayload("decision", "reason", runner.now())
	require.NotNil(t, payload.ChargeWindowActive)
	require.NotNil(t, payload.ReserveWindowActive)
	assert.False(t, *payload.ChargeWindowActive, "invalid window should default to false")
	assert.False(t, *payload.ReserveWindowActive, "invalid reserve window should default to false")
}

func TestRunner_RunNilContext(t *testing.T) {
	runner := NewRunner(RunnerConfig{Now: time.Now}, newFakeClient())

	// Submit a shutdown intent so Run terminates immediately.
	require.True(t, runner.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown}))
	err := runner.Run(nil)
	require.NoError(t, err)
}

func TestRunner_TickUsesInternalClockWhenNowIsZero(t *testing.T) {
	client := newFakeClient()

	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	oldPowerFactory := newPower
	newPower = func() powerClient { return &fakePowerClient{forecastWh: 0, retrieved: false} }
	defer func() { newPower = oldPowerFactory }()

	stub := &stubFroniusClient{}
	oldFroniusFactory := newFronius
	newFronius = func() froniusClient { return stub }
	defer func() { newFronius = oldFroniusFactory }()

	runner := newRunnerForTests(client)

	// Passing zero time should cause Tick to use runner.now() internally.
	err := runner.Tick(context.Background(), time.Time{})
	assert.NoError(t, err)
	assert.Equal(t, 1, fakeStorage.calls)
}

func TestRunner_SubmitQueueFullPublishesError(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)

	for i := 0; i < runnerIntentQueueSize; i++ {
		require.True(t, runner.Submit(mqtt.Intent{Kind: mqtt.IntentTick}))
	}

	ok := runner.Submit(mqtt.Intent{Kind: mqtt.IntentTick})
	assert.False(t, ok)

	msgs := drainPublishes(client)
	errMsg, found := findPublishedBySuffix(msgs, "/error")
	require.True(t, found, "expected error publish on queue full submit")
	assert.Contains(t, string(errMsg.payload), errRunnerIntentQueueFull.Error())
}

func TestRunner_PausePublishesTimedUntil(t *testing.T) {
	client := newFakeClient()
	runner := newRunnerForTests(client)
	future := runner.now().Add(2 * time.Hour)

	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentPause,
		PauseUntil:   &future,
		CommandTopic: "sbam/cmd/pause",
	})

	msgs := drainPublishes(client)
	stateMsg, ok := findPublishedBySuffix(msgs, "/state")
	require.True(t, ok)
	state := decodeStatePayload(t, stateMsg.payload)
	assert.True(t, state.Paused)
	require.NotNil(t, state.NextRun)

	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok)
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
}

func TestRunner_TickReserveWindowError(t *testing.T) {
	client := newFakeClient()
	runner := NewRunner(RunnerConfig{
		StartHR:            "00:00",
		EndHR:              "23:59",
		BattReserveStartHR: "bad",
		BattReserveEndHR:   "also_bad",
		Now: func() time.Time {
			return time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
		},
		MQTT: mqtt.Config{Enabled: true, TopicPrefix: "sbam"},
	}, client)

	err := runner.Tick(context.Background(), runner.now())
	require.Error(t, err)

	msgs := drainPublishes(client)
	errMsg, found := findPublishedBySuffix(msgs, "/error")
	require.True(t, found, "expected error publish for invalid reserve window")
	assert.Contains(t, string(errMsg.payload), "invalid")
}

func TestCheckTimeRangeAt_BoundariesAndErrors(t *testing.T) {
	loc := time.FixedZone("UTC+1", 1*60*60)

	tests := []struct {
		name      string
		now       time.Time
		startHR   string
		endHR     string
		want      bool
		errSubstr string
	}{
		{
			name:    "same-day start boundary inclusive",
			now:     time.Date(2026, time.May, 16, 0, 0, 0, 0, loc),
			startHR: "00:00",
			endHR:   "06:00",
			want:    true,
		},
		{
			name:    "same-day end boundary inclusive",
			now:     time.Date(2026, time.May, 16, 6, 0, 0, 0, loc),
			startHR: "00:00",
			endHR:   "06:00",
			want:    true,
		},
		{
			name:    "same-day after end inactive",
			now:     time.Date(2026, time.May, 16, 6, 1, 0, 0, loc),
			startHR: "00:00",
			endHR:   "06:00",
			want:    false,
		},
		{
			name:    "cross-midnight before midnight active",
			now:     time.Date(2026, time.May, 16, 23, 0, 0, 0, loc),
			startHR: "22:00",
			endHR:   "06:00",
			want:    true,
		},
		{
			name:    "cross-midnight after midnight active",
			now:     time.Date(2026, time.May, 16, 2, 0, 0, 0, loc),
			startHR: "22:00",
			endHR:   "06:00",
			want:    true,
		},
		{
			name:    "cross-midnight daytime inactive",
			now:     time.Date(2026, time.May, 16, 12, 0, 0, 0, loc),
			startHR: "22:00",
			endHR:   "06:00",
			want:    false,
		},
		{
			name:    "cross-midnight just before start inactive",
			now:     time.Date(2026, time.May, 16, 21, 59, 0, 0, loc),
			startHR: "22:00",
			endHR:   "06:00",
			want:    false,
		},
		{
			name:    "cross-midnight exact start active",
			now:     time.Date(2026, time.May, 16, 22, 0, 0, 0, loc),
			startHR: "22:00",
			endHR:   "06:00",
			want:    true,
		},
		{
			name:    "cross-midnight exact end active",
			now:     time.Date(2026, time.May, 16, 6, 0, 0, 0, loc),
			startHR: "22:00",
			endHR:   "06:00",
			want:    true,
		},
		{
			name:      "invalid start format",
			now:       time.Date(2026, time.May, 16, 0, 0, 0, 0, loc),
			startHR:   "bad",
			endHR:     "06:00",
			errSubstr: "invalid start time",
		},
		{
			name:      "invalid end format",
			now:       time.Date(2026, time.May, 16, 0, 0, 0, 0, loc),
			startHR:   "00:00",
			endHR:     "bad",
			errSubstr: "invalid end time",
		},
		{
			name:      "equal start and end rejected",
			now:       time.Date(2026, time.May, 16, 0, 0, 0, 0, loc),
			startHR:   "06:00",
			endHR:     "06:00",
			errSubstr: "must not be equal",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			inRange, err := checkTimeRangeAt(tt.now, tt.startHR, tt.endHR)
			if tt.errSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, inRange)
		})
	}
}

// --- resolveActiveWindow tests ---

func TestRunner_ResolveActiveWindow_WindowsNoneActive(t *testing.T) {
	loc := time.FixedZone("UTC+1", 1*60*60)
	runner := NewRunner(RunnerConfig{
		StartHR: "00:00",
		EndHR:   "23:59",
		Windows: []pw.Window{
			{Name: "morning", Start: "06:00", End: "08:00", MaxCharge: 3500},
			{Name: "evening", Start: "22:00", End: "23:00", MaxCharge: 3000},
		},
		MaxCharge:          5000,
		ForecastHorizon:    "default",
		ConsumptionHorizon: "full_day",
		Now: func() time.Time {
			return time.Date(2026, time.June, 11, 12, 0, 0, 0, loc)
		},
	}, nil)

	inWindow, maxCharge, fh, ch, windowName := runner.resolveActiveWindow(runner.now())
	assert.False(t, inWindow)
	assert.Equal(t, 5000.0, maxCharge)
	assert.Equal(t, "default", fh)
	assert.Equal(t, "full_day", ch)
	assert.Empty(t, windowName)
}

func TestRunner_ResolveActiveWindow_WindowsWithCustomHorizons(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR: "00:00",
		EndHR:   "23:59",
		Windows: []pw.Window{
			{Name: "sunrise", Start: "06:00", End: "14:00", MaxCharge: 3000,
				ForecastHorizon: "today", ConsumptionHorizon: "remaining_today"},
		},
		MaxCharge:          5000,
		ForecastHorizon:    "default",
		ConsumptionHorizon: "full_day",
		Now: func() time.Time {
			return time.Date(2026, time.June, 11, 10, 0, 0, 0, time.UTC)
		},
	}, nil)

	inWindow, maxCharge, fh, ch, windowName := runner.resolveActiveWindow(runner.now())
	assert.True(t, inWindow)
	assert.Equal(t, 3000.0, maxCharge)
	assert.Equal(t, "today", fh)
	assert.Equal(t, "remaining_today", ch)
	assert.Equal(t, "sunrise", windowName)
}

func TestRunner_ResolveActiveWindow_LegacyError(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR: "bad",
		EndHR:   "also_bad",
		Now:     time.Now,
	}, nil)

	inWindow, _, _, _, windowName := runner.resolveActiveWindow(runner.now())
	assert.False(t, inWindow)
	assert.Empty(t, windowName)
}

func TestRunner_ResolveActiveWindow_LegacyOutsideWindow(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR: "06:00",
		EndHR:   "08:00",
		Now: func() time.Time {
			return time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
		},
	}, nil)

	inWindow, _, _, _, windowName := runner.resolveActiveWindow(runner.now())
	assert.False(t, inWindow)
	assert.Empty(t, windowName)
}

func TestRunner_ResolveActiveWindow_WindowWithEmptyHorizonsFallsBack(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR: "00:00",
		EndHR:   "23:59",
		Windows: []pw.Window{
			{Name: "plain", Start: "06:00", End: "14:00", MaxCharge: 3000},
		},
		MaxCharge:          5000,
		ForecastHorizon:    "tomorrow",
		ConsumptionHorizon: "remaining_today",
		Now: func() time.Time {
			return time.Date(2026, time.June, 11, 10, 0, 0, 0, time.UTC)
		},
	}, nil)

	inWindow, maxCharge, fh, ch, windowName := runner.resolveActiveWindow(runner.now())
	assert.True(t, inWindow)
	assert.Equal(t, 3000.0, maxCharge)
	assert.Equal(t, "tomorrow", fh)
	assert.Equal(t, "remaining_today", ch)
	assert.Equal(t, "plain", windowName)
}

func TestRunner_ResolveActiveWindow_LegacyInWindow(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR:            "06:00",
		EndHR:              "23:59",
		MaxCharge:          3500,
		ForecastHorizon:    "next_solar_day",
		ConsumptionHorizon: "full_day",
		Now: func() time.Time {
			return time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC)
		},
	}, nil)

	inWindow, maxCharge, fh, ch, windowName := runner.resolveActiveWindow(runner.now())
	assert.True(t, inWindow)
	assert.Equal(t, 3500.0, maxCharge)
	assert.Equal(t, "next_solar_day", fh)
	assert.Equal(t, "full_day", ch)
	assert.Equal(t, "legacy", windowName)
}

func TestRunner_ResolveActiveWindow_CrossMidnightWindowActive(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR: "00:00",
		EndHR:   "23:59",
		Windows: []pw.Window{
			{Name: "night", Start: "22:00", End: "06:00", MaxCharge: 2500,
				ForecastHorizon: "remaining_today"},
		},
		MaxCharge:          5000,
		ForecastHorizon:    "default",
		ConsumptionHorizon: "full_day",
		Now: func() time.Time {
			return time.Date(2026, time.June, 11, 2, 0, 0, 0, time.UTC)
		},
	}, nil)

	inWindow, maxCharge, fh, ch, windowName := runner.resolveActiveWindow(runner.now())
	assert.True(t, inWindow)
	assert.Equal(t, 2500.0, maxCharge)
	assert.Equal(t, "remaining_today", fh)
	assert.Equal(t, "full_day", ch)
	assert.Equal(t, "night", windowName)
}

func TestNewRunner_DefaultNowFunc(t *testing.T) {
	runner := NewRunner(RunnerConfig{}, nil)
	assert.NotNil(t, runner.cfg.Now)
	now := runner.now()
	assert.False(t, now.IsZero(), "default Now func should return a non-zero time")
}

func TestFinalizeRunnerMode_MQTTEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := NewRunner(RunnerConfig{Now: time.Now}, nil)
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	// Submit shutdown so Run exits cleanly.
	runner.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown})

	err := finalizeRunnerMode(true, runner, runDone, cancel)
	require.NoError(t, err)
}

func TestRunner_HandleIntentTriggerNow(t *testing.T) {
	client := newFakeClient()
	fakeStorage := &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}
	oldStorageFactory := newStorage
	newStorage = func() storageClient { return fakeStorage }
	defer func() { newStorage = oldStorageFactory }()

	oldPowerFactory := newPower
	newPower = func() powerClient { return &fakePowerClient{forecastWh: 0, retrieved: false} }
	defer func() { newPower = oldPowerFactory }()

	stub := &stubFroniusClient{}
	oldFroniusFactory := newFronius
	newFronius = func() froniusClient { return stub }
	defer func() { newFronius = oldFroniusFactory }()

	runner := newRunnerForTests(client)
	runner.handleIntent(context.Background(), mqtt.Intent{
		Kind:         mqtt.IntentTriggerNow,
		CommandTopic: "sbam/cmd/trigger_now",
	})

	assert.Equal(t, 1, stub.calls)
	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish for trigger_now")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
}
