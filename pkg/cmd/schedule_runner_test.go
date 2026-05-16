package cmd

import (
	"context"
	"encoding/json"
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

func (s *stubFroniusClient) Handler(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, forecast_charge_enabled bool, fronius_port ...string) (int16, fronius.Decision, string, fronius.PowerState, error) {
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

	oldFactory := newBatteryWriter
	newBatteryWriter = func() batteryWriter { return fakeWriter }
	defer func() { newBatteryWriter = oldFactory }()

	runner := newRunnerForTests(client)
	accepted := runner.HandleCommand(context.Background(), "sbam/cmd/force_charge", []byte(`{"target_pct":42}`))
	require.True(t, accepted)

	intent := <-runner.intents
	runner.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, fakeWriter.forceChargeCalls)
	assert.Equal(t, "127.0.0.1", fakeWriter.lastFroniusIP)
	assert.Equal(t, int16(42), fakeWriter.lastTargetPct)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "force_charge", ack.Command)
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

func TestCheckTimeRangeAt_BoundariesAndErrors(t *testing.T) {
	loc := time.FixedZone("UTC+1", 1*60*60)

	startBoundary := time.Date(2026, time.May, 16, 0, 0, 0, 0, loc)
	inRange, err := checkTimeRangeAt(startBoundary, "00:00", "06:00")
	require.NoError(t, err)
	assert.True(t, inRange)

	endBoundary := time.Date(2026, time.May, 16, 6, 0, 0, 0, loc)
	inRange, err = checkTimeRangeAt(endBoundary, "00:00", "06:00")
	require.NoError(t, err)
	assert.True(t, inRange)

	afterEnd := time.Date(2026, time.May, 16, 6, 1, 0, 0, loc)
	inRange, err = checkTimeRangeAt(afterEnd, "00:00", "06:00")
	require.NoError(t, err)
	assert.False(t, inRange)

	_, err = checkTimeRangeAt(startBoundary, "bad", "06:00")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start time")

	_, err = checkTimeRangeAt(startBoundary, "00:00", "bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid end time")
}
