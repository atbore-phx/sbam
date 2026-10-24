package cmd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/homeassistant"
	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Runner tests for the EV guard (SPEC-0002, T-0201 … T-0210).

const schEVEntity = "sensor.ev_guard_test"

// schEVHA is a fake haClient serving the EV power state.
type schEVHA struct {
	mu    sync.Mutex
	state string
	unit  string
	err   error
	reads int
}

func (f *schEVHA) State(_ context.Context, entityID string) (homeassistant.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return homeassistant.State{}, f.err
	}
	return homeassistant.State{
		EntityID:   entityID,
		State:      f.state,
		Attributes: map[string]any{"unit_of_measurement": f.unit},
	}, nil
}

func (f *schEVHA) History(context.Context, string, time.Time, time.Time) ([]homeassistant.HistoryPoint, error) {
	return nil, errors.New("not used")
}

func (f *schEVHA) set(state string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state, f.err = state, err
}

func (f *schEVHA) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// newSchEVHarness is the optimize runner harness with the EV guard enabled
// (threshold 1000 W, EV sensor in kW, initially 0 kW).
func newSchEVHarness(t *testing.T, dryRun bool) (*schOptHarness, *schEVHA) {
	t.Helper()
	h := newSchOptHarness(t, dryRun)
	ha := &schEVHA{state: "0", unit: "kW"}
	h.runner.opt.cfg.EVEntity = schEVEntity
	h.runner.opt.cfg.EVGuardW = 1000
	h.runner.opt.deps.HA = ha
	return h, ha
}

func (h *schOptHarness) evPoll(at time.Time) {
	h.clock.set(at)
	h.handle(mqtt.Intent{Kind: mqtt.IntentEVPoll})
}

func (h *schOptHarness) appliedModes() []fronius.DispatchMode {
	h.writer.mu.Lock()
	defer h.writer.mu.Unlock()
	out := make([]fronius.DispatchMode, 0, len(h.writer.applies))
	for _, c := range h.writer.applies {
		out = append(out, c.Mode)
	}
	return out
}

func (h *schOptHarness) evPollTimers() []*schOptTimer {
	return h.timers.withDelay(optEVPollInterval)
}

// --- T-0201: guard engages in a live self_consume slot (AC-1, AC-9) ---------

func TestEVGuard_T0201_EngagesInSelfConsumeSlot(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick() // 10:20, self_consume slot
	require.Equal(t, []fronius.DispatchMode{fronius.DispatchMode(optimize.ActionSelfConsume)}, h.appliedModes())

	ha.set("7.2", nil)
	h.evPoll(h.at(10, 21, 0))

	require.Equal(t, fronius.DispatchMode(optimize.ActionHold), h.appliedModes()[len(h.appliedModes())-1], "hold written")
	assert.Equal(t, fronius.RevertSeconds, h.writer.lastApply().RevertS, "hold carries the revert timeout (AC-10)")
	st := h.client.lastState(t)
	require.NotNil(t, st.OptAction)
	assert.Equal(t, string(optimize.ActionHold), *st.OptAction)
	assert.Equal(t, string(optimize.ReasonEVGuardHold), st.LastDecisionReason, "guard code outranks the action code")
	assert.Contains(t, st.OptReasons, string(optimize.ReasonEVGuardHold))
	entries := h.logs.FilterMessage("ev guard engaged").All()
	require.Len(t, entries, 1)
	assert.Equal(t, 7200.0, entries[0].ContextMap()["ev_power_w"])
	assert.Equal(t, 1000.0, entries[0].ContextMap()["threshold_w"])

	// Below the threshold but not engaged: nothing more happens on the next poll.
	ha.set("1.5", nil)
	h.evPoll(h.at(10, 22, 0))
	assert.Len(t, h.appliedModes(), 2, "no write while engaged and above the release level")
}

func TestEVGuard_T0201_BelowThresholdDoesNotEngage(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("0.9", nil) // 900 W ≤ 1000 W
	h.evPoll(h.at(10, 21, 0))
	assert.Len(t, h.appliedModes(), 1)
	assert.False(t, h.runner.opt.ev.active)
}

// --- T-0202: release with hysteresis and minimum engaged time (AC-2) ------

func TestEVGuard_T0202_ReleaseAfterMinimumEngagedTime(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("7", nil)
	h.evPoll(h.at(10, 21, 0))
	require.True(t, h.runner.opt.ev.active)

	ha.set("0.7", nil) // 700 W: above 50 % of 1000 W
	h.evPoll(h.at(10, 30, 0))
	assert.True(t, h.runner.opt.ev.active, "700 W keeps the guard engaged")

	ha.set("0", nil)
	h.evPoll(h.at(10, 23, 0)) // 2 min after engaging
	assert.True(t, h.runner.opt.ev.active, "minimum engaged time not reached")
	assert.Len(t, h.appliedModes(), 2)

	h.evPoll(h.at(10, 26, 0))
	assert.False(t, h.runner.opt.ev.active)
	modes := h.appliedModes()
	require.Len(t, modes, 3)
	assert.Equal(t, fronius.DispatchMode(optimize.ActionSelfConsume), modes[2], "the slot's planned action is written again")
	st := h.client.lastState(t)
	assert.Equal(t, string(optimize.ActionSelfConsume), *st.OptAction)
	assert.NotContains(t, st.OptReasons, string(optimize.ReasonEVGuardHold))
	assert.True(t, h.logged("ev guard released"))
}

// --- T-0203: hourly runs respect an active guard (AC-3) ---------------------

func TestEVGuard_T0203_HourlyRunKeepsHold(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("11", nil)
	h.evPoll(h.at(10, 40, 0))
	require.True(t, h.runner.opt.ev.active)

	h.evPoll(h.at(10, 59, 0)) // the car still charges
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	modes := h.appliedModes()
	require.Len(t, modes, 3)
	assert.Equal(t, fronius.DispatchMode(optimize.ActionHold), modes[2], "hold directly, no self_consume in between")
	assert.True(t, h.runner.opt.ev.active)
	assert.Contains(t, h.client.lastPlan(t).Reasons, string(optimize.ReasonEVGuardHold))
}

func TestEVGuard_T0203_HourlyRunWithOtherActionReleases(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("11", nil)
	h.evPoll(h.at(10, 40, 0))
	require.True(t, h.runner.opt.ev.active)

	h.setPlan(schOptSlot{action: optimize.ActionCharge, rateW: 3000})
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	modes := h.appliedModes()
	assert.Equal(t, fronius.DispatchMode(optimize.ActionCharge), modes[len(modes)-1])
	assert.False(t, h.runner.opt.ev.active)
	assert.NotContains(t, h.client.lastPlan(t).Reasons, string(optimize.ReasonEVGuardHold))

	// The car still charges: the guard does not touch a charge slot.
	h.evPoll(h.at(11, 1, 0))
	assert.Len(t, h.appliedModes(), len(modes))
}

// --- T-0204: other actions are untouched (AC-4) ------------------------------

func TestEVGuard_T0204_OtherActionsUntouched(t *testing.T) {
	for _, slot := range []schOptSlot{
		{action: optimize.ActionCharge, rateW: 2000},
		{action: optimize.ActionHold},
		{action: optimize.ActionStandby},
		{action: optimize.ActionDischarge, rateW: 3000, startWh: 8000},
	} {
		t.Run(string(slot.action), func(t *testing.T) {
			h, ha := newSchEVHarness(t, false)
			h.setPlan(slot)
			h.tick()
			before := len(h.appliedModes())
			ha.set("11", nil)
			h.evPoll(h.at(10, 21, 0))
			assert.Len(t, h.appliedModes(), before)
			assert.False(t, h.runner.opt.ev.active)
		})
	}
}

// --- T-0205: disabled guard (AC-5) ------------------------------------------

func TestEVGuard_T0205_Disabled(t *testing.T) {
	for name, mod := range map[string]func(*optRunnerState){
		"threshold 0":  func(o *optRunnerState) { o.cfg.EVGuardW = 0 },
		"no EV entity": func(o *optRunnerState) { o.cfg.EVEntity = "" },
	} {
		t.Run(name, func(t *testing.T) {
			h, ha := newSchEVHarness(t, false)
			mod(h.runner.opt)
			h.runner.StartOptimizeTicker()
			h.process()
			assert.Empty(t, h.evPollTimers(), "no poll timer")
			ha.set("11", nil)
			before := ha.readCount() // the daily profile rebuild after the run also uses the HA client
			h.evPoll(h.at(10, 21, 0))
			assert.Equal(t, before, ha.readCount(), "no EV state reads")
			assert.Len(t, h.appliedModes(), 1)
		})
	}
}

func TestEVGuard_T0205_PollTimerArmedAndRearmed(t *testing.T) {
	h, _ := newSchEVHarness(t, false)
	h.runner.StartOptimizeTicker()
	h.process()
	timers := h.evPollTimers()
	require.Len(t, timers, 1)
	timers[0].fn()
	h.process()
	timers = h.evPollTimers()
	require.Len(t, timers, 2, "each poll re-arms the timer")
	assert.True(t, timers[0].stopped())
	assert.False(t, timers[1].stopped())

	h.shutdown()
	assert.True(t, timers[1].stopped(), "shutdown stops the poll timer")
}

// --- T-0206: read failures do not toggle (AC-6) ------------------------------

func TestEVGuard_T0206_ReadFailuresKeepState(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("7", nil)
	h.evPoll(h.at(10, 21, 0))
	require.True(t, h.runner.opt.ev.active)

	cases := []struct {
		state string
		unit  string
		err   error
	}{
		{err: errors.New("ha unreachable")},
		{state: "unavailable", unit: "kW"},
		{state: "NaN", unit: "kW"},
		{state: "99", unit: "kW"}, // 99 kW is implausible
		{state: "0", unit: "kWh"},
	}
	for i, c := range cases {
		ha.mu.Lock()
		ha.state, ha.unit, ha.err = c.state, c.unit, c.err
		ha.mu.Unlock()
		h.evPoll(h.at(10, 30+i, 0))
		assert.True(t, h.runner.opt.ev.active, "case %d keeps the guard engaged", i)
	}
	assert.Len(t, h.appliedModes(), 2, "no writes on read failures")
	assert.Equal(t, 1, h.logs.FilterMessage("ev guard: EV power read failing").Len(), "one Warn per failure streak, at the third failure")
	assert.Equal(t, 4, h.logs.FilterMessage("ev guard: EV power read failed").Len(), "the other failures stay at Debug")

	ha.mu.Lock()
	ha.state, ha.unit, ha.err = "0", "kW", nil
	ha.mu.Unlock()
	h.evPoll(h.at(10, 40, 0))
	assert.True(t, h.logged("ev guard: EV power read recovered"))
	assert.False(t, h.runner.opt.ev.active, "released after recovery")
}

func TestEVGuard_T0206_FailureWhileIdleDoesNotEngage(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("", errors.New("timeout"))
	h.evPoll(h.at(10, 21, 0))
	assert.False(t, h.runner.opt.ev.active)
	assert.Len(t, h.appliedModes(), 1)
}

// --- T-0207: write failure suspends the guard (AC-7) -------------------------

func TestEVGuard_T0207_WriteFailureSuspendsUntilNextRun(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	h.writer.mu.Lock()
	h.writer.applyErr = fronius.ErrModbusWrite
	h.writer.applyRestored = true
	h.writer.mu.Unlock()

	ha.set("7", nil)
	h.evPoll(h.at(10, 21, 0))
	assert.False(t, h.runner.opt.ev.active)
	assert.True(t, h.runner.opt.ev.suspended)
	st := h.client.lastState(t)
	assert.Equal(t, optDecisionFallback, st.LastDecision)
	assert.Contains(t, st.OptReasons, string(optimize.ReasonFallbackModbusWriteFailed))

	applies := len(h.appliedModes())
	h.evPoll(h.at(10, 22, 0))
	assert.Len(t, h.appliedModes(), applies, "no further guard writes while suspended")

	h.writer.mu.Lock()
	h.writer.applyErr = nil
	h.writer.mu.Unlock()
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	assert.False(t, h.runner.opt.ev.suspended, "the hourly run clears the suspension")
	h.evPoll(h.at(11, 1, 0))
	assert.True(t, h.runner.opt.ev.active)
}

// --- T-0208: dry run (AC-8) --------------------------------------------------

func TestEVGuard_T0208_DryRunWritesNothing(t *testing.T) {
	h, ha := newSchEVHarness(t, true)
	h.tick()
	ha.set("7", nil)
	h.evPoll(h.at(10, 21, 0))

	assert.True(t, h.runner.opt.ev.active)
	assert.Equal(t, int32(0), h.factory.Load(), "no dispatch writer created")
	entries := h.logs.FilterMessage("dry run: no Modbus registers written").All()
	require.NotEmpty(t, entries)
	assert.Equal(t, optimize.ActionHold, entries[len(entries)-1].ContextMap()["action"])
	st := h.client.lastState(t)
	assert.Equal(t, string(optimize.ActionHold), *st.OptAction)
	assert.True(t, *st.OptDryRun)
}

// --- T-0209: plan check and reason ranking (AC-9) ----------------------------

func TestEVGuard_T0209_PlanCheckCarriesEVGuard(t *testing.T) {
	h, ha := newSchEVHarness(t, true)
	h.tick() // 10:20
	h.clock.set(h.at(11, 0, 0))
	h.tick() // slot 11:00 remembered for the plan check
	ha.set("7", nil)
	h.evPoll(h.at(11, 10, 0))
	h.clock.set(h.at(12, 0, 0))
	h.tick()

	checks := h.logs.FilterMessage("plan check").All()
	require.NotEmpty(t, checks)
	assert.Equal(t, true, checks[len(checks)-1].ContextMap()["ev_guard"])
}

func TestEVGuard_T0209_ReasonRank(t *testing.T) {
	got := optSortReasons([]optimize.Reason{
		optimize.ReasonSelfConsumeOptimal,
		optimize.ReasonPVForecastMissing,
		optimize.ReasonEVGuardHold,
		optimize.ReasonManualOverride,
	})
	assert.Equal(t, []optimize.Reason{
		optimize.ReasonManualOverride,
		optimize.ReasonEVGuardHold,
		optimize.ReasonPVForecastMissing,
		optimize.ReasonSelfConsumeOptimal,
	}, got)
}

// --- T-0210: pause and force_charge release the guard (AC-10) ----------------

func TestEVGuard_T0210_PauseAndForceChargeRelease(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("7", nil)
	h.evPoll(h.at(10, 21, 0))
	require.True(t, h.runner.opt.ev.active)

	h.command(mqtt.IntentPause, nil)
	assert.False(t, h.runner.opt.ev.active)
	applies := len(h.appliedModes())
	h.evPoll(h.at(10, 22, 0))
	assert.Len(t, h.appliedModes(), applies, "no guard writes while paused")

	h.command(mqtt.IntentResume, nil) // runs the optimizer again
	h.evPoll(h.at(10, 23, 0))
	require.True(t, h.runner.opt.ev.active, "guard re-engages after resume")

	h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 100; i.IgnoreMaxCharge = true })
	assert.False(t, h.runner.opt.ev.active)
	applies = len(h.appliedModes())
	h.evPoll(h.at(10, 40, 0))
	assert.Len(t, h.appliedModes(), applies, "no guard writes during the manual override")
}

// --- T-0212/T-0213: stale EV power releases the guard (AC-6 amendment) ------

func TestEVGuard_T0212_StaleReadsReleaseOnPoll(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("7", nil)
	h.evPoll(h.at(10, 21, 0))
	require.True(t, h.runner.opt.ev.active)

	ha.set("", errors.New("charger offline"))
	h.evPoll(h.at(10, 35, 0)) // 14 min without a good read
	assert.True(t, h.runner.opt.ev.active, "short outages keep the guard")
	assert.Len(t, h.appliedModes(), 2)

	h.evPoll(h.at(10, 36, 0)) // 15 min
	assert.False(t, h.runner.opt.ev.active)
	modes := h.appliedModes()
	require.Len(t, modes, 3)
	assert.Equal(t, fronius.DispatchMode(optimize.ActionSelfConsume), modes[2], "the planned action is written again")
	rel := h.logs.FilterMessage("ev guard released").All()
	require.Len(t, rel, 1)
	assert.Equal(t, "ev_power_unknown", rel[0].ContextMap()["release_reason"])
	assert.NotContains(t, h.client.lastState(t).OptReasons, string(optimize.ReasonEVGuardHold))

	// Still offline: no further writes.
	h.evPoll(h.at(10, 37, 0))
	assert.Len(t, h.appliedModes(), 3)
}

func TestEVGuard_T0213_StaleAtHourlyRunAppliesPlan(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.tick()
	ha.set("7", nil)
	h.evPoll(h.at(10, 40, 0))
	require.True(t, h.runner.opt.ev.active)

	ha.set("", errors.New("charger offline"))
	h.evPoll(h.at(10, 50, 0))
	h.clock.set(h.at(11, 0, 0)) // 20 min since the last good read
	h.tick()
	modes := h.appliedModes()
	assert.Equal(t, fronius.DispatchMode(optimize.ActionSelfConsume), modes[len(modes)-1])
	assert.False(t, h.runner.opt.ev.active)
	assert.NotContains(t, h.client.lastPlan(t).Reasons, string(optimize.ReasonEVGuardHold))
}

// --- T-0223: plan check names mid-slot action changes (AC-18b amendment) ----

// The live trial logged a 17:00 slot as "hold" with a -1.3 kWh deviation,
// although the EV guard had released at 17:01 and self_consume ran for the
// rest of the hour. The plan check now lists every later write in the slot.
func TestEVGuard_T0223_PlanCheckListsActionChanges(t *testing.T) {
	h, ha := newSchEVHarness(t, false)
	h.clock.set(h.at(16, 0, 0))
	h.tick()
	ha.set("3.5", nil)
	h.evPoll(h.at(16, 27, 0)) // engaged
	h.evPoll(h.at(16, 59, 0)) // car still charging
	h.clock.set(h.at(17, 0, 0))
	h.tick() // 17:00 slot starts as hold
	ha.set("0", nil)
	h.evPoll(h.at(17, 1, 0)) // released: self_consume for the rest of the hour
	h.clock.set(h.at(18, 0, 0))
	h.tick()

	checks := h.logs.FilterMessage("plan check").All()
	require.Len(t, checks, 2)
	first := checks[0].ContextMap()
	assert.Equal(t, "self_consume", fmt.Sprint(first["action"]), "16:00 slot started as self_consume")
	assert.Equal(t, []any{"16:27 hold (ev_guard_engaged)"}, first["action_changes"])
	last := checks[1].ContextMap()
	assert.Equal(t, "hold", fmt.Sprint(last["action"]), "17:00 slot started as hold")
	assert.Equal(t, []any{"17:01 self_consume (ev_guard_released)"}, last["action_changes"])
}

func TestEVGuard_T0223_NoChangesNoField(t *testing.T) {
	h, _ := newSchEVHarness(t, false)
	h.clock.set(h.at(10, 0, 0))
	h.tick()
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	checks := h.logs.FilterMessage("plan check").All()
	require.Len(t, checks, 1)
	_, ok := checks[0].ContextMap()["action_changes"]
	assert.False(t, ok, "action_changes only appears when the action changed")
}

func TestEVGuard_T0223_ForceChargeIsListed(t *testing.T) {
	h, _ := newSchEVHarness(t, false)
	h.clock.set(h.at(10, 0, 0))
	h.tick()
	h.clock.set(h.at(10, 15, 0))
	h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 100; i.IgnoreMaxCharge = true })
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	checks := h.logs.FilterMessage("plan check").All()
	require.Len(t, checks, 1)
	assert.Equal(t, []any{"10:15 charge (force_charge)"}, checks[0].ContextMap()["action_changes"])
}

// T-0224: repeated identical writes are counted, not listed again (force_charge
// pressed several times in the live trial 2026-10-18 05:37); a different
// action or cause starts a new entry.
func TestEVGuard_T0224_RepeatedChangesCollapse(t *testing.T) {
	h, _ := newSchEVHarness(t, false)
	h.clock.set(h.at(10, 0, 0))
	h.tick()
	force := func(i *mqtt.Intent) { i.TargetPct = 100; i.IgnoreMaxCharge = true }
	for _, m := range []int{15, 16, 17} {
		h.clock.set(h.at(10, m, 0))
		h.command(mqtt.IntentForceCharge, force)
	}
	h.clock.set(h.at(10, 30, 0))
	h.command(mqtt.IntentSetDefaults, nil)
	h.clock.set(h.at(10, 40, 0))
	h.command(mqtt.IntentForceCharge, force)
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	checks := h.logs.FilterMessage("plan check").All()
	require.Len(t, checks, 1)
	changes, ok := checks[0].ContextMap()["action_changes"].([]any)
	require.True(t, ok)
	require.Len(t, changes, 3)
	assert.Equal(t, "10:15 charge (force_charge) x3", changes[0])
	assert.Equal(t, "10:40 charge (force_charge)", changes[2], "not merged across a different entry")
}
