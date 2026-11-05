package cmd

import (
	"strings"
	"testing"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Expected use: in live optimize mode the hold command is applied through the
// optimizer dispatch path (revert timeout, lastApplied) and starts a manual
// override until the end of the hour, which the next hourly run ends.
func TestOptimizeRunner_HoldLiveDispatchesWithRevert(t *testing.T) {
	h := newSchOptHarness(t, false)
	legacy := &fakeBatteryWriter{}
	h.runner.writer = legacy
	h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
	h.clock.set(h.at(10, 10, 0))

	h.command(mqtt.IntentHold, nil)

	applies, _, _ := h.writer.counts()
	require.Equal(t, 1, applies)
	cmd := h.writer.lastApply()
	assert.Equal(t, fronius.DispatchHold, cmd.Mode)
	assert.Equal(t, fronius.RevertSeconds, cmd.RevertS, "hold carries the revert timeout")
	assert.Equal(t, optimize.ActionHold, h.runner.opt.lastApplied)
	assert.True(t, h.runner.opt.overrideUntil.Equal(h.at(11, 0, 0)))
	assert.Equal(t, 0, legacy.holdCalls, "the legacy hold writer is never used in optimize mode")
	acks := h.client.acks(t)
	require.Len(t, acks, 1)
	assert.True(t, acks[0].Accepted)
	assert.Equal(t, string(optimize.ReasonManualOverride), h.client.lastState(t).LastDecisionReason)

	// 10:20 run during the override: publish only.
	h.clock.set(h.at(10, 20, 0))
	h.tick()
	applies, _, _ = h.writer.counts()
	assert.Equal(t, 1, applies)

	// 11:00 the override has ended: the plan is applied again.
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	applies, _, _ = h.writer.counts()
	assert.Equal(t, 2, applies)
	assert.True(t, h.logged("manual override ended"))
}

// Pause and shutdown release a live hold by restoring defaults.
func TestOptimizeRunner_HoldReleasedByPauseAndShutdown(t *testing.T) {
	t.Run("pause", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.command(mqtt.IntentHold, nil)
		h.command(mqtt.IntentPause, nil)
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 1, defaults)
		assert.Equal(t, optimize.ActionSelfConsume, h.runner.opt.lastApplied)
		assert.True(t, h.runner.opt.overrideUntil.IsZero())
	})
	t.Run("shutdown", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.command(mqtt.IntentHold, nil)
		h.shutdown()
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 1, defaults)
	})
}

// Edge case: in dry run the hold command writes nothing and is acknowledged.
func TestOptimizeRunner_HoldDryRunWritesNothing(t *testing.T) {
	h := newSchOptHarness(t, true)
	legacy := &fakeBatteryWriter{}
	h.runner.writer = legacy

	h.command(mqtt.IntentHold, nil)
	h.shutdown()

	assert.Equal(t, int32(0), h.factory.Load(), "the dispatch writer is never created in dry run")
	assert.Equal(t, 0, legacy.holdCalls)
	assert.True(t, h.runner.opt.overrideUntil.IsZero())
	acks := h.client.acks(t)
	require.Len(t, acks, 1)
	assert.True(t, acks[0].Accepted)
	assert.Equal(t, "dry run: hold not written", h.client.lastState(t).LastDecisionReason)
}

// Failure cases: hold is rejected while paused, and a write failure is
// acknowledged as failed without starting an override.
func TestOptimizeRunner_HoldFailures(t *testing.T) {
	t.Run("paused", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.command(mqtt.IntentPause, nil)
		h.command(mqtt.IntentHold, nil)
		applies, _, _ := h.writer.counts()
		assert.Equal(t, 0, applies)
		acks := h.client.acks(t)
		require.Len(t, acks, 2)
		assert.False(t, acks[1].Accepted)
	})
	t.Run("write failure", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.writer.applyErr = fronius.ErrModbusWrite
		h.writer.applyRestored = true
		h.command(mqtt.IntentHold, nil)
		acks := h.client.acks(t)
		require.Len(t, acks, 1)
		assert.False(t, acks[0].Accepted)
		assert.True(t, h.runner.opt.overrideUntil.IsZero())
		assert.Equal(t, optimize.ActionSelfConsume, h.runner.opt.lastApplied, "defaults restored by the dispatch path")
	})
}

// Every intent kind is handled deliberately in optimize mode: none falls
// through to the legacy handlers (which write without the revert timeout and
// ignore dry run), and none reaches the "unsupported" default. A new kind
// added to mqtt.IntentKinds fails this test until it gets a case.
func TestOptimizeRunner_EveryIntentKindHandled(t *testing.T) {
	for _, kind := range mqtt.IntentKinds() {
		t.Run(string(kind), func(t *testing.T) {
			schOptForceChargeStorage(t)
			h := newSchOptHarness(t, true)
			legacy := &fakeBatteryWriter{}
			h.runner.writer = legacy

			intent := mqtt.Intent{Kind: kind, CommandTopic: "sbam/cmd/" + string(kind)}
			if kind == mqtt.IntentForceCharge {
				intent.TargetPct = 50
			}
			handled := h.runner.handleOptimizeIntent(t.Context(), intent)

			assert.True(t, handled)
			assert.Zero(t, legacy.holdCalls+legacy.forceChargeCalls+legacy.setDefaultsCalls, "legacy battery writer used")
			for _, a := range h.client.acks(t) {
				assert.False(t, strings.Contains(a.Error, errUnsupportedIntent.Error()), "kind %q is not handled in optimize mode", kind)
			}
			for _, m := range h.client.bySuffix("/error") {
				assert.NotContains(t, string(m.payload), errUnsupportedIntent.Error())
			}
		})
	}
}

// The default branch rejects an unknown kind instead of falling through.
func TestOptimizeRunner_UnknownIntentRejected(t *testing.T) {
	h := newSchOptHarness(t, true)
	legacy := &fakeBatteryWriter{}
	h.runner.writer = legacy

	handled := h.runner.handleOptimizeIntent(t.Context(), mqtt.Intent{Kind: "bogus", CommandTopic: "sbam/cmd/bogus"})

	assert.True(t, handled)
	acks := h.client.acks(t)
	require.Len(t, acks, 1)
	assert.False(t, acks[0].Accepted)
	assert.Contains(t, acks[0].Error, errUnsupportedIntent.Error())
	assert.Zero(t, legacy.holdCalls+legacy.forceChargeCalls+legacy.setDefaultsCalls)
}
