package optimize

import "math"

// selfConsumeGainThreshold separates "self_consume is strictly the cheapest"
// from "no gain" in currency units over the horizon (ADR-0002).
const selfConsumeGainThreshold = 0.005

// tieEpsilon is the minimum improvement for a later candidate to replace the
// incumbent, so ties go to the least intrusive action (AC-12).
const tieEpsilon = 1e-9

// hourModel is the per-slot data the DP needs, with the rate limits already
// scaled by the slot's Fraction and expressed in SoC steps.
type hourModel struct {
	cons, pv, s float64 // Wh; s = pv − cons
	buy, sell   float64
	fraction    float64
	etaC, etaD  float64 // charge and discharge efficiency, √η each (AC-11d)
	step        float64 // Δ in Wh
	n           int     // number of steps; states are 0..n
	maxUpWh     float64 // Cmax·f: max stored energy per slot (battery-side)
	maxDownWh   float64 // Dmax·f: max discharged energy per slot
	maxUp       int     // maxUpWh in whole steps
	maxDown     int     // maxDownWh in whole steps
}

func newHourModel(h Hour, p Params, step float64, n int) hourModel {
	m := hourModel{
		cons:      h.ConsumptionWh,
		pv:        h.PVWh,
		s:         h.PVWh - h.ConsumptionWh,
		buy:       h.Buy,
		sell:      h.Sell,
		fraction:  h.Fraction,
		etaC:      ChargeEfficiency(p.Efficiency),
		etaD:      DischargeEfficiency(p.Efficiency),
		step:      step,
		n:         n,
		maxUpWh:   p.MaxChargeW * h.Fraction, // battery-side setpoint (AC-11c)
		maxDownWh: p.MaxDischargeW * h.Fraction,
	}
	m.maxUp = int(math.Floor(m.maxUpWh/step + 1e-9))
	m.maxDown = int(math.Floor(m.maxDownWh/step + 1e-9))
	return m
}

// selfConsume returns the target state of the inverter's default behaviour
// from state i: absorb the surplus or cover the deficit, within the rate
// limits and the headroom, rounded to the grid.
func (m hourModel) selfConsume(i int) int {
	e := float64(i) * m.step
	emax := float64(m.n) * m.step
	if m.s >= 0 {
		e = math.Min(e+math.Min(m.etaC*m.s, m.maxUpWh), emax)
	} else {
		e = math.Max(e-math.Min(-m.s/m.etaD, m.maxDownWh), 0)
	}
	j := int(math.Round(e / m.step))
	if j < 0 {
		j = 0
	}
	if j > m.n {
		j = m.n
	}
	return j
}

// candidates appends the realizable target states from state i to dst in the
// fixed evaluation order of ADR-0002: self-consume, hold/standby, charge
// targets nearest first, discharge targets nearest first. It returns the
// extended slice and the self-consume target.
func (m hourModel) candidates(dst []int, i int) ([]int, int) {
	jsc := m.selfConsume(i)
	dst = append(dst, jsc)
	if i != jsc {
		dst = append(dst, i)
	}
	lo := i
	if jsc > lo {
		lo = jsc
	}
	hi := i + m.maxUp
	if hi > m.n {
		hi = m.n
	}
	for j := lo + 1; j <= hi; j++ {
		dst = append(dst, j)
	}
	top := i - 1 // s ≥ 0: any j < i
	if m.s < 0 {
		top = jsc - 1 // s < 0: discharge beyond the deficit
	}
	bottom := i - m.maxDown
	if bottom < 0 {
		bottom = 0
	}
	for j := top; j >= bottom; j-- {
		dst = append(dst, j)
	}
	return dst, jsc
}

// cost returns the cost of one slot for a stored-energy change deltaWh and
// the net grid flow (> 0 import). Storing deltaWh draws deltaWh/η_c;
// releasing |deltaWh| delivers |deltaWh|·η_d (AC-11d).
func (m hourModel) cost(deltaWh float64) (float64, float64) {
	flow := deltaWh * m.etaD
	if deltaWh > 0 {
		flow = deltaWh / m.etaC
	}
	net := m.cons - m.pv + flow
	if net > 0 {
		return net * m.buy / 1000, net
	}
	return net * m.sell / 1000, net
}

// holdMinGainShare is the part of MinGain that hold and standby pay. They
// move no battery energy, so they carry no wear; the small remainder only
// stops the plan switching away from self_consume for pennies (AC-11b).
const holdMinGainShare = 0.1

// penalty returns the decision-only threshold of the transition i → j
// against self_consume (AC-11b). Charge and discharge pay MinGain per hour
// plus WearCost per kWh moved beyond what self_consume would move in the
// same direction; hold and standby pay holdMinGainShare of MinGain, so a
// cheap charge followed by hours of holding is not taxed for the wait.
func (m hourModel) penalty(i, j, jsc int, p Params) float64 {
	var extra int
	switch {
	case j == jsc:
		return 0
	case j == i:
		return holdMinGainShare * p.MinGain * m.fraction
	case j > i:
		extra = j - max(i, jsc)
	default:
		extra = min(i, jsc) - j
	}
	return p.MinGain*m.fraction + p.WearCost*float64(extra)*m.step/1000
}

// transitionAction labels the transition i → j (design §Labelling).
func transitionAction(i, j, jsc int, s float64) Action {
	switch {
	case j == jsc:
		return ActionSelfConsume
	case j == i && s >= 0:
		return ActionStandby
	case j == i:
		return ActionHold
	case j > i:
		return ActionCharge
	default:
		return ActionDischarge
	}
}

// transitionRate derives the battery power of a charge or discharge
// transition, rounded to 1 W and clamped to the limit: ΔE/η/Fraction for
// charge, −ΔE/Fraction for discharge, 0 for the other actions.
func transitionRate(a Action, deltaWh, eta, fraction float64, p Params) float64 {
	switch a {
	case ActionCharge:
		// The Gen24 charge setpoint is the power into the battery (AC-11c);
		// the grid draw deltaWh/eta only enters the cost.
		return math.Min(math.Round(deltaWh/fraction), p.MaxChargeW)
	case ActionDischarge:
		return math.Min(math.Round(-deltaWh/fraction), p.MaxDischargeW)
	default:
		return 0
	}
}

// classifyReason returns the deterministic action reason for slot 0
// (ADR-0002). chosen is the optimal total cost from the start state and
// bestAlt the best total cost among first actions other than self_consume
// (+Inf if none exists).
func classifyReason(pl Plan, chosen, bestAlt float64) Reason {
	if len(pl.Hours) == 0 {
		return ""
	}
	h0 := pl.Hours[0]
	switch h0.Action {
	case ActionCharge:
		if h0.Buy <= 0 {
			return ReasonChargeNegativePrice
		}
		return ReasonChargeAheadOfPeak
	case ActionHold:
		return ReasonHoldForLaterPeak
	case ActionStandby:
		return ReasonStandbyExportPV
	case ActionDischarge:
		for _, h := range pl.Hours[1:] {
			if h.Action == ActionCharge {
				return ReasonDischargeBeforeCheapCharge
			}
		}
		return ReasonExportHighPrice
	default:
		if bestAlt-chosen > selfConsumeGainThreshold {
			return ReasonSelfConsumeOptimal
		}
		return ReasonSelfConsumeNoGain
	}
}
