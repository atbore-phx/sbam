package cmd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"
	u "sbam/src/utils"
)

// EV guard (SPEC-0002): while the EV charges in a self_consume slot, apply
// hold so the battery does not discharge into the car. PV surplus still
// charges the battery under hold.
const (
	// optEVPollInterval is the EV power poll period.
	optEVPollInterval = 60 * time.Second
	// optEVReadTimeout bounds one EV state read.
	optEVReadTimeout = 10 * time.Second
	// optEVReleaseFraction: the guard releases below this fraction of
	// opt_ev_guard_w (hysteresis).
	optEVReleaseFraction = 0.5
	// optEVMinEngaged is the minimum time the guard stays engaged, which
	// bounds the guard's write rate.
	optEVMinEngaged = 5 * time.Minute
	// optEVStaleAfter: an engaged guard releases when no EV power read has
	// succeeded for this long (charger offline), so a lost sensor never
	// keeps the battery held for hours.
	optEVStaleAfter = 15 * time.Minute
	// optEVWarnAfterFailures: consecutive failed reads before a Warn; short
	// charger Wi-Fi drops stay at Debug.
	optEVWarnAfterFailures = 3
)

var errEVPowerUnavailable = errors.New("ev power unavailable")

// optEVGuard is the EV guard state, owned by the runner goroutine.
type optEVGuard struct {
	timer *time.Timer

	active    bool      // hold is applied (or, in dry run, would be) because of the EV
	since     time.Time // when the guard engaged
	suspended bool      // a guard write failed; no guard writes until the next hourly run
	lastOK    time.Time // last successful EV power read
	failures  int       // consecutive failed reads (log throttling)

	// slot is the decision the last hourly run applied for the current slot,
	// before the guard. slotOK is false when the guard may not act on it
	// (fallback, paused, manual override); slotEnd bounds it in time.
	slot    optDecision
	slotOK  bool
	slotEnd time.Time
}

// optEVGuardEnabled reports whether the EV guard is configured.
func (o *optRunnerState) optEVGuardEnabled() bool {
	return o.cfg.EVEntity != "" && o.cfg.EVGuardW > 0 && o.deps.HA != nil
}

// optArmEVPoll (re)arms the EV poll timer when the guard is enabled.
func (r *Runner) optArmEVPoll() {
	o := r.opt
	optStopTimer(&o.ev.timer)
	if !o.optEVGuardEnabled() {
		return
	}
	o.ev.timer = newTimerFunc(optEVPollInterval, func() {
		if !r.Submit(mqtt.Intent{Kind: mqtt.IntentEVPoll}) {
			u.Log.Warn("ev guard poll dropped because runner queue is full")
		}
	})
}

// optEVGuardRelease clears the guard without writing; the caller writes its
// own action (hourly run, pause, set_defaults, force_charge).
func (o *optRunnerState) optEVGuardRelease(why string) {
	if o.ev.active {
		u.Log.Infow("ev guard released", "reason", why)
	}
	o.ev.active = false
	o.ev.since = time.Time{}
}

// optEVGuardIneligible releases the guard and blocks it until the next
// hourly run sets an eligible slot.
func (o *optRunnerState) optEVGuardIneligible(why string) {
	o.optEVGuardRelease(why)
	o.ev.slotOK = false
}

// optEVGuardOnRun is called by an hourly run after it selected the slot's
// decision d and before the gates. It records the slot and returns the
// decision to apply: hold while the guard is active and d is self_consume.
func (r *Runner) optEVGuardOnRun(d optDecision, eligible bool, slotEnd time.Time) optDecision {
	o := r.opt
	o.ev.slot = d
	o.ev.slotOK = eligible && o.optEVGuardEnabled()
	o.ev.slotEnd = slotEnd
	o.ev.suspended = false
	if !o.ev.active {
		return d
	}
	if !o.ev.slotOK || d.action != optimize.ActionSelfConsume {
		o.optEVGuardRelease("slot action is " + string(d.action))
		return d
	}
	if o.optEVStale(r.now()) {
		o.optEVGuardRelease("ev power unknown for " + optEVStaleAfter.String())
		return d
	}
	return optDecision{action: optimize.ActionHold}
}

// optEVPoll reads the EV power and engages or releases the guard.
func (r *Runner) optEVPoll(ctx context.Context) {
	o := r.opt
	r.optArmEVPoll()
	if !o.optEVGuardEnabled() {
		return
	}

	now := r.now()
	w, err := r.optReadEVPower(ctx)
	if err != nil {
		o.ev.failures++
		if o.ev.failures == optEVWarnAfterFailures {
			u.Log.Warnw("ev guard: EV power read failing", "failures", o.ev.failures, "active", o.ev.active, "error", err)
		} else {
			u.Log.Debugw("ev guard: EV power read failed", "failures", o.ev.failures, "error", err)
		}
		if o.ev.active && o.optEVStale(now) {
			r.optEVGuardWrite(ctx, false, now, "release_reason", "ev_power_unknown",
				"unknown_for", now.Sub(o.ev.lastOK).Round(time.Second).String())
		}
		return
	}
	if o.ev.failures >= optEVWarnAfterFailures {
		u.Log.Infow("ev guard: EV power read recovered", "ev_power_w", math.Round(w), "failures", o.ev.failures)
	}
	o.ev.failures = 0
	o.ev.lastOK = now

	threshold := o.cfg.EVGuardW
	paused, _ := r.pauseStateAt(now)
	eligible := o.ev.slotOK && !o.ev.suspended && !paused && o.overrideUntil.IsZero() &&
		o.ev.slot.action == optimize.ActionSelfConsume && now.Before(o.ev.slotEnd)

	switch {
	case !o.ev.active && eligible && w > threshold:
		r.optEVGuardWrite(ctx, true, now, "ev_power_w", math.Round(w), "threshold_w", threshold)
	case o.ev.active && w < threshold*optEVReleaseFraction && now.Sub(o.ev.since) >= optEVMinEngaged:
		r.optEVGuardWrite(ctx, false, now, "release_reason", "ev_stopped", "ev_power_w", math.Round(w),
			"release_below_w", threshold*optEVReleaseFraction)
	default:
		u.Log.Debugw("ev guard poll", "ev_power_w", math.Round(w), "threshold_w", threshold, "active", o.ev.active, "eligible", eligible)
	}
}

// optEVStale reports whether no EV power read has succeeded for
// optEVStaleAfter.
func (o *optRunnerState) optEVStale(now time.Time) bool {
	return now.Sub(o.ev.lastOK) >= optEVStaleAfter
}

// optEVGuardWrite applies hold (engage) or the slot's decision (release),
// or logs the registers in dry run, and publishes the state. fields are
// added to the engaged/released log line.
func (r *Runner) optEVGuardWrite(ctx context.Context, engage bool, now time.Time, fields ...any) {
	o := r.opt
	d := o.ev.slot
	if engage {
		d = optDecision{action: optimize.ActionHold}
	}

	if o.cfg.DryRun {
		r.optLogWouldWrite(d)
	} else {
		applied, failReason, err := r.optDispatch(ctx, d)
		if err != nil {
			o.ev.suspended = true
			o.optEVGuardRelease("write failed")
			r.optNoteActionChange(optimize.ActionSelfConsume, "ev_guard_write_failed")
			u.Log.Warnw("ev guard suspended until the next hourly run", "reason", failReason)
			r.optEVGuardPublish(optimize.ActionSelfConsume, failReason, now)
			return
		}
		d = applied
		cause := "ev_guard_released"
		if engage {
			cause = "ev_guard_engaged"
		}
		r.optNoteActionChange(d.action, cause)
	}

	if engage {
		o.ev.active, o.ev.since = true, now
		if o.planCheck != nil {
			o.planCheck.evGuard = true
		}
		u.Log.Infow("ev guard engaged", append([]any{"action", d.action, "dry_run", o.cfg.DryRun,
			"reason", optimize.ReasonEVGuardHold}, fields...)...)
	} else {
		o.ev.active, o.ev.since = false, time.Time{}
		u.Log.Infow("ev guard released", append([]any{"action", d.action, "dry_run", o.cfg.DryRun}, fields...)...)
	}
	r.optEVGuardPublish(d.action, "", now)
}

// optEVGuardPublish updates the last optimizer state with the applied action
// and the guard reason, then publishes it. fallback is set when a guard
// write failed.
func (r *Runner) optEVGuardPublish(action optimize.Action, fallback optimize.Reason, now time.Time) {
	o := r.opt
	if o.last == nil {
		return
	}
	last := *o.last
	last.action = action
	if action != optimize.ActionCharge && action != optimize.ActionDischarge {
		last.rateW = 0
	}
	reasons := make([]optimize.Reason, 0, len(last.reasons)+2)
	for _, rs := range last.reasons {
		if rs != optimize.ReasonEVGuardHold {
			reasons = append(reasons, rs)
		}
	}
	if o.ev.active {
		reasons = append(reasons, optimize.ReasonEVGuardHold)
	}
	if fallback != "" {
		reasons = append(reasons, fallback)
	}
	last.reasons = optSortReasons(reasons)
	o.last = &last

	reason := ""
	if len(last.reasons) > 0 {
		reason = string(last.reasons[0])
	}
	lastDecision := string(action)
	if fallback != "" {
		lastDecision = optDecisionFallback
	}
	r.publishState(r.newCommandPayload(lastDecision, reason, now))
}

// optReadEVPower reads the EV charger power in W from Home Assistant and
// validates it like the history samples (S-004).
func (r *Runner) optReadEVPower(ctx context.Context) (float64, error) {
	o := r.opt
	rctx, cancel := context.WithTimeout(ctx, optEVReadTimeout)
	defer cancel()
	st, err := o.deps.HA.State(rctx, o.cfg.EVEntity)
	if err != nil {
		return 0, err
	}
	v, ok := optimize.ParseHAFloat(st.State)
	if !ok {
		return 0, fmt.Errorf("%w: state %q", errEVPowerUnavailable, st.State)
	}
	factor, err := optimize.PowerUnitFactor(optAttrString(st.Attributes, "unit_of_measurement"))
	if err != nil {
		return 0, err
	}
	w := v * factor
	if w < optimize.MinPowerSampleW || w > optimize.MaxPowerSampleW {
		return 0, fmt.Errorf("%w: implausible value %v W", errEVPowerUnavailable, w)
	}
	return w, nil
}
