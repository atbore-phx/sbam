package optimize

import (
	"context"
	"fmt"
	"math"
)

// Solve computes the minimum-expected-cost plan over the grid with a backward
// dynamic program over discretized usable energy (ADR-0002). It checks ctx
// once per hour layer; the caller passes a 2 s deadline (S-007). The result
// is deterministic for identical inputs (AC-12).
func Solve(ctx context.Context, g Grid, b Battery, p Params) (Plan, error) {
	if err := ValidateParams(p); err != nil {
		return Plan{}, fmt.Errorf("solve: %w", err)
	}
	if err := ValidateGrid(g); err != nil {
		return Plan{}, fmt.Errorf("solve: %w", err)
	}
	if err := validateBatteryStruct(b); err != nil {
		return Plan{}, fmt.Errorf("solve: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, fmt.Errorf("solve: %w", err)
	}

	n := p.steps()
	step := b.EmaxWh / float64(n)
	t := len(g.Hours)
	models := make([]hourModel, t)
	for k, h := range g.Hours {
		models[k] = newHourModel(h, p, step, n)
	}
	i0 := int(math.Round(b.E0Wh / step))

	next := make([]float64, n+1)
	cur := make([]float64, n+1)
	for i := range next {
		next[i] = -g.TerminalPrice * float64(i) * step / 1000
	}
	policy := make([][]int16, t)
	buf := make([]int, 0, 2*n+2)
	bestAlt := math.Inf(1)

	for k := t - 1; k >= 0; k-- {
		if err := ctx.Err(); err != nil {
			return Plan{}, fmt.Errorf("solve: %w", err)
		}
		m := models[k]
		row := make([]int16, n+1)
		for i := 0; i <= n; i++ {
			var jsc int
			buf, jsc = m.candidates(buf[:0], i)
			best := math.Inf(1)
			bestJ := jsc
			for _, j := range buf {
				c, _ := m.cost(float64(j-i) * step)
				c += next[j]
				// The reason compares against the best forced alternative
				// without its own decision penalty, so "a forced action would
				// gain less than the threshold" is reported as
				// optimizer_self_consume_no_gain rather than _optimal.
				if k == 0 && i == i0 && j != jsc && c < bestAlt {
					bestAlt = c
				}
				// AC-11b: decision-only; label reports real costs.
				c += m.penalty(i, j, jsc, p)
				if c < best-tieEpsilon {
					best, bestJ = c, j
				}
			}
			cur[i] = best
			row[i] = int16(bestJ)
		}
		policy[k] = row
		cur, next = next, cur
	}

	pl := label(g, b, p, models, policy, i0, step)
	pl.Reason = classifyReason(pl, next[i0], bestAlt)
	return pl, nil
}

// label walks the policy forward from i0 and turns each transition into an
// action, a rate and the slot's flows and cost.
func label(g Grid, b Battery, p Params, models []hourModel, policy [][]int16, i0 int, step float64) Plan {
	pl := Plan{
		Hours:       make([]PlanHour, len(g.Hours)),
		StartSocPct: b.SocPct,
		StepWh:      step,
	}
	i := i0
	sum := 0.0
	for k, h := range g.Hours {
		m := models[k]
		j := int(policy[k][i])
		delta := float64(j-i) * step
		a := transitionAction(i, j, m.selfConsume(i), m.s)
		c, net := m.cost(delta)
		endWh := float64(j) * step
		pl.Hours[k] = PlanHour{
			Start:         h.Start,
			End:           h.End,
			HourOfDay:     h.HourOfDay,
			Fraction:      h.Fraction,
			Action:        a,
			RateW:         transitionRate(a, delta, p.Efficiency, h.Fraction, p),
			StartWh:       float64(i) * step,
			EndWh:         endWh,
			SocEndPct:     b.MinSocPct + endWh/b.CapacityWh*100,
			Buy:           h.Buy,
			Sell:          h.Sell,
			ConsumptionWh: h.ConsumptionWh,
			PVWh:          h.PVWh,
			GridWh:        net,
			Cost:          c,
		}
		sum += c
		i = j
	}
	pl.TerminalValue = g.TerminalPrice * float64(i) * step / 1000
	pl.ExpectedCost = sum - pl.TerminalValue
	return pl
}
