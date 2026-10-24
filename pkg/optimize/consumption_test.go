package optimize

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// profileFxNow is a non-DST reference day; the 10-day window is
// 2026-09-22 00:00 … 2026-10-02 00:00 local.
func profileFxNow(t *testing.T) time.Time {
	return time.Date(2026, 10, 2, 10, 0, 0, 0, t2Loc(t))
}

// profileHourly returns one sample at the start of every local hour in
// [start, end) with value(day, slotStartLocal).
func profileHourly(start, end time.Time, value func(day int, at time.Time) (float64, bool)) []Sample {
	var out []Sample
	for at := start; at.Before(end); at = at.Add(time.Hour) {
		day := int(at.Sub(start).Hours() / 24)
		v, ok := value(day, at)
		out = append(out, Sample{At: at, Value: v, Valid: ok})
	}
	return out
}

func profileWindow(now time.Time, days int) (time.Time, time.Time) {
	l := now.In(now.Location())
	end := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, l.Location())
	return time.Date(l.Year(), l.Month(), l.Day()-days, 0, 0, 0, 0, l.Location()), end
}

// T-0050 (AC-1): 1000 W for 30 min then 2000 W integrates to 1500 Wh.
func TestBuildProfile_T0050_StepIntegration(t *testing.T) {
	loc := t2Loc(t)
	h := time.Date(2026, 10, 1, 10, 0, 0, 0, loc)
	s := []Sample{
		{At: h, Value: 1000, Valid: true},
		{At: h.Add(30 * time.Minute), Value: 2000, Valid: true},
	}
	wh, cov := profileIntegrate(s, h, h.Add(time.Hour))
	assert.InDelta(t, 1500, wh, 1e-9)
	assert.Equal(t, time.Hour, cov)

	// Same shape every hour for 3 days through BuildProfile.
	now := profileFxNow(t)
	start, end := profileWindow(now, 3)
	var load []Sample
	for at := start; at.Before(end); at = at.Add(time.Hour) {
		load = append(load, Sample{At: at, Value: 1000, Valid: true}, Sample{At: at.Add(30 * time.Minute), Value: 2000, Valid: true})
	}
	p, reasons, err := BuildProfile(load, nil, "W", "", 3, now, loc, 1500)
	require.NoError(t, err)
	assert.Empty(t, reasons)
	for hod := 0; hod < 24; hod++ {
		assert.InDelta(t, 1500, p[hod], 1e-9, "hour %d", hod)
	}

	// Edge: a sample before the slot holds into it; time before the first
	// sample is a gap.
	wh, cov = profileIntegrate(s[1:], h, h.Add(time.Hour))
	assert.InDelta(t, 1000, wh, 1e-9)
	assert.Equal(t, 30*time.Minute, cov)
	wh, cov = profileIntegrate(nil, h, h.Add(time.Hour))
	assert.Zero(t, wh)
	assert.Zero(t, cov)
}

// T-0051 (AC-1, TC-004e): load in W, EV in kW; 5000 W − 3.7 kW = 1300 Wh; a
// negative difference clamps to 0.
func TestBuildProfile_T0051_EVSubtractionAndClamp(t *testing.T) {
	loc := t2Loc(t)
	now := profileFxNow(t)
	start, end := profileWindow(now, 3)
	load := profileHourly(start, end, func(int, time.Time) (float64, bool) { return 5000, true })
	ev := profileHourly(start, end, func(_ int, at time.Time) (float64, bool) {
		switch at.In(loc).Hour() {
		case 10:
			return 3.7, true
		case 18:
			return 6.0, true
		}
		return 0, true
	})

	p, reasons, err := BuildProfile(load, ev, "W", "kW", 3, now, loc, 4000)
	require.NoError(t, err)
	assert.Empty(t, reasons)
	assert.InDelta(t, 1300, p[10], 1e-9)
	assert.InDelta(t, 0, p[18], 1e-9, "load − EV < 0 clamps to 0")
	assert.InDelta(t, 5000, p[0], 1e-9)
}

// T-0052 (AC-1): 10 days with a distinct value per hour of day; each hour is
// the mean over its 10 samples.
func TestBuildProfile_T0052_HourOfDayMean(t *testing.T) {
	loc := t2Loc(t)
	now := profileFxNow(t)
	start, end := profileWindow(now, 10)
	load := profileHourly(start, end, func(day int, at time.Time) (float64, bool) {
		return 100*float64(at.In(loc).Hour()+1) + 10*float64(day), true
	})

	b, err := BuildProfileDetail(load, nil, "W", "", 10, now, loc, 1250)
	require.NoError(t, err)
	assert.Empty(t, b.Reasons)
	assert.Empty(t, b.StaticHours)
	for hod := 0; hod < 24; hod++ {
		assert.Equal(t, 10, b.Samples[hod])
		assert.InDelta(t, 100*float64(hod+1)+45, b.Profile[hod], 1e-9, "hour %d", hod)
	}
}

// T-0053 (AC-2): hour 03 with only 2 usable days falls back to the static
// value with optimizer_consumption_static_fallback and hours=[3].
func TestBuildProfile_T0053_FewSamples(t *testing.T) {
	loc := t2Loc(t)
	now := profileFxNow(t)
	start, end := profileWindow(now, 10)
	load := profileHourly(start, end, func(day int, at time.Time) (float64, bool) {
		if at.In(loc).Hour() == 3 && day >= 2 {
			return 0, false // gap for 8 of 10 days
		}
		return 1000, true
	})

	b, err := BuildProfileDetail(load, nil, "W", "", 10, now, loc, 1200)
	require.NoError(t, err)
	assert.Equal(t, 2, b.Samples[3])
	assert.InDelta(t, 1200, b.Profile[3], 1e-9)
	assert.Equal(t, []int{3}, b.StaticHours)
	assert.Equal(t, []Reason{ReasonConsumptionStaticFallback}, b.Reasons)
	assert.InDelta(t, 1000, b.Profile[4], 1e-9)

	// Edge: exactly 3 usable days is enough.
	load3 := profileHourly(start, end, func(day int, at time.Time) (float64, bool) {
		return 1000, !(at.In(loc).Hour() == 3 && day >= 3)
	})
	b, err = BuildProfileDetail(load3, nil, "W", "", 10, now, loc, 1200)
	require.NoError(t, err)
	assert.Empty(t, b.StaticHours)
	assert.InDelta(t, 1000, b.Profile[3], 1e-9)
}

// T-0054 (AC-3): no EV entity configured gives the load-only profile.
func TestBuildProfile_T0054_NoEV(t *testing.T) {
	loc := t2Loc(t)
	now := profileFxNow(t)
	start, end := profileWindow(now, 5)
	load := profileHourly(start, end, func(_ int, at time.Time) (float64, bool) { return 800 + float64(at.In(loc).Hour()), true })
	zeroEV := profileHourly(start, end, func(int, time.Time) (float64, bool) { return 0, true })

	noEV, reasons, err := BuildProfile(load, nil, "W", "", 5, now, loc, 800)
	require.NoError(t, err)
	assert.Empty(t, reasons)
	withZeroEV, _, err := BuildProfile(load, zeroEV, "W", "kW", 5, now, loc, 800)
	require.NoError(t, err)
	assert.Equal(t, withZeroEV, noEV)
}

// T-0055 (TC-004a, S-004): nan/inf/unavailable/unknown/"" are gaps, never 0;
// a slot with < 90 % coverage is unusable.
func TestBuildProfile_T0055_GapsNeverZero(t *testing.T) {
	loc := t2Loc(t)
	h := time.Date(2026, 10, 1, 10, 0, 0, 0, loc)

	for _, bad := range []string{"nan", "inf", "-Inf", "unavailable", "unknown", ""} {
		t.Run("state "+bad, func(t *testing.T) {
			s := []Sample{
				SampleFromHAState(h, "2000"),
				SampleFromHAState(h.Add(30*time.Minute), bad),
			}
			w, err := SanitizePowerSamples(s, "W")
			require.NoError(t, err)
			wh, cov := profileIntegrate(w, h, h.Add(time.Hour))
			assert.InDelta(t, 1000, wh, 1e-9)
			assert.Equal(t, 30*time.Minute, cov, "the gap is not counted as covered")
		})
	}

	// Through BuildProfile: hour 10 is 50 % covered on every day (unusable),
	// hour 11 is 95 % covered (usable, scaled to the full hour).
	now := profileFxNow(t)
	start, end := profileWindow(now, 4)
	var load []Sample
	for at := start; at.Before(end); at = at.Add(time.Hour) {
		load = append(load, SampleFromHAState(at, "1000"))
		switch at.In(loc).Hour() {
		case 10:
			load = append(load, SampleFromHAState(at.Add(30*time.Minute), "nan"))
		case 11:
			load = append(load, SampleFromHAState(at.Add(57*time.Minute), "unavailable"))
		}
	}
	b, err := BuildProfileDetail(load, nil, "W", "", 4, now, loc, 1000)
	require.NoError(t, err)
	assert.Equal(t, 0, b.Samples[10])
	assert.Equal(t, []int{10}, b.StaticHours)
	assert.Equal(t, 4, b.Samples[11])
	assert.InDelta(t, 1000, b.Profile[11], 1e-9)
	for hod, v := range b.Profile {
		assert.False(t, math.IsNaN(v) || math.IsInf(v, 0), "hour %d", hod)
	}
}

// T-0056 (TC-004e): an unsupported or missing load unit makes the profile
// static; an EV with an unsupported unit is skipped with
// optimizer_ev_entity_not_found.
func TestBuildProfile_T0056_Units(t *testing.T) {
	loc := t2Loc(t)
	now := profileFxNow(t)
	start, end := profileWindow(now, 5)
	load := profileHourly(start, end, func(int, time.Time) (float64, bool) { return 900, true })
	ev := profileHourly(start, end, func(int, time.Time) (float64, bool) { return 0.5, true })

	for _, unit := range []string{"kVA", ""} {
		t.Run("load unit "+unit, func(t *testing.T) {
			b, err := BuildProfileDetail(load, nil, unit, "", 5, now, loc, 700)
			require.NoError(t, err)
			assert.Equal(t, []Reason{ReasonConsumptionStaticFallback}, b.Reasons)
			assert.Len(t, b.StaticHours, 24)
			for _, v := range b.Profile {
				assert.InDelta(t, 700, v, 1e-9)
			}
		})
	}

	for _, unit := range []string{"kVA", ""} {
		t.Run("ev unit "+unit, func(t *testing.T) {
			p, reasons, err := BuildProfile(load, ev, "W", unit, 5, now, loc, 700)
			require.NoError(t, err)
			assert.Equal(t, []Reason{ReasonEVEntityNotFound}, reasons)
			assert.InDelta(t, 900, p[12], 1e-9, "EV skipped, load-only")
		})
	}

	t.Run("kW load is converted", func(t *testing.T) {
		kw := profileHourly(start, end, func(int, time.Time) (float64, bool) { return 0.9, true })
		p, reasons, err := BuildProfile(kw, nil, "kW", "", 5, now, loc, 700)
		require.NoError(t, err)
		assert.Empty(t, reasons)
		assert.InDelta(t, 900, p[5], 1e-9)
	})
}

// T-0057 (TC-010b): a profile summing to 10 × the static daily value is
// replaced entirely by the static profile.
func TestBuildProfile_T0057_DailySumBounds(t *testing.T) {
	loc := t2Loc(t)
	now := profileFxNow(t)
	start, end := profileWindow(now, 5)
	const static = 500.0

	for _, tc := range []struct {
		name   string
		value  float64
		static bool
	}{
		{"10x", 10 * static, true},
		{"0.1x", 0.1 * static, true},
		{"exactly 4x", 4 * static, false},
		{"exactly 0.25x", 0.25 * static, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			load := profileHourly(start, end, func(int, time.Time) (float64, bool) { return tc.value, true })
			b, err := BuildProfileDetail(load, nil, "W", "", 5, now, loc, static)
			require.NoError(t, err)
			if !tc.static {
				assert.Empty(t, b.Reasons)
				assert.InDelta(t, tc.value, b.Profile[7], 1e-9)
				return
			}
			assert.Equal(t, []Reason{ReasonConsumptionStaticFallback}, b.Reasons)
			assert.Len(t, b.StaticHours, 24)
			for _, v := range b.Profile {
				assert.InDelta(t, static, v, 1e-9)
			}
		})
	}

	t.Run("hour above 25 kWh is static", func(t *testing.T) {
		load := profileHourly(start, end, func(_ int, at time.Time) (float64, bool) {
			if at.In(loc).Hour() == 19 {
				return 30000, true
			}
			return 20000, true
		})
		b, err := BuildProfileDetail(load, nil, "W", "", 5, now, loc, 20000)
		require.NoError(t, err)
		assert.Equal(t, []int{19}, b.StaticHours)
		assert.InDelta(t, 20000, b.Profile[19], 1e-9)
	})
}

// T-0058 (S-008): the 25-hour fall-back day adds two samples to 02:00; the
// 23-hour spring-forward day adds none.
func TestBuildProfile_T0058_DSTDays(t *testing.T) {
	loc := t2Loc(t)
	constant := func(start time.Time) []Sample {
		return []Sample{{At: start.Add(-time.Minute), Value: 1000, Valid: true}}
	}

	t.Run("fall back", func(t *testing.T) {
		now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
		start, _ := profileWindow(now, 1)
		b, err := BuildProfileDetail(constant(start), nil, "W", "", 1, now, loc, 1000)
		require.NoError(t, err)
		assert.Equal(t, 2, b.Samples[2])
		assert.Equal(t, 1, b.Samples[3])

		b, err = BuildProfileDetail(constant(start.AddDate(0, 0, -2)), nil, "W", "", 3, now, loc, 1000)
		require.NoError(t, err)
		assert.Equal(t, 4, b.Samples[2])
		assert.Equal(t, 3, b.Samples[3])
		assert.InDelta(t, 1000, b.Profile[2], 1e-9)
	})

	t.Run("spring forward", func(t *testing.T) {
		now := time.Date(2026, 3, 30, 10, 0, 0, 0, loc)
		start, _ := profileWindow(now, 1)
		b, err := BuildProfileDetail(constant(start), nil, "W", "", 1, now, loc, 1000)
		require.NoError(t, err)
		assert.Equal(t, 0, b.Samples[2])
		assert.Equal(t, 1, b.Samples[3])
		assert.Contains(t, b.StaticHours, 2)
	})
}

func TestBuildProfile_InvalidArguments(t *testing.T) {
	loc := t2Loc(t)
	now := profileFxNow(t)
	cases := []struct {
		name   string
		days   int
		loc    *time.Location
		static float64
	}{
		{"nil location", 10, nil, 500},
		{"zero days", 0, loc, 500},
		{"too many days", 31, loc, 500},
		{"zero static", 10, loc, 0},
		{"NaN static", 10, loc, math.NaN()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := BuildProfile(nil, nil, "W", "", tc.days, now, tc.loc, tc.static)
			assert.ErrorIs(t, err, ErrProfileArgs)
		})
	}

	// Edge: no history at all is a full static fallback, not an error.
	p, reasons, err := BuildProfile(nil, nil, "W", "", 10, now, loc, 500)
	require.NoError(t, err)
	assert.Equal(t, []Reason{ReasonConsumptionStaticFallback}, reasons)
	assert.InDelta(t, 500, p[0], 1e-9)
}
