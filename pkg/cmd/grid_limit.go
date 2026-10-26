package cmd

import (
	"context"
	"fmt"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	pw "sbam/pkg/power"
	"sbam/pkg/storage"
	u "sbam/src/utils"
)

// readPowerFlow reads the site power flow (grid and battery). Tests replace it.
var readPowerFlow = storage.ReadPowerFlow

// limitCheckInterval is how often grid_import_limit is re-applied while a
// force charge is running. Tests replace it.
var limitCheckInterval = 30 * time.Second

// limitProbeDelay is how long after a tick's charge the first check runs.
// With battery priority the charge is claimed before other loads yield, so
// a load that does not throttle is caught within this delay. Tests replace it.
var limitProbeDelay = 10 * time.Second

// Grid charge priority between the battery and other loads under
// grid_import_limit.
const (
	// GridChargePriorityBattery claims the charge power the battery needs and
	// only backs off when the site import actually exceeds the limit, i.e.
	// when another load does not throttle itself. Loads that throttle to the
	// meter (dynamic EV chargers) yield to the battery.
	GridChargePriorityBattery = "battery"
	// GridChargePriorityLoads charges only with the power other loads leave
	// free under the limit.
	GridChargePriorityLoads = "loads"
)

// limitRewriteStepPct is the smallest change in charge percent that is
// written to the inverter by the limit loop.
const limitRewriteStepPct = 5

// limitSession is the force charge the limit loop keeps within
// grid_import_limit until the next tick replaces or ends it.
type limitSession struct {
	needWh     float64 // energy the tick asked for, as passed to SetChargePower
	ceilingW   float64 // max_charge of the active window
	capacityWh float64
	hold       bool  // write hold instead of defaults when the allowance drops to 0
	lastPct    int16 // percent last written
	battery    bool  // GridChargePriorityBattery: control on the measured import
}

// batteryPriority reports whether the battery claims its charge power
// before other loads (the default) under grid_import_limit.
func (r *Runner) batteryPriority() bool {
	return r.cfg.GridChargePriority != GridChargePriorityLoads
}

// gridChargeAllowance returns the battery charge power (W) a tick may use,
// capped at ceilingW, and the power free under the limit for telemetry.
// With the limit disabled it returns ceilingW. With battery priority the
// tick claims ceilingW and the limit loop backs off if the site import
// exceeds the limit; with loads priority it gets only the free power.
// When the power flow cannot be read, ok is false and no grid charge is
// allowed: charging blind could exceed the contracted power.
func (r *Runner) gridChargeAllowance(ceilingW float64) (capW float64, allowanceW *float64, ok bool) {
	if r.cfg.GridImportLimit <= 0 {
		return ceilingW, nil, true
	}
	pf, err := readPowerFlow(r.cfg.FroniusIP)
	if err != nil {
		u.Log.Warnw("grid_import_limit: cannot read the power flow, no grid charge until it is available", "error", err)
		return 0, nil, false
	}
	allowed := max(0, r.cfg.GridImportLimit-r.cfg.GridImportMargin-pf.BaselineImportW())
	if r.batteryPriority() {
		return ceilingW, &allowed, true
	}
	return min(ceilingW, allowed), &allowed, true
}

// batteryPriorityCapW returns the charge power (W) for the next step of a
// battery-priority charge: keep the current rate while the site import is
// within the limit, back off by the excess when it is above, and grow back
// when there is room below limit - margin.
func (r *Runner) batteryPriorityCapW(s *limitSession) (float64, bool) {
	pf, err := readPowerFlow(r.cfg.FroniusIP)
	if err != nil {
		u.Log.Warnw("grid_import_limit: cannot read the power flow, stopping the grid charge", "error", err)
		return 0, false
	}
	limit, margin := r.cfg.GridImportLimit, r.cfg.GridImportMargin
	current := float64(s.lastPct) * s.capacityWh / 100
	switch {
	case pf.GridW > limit:
		return max(0, current-(pf.GridW-limit)-margin), true
	case pf.GridW < limit-margin:
		return min(s.ceilingW, current+(limit-margin-pf.GridW)), true
	default:
		return current, true
	}
}

// neededChargeWh is the energy a charge decision asks for, matching what
// fronius.SetFroniusChargeBatteryMode passes to SetChargePower.
func neededChargeWh(decision fronius.Decision, ps fronius.PowerState, pwUPT float64) float64 {
	switch decision.String() {
	case "forecast_charge":
		return -ps.Net + pwUPT
	case "reserve_charge":
		return -ps.BattReserveNet
	}
	return 0
}

// startLimitLoop replaces any running loop with one that re-applies
// grid_import_limit every limitCheckInterval through the runner queue.
func (r *Runner) startLimitLoop(s limitSession) {
	r.stopLimitLoop()
	r.limit = &s
	stop := make(chan struct{})
	r.limitStop = stop
	interval, probe := limitCheckInterval, limitProbeDelay
	go func() {
		submit := func() {
			select {
			case r.intents <- mqtt.Intent{Kind: mqtt.IntentLimitCheck}:
			default: // queue busy: the next check will catch up
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(probe):
			submit()
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				submit()
			}
		}
	}()
}

// stopLimitLoop ends the limit loop, if any.
func (r *Runner) stopLimitLoop() {
	if r.limitStop != nil {
		close(r.limitStop)
		r.limitStop = nil
	}
	r.limit = nil
}

// handleLimitCheck re-reads the power flow and rewrites the charge percent
// when the allowance moved by at least limitRewriteStepPct.
func (r *Runner) handleLimitCheck(ctx context.Context) {
	s := r.limit
	if s == nil {
		return
	}
	now := r.now()
	if paused, _ := r.pauseStateAt(now); paused {
		r.stopLimitLoop()
		return
	}
	if inWindow, _, _, _, _ := r.resolveActiveWindow(now); !inWindow {
		r.stopLimitLoop()
		return
	}

	var capW float64
	var ok bool
	if s.battery {
		capW, ok = r.batteryPriorityCapW(s)
	} else {
		capW, _, ok = r.gridChargeAllowance(s.ceilingW)
	}
	var pct int16
	if ok {
		pct = max(0, fronius.SetChargePower(s.capacityWh, s.needWh, capW))
	}
	if !limitRewriteNeeded(s.lastPct, pct) {
		return
	}

	var err error
	switch {
	case pct > 0:
		err = r.writer.ForceCharge(r.cfg.FroniusIP, pct)
	case s.hold:
		err = r.writer.Hold(r.cfg.FroniusIP)
	default:
		err = r.writer.ForceCharge(r.cfg.FroniusIP, 0)
	}
	if err != nil {
		r.publishError(ctx, "grid_import_limit", err)
		return
	}
	u.Log.Infow("grid_import_limit: charge adjusted", "from_pct", s.lastPct, "to_pct", pct)
	s.lastPct = pct
}

// limitRewriteNeeded reports whether a new percent is worth a Modbus write:
// a change of at least limitRewriteStepPct, or crossing to or from 0.
func limitRewriteNeeded(last, next int16) bool {
	if (last == 0) != (next == 0) {
		return true
	}
	d := next - last
	if d < 0 {
		d = -d
	}
	return d >= limitRewriteStepPct
}

// reserveRisk reports the reserve deficit and whether it cannot be charged
// before the window ends at the power available now.
func (r *Runner) reserveRisk(now time.Time, active *pw.Window, targetWh, battWh, chargeCapW float64) (deficitWh float64, atRisk bool) {
	deficitWh = max(0, targetWh-battWh)
	if deficitWh == 0 {
		return 0, false
	}
	end := r.cfg.EndHR
	if active != nil {
		end = active.End
	}
	endAt, err := nextClockAt(now, end)
	if err != nil {
		return deficitWh, false
	}
	return deficitWh, deficitWh > chargeCapW*endAt.Sub(now).Hours()
}

// checkGridLimitOptions validates grid_import_limit and grid_import_margin.
func checkGridLimitOptions(limitW, marginW float64) error {
	if limitW < 0 {
		return fmt.Errorf("grid_import_limit must be >= 0, got %.2f", limitW)
	}
	if marginW < 0 {
		return fmt.Errorf("grid_import_margin must be >= 0, got %.2f", marginW)
	}
	if limitW > 0 && marginW >= limitW {
		return fmt.Errorf("grid_import_margin (%.0f W) must be lower than grid_import_limit (%.0f W)", marginW, limitW)
	}
	return nil
}

// checkGridChargePriority validates grid_charge_priority.
func checkGridChargePriority(p string) error {
	switch p {
	case GridChargePriorityBattery, GridChargePriorityLoads, "":
		return nil
	}
	return fmt.Errorf("grid_charge_priority must be %q or %q, got %q", GridChargePriorityBattery, GridChargePriorityLoads, p)
}
