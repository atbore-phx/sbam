package optimize

import "math"

// Simulate re-evaluates a plan independently of the solver (ADR-0002
// verification seam). Starting from the battery's continuous E0, it replays
// each slot's action and rate against the grid with continuous energy,
// clamped to [0, Emax], and returns the expected cost (Σ slot cost − terminal
// value) and the usable energy at each slot boundary (len = slots + 1). It
// returns (NaN, nil) if the plan and grid lengths differ.
func Simulate(pl Plan, g Grid, b Battery, p Params) (float64, []float64) {
	if len(pl.Hours) != len(g.Hours) {
		return math.NaN(), nil
	}
	e := b.E0Wh
	soc := make([]float64, 0, len(g.Hours)+1)
	soc = append(soc, e)
	total := 0.0
	for k, h := range g.Hours {
		var delta float64
		delta, e = simulateSlot(pl.Hours[k], h, e, b.EmaxWh, p)
		flow := delta * DischargeEfficiency(p.Efficiency)
		if delta > 0 {
			flow = delta / ChargeEfficiency(p.Efficiency)
		}
		net := h.ConsumptionWh - h.PVWh + flow
		if net > 0 {
			total += net * h.Buy / 1000
		} else {
			total += net * h.Sell / 1000
		}
		soc = append(soc, e)
	}
	return total - g.TerminalPrice*e/1000, soc
}

// simulateSlot returns the stored-energy change and the new energy for one
// slot under the inverter semantics of the planned action.
func simulateSlot(ph PlanHour, h Hour, e, emax float64, p Params) (float64, float64) {
	s := h.PVWh - h.ConsumptionWh
	f := h.Fraction
	etaC, etaD := ChargeEfficiency(p.Efficiency), DischargeEfficiency(p.Efficiency)
	var delta float64
	switch ph.Action {
	case ActionCharge:
		delta = math.Min(ph.RateW*f, emax-e) // battery-side setpoint (AC-11c)
	case ActionDischarge:
		delta = -math.Min(ph.RateW*f, e)
	case ActionStandby:
		delta = 0
	case ActionHold:
		if s > 0 {
			delta = math.Min(math.Min(etaC*s, p.MaxChargeW*f), emax-e)
		}
	default: // self_consume
		if s >= 0 {
			delta = math.Min(math.Min(etaC*s, p.MaxChargeW*f), emax-e)
		} else {
			delta = -math.Min(math.Min(-s/etaD, p.MaxDischargeW*f), e)
		}
	}
	return delta, e + delta
}
