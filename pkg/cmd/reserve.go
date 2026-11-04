package cmd

import (
	"time"

	pw "sbam/pkg/power"
	u "sbam/src/utils"
)

// reserveTelemetry is the last reserve evaluation, repeated on every state
// published while the reserve window is active.
type reserveTelemetry struct {
	targetWh, deficitWh float64
	atRisk              bool
}

// windowReserve returns whether the active window holds the battery and the
// reserve target for this tick: pw_batt_reserve, or the until_solar reserve
// when the window asks for it.
func (r *Runner) windowReserve(now time.Time, active *pw.Window, capacityMax float64, sites []pw.Forecasts) (hold bool, reserveWh float64) {
	if active == nil {
		return false, r.cfg.PWBattReserve
	}
	hold = active.HoldBattery != nil && *active.HoldBattery
	if active.ReserveHorizon == pw.ReserveHorizonUntilSolar {
		return hold, r.untilSolarReserve(now, *active, capacityMax, sites)
	}
	return hold, r.cfg.PWBattReserve
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

// untilSolarReserve returns the battery reserve (Wh) for a window with
// reserve_horizon until_solar: enough to keep the base load running from the
// window end until the forecast PV output covers it, on top of the minimum
// SoC the inverter keeps. It never returns less than pw_batt_reserve, and
// falls back to it when no forecast is available.
func (r *Runner) untilSolarReserve(now time.Time, w pw.Window, capacityMax float64, sites []pw.Forecasts) float64 {
	static := r.cfg.PWBattReserve
	if len(sites) == 0 {
		u.Log.Warn("reserve_horizon until_solar: no forecast available, using pw_batt_reserve")
		return static
	}
	windowEnd, err := nextClockAt(now, w.End)
	if err != nil {
		u.Log.Warnw("reserve_horizon until_solar: cannot parse window end, using pw_batt_reserve", "end", w.End, "error", err)
		return static
	}
	baseLoad := r.cfg.PWBaseLoad
	if baseLoad <= 0 {
		baseLoad = r.cfg.PWConsumption / 24
	}
	gap, err := pw.SolarGapEnergy(sites, windowEnd, baseLoad)
	if err != nil {
		u.Log.Warnw("reserve_horizon until_solar: cannot read forecast periods, using pw_batt_reserve", "error", err)
		return static
	}
	if !gap.Covered {
		u.Log.Warnw("reserve_horizon until_solar: forecast PV never covers the base load within 24 h of the window end", "base_load_w", baseLoad)
	}
	effective := max(static, pw.UntilSolarReserve(capacityMax, r.cfg.BattMinSoC, gap))
	if capacityMax > 0 {
		effective = min(effective, capacityMax)
	}
	u.Log.Infow("reserve_horizon until_solar",
		"window_end", windowEnd.Format(time.RFC3339),
		"solar_covers_base_load_at", gap.CoveredAt.Format(time.RFC3339),
		"base_load_w", baseLoad,
		"gap_wh", int(gap.EnergyWh),
		"min_soc_wh", int(capacityMax*r.cfg.BattMinSoC/100),
		"reserve_wh", int(effective))
	return effective
}

// nextClockAt returns the next occurrence of the HH:MM clock value at or
// after now, in now's location.
func nextClockAt(now time.Time, clock string) (time.Time, error) {
	t, err := parseScheduleClock(clock, "end")
	if err != nil {
		return time.Time{}, err
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if at.Before(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, nil
}
