package optimize

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// middayTariff is a three-band time-of-use tariff: cheap 09-15, peak 15-21,
// shoulder otherwise; the feed-in price is higher in the peak band.
func middayTariff() Tariff {
	t := Tariff{Currency: "AUD"}
	for h := range t.Buy {
		t.Buy[h], t.Sell[h] = 0.24, 0.02
		switch {
		case h >= 9 && h < 15:
			t.Buy[h] = 0.09
		case h >= 15 && h < 21:
			t.Buy[h], t.Sell[h] = 0.54, 0.10
		}
	}
	return t
}

func TestTariffHourlyPrices(t *testing.T) {
	perth, err := time.LoadLocation("Australia/Perth")
	require.NoError(t, err)
	tariff := middayTariff()

	start := time.Date(2026, 11, 12, 13, 0, 0, 0, perth)
	got := tariff.HourlyPrices(start, perth, 48)
	require.Len(t, got, 48)
	for k, p := range got {
		assert.True(t, p.Start.Equal(start.Add(time.Duration(k)*time.Hour)), "hour %d start", k)
		assert.Equal(t, tariff.Buy[(13+k)%24], p.PriceInclV, "hour %d price", k)
	}

	// The fall-back day repeats 02:00 local; both hours take the 02:00 price.
	loc := copenhagen(t)
	tariff.Buy[2] = 0.33
	midnight := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	day := tariff.HourlyPrices(midnight, loc, 25)
	require.Len(t, day, 25)
	assert.Equal(t, 0.33, day[2].PriceInclV)
	assert.Equal(t, 0.33, day[3].PriceInclV)
	assert.Equal(t, tariff.Buy[3], day[4].PriceInclV)

	assert.Empty(t, tariff.HourlyPrices(start, perth, 0))

	// A nil location falls back to time.Local.
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = perth
	utcStart := time.Date(2026, 11, 12, 5, 0, 0, 0, time.UTC) // 13:00 Perth
	assert.Equal(t, tariff.HourlyPrices(utcStart, perth, 3), tariff.HourlyPrices(utcStart, nil, 3))
}

func TestTariffBuySell(t *testing.T) {
	tariff := middayTariff()
	for _, tc := range []struct{ hourOfDay, want int }{{0, 0}, {18, 18}, {23, 23}, {27, 3}, {-1, 23}} {
		buy, sell := tariff.BuySell(Hour{HourOfDay: tc.hourOfDay, SpotInclVAT: 9})
		assert.Equal(t, tariff.Buy[tc.want], buy, "buy at hour %d", tc.hourOfDay)
		assert.Equal(t, tariff.Sell[tc.want], sell, "sell at hour %d", tc.hourOfDay)
	}
}

// A fixed tariff sets buy and sell per hour of day; the spot-price parameters
// (surcharge, VAT, sell margin) of the grid input are not applied.
func TestBuildGridStaticTariff(t *testing.T) {
	perth, err := time.LoadLocation("Australia/Perth")
	require.NoError(t, err)
	tariff := middayTariff()
	now := time.Date(2026, 11, 12, 13, 20, 0, 0, perth)
	slot0 := time.Date(2026, 11, 12, 13, 0, 0, 0, perth)

	in := gridInput(now, perth, tariff.HourlyPrices(slot0, perth, 48))
	in.Params.SellMargin = 0.30
	in.BuySell = tariff.BuySell
	g, _, _, err := BuildGrid(in)
	require.NoError(t, err)
	require.Len(t, g.Hours, DefaultMaxHours)

	sumBuy := 0.0
	for k, h := range g.Hours {
		assert.Equal(t, (13+k)%24, h.HourOfDay)
		assert.Equal(t, tariff.Buy[h.HourOfDay], h.Buy, "buy of slot %d", k)
		assert.Equal(t, tariff.Sell[h.HourOfDay], h.Sell, "sell of slot %d", k)
		sumBuy += h.Buy
	}
	assert.InDelta(t, in.Params.Efficiency*sumBuy/float64(len(g.Hours)), g.TerminalPrice, 1e-12)

	// A price outside the grid bounds is rejected like a spot price would be.
	tariff.Buy[14] = maxBuyPrice + 1
	in.BuySell = tariff.BuySell
	_, _, _, err = BuildGrid(in)
	assert.ErrorIs(t, err, ErrInvalidGrid)
}

// A nil BuySell keeps the spot-price derivation with Params.
func TestBuildGridNilBuySellDerivesFromSpot(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 2, 13, 0, 0, 0, loc)
	in := gridInput(now, loc, hourlyPrices(now, 24, 1.0))
	in.Params.SellMargin = 0.30
	want, _, _, err := BuildGrid(in)
	require.NoError(t, err)

	p := in.Params
	in.BuySell = func(h Hour) (float64, float64) {
		return BuySell(h.SpotInclVAT, h.HourOfDay, p.Surcharge, p.VATRate, p.SellMargin)
	}
	got, _, _, err := BuildGrid(in)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.InDelta(t, want.Hours[5].SpotInclVAT+p.Surcharge[want.Hours[5].HourOfDay], want.Hours[5].Buy, 1e-12)
}
