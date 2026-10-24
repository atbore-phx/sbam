package optimize

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bruteForce enumerates every path over the realizable transitions and
// returns the minimum Σ cost − terminal value.
func bruteForce(g Grid, b Battery, p Params) float64 {
	n := p.steps()
	step := b.EmaxWh / float64(n)
	models := make([]hourModel, len(g.Hours))
	for k, h := range g.Hours {
		models[k] = newHourModel(h, p, step, n)
	}
	var walk func(k, i int) float64
	walk = func(k, i int) float64 {
		if k == len(models) {
			return -g.TerminalPrice * float64(i) * step / 1000
		}
		cands, _ := models[k].candidates(nil, i)
		best := math.Inf(1)
		for _, j := range cands {
			c, _ := models[k].cost(float64(j-i) * step)
			best = math.Min(best, c+walk(k+1, j))
		}
		return best
	}
	return walk(0, int(math.Round(b.E0Wh/step)))
}

// T-0001: AC-8 — on random grids (T ≤ 6, Steps = 10, fixed seeds) the DP cost
// equals the exhaustive optimum over the same realizable transitions.
func TestSolveMatchesBruteForce_T0001(t *testing.T) {
	p := testParams()
	p.Steps = 10
	for seed := int64(1); seed <= 12; seed++ {
		r := rand.New(rand.NewSource(seed))
		g := randomGrid(r, p, 1+r.Intn(6))
		b := testBattery(t, r.Float64()*100, p)
		pl := mustSolve(t, g, b, p)
		want := bruteForce(g, b, p)
		require.False(t, math.IsInf(want, 0))
		// Same discretization: exact up to float rounding, well inside the
		// one-Δ cost tolerance of AC-8.
		stepTol := pl.StepWh / p.Efficiency * 4 / 1000
		assert.InDelta(t, want, pl.ExpectedCost, math.Min(1e-6, stepTol), "seed %d", seed)
	}
}

// T-0001 (edge): a 1-slot grid with an empty battery and no PV.
func TestSolveMatchesBruteForceSingleSlot_T0001(t *testing.T) {
	p := testParams()
	p.Steps = 10
	g := testGrid(p, slotSpec{buy: 1, sell: 0.5, cons: 700})
	b := testBattery(t, 7, p)
	pl := mustSolve(t, g, b, p)
	assert.InDelta(t, bruteForce(g, b, p), pl.ExpectedCost, 1e-9)
}
