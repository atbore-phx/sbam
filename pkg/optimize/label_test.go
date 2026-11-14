package optimize

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// T-0014: rates are ΔE/η/Fraction for charge and −ΔE/Fraction for discharge,
// rounded to 1 W and clamped to the limit; other actions have no rate.
func TestTransitionRate_T0014(t *testing.T) {
	p := testParams()
	tests := []struct {
		name     string
		action   Action
		deltaWh  float64
		fraction float64
		want     float64
	}{
		// T-0019 / AC-11c: the charge setpoint is the power into the battery,
		// so the rate equals the stored energy per hour (no 1/η).
		{"charge full hour", ActionCharge, 830, 1, 830},
		{"charge partial slot", ActionCharge, 830, 2.0 / 3, 1245},
		{"charge rounded", ActionCharge, 100.4, 1, 100},
		{"charge clamped", ActionCharge, 4510, 1, 4500},
		{"discharge full hour", ActionDischarge, -1234.4, 1, 1234},
		{"discharge partial slot", ActionDischarge, -1000, 0.5, 2000},
		{"discharge clamped", ActionDischarge, -9000, 1, 4500},
		{"hold has no rate", ActionHold, 0, 1, 0},
		{"standby has no rate", ActionStandby, 0, 1, 0},
		{"self_consume has no rate", ActionSelfConsume, 500, 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, transitionRate(tc.action, tc.deltaWh, p.Efficiency, tc.fraction, p))
		})
	}
}

// T-0014: each target class maps to exactly one action.
func TestTransitionAction_T0014(t *testing.T) {
	tests := []struct {
		name       string
		i, j, jsc  int
		surplus    float64
		wantAction Action
	}{
		{"self-consume target", 5, 7, 7, 300, ActionSelfConsume},
		{"self-consume equals i", 5, 5, 5, 0, ActionSelfConsume},
		{"stay with surplus", 5, 5, 7, 300, ActionStandby},
		{"stay with deficit", 5, 5, 3, -300, ActionHold},
		{"up", 5, 9, 7, 300, ActionCharge},
		{"down with surplus", 5, 2, 7, 300, ActionDischarge},
		{"down beyond deficit", 5, 1, 3, -300, ActionDischarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantAction, transitionAction(tc.i, tc.j, tc.jsc, tc.surplus))
		})
	}
}

// T-0026: AC-11d — the round-trip loss is split evenly. A 1000 Wh deficit in
// self-consumption takes 1000/√η from the battery; releasing 1000 Wh delivers
// 1000·√η; storing 1000 Wh draws 1000/√η; the round trip stays η.
func TestSplitEfficiency_T0026(t *testing.T) {
	p := testParams()
	etaC, etaD := ChargeEfficiency(p.Efficiency), DischargeEfficiency(p.Efficiency)
	assert.InDelta(t, 0.911, etaC, 0.001)
	assert.InDelta(t, p.Efficiency, etaC*etaD, 1e-12, "round trip unchanged")

	m := newHourModel(Hour{Fraction: 1, ConsumptionWh: 1000}, p, 1, 9300)
	assert.InDelta(t, 1000/etaD, float64(5000-m.selfConsume(5000)), 1, "deficit drains 1098 Wh")

	_, net := m.cost(-1000) // discharge 1000 Wh from the battery
	assert.InDelta(t, 1000-1000*etaD, net, 1e-9, "delivers 911 Wh to the 1000 Wh load")
	_, net = m.cost(1000) // store 1000 Wh
	assert.InDelta(t, 1000+1000/etaC, net, 1e-9, "draws 1098 Wh for 1000 Wh stored")
}

// T-0014 (edge): the realizable set excludes partial PV storage and partial
// deficit cover (ADR-0002).
func TestCandidatesRealizable_T0014(t *testing.T) {
	p := testParams()
	surplus := newHourModel(Hour{Fraction: 1, PVWh: 1500, ConsumptionWh: 500}, p, 100, 93)
	c, jsc := surplus.candidates(nil, 10)
	assert.Equal(t, 19, jsc) // 1000 Wh·√0.83 = 911 Wh → round(9.11) steps (AC-11d)
	for _, j := range c {
		assert.False(t, j > 10 && j < jsc, "partial PV storage %d", j)
	}
	assert.Equal(t, []int{19, 10}, c[:2], "self-consume first, then standby")

	deficit := newHourModel(Hour{Fraction: 1, ConsumptionWh: 1000}, p, 100, 93)
	c, jsc = deficit.candidates(nil, 20)
	assert.Equal(t, 9, jsc) // 1000 Wh/√0.83 = 1098 Wh → round(10.98) steps (AC-11d)
	for _, j := range c {
		assert.False(t, j > jsc && j < 20, "partial deficit cover %d", j)
	}
	// Edge: an empty battery with a deficit has self-consume == hold.
	c, jsc = deficit.candidates(nil, 0)
	assert.Equal(t, 0, jsc)
	assert.Equal(t, 0, c[0])
	assert.NotContains(t, c[1:], 0)
}

// T-0015: each of the 8 action codes is produced by its scenario.
func TestClassifyReason_T0015(t *testing.T) {
	plan := func(buy0 float64, actions ...Action) Plan {
		pl := Plan{}
		for k, a := range actions {
			buy := 1.0
			if k == 0 {
				buy = buy0
			}
			pl.Hours = append(pl.Hours, PlanHour{Action: a, Buy: buy})
		}
		return pl
	}
	inf := math.Inf(1)
	tests := []struct {
		name    string
		pl      Plan
		chosen  float64
		bestAlt float64
		want    Reason
	}{
		{"charge ahead of peak", plan(1, ActionCharge, ActionDischarge), 0, inf, ReasonChargeAheadOfPeak},
		{"charge negative price", plan(-0.1, ActionCharge), 0, inf, ReasonChargeNegativePrice},
		{"charge zero price", plan(0, ActionCharge), 0, inf, ReasonChargeNegativePrice},
		{"hold", plan(1, ActionHold, ActionDischarge), 0, inf, ReasonHoldForLaterPeak},
		{"standby", plan(1, ActionStandby, ActionSelfConsume), 0, inf, ReasonStandbyExportPV},
		{"discharge then charge", plan(1, ActionDischarge, ActionHold, ActionCharge), 0, inf, ReasonDischargeBeforeCheapCharge},
		{"discharge only", plan(1, ActionDischarge, ActionSelfConsume), 0, inf, ReasonExportHighPrice},
		{"self-consume optimal", plan(1, ActionSelfConsume), 1.0, 1.006, ReasonSelfConsumeOptimal},
		{"self-consume no gain", plan(1, ActionSelfConsume), 1.0, 1.004, ReasonSelfConsumeNoGain},
		{"self-consume no alternative", plan(1, ActionSelfConsume), 1.0, inf, ReasonSelfConsumeOptimal},
		{"empty plan", Plan{}, 0, inf, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyReason(tc.pl, tc.chosen, tc.bestAlt))
		})
	}
}

// T-0015: the solver reports self_consume_optimal when every alternative
// first action is clearly more expensive.
func TestSolveSelfConsumeOptimal_T0015(t *testing.T) {
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: 2.0, sell: 0.1, cons: 1000},
		slotSpec{buy: 2.0, sell: 0.1, cons: 1000},
	)
	pl := mustSolve(t, g, testBattery(t, 50, p), p)
	assert.Equal(t, ActionSelfConsume, pl.Hours[0].Action)
	assert.Equal(t, ReasonSelfConsumeOptimal, pl.Reason)
}
