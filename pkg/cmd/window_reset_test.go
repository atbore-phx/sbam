package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	pw "sbam/pkg/power"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func intPtr(i int) *int { return &i }

// endOfWindow is a set_defaults window whose reset lands exactly on its end.
var endOfWindow = pw.Window{Name: "valle", Start: "00:00", End: "08:00", MaxCharge: 2500,
	Defaults: boolPtr(true), BeforeEndDefaults: intPtr(0), TickMinutes: intPtr(30)}

// afterEnd is where a real tick lands: the 30-minute ticker started just after
// the window opened, so it fires a few milliseconds past the end.
var afterEnd = localAt("08:00").Add(5 * time.Millisecond)

type resetHarness struct {
	r       *Runner
	writer  *fakeBatteryWriter
	clock   time.Time
	retries []func()
}

func newResetHarness(t *testing.T, window pw.Window, start time.Time) *resetHarness {
	t.Helper()
	oldStorage, oldPower, oldFronius := newStorage, newPower, newFronius
	oldTimer, oldDelay := newTimerFunc, windowResetRetryDelay
	t.Cleanup(func() {
		newStorage, newPower, newFronius = oldStorage, oldPower, oldFronius
		newTimerFunc, windowResetRetryDelay = oldTimer, oldDelay
	})
	newPower = func() powerClient { return &fakePowerClient{forecastWh: 12000, retrieved: true} }
	newFronius = func(fronius.SunSpecType) froniusClient {
		return &trackingFroniusClient{decision: fronius.DecisionIdle, reason: "test"}
	}
	newStorage = func() storageClient {
		return &fakeStorageClient{capacityToCharge: 3000, capacityMax: 5120, socPct: 41}
	}

	h := &resetHarness{writer: &fakeBatteryWriter{}, clock: start}
	windowResetRetryDelay = time.Hour + time.Second // tells a retry from a boundary tick
	newTimerFunc = func(d time.Duration, f func()) *time.Timer {
		if d == windowResetRetryDelay {
			h.retries = append(h.retries, f)
		}
		return nil
	}
	h.r = NewRunner(RunnerConfig{
		FroniusIP:          "127.0.0.1",
		SchedulerMode:      "windows",
		Windows:            []pw.Window{window},
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "08:00",
		Now:                func() time.Time { return h.clock },
	}, newFakeClient())
	h.r.writer = h.writer
	t.Cleanup(h.r.stopWindowsTicker)
	h.r.startWindowsTicker(start)
	return h
}

func (h *resetHarness) tickAt(t *testing.T, at time.Time) {
	t.Helper()
	h.clock = at
	require.NoError(t, h.r.Tick(t.Context(), at))
}

func TestWindowExitTickFirstStillResets(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("07:30"))
	h.writer.controlMode = 2

	h.tickAt(t, afterEnd)

	assert.Nil(t, h.r.defaultsTimer, "the tick cancelled the window-end timer")
	assert.Equal(t, 1, h.writer.setDefaultsCalls, "the reset must not be lost with the timer")
	assert.Empty(t, h.r.activeWindow)
}

func TestTickExactlyOnWindowEndKeepsTheTimerArmed(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("07:30"))

	h.tickAt(t, localAt("08:00")) // the end instant still belongs to the window

	assert.Equal(t, "valle", h.r.activeWindow)
	assert.NotNil(t, h.r.defaultsTimer, "the reset is still on its way")
	assert.Zero(t, h.writer.setDefaultsCalls)

	h.tickAt(t, localAt("08:30"))
	assert.Equal(t, 1, h.writer.setDefaultsCalls, "and the next tick is the backstop")
}

func TestWindowExitAfterTimerResetIsHarmless(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("07:30"))

	h.clock = afterEnd
	h.r.handleIntent(t.Context(), mqtt.Intent{Kind: mqtt.IntentSetDefaults, System: true})
	h.tickAt(t, afterEnd)

	assert.Equal(t, 2, h.writer.setDefaultsCalls)
}

func TestWindowExitResetsAcrossRaceOrders(t *testing.T) {
	for i := range 50 {
		h := newResetHarness(t, endOfWindow, localAt("08:00").Add(-time.Millisecond))
		h.clock = afterEnd

		ctx, stop := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- h.r.Run(ctx) }()
		require.True(t, h.r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}))
		time.Sleep(3 * time.Millisecond)
		stop()
		<-done
		assert.GreaterOrEqual(t, h.writer.setDefaultsCalls, 1, "iteration %d lost the reset", i)
	}
}

func TestWindowExitResetsWhileHoldWindowHasNoSetDefaults(t *testing.T) {
	w := pw.Window{Name: "valle", Start: "00:00", End: "08:00", MaxCharge: 2500, HoldBattery: boolPtr(true)}
	h := newResetHarness(t, w, localAt("07:30"))

	h.tickAt(t, afterEnd)

	assert.Equal(t, 1, h.writer.setDefaultsCalls)
}

func TestWindowExitIgnoresPause(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("07:30"))
	until := localAt("12:00")
	h.r.setPause(&until)

	h.tickAt(t, afterEnd)

	assert.Equal(t, 1, h.writer.setDefaultsCalls, "a pause must not leave the inverter forced")
}

func TestWindowWithoutResetLeavesInverterAlone(t *testing.T) {
	w := pw.Window{Name: "valle", Start: "00:00", End: "08:00", MaxCharge: 2500}
	h := newResetHarness(t, w, localAt("07:30"))
	h.writer.controlMode = 2

	h.tickAt(t, afterEnd)
	h.tickAt(t, localAt("09:00"))

	assert.Zero(t, h.writer.setDefaultsCalls)
	assert.Zero(t, h.writer.controlModeCalls)
}

func TestGuardRestoresLostResetOutsideWindow(t *testing.T) {
	for name, mode := range map[string]int16{"hold": 2, "force charge": 3} {
		t.Run(name, func(t *testing.T) {
			h := newResetHarness(t, endOfWindow, localAt("08:30")) // a restart lost the reset
			h.writer.controlMode = mode

			h.tickAt(t, localAt("09:00"))

			assert.Equal(t, 1, h.writer.setDefaultsCalls)
		})
	}
}

func TestGuardDoesNothingInNormalMode(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("08:30"))

	h.tickAt(t, localAt("09:00"))

	assert.Equal(t, 1, h.writer.controlModeCalls)
	assert.Zero(t, h.writer.setDefaultsCalls)
}

func TestGuardDoesNotRunInsideWindow(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("03:00"))
	h.writer.controlMode = 2

	h.tickAt(t, localAt("03:30"))

	assert.Zero(t, h.writer.controlModeCalls)
	assert.Zero(t, h.writer.setDefaultsCalls)
}

func TestGuardKeepsManualCommandOutsideWindow(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("12:00"))
	h.writer.controlMode = 2

	h.r.handleIntent(t.Context(), mqtt.Intent{Kind: mqtt.IntentHold})
	h.tickAt(t, localAt("13:00"))
	assert.Zero(t, h.writer.setDefaultsCalls, "the operator asked for this hold")

	h.r.handleIntent(t.Context(), mqtt.Intent{Kind: mqtt.IntentSetDefaults})
	assert.False(t, h.r.manualOutsideWindow)
	h.writer.setDefaultsCalls = 0
	h.tickAt(t, localAt("14:00"))
	assert.Equal(t, 1, h.writer.setDefaultsCalls, "after set_defaults the guard is back on duty")
}

func TestManualCommandInsideWindowEndsWithIt(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("07:30"))

	h.r.handleIntent(t.Context(), mqtt.Intent{Kind: mqtt.IntentHold})
	assert.False(t, h.r.manualOutsideWindow)
	h.tickAt(t, afterEnd)

	assert.Equal(t, 1, h.writer.setDefaultsCalls)
}

func TestFailedWindowExitResetRetriesUntilItWorks(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("07:30"))
	h.writer.controlMode = 2
	h.writer.setDefaultsErr = errors.New("modbus down")

	h.tickAt(t, afterEnd)
	require.Len(t, h.retries, 1)

	h.retries[0]()
	require.Len(t, h.r.intents, 1, "the retry queues a tick")
	<-h.r.intents
	h.tickAt(t, localAt("08:05"))
	require.Len(t, h.retries, 2, "still failing: keep retrying")

	h.writer.setDefaultsErr = nil
	h.tickAt(t, localAt("08:10"))
	assert.Equal(t, 3, h.writer.setDefaultsCalls)
	assert.Len(t, h.retries, 2, "done: no more retries")
}

func TestGuardRetriesWhenModeCannotBeRead(t *testing.T) {
	h := newResetHarness(t, endOfWindow, localAt("08:30"))
	h.writer.controlModeErr = errors.New("modbus down")

	h.tickAt(t, localAt("09:00"))

	assert.Len(t, h.retries, 1)
	assert.Zero(t, h.writer.setDefaultsCalls, "never write blind")
}
