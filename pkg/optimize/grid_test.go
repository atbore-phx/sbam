package optimize

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata" // guarantees Europe/Copenhagen on CI images without tzdata

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func copenhagen(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

// hourlyPrices returns n consecutive absolute hours from start with price
// base + k/100.
func hourlyPrices(start time.Time, n int, base float64) []HourPrice {
	out := make([]HourPrice, n)
	for k := range out {
		out[k] = HourPrice{Start: start.Add(time.Duration(k) * time.Hour), PriceInclV: base + float64(k)/100}
	}
	return out
}

func gridInput(now time.Time, loc *time.Location, prices []HourPrice) GridInput {
	p := testParams()
	for h := range p.Surcharge {
		p.Surcharge[h] = float64(h) / 10
	}
	var prof Profile
	for h := range prof {
		prof[h] = 500
	}
	return GridInput{Prices: prices, Profile: prof, SocPct: 50, CapacityWh: 10000, Params: p, Now: now, Loc: loc}
}

// T-0020: TC-008a, S-008 — the 2026-10-25 fall-back day has 25 slots; both
// 02:00 slots use surcharge[2] and keep their own price.
func TestFallBackDay25Slots_T0020(t *testing.T) {
	loc := copenhagen(t)
	midnight := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	in := gridInput(midnight, loc, hourlyPrices(midnight, 25, 1.0))
	g, _, _, err := BuildGrid(in)
	require.NoError(t, err)
	require.Len(t, g.Hours, 25)

	var twos []Hour
	for _, h := range g.Hours {
		if h.HourOfDay == 2 {
			twos = append(twos, h)
		}
	}
	require.Len(t, twos, 2)
	assert.NotEqual(t, twos[0].SpotInclVAT, twos[1].SpotInclVAT)
	for _, h := range twos {
		assert.InDelta(t, h.SpotInclVAT+in.Params.Surcharge[2], h.Buy, 1e-12)
	}
	assert.Equal(t, time.Hour, twos[1].Start.Sub(twos[0].Start))
}

// T-0021: TC-008b — the 2026-03-29 spring-forward day has 23 slots and none
// starts at 02:00 local.
func TestSpringForwardDay23Slots_T0021(t *testing.T) {
	loc := copenhagen(t)
	midnight := time.Date(2026, 3, 29, 0, 0, 0, 0, loc)
	slots, dropped := HourSlots(midnight, loc, hourlyPrices(midnight, 23, 1.0), 36)
	require.Len(t, slots, 23)
	assert.Zero(t, dropped)
	for _, h := range slots {
		assert.NotEqual(t, 2, h.HourOfDay)
	}
	assert.Equal(t, 23, slots[len(slots)-1].HourOfDay)
}

// T-0022: now = 10:20 gives slot 0 Fraction 2/3; consumption, PV and the
// rate limits are scaled by it.
func TestPartialFirstSlot_T0022(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 20, 0, 0, time.UTC)
	start := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	prices := hourlyPrices(start, 3, 0.1)
	prices[1].PriceInclV, prices[2].PriceInclV = 5, 5
	in := gridInput(now, time.UTC, prices)
	in.Profile[10] = 900
	in.SocPct = 7
	in.PV = func(slots []Hour) ([]float64, []Reason) {
		return []float64{1500, 0, 0}, []Reason{ReasonPVForecastMissing}
	}
	g, b, reasons, err := BuildGrid(in)
	require.NoError(t, err)
	assert.Equal(t, []Reason{ReasonPVForecastMissing}, reasons)

	h0 := g.Hours[0]
	assert.InDelta(t, 2.0/3, h0.Fraction, 1e-12)
	assert.InDelta(t, 600, h0.ConsumptionWh, 1e-9)
	assert.InDelta(t, 1000, h0.PVWh, 1e-9)
	assert.Equal(t, 1.0, g.Hours[1].Fraction)
	assert.Equal(t, start, h0.Start)

	pl, err := Solve(context.Background(), g, b, in.Params)
	require.NoError(t, err)
	require.NoError(t, ValidatePlan(pl, g, b, in.Params, now))
	ph := pl.Hours[0]
	require.Equal(t, ActionCharge, ph.Action)
	assert.LessOrEqual(t, ph.EndWh-ph.StartWh, in.Params.MaxChargeW*2/3+1e-6, "battery-side limit (AC-11c)")
	assert.LessOrEqual(t, ph.RateW, in.Params.MaxChargeW)
}

// T-0023: AC-5, Q1 — 40 priced hours give 36 slots and 4 dropped.
func TestHorizonCap_T0023(t *testing.T) {
	now := time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC)
	in := gridInput(now, time.UTC, hourlyPrices(now, 40, 1.0))
	g, _, _, err := BuildGrid(in)
	require.NoError(t, err)
	assert.Len(t, g.Hours, 36)
	assert.Equal(t, 4, g.Dropped)

	// Edge: a gap ends the horizon there, not at the cap.
	prices := hourlyPrices(now, 10, 1.0)
	prices = append(prices[:4], prices[5:]...)
	slots, dropped := HourSlots(now, time.UTC, prices, 36)
	assert.Len(t, slots, 4)
	assert.Zero(t, dropped)
}

// Failure cases: unpriced current hour, nil location, PV length mismatch,
// invalid params.
func TestBuildGridFailures(t *testing.T) {
	now := time.Date(2026, 10, 2, 13, 5, 0, 0, time.UTC)
	hour := time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC)

	_, _, _, err := BuildGrid(gridInput(now, time.UTC, hourlyPrices(hour.Add(time.Hour), 5, 1)))
	assert.ErrorIs(t, err, ErrNoPricedHours)

	_, _, _, err = BuildGrid(gridInput(now, nil, hourlyPrices(hour, 5, 1)))
	assert.Error(t, err)

	in := gridInput(now, time.UTC, hourlyPrices(hour, 5, 1))
	in.PV = func([]Hour) ([]float64, []Reason) { return []float64{1}, nil }
	_, _, _, err = BuildGrid(in)
	assert.ErrorIs(t, err, ErrInvalidGrid)

	in = gridInput(now, time.UTC, hourlyPrices(hour, 5, 1))
	in.Params.Efficiency = 1.2
	_, _, _, err = BuildGrid(in)
	assert.ErrorIs(t, err, ErrInvalidParams)

	in = gridInput(now, time.UTC, hourlyPrices(hour, 5, 1))
	in.SocPct = 150
	_, _, _, err = BuildGrid(in)
	assert.ErrorIs(t, err, ErrInvalidBattery)

	in = gridInput(now, time.UTC, hourlyPrices(hour, 5, 1))
	in.Prices[2].PriceInclV = 500 // implausible spot → buy out of bounds
	_, _, _, err = BuildGrid(in)
	assert.ErrorIs(t, err, ErrInvalidGrid)
}

// AC-6 buy/sell as used by BuildGrid, and the terminal price.
func TestBuySellAndTerminalPrice(t *testing.T) {
	p := testParams()
	p.SellMargin = 0.30
	p.Surcharge[18] = 1.20
	buy, sell := BuySell(2.50, 18, p.Surcharge, p.VATRate, p.SellMargin)
	assert.InDelta(t, 3.70, buy, 1e-12)
	assert.InDelta(t, 1.70, sell, 1e-12)
	_, sell = BuySell(-0.10, 18, p.Surcharge, p.VATRate, p.SellMargin)
	assert.Less(t, sell, 0.0)

	now := time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC)
	in := gridInput(now, time.UTC, []HourPrice{{Start: now, PriceInclV: 1}, {Start: now.Add(time.Hour), PriceInclV: 3}})
	g, _, _, err := BuildGrid(in)
	require.NoError(t, err)
	assert.InDelta(t, 0.83*((1+1.3)+(3+1.4))/2, g.TerminalPrice, 1e-12)
}

// T-0025: AC-7 — SoC 50 %, min 7 %, 10 kWh: E0 = 4300, Emax = 9300; SoC 5 %
// floors E0 at 0.
func TestNewBattery_T0025(t *testing.T) {
	b, err := NewBattery(50, 10000, 7)
	require.NoError(t, err)
	assert.InDelta(t, 4300, b.E0Wh, 1e-9)
	assert.InDelta(t, 9300, b.EmaxWh, 1e-9)

	b, err = NewBattery(5, 10000, 7)
	require.NoError(t, err)
	assert.Equal(t, 0.0, b.E0Wh)

	b, err = NewBattery(100, 10000, 7)
	require.NoError(t, err)
	assert.InDelta(t, b.EmaxWh, b.E0Wh, 1e-9)

	_, err = NewBattery(50, 0, 7)
	assert.ErrorIs(t, err, ErrInvalidBattery)
	_, err = NewBattery(50, 10000, 100)
	assert.ErrorIs(t, err, ErrInvalidBattery)
}
