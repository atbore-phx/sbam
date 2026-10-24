package optimize

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-0070: TC-004f — implausible SoC or capacity is rejected with
// ErrInvalidBattery (mapped to optimizer_fallback_soc_unavailable).
func TestValidateBattery_T0070(t *testing.T) {
	tests := []struct {
		name     string
		soc, cap float64
		wantErr  bool
	}{
		{"valid", 50, 10000, false},
		{"edge empty", 0, 10000, false},
		{"edge full max capacity", 100, 200000, false},
		{"SoC 150", 150, 10000, true},
		{"SoC NaN", math.NaN(), 10000, true},
		{"SoC negative", -1, 10000, true},
		{"capacity 0", 50, 0, true},
		{"capacity Inf", 50, math.Inf(1), true},
		{"capacity above 200 kWh", 50, 200001, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBattery(tc.soc, tc.cap)
			if tc.wantErr {
				assert.ErrorIs(t, err, ErrInvalidBattery)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// validPlan returns a solved, valid plan with charge, discharge and
// self-consume slots for mutation tests.
func validPlan(t *testing.T) (Plan, Grid, Battery, Params) {
	t.Helper()
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: 0.5, sell: 0.1, cons: 500},
		slotSpec{buy: 4.0, sell: 3.5, cons: 500},
		slotSpec{buy: 1.0, sell: 0.2, cons: 500},
	)
	b := testBattery(t, 50, p)
	pl := mustSolve(t, g, b, p)
	return pl, g, b, p
}

func clonePlan(pl Plan) Plan {
	c := pl
	c.Hours = append([]PlanHour(nil), pl.Hours...)
	return c
}

// T-0071: TC-007a, S-007 — hand-crafted invalid plans are rejected.
func TestValidatePlanInvariants_T0071(t *testing.T) {
	pl, g, b, p := validPlan(t)
	now := g.Hours[0].Start.Add(10 * time.Minute)
	require.NoError(t, ValidatePlan(pl, g, b, p, now))
	require.Equal(t, ActionCharge, pl.Hours[0].Action)
	require.Equal(t, ActionDischarge, pl.Hours[1].Action)

	tests := []struct {
		name   string
		mutate func(*Plan)
		now    time.Time
	}{
		{"empty horizon", func(x *Plan) { x.Hours = nil }, now},
		{"SoC below min", func(x *Plan) {
			x.Hours[1].EndWh = -100
			x.Hours[1].SocEndPct = b.MinSocPct - 1
			x.Hours[2].StartWh = -100
		}, now},
		{"SoC above 100", func(x *Plan) { x.Hours[0].SocEndPct = 101 }, now},
		{"charge rate above limit", func(x *Plan) { x.Hours[0].RateW = p.MaxChargeW + 1 }, now},
		{"discharge rate above limit", func(x *Plan) { x.Hours[1].RateW = p.MaxDischargeW + 1 }, now},
		{"gap between slots", func(x *Plan) {
			x.Hours[2].Start = x.Hours[2].Start.Add(time.Hour)
			x.Hours[2].End = x.Hours[2].End.Add(time.Hour)
		}, now},
		{"slot 0 does not contain now (before)", func(*Plan) {}, g.Hours[0].Start.Add(-time.Second)},
		{"slot 0 does not contain now (at end)", func(*Plan) {}, g.Hours[0].End},
		{"non-finite expected cost", func(x *Plan) { x.ExpectedCost = math.NaN() }, now},
		{"non-finite slot cost", func(x *Plan) { x.Hours[2].Cost = math.Inf(1) }, now},
		{"charge with negative flow", func(x *Plan) { x.Hours[0].Action = ActionDischarge }, now},
		{"discharge with positive flow", func(x *Plan) { x.Hours[0].Action = ActionCharge; x.Hours[1].Action = ActionCharge }, now},
		{"hold with flow", func(x *Plan) { x.Hours[0].Action = ActionHold; x.Hours[0].RateW = 0 }, now},
		{"unknown action", func(x *Plan) { x.Hours[2].Action = "boost" }, now},
		{"energy discontinuity", func(x *Plan) { x.Hours[1].StartWh += 50 }, now},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bad := clonePlan(pl)
			tc.mutate(&bad)
			assert.ErrorIs(t, ValidatePlan(bad, g, b, p, tc.now), ErrInvalidPlan)
		})
	}
}

// T-0071 (edge): self_consume against the surplus sign is rejected.
func TestValidatePlanSelfConsumeSign_T0071(t *testing.T) {
	p := testParams()
	g := testGrid(p, slotSpec{buy: 2, sell: 0.1, cons: 1000})
	b := testBattery(t, 50, p)
	pl := mustSolve(t, g, b, p)
	require.Equal(t, ActionSelfConsume, pl.Hours[0].Action)
	bad := clonePlan(pl)
	bad.Hours[0].EndWh = bad.Hours[0].StartWh + 100
	bad.Hours[0].SocEndPct = b.MinSocPct + bad.Hours[0].EndWh/b.CapacityWh*100
	assert.ErrorIs(t, ValidatePlan(bad, g, b, p, g.Hours[0].Start), ErrInvalidPlan)
}

// T-0072: S-004 — a NaN in any grid field is rejected.
func TestValidateGridNaN_T0072(t *testing.T) {
	p := testParams()
	base := func() Grid {
		return testGrid(p, slotSpec{buy: 1, sell: 0.5, cons: 500, pv: 100}, slotSpec{buy: 1, sell: 0.5, cons: 500})
	}
	require.NoError(t, ValidateGrid(base()))
	nan := math.NaN()
	mutations := map[string]func(*Grid){
		"buy":            func(g *Grid) { g.Hours[1].Buy = nan },
		"sell":           func(g *Grid) { g.Hours[1].Sell = nan },
		"consumption":    func(g *Grid) { g.Hours[0].ConsumptionWh = nan },
		"pv":             func(g *Grid) { g.Hours[0].PVWh = nan },
		"spot":           func(g *Grid) { g.Hours[0].SpotInclVAT = nan },
		"fraction":       func(g *Grid) { g.Hours[0].Fraction = nan },
		"terminal price": func(g *Grid) { g.TerminalPrice = nan },
		"inf buy":        func(g *Grid) { g.Hours[0].Buy = math.Inf(-1) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			g := base()
			mutate(&g)
			assert.ErrorIs(t, ValidateGrid(g), ErrInvalidGrid)
		})
	}
}

// T-0072 (bounds): out-of-range and non-contiguous grids are rejected.
func TestValidateGridBounds_T0072(t *testing.T) {
	p := testParams()
	tests := map[string]func(*Grid){
		"empty":              func(g *Grid) { g.Hours = nil },
		"negative pv":        func(g *Grid) { g.Hours[0].PVWh = -1 },
		"huge consumption":   func(g *Grid) { g.Hours[0].ConsumptionWh = 25001 },
		"buy above bound":    func(g *Grid) { g.Hours[0].Buy = 61 },
		"sell below bound":   func(g *Grid) { g.Hours[0].Sell = -16 },
		"fraction zero":      func(g *Grid) { g.Hours[0].Fraction = 0 },
		"partial later slot": func(g *Grid) { g.Hours[1].Fraction = 0.5 },
		"hour of day 24":     func(g *Grid) { g.Hours[0].HourOfDay = 24 },
		"not contiguous": func(g *Grid) {
			g.Hours[1].Start = g.Hours[1].Start.Add(time.Hour)
			g.Hours[1].End = g.Hours[1].End.Add(time.Hour)
		},
		"two-hour slot": func(g *Grid) { g.Hours[1].End = g.Hours[1].End.Add(time.Hour) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			g := testGrid(p, slotSpec{buy: 1, sell: 0.5, cons: 500}, slotSpec{buy: 1, sell: 0.5, cons: 500})
			mutate(&g)
			assert.ErrorIs(t, ValidateGrid(g), ErrInvalidGrid)
		})
	}
}

// ValidateParams: defaults pass, each bound fails (AC-20, S-016).
func TestValidateParams(t *testing.T) {
	require.NoError(t, ValidateParams(testParams()))
	tests := map[string]func(*Params){
		"efficiency 0":     func(p *Params) { p.Efficiency = 0 },
		"efficiency 1.2":   func(p *Params) { p.Efficiency = 1.2 },
		"efficiency NaN":   func(p *Params) { p.Efficiency = math.NaN() },
		"charge rate 0":    func(p *Params) { p.MaxChargeW = 0 },
		"discharge 1e9":    func(p *Params) { p.MaxDischargeW = 1e9 },
		"min soc 100":      func(p *Params) { p.MinSocPct = 100 },
		"vat 25":           func(p *Params) { p.VATRate = 25 },
		"margin 11":        func(p *Params) { p.SellMargin = 11 },
		"surcharge 11":     func(p *Params) { p.Surcharge[5] = 11 },
		"surcharge Inf":    func(p *Params) { p.Surcharge[0] = math.Inf(1) },
		"max hours 49":     func(p *Params) { p.MaxHours = 49 },
		"steps 1001":       func(p *Params) { p.Steps = 1001 },
		"negative steps":   func(p *Params) { p.Steps = -1 },
		"negative horizon": func(p *Params) { p.MaxHours = -1 },
		"min gain 11":      func(p *Params) { p.MinGain = 11 },
		"wear cost -0.1":   func(p *Params) { p.WearCost = -0.1 },
		"wear cost 6":      func(p *Params) { p.WearCost = 6 },
		"wear cost NaN":    func(p *Params) { p.WearCost = math.NaN() },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := testParams()
			mutate(&p)
			assert.ErrorIs(t, ValidateParams(p), ErrInvalidParams)
		})
	}
	edge := testParams()
	edge.Efficiency, edge.MinSocPct, edge.MaxHours, edge.Steps = 1, 0, 48, 1000
	edge.MinGain, edge.WearCost = MaxMinGain, MaxWearCost
	assert.NoError(t, ValidateParams(edge))
}

// T-0073: TC-006a, S-006a — SoC 12 %, min 10 %, 10 kWh, planned discharge
// 4500 W: rate·Fraction ≤ 0.95·(12−10−1) %·cap; a clamp to 0 becomes standby.
func TestApplyDischargeMargin_T0073(t *testing.T) {
	b, err := NewBattery(12, 10000, 10)
	require.NoError(t, err)

	a, rate := ApplyDischargeMargin(ActionDischarge, 4500, 1, b)
	assert.Equal(t, ActionDischarge, a)
	assert.LessOrEqual(t, rate*1, 0.95*(12-10-1)/100*10000)
	assert.Equal(t, 95.0, rate)

	// Partial slot: the budget is spread over the remaining fraction.
	a, rate = ApplyDischargeMargin(ActionDischarge, 4500, 0.5, b)
	assert.Equal(t, ActionDischarge, a)
	assert.Equal(t, 190.0, rate)

	// Edge: a rate already inside the margin is unchanged.
	a, rate = ApplyDischargeMargin(ActionDischarge, 50, 1, b)
	assert.Equal(t, ActionDischarge, a)
	assert.Equal(t, 50.0, rate)

	// SoC at min + 1 % or below: the clamp reaches 0 and the action is
	// standby (no PV charging, unlike hold; live trial 2026-10-06 10:00).
	low, err := NewBattery(10.5, 10000, 10)
	require.NoError(t, err)
	a, rate = ApplyDischargeMargin(ActionDischarge, 4500, 1, low)
	assert.Equal(t, ActionStandby, a)
	assert.Equal(t, 0.0, rate)

	// Failure input: a non-finite rate never yields a discharge.
	a, rate = ApplyDischargeMargin(ActionDischarge, math.NaN(), 1, b)
	assert.Equal(t, ActionStandby, a)
	assert.Equal(t, 0.0, rate)

	// Other actions pass through.
	a, rate = ApplyDischargeMargin(ActionCharge, 3000, 1, low)
	assert.Equal(t, ActionCharge, a)
	assert.Equal(t, 3000.0, rate)
}
