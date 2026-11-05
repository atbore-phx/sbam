package optimize

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // CI must not depend on system tzdata.

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t2Loc returns Europe/Copenhagen for the input-processing tests.
func t2Loc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	require.NoError(t, err)
	return loc
}

// priceFixture is a recorded-shape HA /api/states response.
type priceFixture struct {
	State       string         `json:"state"`
	LastUpdated time.Time      `json:"last_updated"`
	Attributes  map[string]any `json:"attributes"`
}

func priceLoadFixture(t *testing.T, name string) priceFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var f priceFixture
	require.NoError(t, json.Unmarshal(b, &f))
	return f
}

const priceFixtureFile = "nordpool_dk2_15min_2026-10-02.json"

// priceFxNow is 2026-10-02 14:20 local, inside quarter 14:15 of the fixture.
func priceFxNow(t *testing.T) time.Time {
	return time.Date(2026, 10, 2, 14, 20, 0, 0, t2Loc(t))
}

// priceRawDay builds a raw_* list for the local day y-m-d with entries of the
// given step; value(i) returns the i-th value (any JSON-like value).
func priceRawDayFx(loc *time.Location, y int, m time.Month, d int, step time.Duration, value func(i int) any) []any {
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	end := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	var out []any
	for t, i := start, 0; t.Before(end); t, i = t.Add(step), i+1 {
		out = append(out, map[string]any{
			"start": t.Format("2006-01-02T15:04:05-07:00"),
			"end":   t.Add(step).Format("2006-01-02T15:04:05-07:00"),
			"value": value(i),
		})
	}
	return out
}

func priceAttrsFx(today, tomorrow []any) map[string]any {
	a := map[string]any{
		"unit_of_measurement": "DKK/kWh",
		"currency":            "DKK",
		"raw_today":           today,
		"tomorrow_valid":      tomorrow != nil,
	}
	if tomorrow != nil {
		a["raw_tomorrow"] = tomorrow
	} else {
		a["raw_tomorrow"] = []any{}
	}
	return a
}

func priceConst(v any) func(int) any { return func(int) any { return v } }

// T-0030: 96 quarter-hour entries per day; each hourly price is the mean of
// its 4 quarter-hour values.
func TestParseNordpool_T0030_QuarterHourMean(t *testing.T) {
	loc := t2Loc(t)
	f := priceLoadFixture(t, priceFixtureFile)
	now := priceFxNow(t)

	points, meta, err := ParseNordpool(f.Attributes, f.State, f.LastUpdated, now, loc)
	require.NoError(t, err)
	assert.Len(t, points, 192)
	assert.Equal(t, "DKK/kWh", meta.Unit)
	assert.Equal(t, "DKK", meta.Currency)
	assert.Equal(t, 15*time.Minute, meta.Resolution)
	assert.False(t, meta.TZMismatch)

	slot0 := time.Date(2026, 10, 2, 14, 0, 0, 0, loc)
	hp := HourlyPrices(points, slot0, 36)
	require.Len(t, hp, 34, "14:00 today through 23:00 tomorrow")
	assert.True(t, hp[0].Start.Equal(slot0))
	assert.InDelta(t, 1.0+1.4+0.015, hp[0].PriceInclV, 1e-9)
	assert.True(t, hp[10].Start.Equal(time.Date(2026, 10, 3, 0, 0, 0, 0, loc)))
	assert.InDelta(t, 2.015, hp[10].PriceInclV, 1e-9)
	assert.InDelta(t, 2.0+2.3+0.015, hp[33].PriceInclV, 1e-9)

	// Edge: the cap limits the horizon.
	assert.Len(t, HourlyPrices(points, slot0, 5), 5)
	// Edge: no points, no horizon.
	assert.Empty(t, HourlyPrices(nil, slot0, 36))
}

// T-0031: hourly entries are used as-is; the today/tomorrow array shape gives
// the same result as the raw shape.
func TestParseNordpool_T0031_HourlyAndArrayShape(t *testing.T) {
	loc := t2Loc(t)
	now := priceFxNow(t)
	slot0 := time.Date(2026, 10, 2, 14, 0, 0, 0, loc)

	t.Run("hourly raw entries", func(t *testing.T) {
		today := priceRawDayFx(loc, 2026, 10, 2, time.Hour, func(i int) any { return 0.5 + float64(i)*0.05 })
		points, meta, err := ParseNordpool(priceAttrsFx(today, nil), "1.2", now.Add(-time.Hour), now, loc)
		require.NoError(t, err)
		assert.Equal(t, time.Hour, meta.Resolution)
		hp := HourlyPrices(points, slot0, 36)
		require.Len(t, hp, 10)
		for k, h := range hp {
			assert.InDelta(t, 0.5+float64(14+k)*0.05, h.PriceInclV, 1e-9)
		}
	})

	t.Run("array shape equals raw shape", func(t *testing.T) {
		f := priceLoadFixture(t, priceFixtureFile)
		rawPoints, _, err := ParseNordpool(f.Attributes, f.State, f.LastUpdated, now, loc)
		require.NoError(t, err)

		arrAttrs := map[string]any{
			"unit_of_measurement": "DKK/kWh",
			"today":               f.Attributes["today"],
			"tomorrow":            f.Attributes["tomorrow"],
			"tomorrow_valid":      true,
		}
		arrPoints, meta, err := ParseNordpool(arrAttrs, f.State, f.LastUpdated, now, loc)
		require.NoError(t, err)
		assert.Equal(t, "DKK", meta.Currency, "currency derived from the unit")
		assert.Equal(t, HourlyPrices(rawPoints, slot0, 36), HourlyPrices(arrPoints, slot0, 36))
	})

	t.Run("array with wrong length is rejected", func(t *testing.T) {
		attrs := map[string]any{"unit_of_measurement": "DKK/kWh", "today": []any{1.0, 2.0, 3.0}}
		_, _, err := ParseNordpool(attrs, "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
}

// T-0032: tomorrow_valid=false ignores a populated raw_tomorrow; the horizon
// ends at 24:00 today.
func TestParseNordpool_T0032_TomorrowInvalid(t *testing.T) {
	loc := t2Loc(t)
	f := priceLoadFixture(t, priceFixtureFile)
	f.Attributes["tomorrow_valid"] = false
	now := priceFxNow(t)

	points, _, err := ParseNordpool(f.Attributes, f.State, f.LastUpdated, now, loc)
	require.NoError(t, err)
	assert.Len(t, points, 96)
	hp := HourlyPrices(points, time.Date(2026, 10, 2, 14, 0, 0, 0, loc), 36)
	require.Len(t, hp, 10)
	assert.True(t, hp[9].Start.Equal(time.Date(2026, 10, 2, 23, 0, 0, 0, loc)))

	// Edge: a missing tomorrow_valid with populated raw_tomorrow uses tomorrow.
	delete(f.Attributes, "tomorrow_valid")
	points, _, err = ParseNordpool(f.Attributes, f.State, f.LastUpdated, now, loc)
	require.NoError(t, err)
	assert.Len(t, points, 192)
}

// T-0033 (TC-004b): a null value in the current hour leaves it unpriced, so
// the run falls back with missing_prices. A null later ends the horizon there.
func TestParseNordpool_T0033_NullValues(t *testing.T) {
	loc := t2Loc(t)
	now := priceFxNow(t)
	slot0 := time.Date(2026, 10, 2, 14, 0, 0, 0, loc)

	t.Run("null in current hour", func(t *testing.T) {
		today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, func(i int) any {
			if i == 14*4+2 { // 14:30
				return nil
			}
			return 1.0
		})
		points, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		require.NoError(t, err)
		hp, err := PriceHorizon(points, slot0, 36)
		assert.Empty(t, hp)
		require.ErrorIs(t, err, ErrPriceMissing)
		assert.Equal(t, ReasonFallbackMissingPrices, ReasonForPriceError(err))
	})

	t.Run("null in a later hour ends the horizon", func(t *testing.T) {
		today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, func(i int) any {
			if i == 18*4+2 { // 18:30
				return nil
			}
			return "1.0" // numeric strings are accepted
		})
		points, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		require.NoError(t, err)
		hp, err := PriceHorizon(points, slot0, 36)
		require.NoError(t, err)
		require.Len(t, hp, 4)
		assert.True(t, hp[3].Start.Equal(time.Date(2026, 10, 2, 17, 0, 0, 0, loc)))
	})
}

// T-0034 (TC-004c, S-004): non-finite or out-of-bounds values are implausible.
func TestParseNordpool_T0034_ImplausibleValues(t *testing.T) {
	loc := t2Loc(t)
	now := priceFxNow(t)
	cases := []struct {
		name  string
		value any
	}{
		{"huge", 1e308},
		{"very negative", -1000.0},
		{"NaN float", math.NaN()},
		{"NaN string", "NaN"},
		{"inf string", "inf"},
		{"overflow string", "1e400"},
		{"just above max", 50.0001},
		{"just below min", -5.0001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, func(i int) any {
				if i == 3 {
					return tc.value
				}
				return 1.0
			})
			_, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
			require.ErrorIs(t, err, ErrPriceImplausible)
			assert.Equal(t, ReasonFallbackImplausibleInput, ReasonForPriceError(err))
		})
	}

	t.Run("bounds are inclusive", func(t *testing.T) {
		today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, func(i int) any {
			switch i {
			case 0:
				return -5.0
			case 1:
				return 50.0
			}
			return 1.0
		})
		_, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		assert.NoError(t, err)
	})

	t.Run("non-numeric string is a format error", func(t *testing.T) {
		today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, priceConst("cheap"))
		_, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
}

// T-0035 (TC-004d): /MWh is divided by 1000; any unit not ending in /kWh or
// /MWh is a price_format fallback.
func TestParseNordpool_T0035_Units(t *testing.T) {
	loc := t2Loc(t)
	now := priceFxNow(t)
	slot0 := time.Date(2026, 10, 2, 14, 0, 0, 0, loc)

	for _, unit := range []string{"DKK/MWh", "EUR/MWh"} {
		t.Run(unit, func(t *testing.T) {
			attrs := priceAttrsFx(priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, priceConst(850.0)), nil)
			attrs["unit_of_measurement"] = unit
			delete(attrs, "currency")
			points, meta, err := ParseNordpool(attrs, "850", now, now, loc)
			require.NoError(t, err)
			assert.Equal(t, unit, meta.Unit)
			assert.Equal(t, unit[:3], meta.Currency)
			hp := HourlyPrices(points, slot0, 1)
			require.Len(t, hp, 1)
			assert.InDelta(t, 0.85, hp[0].PriceInclV, 1e-9)
		})
	}

	t.Run("unit from currency and unit attributes", func(t *testing.T) {
		attrs := priceAttrsFx(priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, priceConst(1.0)), nil)
		delete(attrs, "unit_of_measurement")
		attrs["unit"] = "kWh"
		_, meta, err := ParseNordpool(attrs, "1.0", now, now, loc)
		require.NoError(t, err)
		assert.Equal(t, "DKK/kWh", meta.Unit)
	})

	for _, unit := range []string{"øre/Wh", "DKK", "", "kWh/DKK"} {
		t.Run("reject "+unit, func(t *testing.T) {
			attrs := priceAttrsFx(priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, priceConst(1.0)), nil)
			attrs["unit_of_measurement"] = unit
			_, _, err := ParseNordpool(attrs, "1.0", now, now, loc)
			require.ErrorIs(t, err, ErrPriceFormat)
			assert.Equal(t, ReasonFallbackPriceFormat, ReasonForPriceError(err))
		})
	}

	t.Run("price_in_cents is rejected", func(t *testing.T) {
		attrs := priceAttrsFx(priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, priceConst(1.0)), nil)
		attrs["price_in_cents"] = true
		_, _, err := ParseNordpool(attrs, "1.0", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
}

// T-0036 (TC-010c): entry counts per day; 7 is rejected, 92/100 are accepted
// on the matching DST day only.
func TestParseNordpool_T0036_EntryCounts(t *testing.T) {
	loc := t2Loc(t)

	t.Run("7 entries rejected", func(t *testing.T) {
		now := priceFxNow(t)
		today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, priceConst(1.0))[:7]
		_, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		require.ErrorIs(t, err, ErrPriceFormat)
		assert.Equal(t, ReasonFallbackPriceFormat, ReasonForPriceError(err))
	})

	t.Run("100 entries on the fall-back day", func(t *testing.T) {
		now := time.Date(2026, 10, 25, 10, 5, 0, 0, loc)
		today := priceRawDayFx(loc, 2026, 10, 25, 15*time.Minute, priceConst(1.0))
		require.Len(t, today, 100)
		points, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		require.NoError(t, err)
		hp := HourlyPrices(points, time.Date(2026, 10, 25, 0, 0, 0, 0, loc), 36)
		assert.Len(t, hp, 25, "both 02:00 hours are priced")
	})

	t.Run("25 hourly entries on the fall-back day", func(t *testing.T) {
		now := time.Date(2026, 10, 25, 10, 5, 0, 0, loc)
		today := priceRawDayFx(loc, 2026, 10, 25, time.Hour, priceConst(1.0))
		require.Len(t, today, 25)
		_, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		assert.NoError(t, err)
	})

	t.Run("92 entries on the spring-forward day", func(t *testing.T) {
		now := time.Date(2026, 3, 29, 10, 5, 0, 0, loc)
		today := priceRawDayFx(loc, 2026, 3, 29, 15*time.Minute, priceConst(1.0))
		require.Len(t, today, 92)
		points, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		require.NoError(t, err)
		assert.Len(t, HourlyPrices(points, time.Date(2026, 3, 29, 0, 0, 0, 0, loc), 36), 23)
	})

	t.Run("100 entries on a normal day rejected", func(t *testing.T) {
		now := priceFxNow(t)
		start := time.Date(2026, 10, 2, 0, 0, 0, 0, loc)
		var today []any
		for i := 0; i < 100; i++ {
			s := start.Add(time.Duration(i) * 15 * time.Minute)
			today = append(today, map[string]any{"start": s.Format(time.RFC3339), "value": 1.0})
		}
		_, _, err := ParseNordpool(priceAttrsFx(today, nil), "1.0", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})

	t.Run("92-entry array on a normal day rejected", func(t *testing.T) {
		now := priceFxNow(t)
		arr := make([]any, 92)
		for i := range arr {
			arr[i] = 1.0
		}
		_, _, err := ParseNordpool(map[string]any{"unit_of_measurement": "DKK/kWh", "today": arr}, "1.0", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
}

// T-0037 (TC-009a, S-009): yesterday's raw_today leaves the current hour
// unpriced, so the run falls back.
func TestParseNordpool_T0037_YesterdaysData(t *testing.T) {
	loc := t2Loc(t)
	now := priceFxNow(t)
	today := priceRawDayFx(loc, 2026, 10, 1, 15*time.Minute, priceConst(1.0))
	attrs := priceAttrsFx(today, nil)
	attrs["tomorrow_valid"] = false

	points, _, err := ParseNordpool(attrs, "1.0", now.Add(-2*time.Hour), now, loc)
	require.NoError(t, err)
	hp, err := PriceHorizon(points, time.Date(2026, 10, 2, 14, 0, 0, 0, loc), 36)
	assert.Empty(t, hp)
	require.ErrorIs(t, err, ErrPriceMissing)
	assert.Equal(t, ReasonFallbackMissingPrices, ReasonForPriceError(err))
}

// T-0038 (TC-009c): last_updated 30 h ago falls back with missing_prices.
func TestParseNordpool_T0038_StaleEntity(t *testing.T) {
	loc := t2Loc(t)
	f := priceLoadFixture(t, priceFixtureFile)
	now := priceFxNow(t)

	_, _, err := ParseNordpool(f.Attributes, f.State, now.Add(-30*time.Hour), now, loc)
	require.ErrorIs(t, err, ErrPriceStale)
	assert.Equal(t, ReasonFallbackMissingPrices, ReasonForPriceError(err))

	_, _, err = ParseNordpool(f.Attributes, f.State, time.Time{}, now, loc)
	assert.ErrorIs(t, err, ErrPriceStale, "zero last_updated counts as stale")

	// Edge: exactly 26 h is still accepted.
	_, _, err = ParseNordpool(f.Attributes, f.State, now.Add(-PriceMaxAge), now, loc)
	assert.NoError(t, err)
}

// T-0039 (TC-010a, S-010): state 2.10 while the current entry is 0.50 is
// inconsistent.
func TestParseNordpool_T0039_StateConsistency(t *testing.T) {
	loc := t2Loc(t)
	now := priceFxNow(t)
	today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, func(i int) any {
		return 0.50 + float64(i%4)*0.04 // 14:15 entry = 0.54, hour mean = 0.56
	})
	attrs := priceAttrsFx(today, nil)

	cases := []struct {
		name  string
		state string
		ok    bool
	}{
		{"mismatch", "2.10", false},
		{"current entry", "0.54", true},
		{"within 1 %", "0.545", true},
		{"hour mean", "0.56", true},
		{"just outside 1 %", "0.5460", false},
		{"unavailable", "unavailable", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseNordpool(attrs, tc.state, now, now, loc)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrPriceInconsistent)
			assert.Equal(t, ReasonFallbackPriceInconsistent, ReasonForPriceError(err))
		})
	}
}

// T-0039 edge case: the hourly run starts exactly at xx:00:00, before the
// Nordpool integration has moved its state to the new quarter. The state then
// still shows the previous entry or the previous hour's mean, which must not
// trigger a fallback; a value matching nothing nearby still does.
func TestParseNordpool_T0039_StateAtBoundary(t *testing.T) {
	loc := t2Loc(t)
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, loc) // boundary: current entry 15:00 = 0.50
	today := priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, func(i int) any {
		if i/4 == 14 { // 14:00 hour: 0.70, 0.74, 0.78, 0.82 (mean 0.76)
			return 0.70 + float64(i%4)*0.04
		}
		return 0.50 + float64(i%4)*0.04 // other hours: 0.50 … 0.62 (mean 0.56)
	})
	attrs := priceAttrsFx(today, nil)

	cases := []struct {
		name  string
		state string
		ok    bool
	}{
		{"current entry", "0.50", true},
		{"previous entry (14:45)", "0.82", true},
		{"previous hour mean", "0.76", true},
		{"two entries back (14:30)", "0.78", false},
		{"mismatch", "2.10", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseNordpool(attrs, tc.state, now, now, loc)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, ErrPriceInconsistent)
		})
	}
}

// T-0040 (TC-008d): +02:00 entries with the local zone UTC set TZMismatch.
func TestParseNordpool_T0040_TZMismatch(t *testing.T) {
	f := priceLoadFixture(t, priceFixtureFile)
	now := priceFxNow(t)

	_, meta, err := ParseNordpool(f.Attributes, f.State, f.LastUpdated, now, time.UTC)
	require.NoError(t, err)
	assert.True(t, meta.TZMismatch)

	_, meta, err = ParseNordpool(f.Attributes, f.State, f.LastUpdated, now, t2Loc(t))
	require.NoError(t, err)
	assert.False(t, meta.TZMismatch)
}

func TestParseNordpool_FormatFailures(t *testing.T) {
	loc := t2Loc(t)
	now := priceFxNow(t)
	good := func() []any { return priceRawDayFx(loc, 2026, 10, 2, 15*time.Minute, priceConst(1.0)) }

	t.Run("nil attributes", func(t *testing.T) {
		_, _, err := ParseNordpool(nil, "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("nil location", func(t *testing.T) {
		_, _, err := ParseNordpool(priceAttrsFx(good(), nil), "1", now, now, nil)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("no price shape", func(t *testing.T) {
		_, _, err := ParseNordpool(map[string]any{"unit_of_measurement": "DKK/kWh"}, "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("raw_today not a list", func(t *testing.T) {
		_, _, err := ParseNordpool(map[string]any{"unit_of_measurement": "DKK/kWh", "raw_today": "x"}, "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("bad timestamp", func(t *testing.T) {
		d := good()
		d[5].(map[string]any)["start"] = "yesterday"
		_, _, err := ParseNordpool(priceAttrsFx(d, nil), "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("end does not match resolution", func(t *testing.T) {
		d := good()
		d[5].(map[string]any)["end"] = "2026-10-02T03:00:00+02:00"
		_, _, err := ParseNordpool(priceAttrsFx(d, nil), "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("overlapping days", func(t *testing.T) {
		_, _, err := ParseNordpool(priceAttrsFx(good(), good()), "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("value of wrong type", func(t *testing.T) {
		d := good()
		d[5].(map[string]any)["value"] = true
		_, _, err := ParseNordpool(priceAttrsFx(d, nil), "1", now, now, loc)
		assert.ErrorIs(t, err, ErrPriceFormat)
	})
	t.Run("space-separated timestamps and json.Number", func(t *testing.T) {
		d := good()
		for _, e := range d {
			m := e.(map[string]any)
			s, _ := time.Parse(time.RFC3339, m["start"].(string))
			m["start"] = s.Format("2006-01-02 15:04:05-07:00")
			delete(m, "end")
			m["value"] = json.Number("1.0")
		}
		_, _, err := ParseNordpool(priceAttrsFx(d, nil), "1", now, now, loc)
		assert.NoError(t, err)
	})
}

func TestReasonForPriceError(t *testing.T) {
	assert.Equal(t, ReasonFallbackPriceFormat, ReasonForPriceError(ErrPriceFormat))
	assert.Equal(t, ReasonFallbackImplausibleInput, ReasonForPriceError(ErrPriceImplausible))
	assert.Equal(t, ReasonFallbackMissingPrices, ReasonForPriceError(ErrPriceStale))
	assert.Equal(t, ReasonFallbackMissingPrices, ReasonForPriceError(ErrPriceMissing))
	assert.Equal(t, ReasonFallbackPriceInconsistent, ReasonForPriceError(ErrPriceInconsistent))
	assert.Equal(t, ReasonFallbackPriceFormat, ReasonForPriceError(errors.New("other")))
}

func TestHourlyPrices_PartialCoverage(t *testing.T) {
	loc := t2Loc(t)
	hs := time.Date(2026, 10, 2, 14, 0, 0, 0, loc)
	q := func(min int, v float64) PricePoint {
		s := hs.Add(time.Duration(min) * time.Minute)
		return PricePoint{Start: s, End: s.Add(15 * time.Minute), Value: v, Valid: true}
	}
	// Only three quarters: the hour is not fully covered.
	assert.Empty(t, HourlyPrices([]PricePoint{q(0, 1), q(15, 1), q(30, 1)}, hs, 3))
	// A slot0 that is not aligned with the entries leaves the hour unpriced.
	assert.Empty(t, HourlyPrices([]PricePoint{q(0, 1), q(15, 1), q(30, 1), q(45, 1)}, hs.Add(30*time.Minute), 1))
	// Zero maxHours.
	assert.Empty(t, HourlyPrices([]PricePoint{q(0, 1), q(15, 1), q(30, 1), q(45, 1)}, hs, 0))
}

// T-0024 (AC-6): buy = spot + surcharge[hod]; sell = spot/(1+vat) − margin.
func TestBuySell_T0024(t *testing.T) {
	var sur [24]float64
	sur[18] = 1.20

	buy, sell := BuySell(2.50, 18, sur, 0.25, 0.30)
	assert.InDelta(t, 3.70, buy, 1e-9)
	assert.InDelta(t, 1.70, sell, 1e-9)

	// Edge: a negative spot price gives a negative sell price.
	buy, sell = BuySell(-0.50, 3, sur, 0.25, 0.30)
	assert.InDelta(t, -0.50, buy, 1e-9)
	assert.InDelta(t, -0.70, sell, 1e-9)

	// Edge: the hour of day wraps modulo 24.
	buy, _ = BuySell(2.50, 42, sur, 0.25, 0.30)
	assert.InDelta(t, 3.70, buy, 1e-9)
	buy, _ = BuySell(2.50, -6, sur, 0.25, 0.30)
	assert.InDelta(t, 3.70, buy, 1e-9)
}
