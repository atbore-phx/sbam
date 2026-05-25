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
