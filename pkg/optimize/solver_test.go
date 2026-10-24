package optimize

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var solverT0 = time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC)

func testParams() Params {
	return Params{
		Efficiency:    0.83,
		MaxChargeW:    4500,
		MaxDischargeW: 4500,
		MinSocPct:     7,
		VATRate:       0.25,
	}
}

// slotSpec is a compact grid slot description for tests.
type slotSpec struct {
	buy, sell, cons, pv float64
}

// testGrid builds a grid of full one-hour UTC slots starting at solverT0.
func testGrid(p Params, specs ...slotSpec) Grid {
	g := Grid{}
	sum := 0.0
	for k, s := range specs {
		start := solverT0.Add(time.Duration(k) * time.Hour)
		g.Hours = append(g.Hours, Hour{
			Start: start, End: start.Add(time.Hour), HourOfDay: start.Hour(), Fraction: 1,
			Buy: s.buy, Sell: s.sell, ConsumptionWh: s.cons, PVWh: s.pv,
		})
		sum += s.buy
	}
	g.TerminalPrice = p.Efficiency * sum / float64(len(specs))
	return g
}

func testBattery(t testing.TB, socPct float64, p Params) Battery {
	t.Helper()
	b, err := NewBattery(socPct, 10000, p.MinSocPct)
	require.NoError(t, err)
	return b
}

func mustSolve(t testing.TB, g Grid, b Battery, p Params) Plan {
	t.Helper()
	pl, err := Solve(context.Background(), g, b, p)
	require.NoError(t, err)
	require.NoError(t, ValidatePlan(pl, g, b, p, g.Hours[0].Start))
	return pl
}

// randomGrid generates a reproducible grid with mixed PV, consumption and
// prices, including negative prices.
func randomGrid(r *rand.Rand, p Params, slots int) Grid {
	specs := make([]slotSpec, slots)
	for k := range specs {
		buy := -0.5 + r.Float64()*4
		specs[k] = slotSpec{
			buy:  buy,
			sell: buy/1.25 - 0.3*r.Float64(),
			cons: 200 + r.Float64()*2500,
			pv:   math.Max(0, -1500+r.Float64()*6000),
		}
	}
	return testGrid(p, specs...)
}

// T-0002: Simulate re-evaluates every solved plan independently; cost and SoC
// trajectory match the solver within the discretization bound.
func TestSimulateCrossCheck_T0002(t *testing.T) {
	p := testParams()
	r := rand.New(rand.NewSource(2))
	for n := 0; n < 30; n++ {
		g := randomGrid(r, p, 1+r.Intn(12))
		b := testBattery(t, 7+r.Float64()*93, p)
		pl := mustSolve(t, g, b, p)
		cost, soc := Simulate(pl, g, b, p)
		require.Len(t, soc, len(g.Hours)+1)

		half := pl.StepWh / 2
		maxP := 0.0
		for _, h := range g.Hours {
			maxP = math.Max(maxP, math.Max(math.Abs(h.Buy), math.Abs(h.Sell)))
		}
		costTol := 1e-6
		for k := 0; k < len(g.Hours); k++ {
			// Each self-consume slot and the initial rounding may add Δ/2.
			socTol := half*float64(k+1) + 1
			assert.InDelta(t, pl.Hours[k].StartWh, soc[k], socTol, "grid %d slot %d start", n, k)
			costTol += (socTol + half) / p.Efficiency * maxP / 1000
		}
		endTol := half*float64(len(g.Hours)+1) + 1
		assert.InDelta(t, pl.Hours[len(g.Hours)-1].EndWh, soc[len(g.Hours)], endTol)
		costTol += g.TerminalPrice * endTol / 1000
		assert.InDelta(t, pl.ExpectedCost, cost, costTol, "grid %d cost", n)
	}
}

// T-0002 (exact): with energies on the grid and no rounding, Simulate equals
// the solver exactly. Lossless (η = 1) keeps every flow on the 100 Wh grid;
// with the split losses of AC-11d discharge targets fall between steps.
func TestSimulateExact_T0002(t *testing.T) {
	p := testParams()
	p.Efficiency = 1
	p.Steps = 93 // Δ = 100 Wh for 10 kWh at 7 % min
	g := testGrid(p,
		slotSpec{buy: 1.0, sell: 0.5, cons: 1000},
		slotSpec{buy: 2.0, sell: 1.0, cons: 1000},
		slotSpec{buy: 3.0, sell: 2.5, cons: 500},
	)
	b := testBattery(t, 37, p) // E0 = 3000 Wh, on the grid
	pl := mustSolve(t, g, b, p)
	cost, soc := Simulate(pl, g, b, p)
	assert.InDelta(t, pl.ExpectedCost, cost, 0.01)
	for k, h := range pl.Hours {
		assert.InDelta(t, h.StartWh, soc[k], 2)
	}

	_, bad := Simulate(Plan{}, g, b, p)
	assert.Nil(t, bad, "length mismatch yields no trajectory")
}

// T-0003: AC-9 — 1.00 then 1.15 with η = 0.83: charging in hour 1 to cover
// hour 2 does not pay, because 1.00/0.83 > 1.15.
func TestEfficiencyNoCharge_T0003(t *testing.T) {
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: 1.00, sell: 0.5, cons: 1000},
		slotSpec{buy: 1.15, sell: 0.5, cons: 1000},
	)
	pl := mustSolve(t, g, testBattery(t, 7, p), p)
	assert.NotEqual(t, ActionCharge, pl.Hours[0].Action)
}

// T-0004: AC-9 — 1.00 then 2.00: charging in hour 1 pays.
func TestEfficiencyCharge_T0004(t *testing.T) {
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: 1.00, sell: 0.5, cons: 1000},
		slotSpec{buy: 2.00, sell: 0.5, cons: 1000},
	)
	pl := mustSolve(t, g, testBattery(t, 7, p), p)
	assert.Equal(t, ActionCharge, pl.Hours[0].Action)
	assert.Equal(t, ReasonChargeAheadOfPeak, pl.Reason)
}

// T-0016: AC-11b — a forced action needs a purpose. Charging at 1.00 to
// cover 1 kWh at 1.30 gains only ~0.10 DKK (1.30 − 1.00/0.83), below a 0.50
// DKK/h minimum gain; covering 3 kWh at 3.00 gains several DKK and still
// charges. MinGain = 0 keeps the original behaviour.
func TestMinGainSuppressesMarginalActions_T0016(t *testing.T) {
	small := []slotSpec{{buy: 1.00, sell: 0.5, cons: 1000}, {buy: 1.30, sell: 0.5, cons: 1000}}
	large := []slotSpec{{buy: 1.00, sell: 0.5, cons: 1000}, {buy: 3.00, sell: 0.5, cons: 3000}}

	p := testParams()
	pl := mustSolve(t, testGrid(p, small...), testBattery(t, 7, p), p)
	assert.Equal(t, ActionCharge, pl.Hours[0].Action, "MinGain 0: even a small gain charges")

	p.MinGain = 0.50
	pl = mustSolve(t, testGrid(p, small...), testBattery(t, 7, p), p)
	assert.Equal(t, ActionSelfConsume, pl.Hours[0].Action, "gain below MinGain: self_consume")
	assert.Equal(t, ReasonSelfConsumeNoGain, pl.Reason, "a forced action would gain, but less than MinGain")

	pl = mustSolve(t, testGrid(p, large...), testBattery(t, 7, p), p)
	assert.Equal(t, ActionCharge, pl.Hours[0].Action, "a gain of several DKK still charges")
}

// T-0017: AC-11b — the threshold is decision-only. Reported costs stay real
// (sum of slot costs minus terminal value), the real expected cost never
// beats the unpenalized optimum, and a higher MinGain never adds forced hours.
func TestMinGainKeepsRealCosts_T0017(t *testing.T) {
	r := rand.New(rand.NewSource(17))
	forced := func(pl Plan) int {
		n := 0
		for _, h := range pl.Hours {
			if h.Action != ActionSelfConsume {
				n++
			}
		}
		return n
	}
	for n := 0; n < 30; n++ {
		p := testParams()
		g := randomGrid(r, p, 2+r.Intn(10))
		b := testBattery(t, 7+r.Float64()*93, p)
		base := mustSolve(t, g, b, p)

		p.MinGain = 0.50
		p.WearCost = 0.3 * r.Float64()
		pl := mustSolve(t, g, b, p)
		sum := 0.0
		for _, h := range pl.Hours {
			sum += h.Cost
		}
		assert.InDelta(t, sum-pl.TerminalValue, pl.ExpectedCost, 1e-9, "grid %d: no penalty in ExpectedCost", n)
		assert.GreaterOrEqual(t, pl.ExpectedCost, base.ExpectedCost-1e-9, "grid %d: real cost never below the unpenalized optimum", n)
		assert.LessOrEqual(t, forced(pl), forced(base), "grid %d: MinGain never adds forced hours", n)
	}
}

// T-0093: AC-11b — hold pays only a tenth of MinGain. Charging ~4.5 kWh at
// 1.00 for a 4.00 spike three hours later beats charging at 1.10 just before
// it by ~0.45 DKK. With the full MinGain on each of the two hold hours (1.00)
// the charge moved to the dearer hour 2; now it stays in the cheapest one.
func TestChargeCheapestThenHold_T0093(t *testing.T) {
	p := testParams()
	p.MinGain = 0.50
	g := testGrid(p,
		slotSpec{buy: 1.00, sell: 0.5, cons: 300},
		slotSpec{buy: 1.10, sell: 0.5, cons: 300},
		slotSpec{buy: 1.10, sell: 0.5, cons: 300},
		slotSpec{buy: 4.00, sell: 0.5, cons: 4000},
		slotSpec{buy: 0.50, sell: 0.2, cons: 300},
		slotSpec{buy: 0.50, sell: 0.2, cons: 300},
	)
	pl := mustSolve(t, g, testBattery(t, 7, p), p)
	assert.Equal(t, ActionCharge, pl.Hours[0].Action, "charge in the cheapest hour")
	assert.Equal(t, ActionHold, pl.Hours[1].Action)
	assert.Equal(t, ActionHold, pl.Hours[2].Action)
	assert.Equal(t, ReasonChargeAheadOfPeak, pl.Reason)
}

// T-0094: AC-11a, AC-11b — morning standby that gains ~0.30 DKK (export
// 1.5 kWh at 0.60 instead of storing it, while midday PV fills the battery
// anyway and its surplus sells at 0.40) is chosen with MinGain 0.50: the
// gain is above the hold/standby share, though below the full MinGain.
func TestStandbyBelowMinGain_T0094(t *testing.T) {
	p := testParams()
	p.MinGain = 0.50
	g := testGrid(p,
		slotSpec{buy: 1.0, sell: 0.6, cons: 500, pv: 2000},
		slotSpec{buy: 0.5, sell: 0.4, cons: 500, pv: 8000},
		slotSpec{buy: 0.5, sell: 0.4, cons: 500, pv: 8000},
		slotSpec{buy: 0.5, sell: 0.4, cons: 500, pv: 8000},
		slotSpec{buy: 3.0, sell: 1.5, cons: 2000},
		slotSpec{buy: 3.0, sell: 1.5, cons: 2000},
	)
	pl := mustSolve(t, g, testBattery(t, 7, p), p)
	assert.Equal(t, ActionStandby, pl.Hours[0].Action)
	assert.Equal(t, ReasonStandbyExportPV, pl.Reason)

	p.MinGain = 5 // the share (0.50) now exceeds the gain
	pl = mustSolve(t, g, testBattery(t, 7, p), p)
	assert.Equal(t, ActionSelfConsume, pl.Hours[0].Action)
	assert.Equal(t, ReasonSelfConsumeNoGain, pl.Reason)
}

// T-0095: AC-11b — the wear cost scales with the energy moved. Charging
// ~1.1 kWh at 1.00 to cover 1 kWh at 1.50 gains ~0.30 DKK: it passes a wear
// cost of 0.15 DKK/kWh (~0.16) but not 0.50 DKK/kWh (~0.55). A spike worth
// several DKK still charges with the higher wear cost.
func TestWearCostScalesWithEnergy_T0095(t *testing.T) {
	small := []slotSpec{{buy: 1.00, sell: 0.5, cons: 1000}, {buy: 1.50, sell: 0.5, cons: 1000}}
	large := []slotSpec{{buy: 1.00, sell: 0.5, cons: 1000}, {buy: 3.00, sell: 0.5, cons: 3000}}

	p := testParams()
	p.WearCost = 0.15
	pl := mustSolve(t, testGrid(p, small...), testBattery(t, 7, p), p)
	assert.Equal(t, ActionCharge, pl.Hours[0].Action, "gain above the wear cost")

	p.WearCost = 0.50
	pl = mustSolve(t, testGrid(p, small...), testBattery(t, 7, p), p)
	assert.Equal(t, ActionSelfConsume, pl.Hours[0].Action, "gain below the wear cost")
	assert.Equal(t, ReasonSelfConsumeNoGain, pl.Reason)

	pl = mustSolve(t, testGrid(p, large...), testBattery(t, 7, p), p)
	assert.Equal(t, ActionCharge, pl.Hours[0].Action, "a large gain still charges")
}

// T-0096: AC-11b — the penalty per transition: none for self_consume, a
// tenth of MinGain for hold/standby, MinGain plus wear on the energy moved
// beyond self_consume for charge and discharge.
func TestTransitionPenalty_T0096(t *testing.T) {
	p := Params{MinGain: 0.5, WearCost: 0.2}
	m := hourModel{fraction: 1, step: 100}
	// Surplus hour: self_consume would store i=10 → jsc=14.
	assert.Equal(t, 0.0, m.penalty(10, 14, 14, p), "self_consume")
	assert.InDelta(t, 0.05, m.penalty(10, 10, 14, p), 1e-12, "standby")
	assert.InDelta(t, 0.5+0.2*0.3, m.penalty(10, 17, 14, p), 1e-12, "charge: 300 Wh beyond self_consume")
	assert.InDelta(t, 0.5+0.2*0.4, m.penalty(10, 6, 14, p), 1e-12, "discharge: 400 Wh out of the battery")
	// Deficit hour: self_consume would discharge i=10 → jsc=8.
	assert.InDelta(t, 0.05, m.penalty(10, 10, 8, p), 1e-12, "hold")
	assert.InDelta(t, 0.5+0.2*0.3, m.penalty(10, 13, 8, p), 1e-12, "charge: 300 Wh into the battery")
	assert.InDelta(t, 0.5+0.2*0.5, m.penalty(10, 3, 8, p), 1e-12, "discharge: 500 Wh beyond the deficit")
	// A partial first slot scales the per-hour part, not the wear.
	m.fraction = 0.5
	assert.InDelta(t, 0.25+0.2*0.3, m.penalty(10, 13, 8, p), 1e-12)
	assert.InDelta(t, 0.025, m.penalty(10, 10, 8, p), 1e-12)
	// Both off: no threshold at all.
	assert.Equal(t, 0.0, m.penalty(10, 13, 8, Params{}))
}

// T-0019: AC-11c — the charge rate and limit are battery-side. A cheap hour
// before an expensive one stores up to MaxChargeW (not η·MaxChargeW), the
// dispatched rate equals the stored energy, and the grid import in the cost
// is the stored energy divided by η.
func TestBatterySideChargeRate_T0019(t *testing.T) {
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: 0.50, sell: 0.2, cons: 500},
		slotSpec{buy: 5.00, sell: 0.2, cons: 8000},
	)
	pl := mustSolve(t, g, testBattery(t, 7, p), p)
	h := pl.Hours[0]
	require.Equal(t, ActionCharge, h.Action)
	stored := h.EndWh - h.StartWh
	assert.Greater(t, stored, 0.83*p.MaxChargeW+pl.StepWh, "stores more than the old η·Cmax limit")
	assert.LessOrEqual(t, stored, p.MaxChargeW+1e-6)
	assert.InDelta(t, stored, h.RateW, 1, "rate equals stored energy per hour")
	assert.InDelta(t, 500+stored/ChargeEfficiency(p.Efficiency), h.GridWh, 1e-6, "grid import = consumption + stored/η_c (AC-11d)")
}

// T-0005: AC-10 — 13:00–24:00 with a 17–19 spike and a half-full battery:
// energy is left at the horizon end and the battery is charged before 17:00.
func TestShortHorizonTerminalValue_T0005(t *testing.T) {
	p := testParams()
	specs := make([]slotSpec, 11)
	for k := range specs {
		specs[k] = slotSpec{buy: 1.0, sell: 0.4, cons: 1000}
		if hod := 13 + k; hod >= 17 && hod <= 19 {
			// The spike needs more energy than the half-full battery holds.
			specs[k].buy = 4.0
			specs[k].cons = 2500
		}
	}
	g := testGrid(p, specs...)
	pl := mustSolve(t, g, testBattery(t, 50, p), p)

	assert.Greater(t, pl.Hours[len(pl.Hours)-1].EndWh, 0.0)
	charged := false
	for _, h := range pl.Hours {
		if h.HourOfDay < 17 && h.Action == ActionCharge {
			charged = true
		}
		if h.HourOfDay >= 17 && h.HourOfDay <= 19 {
			// Within one SoC step: discharge targets are discrete and, with
			// discharge losses (AC-11d), rarely land exactly on the deficit.
			assert.LessOrEqual(t, h.GridWh, pl.StepWh, "no import during the spike at %d", h.HourOfDay)
		}
	}
	assert.True(t, charged, "charges ahead of the peak")
}

// T-0006: terminal value and expected cost accounting.
func TestTerminalValueAccounting_T0006(t *testing.T) {
	p := testParams()
	r := rand.New(rand.NewSource(6))
	for n := 0; n < 10; n++ {
		g := randomGrid(r, p, 1+r.Intn(24))
		pl := mustSolve(t, g, testBattery(t, 7+r.Float64()*93, p), p)
		sumBuy, sumCost := 0.0, 0.0
		for k, h := range pl.Hours {
			sumBuy += g.Hours[k].Buy
			sumCost += h.Cost
		}
		eT := pl.Hours[len(pl.Hours)-1].EndWh
		assert.InDelta(t, p.Efficiency*sumBuy/float64(len(g.Hours))*eT/1000, pl.TerminalValue, 1e-9)
		assert.InDelta(t, sumCost-pl.TerminalValue, pl.ExpectedCost, 1e-9)
	}
}

// T-0007: AC-11 — sell × η above every future buy with a full battery:
// discharge in hour 1 at ≤ MaxDischargeW, export valued at the sell price.
func TestExportHighPrice_T0007(t *testing.T) {
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: 3.5, sell: 3.0, cons: 500},
		slotSpec{buy: 1.0, sell: 0.2, cons: 500},
		slotSpec{buy: 1.0, sell: 0.2, cons: 500},
	)
	pl := mustSolve(t, g, testBattery(t, 100, p), p)
	h0 := pl.Hours[0]
	require.Equal(t, ActionDischarge, h0.Action)
	assert.LessOrEqual(t, h0.RateW, p.MaxDischargeW)
	require.Less(t, h0.GridWh, 0.0)
	assert.InDelta(t, h0.GridWh*h0.Sell/1000, h0.Cost, 1e-9)
}

// T-0008: AC-11a — high morning sell price, midday PV that fills the battery
// anyway, evening peak: the morning hour is standby.
func TestStandbyExportsMorningPV_T0008(t *testing.T) {
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: 2.5, sell: 2.0, cons: 500, pv: 2000}, // morning: sell high
		slotSpec{buy: 0.5, sell: 0.1, cons: 500, pv: 8000},
		slotSpec{buy: 0.5, sell: 0.1, cons: 500, pv: 8000},
		slotSpec{buy: 0.5, sell: 0.1, cons: 500, pv: 8000},
		slotSpec{buy: 3.0, sell: 1.5, cons: 2000}, // evening peak
		slotSpec{buy: 3.0, sell: 1.5, cons: 2000},
	)
	pl := mustSolve(t, g, testBattery(t, 7, p), p)
	assert.Equal(t, ActionStandby, pl.Hours[0].Action)
	assert.Equal(t, ReasonStandbyExportPV, pl.Reason)
}

// T-0009: AC-12, TC-007c — identical inputs give deep-equal plans; with all
// prices equal (zero), every slot ties and the least intrusive action wins.
func TestDeterministicTieBreak_T0009(t *testing.T) {
	p := testParams()
	specs := make([]slotSpec, 8)
	for k := range specs {
		specs[k] = slotSpec{cons: 800, pv: float64(k%3) * 700}
	}
	g := testGrid(p, specs...)
	b := testBattery(t, 50, p)
	a := mustSolve(t, g, b, p)
	c := mustSolve(t, g, b, p)
	assert.Equal(t, a, c)
	for _, h := range a.Hours {
		assert.Equal(t, ActionSelfConsume, h.Action, "slot %d", h.HourOfDay)
	}
	assert.Equal(t, ReasonSelfConsumeNoGain, a.Reason)

	r := rand.New(rand.NewSource(9))
	rg := randomGrid(r, p, 24)
	assert.Equal(t, mustSolve(t, rg, b, p), mustSolve(t, rg, b, p))
}

// T-0010: AC-8 — every plan is consistent: flow signs per action, charge
// ≤ Cmax (battery-side, AC-11c), discharge ≤ Dmax, SoC within [min, 100].
func TestPlanConsistency_T0010(t *testing.T) {
	p := testParams()
	r := rand.New(rand.NewSource(10))
	for n := 0; n < 20; n++ {
		g := randomGrid(r, p, 1+r.Intn(36))
		b := testBattery(t, r.Float64()*100, p)
		pl := mustSolve(t, g, b, p)
		for _, h := range pl.Hours {
			d := h.EndWh - h.StartWh
			assert.LessOrEqual(t, d, p.MaxChargeW*h.Fraction+1e-6)
			assert.GreaterOrEqual(t, d, -p.MaxDischargeW*h.Fraction-1e-6)
			assert.GreaterOrEqual(t, h.SocEndPct, p.MinSocPct-1e-9)
			assert.LessOrEqual(t, h.SocEndPct, 100+1e-9)
			switch h.Action {
			case ActionCharge:
				assert.Greater(t, d, 0.0)
			case ActionDischarge:
				assert.Less(t, d, 0.0)
			case ActionHold:
				assert.Equal(t, 0.0, d)
				assert.Less(t, h.PVWh-h.ConsumptionWh, 0.0)
			case ActionStandby:
				assert.Equal(t, 0.0, d)
				assert.GreaterOrEqual(t, h.PVWh-h.ConsumptionWh, 0.0)
			}
		}
	}
}

// T-0011: negative buy price — charge is chosen with the negative-price reason.
func TestNegativePriceCharge_T0011(t *testing.T) {
	p := testParams()
	g := testGrid(p,
		slotSpec{buy: -0.5, sell: -0.8, cons: 500},
		slotSpec{buy: 1.0, sell: 0.5, cons: 500},
		slotSpec{buy: 1.5, sell: 0.8, cons: 500},
	)
	pl := mustSolve(t, g, testBattery(t, 20, p), p)
	assert.Equal(t, ActionCharge, pl.Hours[0].Action)
	assert.Equal(t, ReasonChargeNegativePrice, pl.Reason)
}

// T-0012: TC-007b, S-007 — an expired deadline returns DeadlineExceeded and
// no plan.
func TestSolveDeadline_T0012(t *testing.T) {
	p := testParams()
	g := randomGrid(rand.New(rand.NewSource(12)), p, 36)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	pl, err := Solve(ctx, g, testBattery(t, 50, p), p)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, pl.Hours)
}

// Failure cases: invalid params, grid or battery are rejected before solving.
func TestSolveRejectsInvalidInput(t *testing.T) {
	p := testParams()
	g := testGrid(p, slotSpec{buy: 1, sell: 0.5, cons: 500})
	b := testBattery(t, 50, p)

	bad := p
	bad.Efficiency = 0
	_, err := Solve(context.Background(), g, b, bad)
	assert.ErrorIs(t, err, ErrInvalidParams)

	_, err = Solve(context.Background(), Grid{}, b, p)
	assert.ErrorIs(t, err, ErrInvalidGrid)

	_, err = Solve(context.Background(), g, Battery{}, p)
	assert.ErrorIs(t, err, ErrInvalidBattery)
}

// T-0013: a 36-slot solve with Steps = 200 stays well inside the budget.
func TestSolve36hDuration_T0013(t *testing.T) {
	if raceEnabled {
		t.Skip("timing is not meaningful under -race; run without -race or see BenchmarkSolve36h")
	}
	p := testParams()
	g := randomGrid(rand.New(rand.NewSource(13)), p, 36)
	b := testBattery(t, 50, p)
	// Best of three runs, to be robust against a loaded CI runner.
	best := time.Duration(math.MaxInt64)
	for run := 0; run < 3; run++ {
		start := time.Now()
		_, err := Solve(context.Background(), g, b, p)
		require.NoError(t, err)
		best = min(best, time.Since(start))
	}
	assert.Less(t, best, 500*time.Millisecond)
}

// T-0013: BenchmarkSolve36h guards the O(T·N²) performance (Pi 4 budget 2 s).
func BenchmarkSolve36h(bm *testing.B) {
	p := testParams()
	g := randomGrid(rand.New(rand.NewSource(13)), p, 36)
	b := testBattery(bm, 50, p)
	ctx := context.Background()
	bm.ResetTimer()
	for n := 0; n < bm.N; n++ {
		if _, err := Solve(ctx, g, b, p); err != nil {
			bm.Fatal(err)
		}
	}
}
