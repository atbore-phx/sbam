package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	pw "sbam/pkg/power"
	"sbam/pkg/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPowerFlow makes readPowerFlow return the given baseline import (other
// loads, battery idle) or an error.
func stubPowerFlow(t *testing.T, baselineW float64, err error) *int {
	t.Helper()
	calls := 0
	old := readPowerFlow
	readPowerFlow = func(string) (storage.PowerFlow, error) {
		calls++
		if err != nil {
			return storage.PowerFlow{}, err
		}
		return storage.PowerFlow{GridW: baselineW}, nil
	}
	t.Cleanup(func() { readPowerFlow = old })
	return &calls
}

func limitRunner(t *testing.T, limitW float64) (*Runner, *fakeBatteryWriter) {
	t.Helper()
	w := &fakeBatteryWriter{}
	withFakeWriter(t, w)
	r := reserveRunner(pw.Window{Name: "night", Start: "00:00", End: "08:00", MaxCharge: 2500, HoldBattery: boolPtr(true)}, 1800)
	r.cfg.GridImportLimit = limitW
	r.cfg.GridImportMargin = 200
	r.cfg.GridChargePriority = GridChargePriorityLoads // battery priority has its own tests
	r.cfg.Now = func() time.Time { return localAt("03:00") }
	return r, w
}

func TestGridChargeAllowance(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		r, _ := limitRunner(t, 0)
		calls := stubPowerFlow(t, 9999, nil)

		capW, allowance, err := r.gridChargeAllowance(2500)

		assert.NoError(t, err)
		assert.Equal(t, 2500.0, capW)
		assert.Nil(t, allowance)
		assert.Zero(t, *calls, "no power flow read when the limit is off")
	})
	t.Run("car takes most of the line", func(t *testing.T) {
		r, _ := limitRunner(t, 3200)
		stubPowerFlow(t, 2850, nil)

		capW, allowance, err := r.gridChargeAllowance(2500)

		assert.NoError(t, err)
		assert.InDelta(t, 150, capW, 0.01, "3200 - 200 margin - 2850")
		assert.InDelta(t, 150, *allowance, 0.01)
	})
	t.Run("free line capped at max_charge", func(t *testing.T) {
		r, _ := limitRunner(t, 3200)
		stubPowerFlow(t, 300, nil)

		capW, _, err := r.gridChargeAllowance(2500)

		assert.NoError(t, err)
		assert.Equal(t, 2500.0, capW)
	})
	t.Run("over the limit already", func(t *testing.T) {
		r, _ := limitRunner(t, 3200)
		stubPowerFlow(t, 3600, nil)

		capW, allowance, err := r.gridChargeAllowance(2500)

		assert.NoError(t, err)
		assert.Zero(t, capW)
		assert.Zero(t, *allowance, "never negative")
	})
	t.Run("power flow unavailable", func(t *testing.T) {
		r, _ := limitRunner(t, 3200)
		stubPowerFlow(t, 0, errors.New("timeout"))

		capW, allowance, err := r.gridChargeAllowance(2500)

		assert.Error(t, err)
		assert.Zero(t, capW, "no grid charge without the meter")
		assert.Nil(t, allowance)
	})
}

func TestLimitRewriteNeeded(t *testing.T) {
	assert.False(t, limitRewriteNeeded(16, 18), "small change is not written")
	assert.True(t, limitRewriteNeeded(16, 21))
	assert.True(t, limitRewriteNeeded(16, 11))
	assert.True(t, limitRewriteNeeded(2, 0), "dropping to 0 is always written")
	assert.True(t, limitRewriteNeeded(0, 2), "leaving 0 is always written")
	assert.False(t, limitRewriteNeeded(0, 0))
}

func TestReserveRisk(t *testing.T) {
	r, _ := limitRunner(t, 3200)
	w := r.cfg.Windows[0]
	now := localAt("06:00") // 2 h left

	deficit, risk := r.reserveRisk(now, &w, 2100, 1250, 2500)
	assert.InDelta(t, 850, deficit, 0.01)
	assert.False(t, risk, "2500 W for 2 h covers 850 Wh")

	_, risk = r.reserveRisk(now, &w, 2100, 1250, 150)
	assert.True(t, risk, "150 W for 2 h is 300 Wh, short of 850")

	deficit, risk = r.reserveRisk(now, &w, 2100, 2600, 0)
	assert.Zero(t, deficit)
	assert.False(t, risk, "already above the target")

	r.cfg.EndHR = "bogus"
	_, risk = r.reserveRisk(now, nil, 2100, 1250, 0)
	assert.False(t, risk, "no window end known: no warning")
}

func TestCheckGridLimitOptions(t *testing.T) {
	assert.NoError(t, checkGridLimitOptions(0, 200))
	assert.NoError(t, checkGridLimitOptions(3200, 200))
	assert.ErrorContains(t, checkGridLimitOptions(-1, 200), "grid_import_limit")
	assert.ErrorContains(t, checkGridLimitOptions(3200, -1), "grid_import_margin")
	assert.ErrorContains(t, checkGridLimitOptions(3200, 3200), "lower than")
}

func lastState(t *testing.T, client *fakeClient) mqtt.StatePayload {
	t.Helper()
	var p mqtt.StatePayload
	msgs := drainPublishes(client)
	require.NotEmpty(t, msgs)
	require.NoError(t, json.Unmarshal(msgs[len(msgs)-1].payload, &p))
	return p
}

func TestTickCapsChargeAndStartsLimitLoop(t *testing.T) {
	r, _ := limitRunner(t, 3200)
	client := newFakeClient()
	r.client = client
	r.cfg.MQTT.Enabled = true
	stubPowerFlow(t, 2850, nil)

	oldStorage, oldPower, oldFronius := newStorage, newPower, newFronius
	t.Cleanup(func() { newStorage, newPower, newFronius = oldStorage, oldPower, oldFronius })
	newStorage = func() storageClient { return &fakeStorageClient{capacityToCharge: 3870, capacityMax: 5120, socPct: 24} }
	newPower = func() powerClient { return &fakePowerClient{forecastWh: 12000, retrieved: true} }
	tracker := &trackingFroniusClient{decision: "reserve_charge", reason: "test", chargePct: 2, powerState: fronius.PowerState{BattReserveNet: -550}}
	newFronius = func(fronius.SunSpecType) froniusClient { return tracker }
	t.Cleanup(r.stopLimitLoop)

	require.NoError(t, r.Tick(context.Background(), localAt("03:00")))

	assert.InDelta(t, 150, tracker.lastMaxCharge, 0.01, "max_charge passed to the decision = free power under the limit")
	require.NotNil(t, r.limit, "a force charge under a limit starts the loop")
	assert.InDelta(t, 550, r.limit.needWh, 0.01)
	assert.Equal(t, int16(2), r.limit.lastPct)
	assert.True(t, r.limit.hold)

	p := lastState(t, client)
	require.NotNil(t, p.ReserveAtRisk)
	assert.False(t, *p.ReserveAtRisk, "550 Wh at 150 W over the 5 h left is reachable")
	require.NotNil(t, p.ReserveDeficitWh)
	assert.InDelta(t, 550, *p.ReserveDeficitWh, 0.01)
	require.NotNil(t, p.GridChargeAllowanceW)
	assert.InDelta(t, 150, *p.GridChargeAllowanceW, 0.01)
}

func TestTickUsesWindowMaxChargeWithoutLimit(t *testing.T) {
	r, _ := limitRunner(t, 0)
	r.cfg.MaxCharge = 3500
	tracker := tickWith(t, r, localAt("03:00"), nil)

	assert.InDelta(t, 2500, tracker.lastMaxCharge, 0.01, "the active window's max_charge, not the global one (#209)")
	assert.Nil(t, r.limit, "no loop without a limit")
}

func TestHandleLimitCheck(t *testing.T) {
	r, w := limitRunner(t, 3200)
	t.Cleanup(r.stopLimitLoop)
	session := limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, hold: true, lastPct: 2}
	r.limit = &session

	stubPowerFlow(t, 0, nil) // car stopped: the line is free
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 1, w.forceChargeCalls)
	assert.Equal(t, int16(16), w.lastTargetPct, "850 Wh within the hour, under max_charge")
	assert.Equal(t, int16(16), r.limit.lastPct)

	stubPowerFlow(t, 50, nil) // small change: no write
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 1, w.forceChargeCalls)

	stubPowerFlow(t, 3100, nil) // an oven starts: no room left
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 1, w.holdCalls, "allowance 0 with hold_battery writes the hold, not defaults")
	assert.Equal(t, int16(0), r.limit.lastPct)

	stubPowerFlow(t, 0, errors.New("meter down")) // still 0: nothing written
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 1, w.holdCalls)
	assert.Equal(t, 1, w.forceChargeCalls)
}

func TestHandleLimitCheckWithoutHoldWritesDefaults(t *testing.T) {
	r, w := limitRunner(t, 3200)
	r.limit = &limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, lastPct: 16}
	stubPowerFlow(t, 3100, nil)

	r.handleLimitCheck(context.Background())

	assert.Equal(t, 1, w.forceChargeCalls)
	assert.Equal(t, int16(0), w.lastTargetPct, "force charge 0 = defaults")
	assert.Zero(t, w.holdCalls)
}

func TestHandleLimitCheckStops(t *testing.T) {
	t.Run("paused", func(t *testing.T) {
		r, w := limitRunner(t, 3200)
		r.limit = &limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, lastPct: 16}
		r.setPause(nil)

		r.handleLimitCheck(context.Background())

		assert.Nil(t, r.limit)
		assert.Zero(t, w.forceChargeCalls)
	})
	t.Run("window over", func(t *testing.T) {
		r, w := limitRunner(t, 3200)
		r.cfg.Now = func() time.Time { return localAt("09:00") }
		r.limit = &limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, lastPct: 16}

		r.handleLimitCheck(context.Background())

		assert.Nil(t, r.limit)
		assert.Zero(t, w.forceChargeCalls)
	})
	t.Run("no session", func(t *testing.T) {
		r, w := limitRunner(t, 3200)
		r.handleLimitCheck(context.Background())
		assert.Zero(t, w.forceChargeCalls)
	})
}

func TestLimitLoopSubmitsChecksUntilStopped(t *testing.T) {
	old, oldProbe := limitCheckInterval, limitProbeDelay
	limitCheckInterval, limitProbeDelay = 5*time.Millisecond, time.Millisecond
	t.Cleanup(func() { limitCheckInterval, limitProbeDelay = old, oldProbe })
	r, w := limitRunner(t, 3200)
	stubPowerFlow(t, 2850, nil) // 150 W free

	next := func() mqtt.Intent {
		t.Helper()
		select {
		case intent := <-r.intents:
			return intent
		case <-time.After(2 * time.Second):
			t.Fatal("no limit check submitted")
		}
		return mqtt.Intent{}
	}

	r.startLimitLoop(limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, lastPct: 16})
	probe := next()
	assert.Equal(t, mqtt.IntentLimitCheck, probe.Kind, "the probe check")
	r.handleIntent(context.Background(), probe)
	assert.Equal(t, 1, w.forceChargeCalls, "the check runs through the runner and rewrites the rate")
	assert.Equal(t, int16(2), r.limit.lastPct)

	assert.Equal(t, mqtt.IntentLimitCheck, next().Kind, "then the periodic checks")

	r.stopLimitLoop()
	assert.Nil(t, r.limit)
	assert.Nil(t, r.limitStop)
	r.stopLimitLoop() // idempotent
}

// A full queue drops checks instead of blocking the loop, and the loop still
// stops.
func TestLimitLoopDropsChecksWhenQueueFull(t *testing.T) {
	old, oldProbe := limitCheckInterval, limitProbeDelay
	limitCheckInterval, limitProbeDelay = time.Millisecond, time.Millisecond
	t.Cleanup(func() { limitCheckInterval, limitProbeDelay = old, oldProbe })
	r, _ := limitRunner(t, 3200)
	for len(r.intents) < cap(r.intents) {
		r.intents <- mqtt.Intent{Kind: mqtt.IntentTick}
	}

	r.startLimitLoop(limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, lastPct: 16})
	time.Sleep(20 * time.Millisecond)
	assert.Len(t, r.intents, cap(r.intents))
	r.stopLimitLoop()

	time.Sleep(10 * time.Millisecond) // a check already past the stop select may still land
	for len(r.intents) > 0 {
		<-r.intents
	}
	time.Sleep(10 * time.Millisecond)
	assert.Empty(t, r.intents, "the loop stopped")
}

func TestCommandsStopLimitLoop(t *testing.T) {
	for _, kind := range []mqtt.IntentKind{mqtt.IntentSetDefaults, mqtt.IntentHold, mqtt.IntentPause, mqtt.IntentForceCharge, mqtt.IntentShutdown} {
		t.Run(string(kind), func(t *testing.T) {
			r, _ := limitRunner(t, 3200)
			r.startLimitLoop(limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, lastPct: 16})

			r.handleIntent(context.Background(), mqtt.Intent{Kind: kind})

			assert.Nil(t, r.limit, "%s ends the limit loop", kind)
		})
	}
}

func TestWindowEndDefaultsRunWhilePaused(t *testing.T) {
	r, w := limitRunner(t, 0)
	client := newFakeClient()
	r.client = client
	r.cfg.MQTT.Enabled = true
	until := localAt("09:00")
	r.setPause(&until)

	r.handleIntent(context.Background(), mqtt.Intent{Kind: mqtt.IntentSetDefaults, System: true})
	assert.Equal(t, 1, w.setDefaultsCalls, "the window-end reset is never blocked by a pause")
	p := lastState(t, client)
	assert.True(t, p.Paused, "the reset does not report the runner as resumed")
	require.NotNil(t, p.NextRun)
	assert.True(t, until.Equal(*p.NextRun))

	r.handleIntent(context.Background(), mqtt.Intent{Kind: mqtt.IntentSetDefaults, CommandTopic: "sbam/cmd/set_defaults"})
	assert.Equal(t, 1, w.setDefaultsCalls, "a user set_defaults is still rejected while paused")
}

// The window-end producers (windows timer and crontab end_hr) share
// submitSystemDefaults; what it queues resets the inverter while paused.
func TestSubmitSystemDefaultsRunsWhilePaused(t *testing.T) {
	r, w := limitRunner(t, 0)
	r.setPause(nil)

	r.submitSystemDefaults()
	require.Len(t, r.intents, 1)
	intent := <-r.intents
	assert.True(t, intent.System)
	r.handleIntent(context.Background(), intent)

	assert.Equal(t, 1, w.setDefaultsCalls)
}

func TestScheduleGridLimitFlags(t *testing.T) {
	for name, def := range map[string]string{"grid_import_limit": "0", "grid_import_margin": "200"} {
		flag := scdCmd.Flags().Lookup(name)
		if assert.NotNil(t, flag, name) {
			assert.Equal(t, def, flag.DefValue, name)
		}
	}
}

// With battery priority the tick claims the window's max_charge even when a
// throttling load (the EV) holds the line: otherwise both wait for each other
// and the battery never charges (night of 2026-10-26).
func TestBatteryPriorityClaimsAtTick(t *testing.T) {
	r, _ := limitRunner(t, 3200)
	r.cfg.GridChargePriority = ""
	stubPowerFlow(t, 3150, nil) // EV at ~2.85 kW + house

	capW, allowance, err := r.gridChargeAllowance(2500)

	assert.NoError(t, err)
	assert.Equal(t, 2500.0, capW, "claim, do not wait for room")
	assert.Zero(t, *allowance, "telemetry still reports the free power")

	stubPowerFlow(t, 0, errors.New("meter down"))
	capW, _, err = r.gridChargeAllowance(2500)
	assert.Error(t, err)
	assert.Zero(t, capW, "no grid charge without the meter, whatever the priority")
}

func TestBatteryPriorityTickStartsBatterySession(t *testing.T) {
	r, _ := limitRunner(t, 3200)
	r.cfg.GridChargePriority = GridChargePriorityBattery
	stubPowerFlow(t, 3150, nil)
	t.Cleanup(r.stopLimitLoop)
	oldStorage, oldPower, oldFronius := newStorage, newPower, newFronius
	t.Cleanup(func() { newStorage, newPower, newFronius = oldStorage, oldPower, oldFronius })
	newStorage = func() storageClient { return &fakeStorageClient{capacityToCharge: 3870, capacityMax: 5120, socPct: 24} }
	newPower = func() powerClient { return &fakePowerClient{forecastWh: 12000, retrieved: true} }
	tracker := &trackingFroniusClient{decision: "reserve_charge", reason: "test", chargePct: 11, powerState: fronius.PowerState{BattReserveNet: -550}}
	newFronius = func(fronius.SunSpecType) froniusClient { return tracker }

	require.NoError(t, r.Tick(context.Background(), localAt("03:00")))

	assert.InDelta(t, 2500, tracker.lastMaxCharge, 0.01)
	require.NotNil(t, r.limit)
	assert.True(t, r.limit.battery)
}

// A load that holds the line leaves no free power: the reserve is at risk
// even though battery priority still claims the window's max_charge.
func TestBatteryPriorityReserveAtRiskUsesFreePower(t *testing.T) {
	r, _ := limitRunner(t, 3200)
	r.cfg.GridChargePriority = GridChargePriorityBattery
	client := newFakeClient()
	r.client = client
	r.cfg.MQTT.Enabled = true
	stubPowerFlow(t, 3400, nil) // a load that does not throttle, over the limit
	t.Cleanup(r.stopLimitLoop)
	oldStorage, oldPower, oldFronius := newStorage, newPower, newFronius
	t.Cleanup(func() { newStorage, newPower, newFronius = oldStorage, oldPower, oldFronius })
	newStorage = func() storageClient { return &fakeStorageClient{capacityToCharge: 3870, capacityMax: 5120, socPct: 24} }
	newPower = func() powerClient { return &fakePowerClient{forecastWh: 12000, retrieved: true} }
	tracker := &trackingFroniusClient{decision: "reserve_charge", reason: "test", chargePct: 11, powerState: fronius.PowerState{BattReserveNet: -550}}
	newFronius = func(fronius.SunSpecType) froniusClient { return tracker }

	require.NoError(t, r.Tick(context.Background(), localAt("03:00")))

	assert.InDelta(t, 2500, tracker.lastMaxCharge, 0.01, "the charge is still claimed")
	p := lastState(t, client)
	require.NotNil(t, p.GridChargeAllowanceW)
	assert.Zero(t, *p.GridChargeAllowanceW)
	require.NotNil(t, p.ReserveAtRisk)
	assert.True(t, *p.ReserveAtRisk, "550 Wh with no free power cannot be charged")
}

func TestBatteryPriorityLoop(t *testing.T) {
	r, w := limitRunner(t, 3200)
	r.cfg.GridChargePriority = GridChargePriorityBattery
	t.Cleanup(r.stopLimitLoop)
	// 16 % of 5120 Wh is ~819 W; the tick asked for 850 Wh.
	r.limit = &limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, hold: true, lastPct: 16, battery: true}

	stubPowerFlow(t, 3150, nil) // the EV yielded: import within the limit
	r.handleLimitCheck(context.Background())
	assert.Zero(t, w.forceChargeCalls+w.holdCalls, "keep the claimed rate")

	stubPowerFlow(t, 3900, nil) // a load that does not throttle: 700 W over
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 1, w.holdCalls, "back off below the limit (to 0 here)")
	assert.Equal(t, int16(0), r.limit.lastPct)

	stubPowerFlow(t, 2000, nil) // room again
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 1, w.forceChargeCalls)
	assert.Equal(t, int16(16), w.lastTargetPct, "grows back, never above what the tick asked for")

	stubPowerFlow(t, 0, errors.New("meter down"))
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 2, w.holdCalls, "meter lost: stop charging")
}

func TestBatteryPriorityLoopPartialBackoff(t *testing.T) {
	r, w := limitRunner(t, 3200)
	r.cfg.GridChargePriority = GridChargePriorityBattery
	r.limit = &limitSession{needWh: 2500, ceilingW: 2500, capacityWh: 5120, lastPct: 48, battery: true} // ~2458 W

	stubPowerFlow(t, 3500, nil) // 300 W over

	r.handleLimitCheck(context.Background())

	assert.Equal(t, 1, w.forceChargeCalls)
	assert.Equal(t, int16(38), w.lastTargetPct, "2458 - 300 - 200 = 1958 W = 38 %")
}

func TestCheckGridChargePriority(t *testing.T) {
	assert.NoError(t, checkGridChargePriority("battery"))
	assert.NoError(t, checkGridChargePriority("loads"))
	assert.NoError(t, checkGridChargePriority(""))
	assert.ErrorContains(t, checkGridChargePriority("car"), "grid_charge_priority")
}

func TestGridChargePriorityFlagDefault(t *testing.T) {
	flag := scdCmd.Flags().Lookup("grid_charge_priority")
	require.NotNil(t, flag)
	assert.Equal(t, "battery", flag.DefValue)
}

// The reserve sensors keep their last values on every state published while
// the reserve window is active, and are dropped once it closes.
func TestReserveTelemetryRepeatedInReserveWindow(t *testing.T) {
	r, _ := limitRunner(t, 0)
	client := newFakeClient()
	r.client = client
	r.cfg.MQTT.Enabled = true
	oldStorage, oldPower, oldFronius := newStorage, newPower, newFronius
	t.Cleanup(func() { newStorage, newPower, newFronius = oldStorage, oldPower, oldFronius })
	newStorage = func() storageClient { return &fakeStorageClient{capacityToCharge: 3870, capacityMax: 5120, socPct: 24} }
	newPower = func() powerClient { return &fakePowerClient{forecastWh: 12000, retrieved: true} }
	newFronius = func(fronius.SunSpecType) froniusClient {
		return &trackingFroniusClient{decision: "reserve_charge", reason: "test", chargePct: 11, powerState: fronius.PowerState{BattReserveNet: -550}}
	}
	t.Cleanup(r.stopLimitLoop)

	require.NoError(t, r.Tick(context.Background(), localAt("03:00")))
	evaluated := lastState(t, client)
	require.NotNil(t, evaluated.ReserveAtRisk)

	r.setPause(nil)
	require.NoError(t, r.Tick(context.Background(), localAt("03:30")))
	paused := lastState(t, client)
	assert.True(t, paused.Paused)
	require.NotNil(t, paused.ReserveAtRisk, "a pause does not clear reserve_at_risk")
	assert.Equal(t, *evaluated.ReserveAtRisk, *paused.ReserveAtRisk)
	require.NotNil(t, paused.ReserveDeficitWh)
	assert.InDelta(t, *evaluated.ReserveDeficitWh, *paused.ReserveDeficitWh, 0.01)

	r.clearPause()
	require.NoError(t, r.Tick(context.Background(), localAt("10:00")))
	after := lastState(t, client)
	assert.Nil(t, after.ReserveAtRisk, "outside the reserve window the sensors are not published")
	assert.Nil(t, r.reserve)
}

func TestForceChargeCommandCappedByGridImportLimit(t *testing.T) {
	w := &fakeBatteryWriter{}
	withFakeWriter(t, w)
	oldStorage := newStorage
	t.Cleanup(func() { newStorage = oldStorage })
	newStorage = func() storageClient { return &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40} }
	r := newRunnerForTests(newFakeClient())
	r.cfg.GridImportLimit = 3200
	r.cfg.GridImportMargin = 200

	stubPowerFlow(t, 2500, nil) // 500 W free
	require.NoError(t, r.handleForceCharge(context.Background(), mqtt.Intent{Kind: mqtt.IntentForceCharge, TargetPct: 42}))
	assert.Equal(t, int16(5), w.lastTargetPct, "500 W free on 10 kWh, not the 35 %% of max_charge")

	stubPowerFlow(t, 0, errors.New("meter down"))
	calls := w.forceChargeCalls
	assert.Error(t, r.handleForceCharge(context.Background(), mqtt.Intent{Kind: mqtt.IntentForceCharge, TargetPct: 42}))
	assert.Equal(t, calls, w.forceChargeCalls, "no blind charge under the limit")

	require.NoError(t, r.handleForceCharge(context.Background(), mqtt.Intent{Kind: mqtt.IntentForceCharge, TargetPct: 100, IgnoreMaxCharge: true}))
	assert.Equal(t, int16(100), w.lastTargetPct, "the explicit override is not capped")
}

// A meter read failure is published as an MQTT error, from the tick and from
// the limit loop, and no grid charge is allowed.
func TestGridLimitMeterErrorPublished(t *testing.T) {
	r, w := limitRunner(t, 3200)
	client := newFakeClient()
	r.client = client
	r.cfg.MQTT.Enabled = true
	stubPowerFlow(t, 0, errors.New("meter down"))
	tracker := tickWith(t, r, localAt("03:00"), nil)

	assert.Zero(t, tracker.lastMaxCharge, "no grid charge without the meter")
	_, ok := findPublishedBySuffix(drainPublishes(client), "/error")
	assert.True(t, ok, "the tick publishes the meter error")

	r.limit = &limitSession{needWh: 850, ceilingW: 2500, capacityWh: 5120, lastPct: 16, hold: true}
	r.handleLimitCheck(context.Background())
	assert.Equal(t, 1, w.holdCalls, "the loop drops to the hold")
	_, ok = findPublishedBySuffix(drainPublishes(client), "/error")
	assert.True(t, ok, "the loop publishes the meter error")
}
