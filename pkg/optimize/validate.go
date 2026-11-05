package optimize

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Sentinel errors of the core validators. Callers map them to fallback
// reasons: ErrInvalidBattery → ReasonFallbackSocUnavailable,
// ErrInvalidGrid → ReasonFallbackImplausibleInput,
// ErrInvalidPlan → ReasonFallbackInvalidPlan, ErrNoPricedHours →
// ReasonFallbackMissingPrices.
var (
	ErrInvalidParams  = errors.New("invalid optimizer params")
	ErrInvalidBattery = errors.New("invalid battery state")
	ErrInvalidGrid    = errors.New("invalid grid")
	ErrInvalidPlan    = errors.New("invalid plan")
	ErrNoPricedHours  = errors.New("current hour has no price")
)

// Bounds of the core validation layer (S-004, S-007, S-016).
const (
	maxRateW          = 30000.0
	maxCapacityWh     = 200000.0
	maxSlots          = 48
	maxSteps          = 1000
	maxSlotConsWh     = 25000.0
	maxSlotPVWh       = 200000.0
	minSurcharge      = -5.0
	maxSurcharge      = 10.0
	minSellMargin     = -5.0
	maxSellMargin     = 10.0
	minBuyPrice       = -10.0 // spot [-5, 50] + surcharge [-5, 10]
	maxBuyPrice       = 60.0
	minSellPrice      = -15.0 // spot/(1+vat) - margin
	maxSellPrice      = 55.0
	dischargeSafety   = 0.95 // S-006a
	dischargeFloorPct = 1.0  // S-006a: never below min + 1 %
	planEpsWh         = 1e-6
)

// MaxMinGain is the upper bound of Params.MinGain (opt_min_gain), per hour.
const MaxMinGain = 10.0

// MaxWearCost is the upper bound of Params.WearCost (opt_wear_cost), per kWh.
const MaxWearCost = 5.0

func finiteAll(vals ...float64) bool {
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// ValidateParams checks the optimizer settings (AC-20, S-016).
func ValidateParams(p Params) error {
	if !finiteAll(p.Efficiency, p.MaxChargeW, p.MaxDischargeW, p.MinSocPct, p.VATRate, p.SellMargin, p.MinGain, p.WearCost) {
		return fmt.Errorf("%w: non-finite value", ErrInvalidParams)
	}
	switch {
	case p.Efficiency <= 0 || p.Efficiency > 1:
		return fmt.Errorf("%w: efficiency %v not in (0, 1]", ErrInvalidParams, p.Efficiency)
	case p.MaxChargeW <= 0 || p.MaxChargeW > maxRateW:
		return fmt.Errorf("%w: max charge rate %v W not in (0, %v]", ErrInvalidParams, p.MaxChargeW, maxRateW)
	case p.MaxDischargeW <= 0 || p.MaxDischargeW > maxRateW:
		return fmt.Errorf("%w: max discharge rate %v W not in (0, %v]", ErrInvalidParams, p.MaxDischargeW, maxRateW)
	case p.MinSocPct < 0 || p.MinSocPct >= 100:
		return fmt.Errorf("%w: min SoC %v not in [0, 100)", ErrInvalidParams, p.MinSocPct)
	case p.VATRate < 0 || p.VATRate > 1:
		return fmt.Errorf("%w: VAT rate %v not in [0, 1]", ErrInvalidParams, p.VATRate)
	case p.SellMargin < minSellMargin || p.SellMargin > maxSellMargin:
		return fmt.Errorf("%w: sell margin %v not in [%v, %v]", ErrInvalidParams, p.SellMargin, minSellMargin, maxSellMargin)
	case p.MinGain < 0 || p.MinGain > MaxMinGain:
		return fmt.Errorf("%w: min gain %v not in [0, %v]", ErrInvalidParams, p.MinGain, MaxMinGain)
	case p.WearCost < 0 || p.WearCost > MaxWearCost:
		return fmt.Errorf("%w: wear cost %v not in [0, %v]", ErrInvalidParams, p.WearCost, MaxWearCost)
	case p.MaxHours < 0 || p.MaxHours > maxSlots:
		return fmt.Errorf("%w: max hours %d not in [0, %d]", ErrInvalidParams, p.MaxHours, maxSlots)
	case p.Steps < 0 || p.Steps > maxSteps:
		return fmt.Errorf("%w: steps %d not in [0, %d]", ErrInvalidParams, p.Steps, maxSteps)
	}
	for h, s := range p.Surcharge {
		if !finiteAll(s) || s < minSurcharge || s > maxSurcharge {
			return fmt.Errorf("%w: surcharge[%d] %v not in [%v, %v]", ErrInvalidParams, h, s, minSurcharge, maxSurcharge)
		}
	}
	return nil
}

// ValidateBattery checks the SoC and capacity read from the inverter
// (S-004, TC-004f). Map a failure to ReasonFallbackSocUnavailable.
func ValidateBattery(socPct, capacityWh float64) error {
	if !finiteAll(socPct, capacityWh) {
		return fmt.Errorf("%w: non-finite SoC or capacity", ErrInvalidBattery)
	}
	if socPct < 0 || socPct > 100 {
		return fmt.Errorf("%w: SoC %v not in [0, 100]", ErrInvalidBattery, socPct)
	}
	if capacityWh <= 0 || capacityWh > maxCapacityWh {
		return fmt.Errorf("%w: capacity %v Wh not in (0, %v]", ErrInvalidBattery, capacityWh, maxCapacityWh)
	}
	return nil
}

func validateBatteryStruct(b Battery) error {
	if err := ValidateBattery(b.SocPct, b.CapacityWh); err != nil {
		return err
	}
	if !finiteAll(b.MinSocPct, b.E0Wh, b.EmaxWh) || b.MinSocPct < 0 || b.MinSocPct >= 100 {
		return fmt.Errorf("%w: min SoC or energy not valid", ErrInvalidBattery)
	}
	if b.EmaxWh <= 0 || b.E0Wh < 0 || b.E0Wh > b.EmaxWh+planEpsWh {
		return fmt.Errorf("%w: E0 %v Wh not in [0, %v]", ErrInvalidBattery, b.E0Wh, b.EmaxWh)
	}
	return nil
}

// ValidateGrid asserts that every grid value is finite and within bounds and
// that the slots are contiguous absolute hours (S-004, S-008).
func ValidateGrid(g Grid) error {
	n := len(g.Hours)
	if n == 0 || n > maxSlots {
		return fmt.Errorf("%w: %d slots not in [1, %d]", ErrInvalidGrid, n, maxSlots)
	}
	if !finiteAll(g.TerminalPrice) {
		return fmt.Errorf("%w: non-finite terminal price", ErrInvalidGrid)
	}
	for k, h := range g.Hours {
		if err := validateGridHour(k, h); err != nil {
			return err
		}
		if k > 0 && !h.Start.Equal(g.Hours[k-1].End) {
			return fmt.Errorf("%w: slot %d not contiguous", ErrInvalidGrid, k)
		}
	}
	return nil
}

func validateGridHour(k int, h Hour) error {
	if !finiteAll(h.Fraction, h.SpotInclVAT, h.Buy, h.Sell, h.ConsumptionWh, h.PVWh) {
		return fmt.Errorf("%w: slot %d has a non-finite value", ErrInvalidGrid, k)
	}
	switch {
	case h.End.Sub(h.Start) != time.Hour:
		return fmt.Errorf("%w: slot %d is not one hour", ErrInvalidGrid, k)
	case h.HourOfDay < 0 || h.HourOfDay > 23:
		return fmt.Errorf("%w: slot %d hour of day %d", ErrInvalidGrid, k, h.HourOfDay)
	case h.Fraction <= 0 || h.Fraction > 1 || (k > 0 && h.Fraction != 1):
		return fmt.Errorf("%w: slot %d fraction %v", ErrInvalidGrid, k, h.Fraction)
	case h.ConsumptionWh < 0 || h.ConsumptionWh > maxSlotConsWh:
		return fmt.Errorf("%w: slot %d consumption %v Wh", ErrInvalidGrid, k, h.ConsumptionWh)
	case h.PVWh < 0 || h.PVWh > maxSlotPVWh:
		return fmt.Errorf("%w: slot %d PV %v Wh", ErrInvalidGrid, k, h.PVWh)
	case h.Buy < minBuyPrice || h.Buy > maxBuyPrice:
		return fmt.Errorf("%w: slot %d buy price %v", ErrInvalidGrid, k, h.Buy)
	case h.Sell < minSellPrice || h.Sell > maxSellPrice:
		return fmt.Errorf("%w: slot %d sell price %v", ErrInvalidGrid, k, h.Sell)
	}
	return nil
}

// ValidatePlan checks the invariants of a solved plan before it is applied
// (S-007): at least one slot, contiguous one-hour slots, slot 0 contains now,
// rates within limits, the SoC trajectory within [min, 100], finite costs and
// each action consistent with its energy flow.
func ValidatePlan(pl Plan, g Grid, b Battery, p Params, now time.Time) error {
	n := len(pl.Hours)
	if n == 0 {
		return fmt.Errorf("%w: empty horizon", ErrInvalidPlan)
	}
	if n != len(g.Hours) {
		return fmt.Errorf("%w: %d plan slots for %d grid slots", ErrInvalidPlan, n, len(g.Hours))
	}
	if !finiteAll(pl.ExpectedCost, pl.TerminalValue) {
		return fmt.Errorf("%w: non-finite cost", ErrInvalidPlan)
	}
	first := pl.Hours[0]
	if now.Before(first.Start) || !now.Before(first.End) {
		return fmt.Errorf("%w: slot 0 does not contain now", ErrInvalidPlan)
	}
	for k, h := range pl.Hours {
		if h.End.Sub(h.Start) != time.Hour {
			return fmt.Errorf("%w: slot %d is not one hour", ErrInvalidPlan, k)
		}
		if k > 0 {
			prev := pl.Hours[k-1]
			if !h.Start.Equal(prev.End) {
				return fmt.Errorf("%w: gap before slot %d", ErrInvalidPlan, k)
			}
			if math.Abs(h.StartWh-prev.EndWh) > planEpsWh {
				return fmt.Errorf("%w: energy discontinuity at slot %d", ErrInvalidPlan, k)
			}
		}
		if err := validatePlanHour(k, h, b, p); err != nil {
			return err
		}
	}
	return nil
}

func validatePlanHour(k int, h PlanHour, b Battery, p Params) error {
	if !finiteAll(h.RateW, h.StartWh, h.EndWh, h.SocEndPct, h.GridWh, h.Cost) {
		return fmt.Errorf("%w: slot %d has a non-finite value", ErrInvalidPlan, k)
	}
	if h.StartWh < -planEpsWh || h.EndWh < -planEpsWh || h.EndWh > b.EmaxWh+planEpsWh || h.StartWh > b.EmaxWh+planEpsWh {
		return fmt.Errorf("%w: slot %d energy outside [0, %v] Wh", ErrInvalidPlan, k, b.EmaxWh)
	}
	if h.SocEndPct < b.MinSocPct-1e-6 || h.SocEndPct > 100+1e-6 {
		return fmt.Errorf("%w: slot %d SoC %v outside [%v, 100]", ErrInvalidPlan, k, h.SocEndPct, b.MinSocPct)
	}
	d := h.EndWh - h.StartWh
	switch h.Action {
	case ActionCharge:
		if d <= 0 || h.RateW <= 0 || h.RateW > p.MaxChargeW {
			return fmt.Errorf("%w: slot %d charge flow %v Wh or rate %v W", ErrInvalidPlan, k, d, h.RateW)
		}
	case ActionDischarge:
		if d >= 0 || h.RateW <= 0 || h.RateW > p.MaxDischargeW {
			return fmt.Errorf("%w: slot %d discharge flow %v Wh or rate %v W", ErrInvalidPlan, k, d, h.RateW)
		}
	case ActionHold, ActionStandby:
		if math.Abs(d) > planEpsWh || h.RateW != 0 {
			return fmt.Errorf("%w: slot %d %s with flow %v Wh", ErrInvalidPlan, k, h.Action, d)
		}
	case ActionSelfConsume:
		s := h.PVWh - h.ConsumptionWh
		if h.RateW != 0 || (s >= 0 && d < -planEpsWh) || (s < 0 && d > planEpsWh) {
			return fmt.Errorf("%w: slot %d self_consume flow %v Wh against surplus %v Wh", ErrInvalidPlan, k, d, s)
		}
	default:
		return fmt.Errorf("%w: slot %d unknown action %q", ErrInvalidPlan, k, h.Action)
	}
	return nil
}

// ApplyDischargeMargin applies the dispatch-level SoC margin (S-006a) to the
// command for the applied slot only. A discharge rate is clamped so that
// rate·fraction ≤ 0.95·max(0, E0 − 0.01·capacity), so the battery is never
// pushed below min + 1 %. If the clamp leaves less than 1 W, the action
// becomes standby: like the planned discharge it neither charges the battery
// nor stops PV surplus being exported (hold would let PV charge it, the
// opposite of the plan's intent). Other actions pass through unchanged. The
// plan is not modified.
func ApplyDischargeMargin(action Action, rateW, fraction float64, b Battery) (Action, float64) {
	if action != ActionDischarge {
		return action, rateW
	}
	if !finiteAll(rateW, fraction, b.E0Wh, b.CapacityWh) || fraction <= 0 || rateW <= 0 {
		return ActionStandby, 0
	}
	budgetWh := dischargeSafety * math.Max(0, b.E0Wh-dischargeFloorPct/100*b.CapacityWh)
	limitW := math.Floor(budgetWh / fraction)
	if rateW <= limitW {
		return action, rateW
	}
	if limitW < 1 {
		return ActionStandby, 0
	}
	return action, limitW
}
