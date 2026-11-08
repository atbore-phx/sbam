package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/homeassistant"
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

// fillRunnerQueue fills the intent queue so the next Submit fails.
func fillRunnerQueue(t *testing.T, r *Runner) {
	t.Helper()
	for len(r.intents) < cap(r.intents) {
		r.intents <- mqtt.Intent{Kind: mqtt.IntentResume}
	}
}

// drainRunnerQueue empties the intent queue and returns what it held.
func drainRunnerQueue(r *Runner) []mqtt.Intent {
	var out []mqtt.Intent
	for {
		select {
		case i := <-r.intents:
			out = append(out, i)
		default:
			return out
		}
	}
}

// Expected use: an hourly tick that meets a full queue is retried on a new
// timer instead of being dropped, so hourly planning continues.
func TestOptimizeRunner_HourlyTickRetriedWhenQueueFull(t *testing.T) {
	h := newSchOptHarness(t, true)
	h.tick()
	hourly := h.hourTimers()
	require.Len(t, hourly, 1)

	fillRunnerQueue(t, h.runner)
	hourly[0].fn()

	retries := h.timers.withDelay(optSubmitRetryDelay)
	require.Len(t, retries, 1, "a retry timer is armed")
	assert.True(t, h.logged("optimizer hourly run deferred because runner queue is full"))

	drainRunnerQueue(h.runner)
	retries[0].fn()
	queued := drainRunnerQueue(h.runner)
	require.Len(t, queued, 1)
	assert.Equal(t, mqtt.IntentTick, queued[0].Kind)
}

// Failure case: retries are bounded; when the queue stays full the intent is
// dropped with an error log.
func TestOptimizeRunner_TimedSubmitGivesUp(t *testing.T) {
	oldRetries := optSubmitRetries
	optSubmitRetries = 2
	t.Cleanup(func() { optSubmitRetries = oldRetries })
	h := newSchOptHarness(t, true)
	fillRunnerQueue(t, h.runner)

	h.runner.optSubmitTimed(mqtt.Intent{Kind: mqtt.IntentEVPoll}, "ev guard poll", 0)
	for i := 0; i < optSubmitRetries; i++ {
		retries := h.timers.withDelay(optSubmitRetryDelay)
		require.Len(t, retries, i+1)
		retries[i].fn()
	}

	assert.Len(t, h.timers.withDelay(optSubmitRetryDelay), optSubmitRetries, "no retry after the last attempt")
	assert.True(t, h.logged("ev guard poll dropped: runner queue stayed full"))
}

// blockingHistoryHA blocks History until its context ends and queues an
// intent first, simulating a command that arrives during a slow rebuild.
type blockingHistoryHA struct {
	inner   haClient
	onBlock func()
	calls   *int
}

func (f blockingHistoryHA) State(ctx context.Context, entity string) (homeassistant.State, error) {
	return f.inner.State(ctx, entity)
}

func (f blockingHistoryHA) History(ctx context.Context, _ string, _, _ time.Time) ([]homeassistant.HistoryPoint, error) {
	*f.calls++
	if f.onBlock != nil {
		f.onBlock()
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// rebuildHarness is a live optimize runner with the real pipeline and a
// profile built yesterday, so the tick is due for its daily rebuild.
func rebuildHarness(t *testing.T, ha func(env *optRunEnv) haClient) (*schOptHarness, *optRunEnv) {
	t.Helper()
	env := newOptRunEnv(t)
	h := newSchOptHarness(t, false)
	optRun = runOptimization
	h.clock.set(env.now)
	o := h.runner.opt
	o.cfg = env.cfg
	o.cfg.DryRun = false
	o.deps = optimizeDeps{HA: ha(env), Forecasts: env.fc, Battery: env.bat}
	yesterday := env.now.Add(-20 * time.Hour)
	o.cache = profileCache{date: optLocalDate(yesterday, env.loc), profile: optStaticProfile(env.cfg), builtAt: yesterday, lastAttempt: yesterday}
	return h, env
}

// Edge case: with a command already queued the rebuild is deferred to the
// next run, and it is not counted as a failed build.
func TestOptimizeRunner_RebuildDeferredWhileIntentsQueued(t *testing.T) {
	calls := 0
	h, env := rebuildHarness(t, func(env *optRunEnv) haClient {
		return blockingHistoryHA{inner: env.ha, calls: &calls}
	})
	// The run itself reads only states; the rebuild is the only History user.
	require.True(t, h.runner.Submit(mqtt.Intent{Kind: mqtt.IntentSetDefaults}))

	h.tick()

	assert.Zero(t, calls, "no history request while a command is queued")
	o := h.runner.opt
	assert.NotEqual(t, optLocalDate(env.now, env.loc), o.cache.date)
	assert.False(t, o.cache.lastFailed)
	assert.True(t, o.cache.needsRebuild(env.now, env.loc), "retried at the next run")
	assert.Len(t, h.runner.intents, 1)
}

// Expected use: a command that arrives during a slow rebuild cancels it
// promptly instead of waiting for the 4-minute deadline.
func TestOptimizeRunner_RebuildYieldsToQueuedIntent(t *testing.T) {
	oldPoll := optRebuildPollInterval
	optRebuildPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { optRebuildPollInterval = oldPoll })
	calls := 0
	var h *schOptHarness
	h, env := rebuildHarness(t, func(env *optRunEnv) haClient {
		return blockingHistoryHA{inner: env.ha, calls: &calls, onBlock: func() {
			h.runner.Submit(mqtt.Intent{Kind: mqtt.IntentHold})
		}}
	})

	start := time.Now()
	h.tick()

	assert.Less(t, time.Since(start), 5*time.Second, "the rebuild yields instead of running to its deadline")
	assert.Equal(t, 1, calls)
	o := h.runner.opt
	assert.False(t, o.cache.lastFailed, "a yielded rebuild is not a failure")
	assert.True(t, o.cache.needsRebuild(env.now, env.loc))
	assert.True(t, h.logged("consumption profile rebuild yielded to a queued command; retrying at the next run"))
	assert.False(t, h.logged("optimizer degraded input"), "a yield is not logged as a failed build")
}

// timerIntentHA submits sbam's own timer intents on the first History call
// and lets the rebuild poll the queue a few times before answering, like the
// EV guard poll landing during a slow rebuild. Like the real client it fails
// when the rebuild was cancelled meanwhile, so a rebuild that gives way to
// the timer intents does not complete.
type timerIntentHA struct {
	inner  haClient
	submit func()
	once   *bool
}

func (f timerIntentHA) State(ctx context.Context, entity string) (homeassistant.State, error) {
	return f.inner.State(ctx, entity)
}

func (f timerIntentHA) History(ctx context.Context, entity string, start, end time.Time) ([]homeassistant.HistoryPoint, error) {
	if !*f.once {
		*f.once = true
		f.submit()
		time.Sleep(20 * optRebuildPollInterval)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return f.inner.History(ctx, entity, start, end)
}

// Regression (2.3.0 beta): with the EV guard enabled an EV poll is queued
// every 60 s. The rebuild must not give way to it (or to the watchdog or a
// limit check), otherwise it never completes and the plan falls back to the
// static profile after 26 h.
func TestOptimizeRunner_RebuildNotInterruptedByTimerIntents(t *testing.T) {
	oldPoll := optRebuildPollInterval
	optRebuildPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { optRebuildPollInterval = oldPoll })
	once := false
	var h *schOptHarness
	h, env := rebuildHarness(t, func(env *optRunEnv) haClient {
		return timerIntentHA{inner: env.ha, once: &once, submit: func() {
			h.runner.Submit(mqtt.Intent{Kind: mqtt.IntentEVPoll})
			h.runner.Submit(mqtt.Intent{Kind: mqtt.IntentWatchdog})
			h.runner.Submit(mqtt.Intent{Kind: mqtt.IntentLimitCheck})
		}}
	})

	h.tick()

	o := h.runner.opt
	assert.True(t, once, "the rebuild ran")
	assert.Equal(t, optLocalDate(env.now, env.loc), o.cache.date, "the profile was rebuilt")
	assert.False(t, o.cache.lastFailed)
	assert.False(t, h.logged("consumption profile rebuild yielded to a queued command; retrying at the next run"))
	assert.Len(t, h.runner.intents, 3, "the timer intents wait and are handled afterwards")
}

// Edge case: an EV poll already queued when the run starts does not defer
// the rebuild either.
func TestOptimizeRunner_RebuildNotDeferredByQueuedEVPoll(t *testing.T) {
	oldPoll := optRebuildPollInterval
	optRebuildPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { optRebuildPollInterval = oldPoll })
	once := false
	h, env := rebuildHarness(t, func(env *optRunEnv) haClient {
		return timerIntentHA{inner: env.ha, once: &once, submit: func() {}}
	})
	require.True(t, h.runner.Submit(mqtt.Intent{Kind: mqtt.IntentEVPoll}))

	h.tick()

	assert.True(t, once, "the rebuild ran")
	assert.Equal(t, optLocalDate(env.now, env.loc), h.runner.opt.cache.date)
}

// queuedCommands counts commands from Submit until they are handled; timer
// intents are not counted, a failed Submit leaves no count, and the count
// never goes below zero.
func TestRunner_QueuedCommandsCount(t *testing.T) {
	r := NewRunner(RunnerConfig{SchedulerMode: "windows", Now: time.Now}, newFakeClient())

	for _, k := range []mqtt.IntentKind{mqtt.IntentTick, mqtt.IntentEVPoll, mqtt.IntentWatchdog, mqtt.IntentLimitCheck} {
		require.True(t, r.Submit(mqtt.Intent{Kind: k}))
	}
	assert.Zero(t, r.queuedCommands.Load(), "timer intents are not counted")

	require.True(t, r.Submit(mqtt.Intent{Kind: mqtt.IntentPause}))
	require.True(t, r.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown}))
	assert.Equal(t, int32(2), r.queuedCommands.Load())

	r.commandDequeued(mqtt.IntentPause)
	r.commandDequeued(mqtt.IntentEVPoll)
	assert.Equal(t, int32(1), r.queuedCommands.Load(), "only commands decrement")
	r.commandDequeued(mqtt.IntentShutdown)
	r.commandDequeued(mqtt.IntentHold) // injected without Submit
	assert.Zero(t, r.queuedCommands.Load(), "never below zero")

	for len(r.intents) < cap(r.intents) {
		r.intents <- mqtt.Intent{Kind: mqtt.IntentTick}
	}
	assert.False(t, r.Submit(mqtt.Intent{Kind: mqtt.IntentForceCharge}))
	assert.Zero(t, r.queuedCommands.Load(), "a failed Submit leaves no count")
}
