package optimize

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pvFx(t *testing.T, end, period string, kw float64) PVPeriod {
	t.Helper()
	p, err := PVPeriodsFromSolcast(end, period, kw)
	require.NoError(t, err)
	return p
}

// T-0059 (AC-4): two PT30M periods (UTC period_end) of 2.0 and 1.0 kW inside
// one local hour give 1500 Wh.
func TestBucketPV_T0059_HalfHourPeriods(t *testing.T) {
	loc := t2Loc(t)
	slot := time.Date(2026, 10, 2, 12, 0, 0, 0, loc) // 10:00Z
	periods := []PVPeriod{
		pvFx(t, "2026-10-02T10:30:00.0000000Z", "PT30M", 2.0),
		pvFx(t, "2026-10-02T11:00:00.0000000Z", "PT30M", 1.0),
	}
	wh, reasons := BucketPV(periods, []time.Time{slot})
	require.Len(t, wh, 1)
	assert.InDelta(t, 1500, wh[0], 1e-9)
	assert.Empty(t, reasons)
}

// T-0060 (AC-4): two Solcast URLs are summed; a period straddling a slot
// boundary is split by overlap.
func TestBucketPV_T0060_SumAndSplit(t *testing.T) {
	loc := t2Loc(t)
	s0 := time.Date(2026, 10, 2, 12, 0, 0, 0, loc)
	slots := []time.Time{s0, s0.Add(time.Hour)}

	urlA := []PVPeriod{
		pvFx(t, "2026-10-02T10:30:00Z", "PT30M", 2.0),
		pvFx(t, "2026-10-02T11:00:00Z", "PT30M", 1.0),
		pvFx(t, "2026-10-02T11:30:00Z", "PT30M", 1.0),
		pvFx(t, "2026-10-02T12:00:00Z", "PT30M", 1.0),
	}
	urlB := []PVPeriod{
		pvFx(t, "2026-10-02T10:30:00Z", "", 1.0), // default PT30M
		pvFx(t, "2026-10-02T11:30:00Z", "PT60M", 2.0),
		pvFx(t, "2026-10-02T12:00:00Z", "PT30M", 0.5),
	}
	wh, reasons := BucketPV(append(append([]PVPeriod{}, urlA...), urlB...), slots)
	require.Len(t, wh, 2)
	// Slot 0: A = 1000+500 = 1500; B = 500 + half of the 60-min period (1000).
	assert.InDelta(t, 1500+500+1000, wh[0], 1e-9)
	// Slot 1: A = 500+500 = 1000; B = other half (1000) + 250.
	assert.InDelta(t, 1000+1000+250, wh[1], 1e-9)
	assert.Empty(t, reasons)
}

// T-0061 (AC-4): slots beyond the forecast are 0 Wh with
// optimizer_pv_forecast_missing; negative and NaN estimates are dropped.
func TestBucketPV_T0061_UncoveredAndInvalid(t *testing.T) {
	loc := t2Loc(t)
	s0 := time.Date(2026, 10, 2, 12, 0, 0, 0, loc)
	slots := []time.Time{s0, s0.Add(time.Hour), s0.Add(2 * time.Hour)}

	periods := []PVPeriod{
		pvFx(t, "2026-10-02T10:30:00Z", "PT30M", 2.0),
		pvFx(t, "2026-10-02T11:00:00Z", "PT30M", 2.0),
	}
	wh, reasons := BucketPV(periods, slots)
	assert.Equal(t, []float64{2000, 0, 0}, wh)
	assert.Equal(t, []Reason{ReasonPVForecastMissing}, reasons)

	// Invalid estimates are dropped and do not count as coverage.
	end := time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC)
	invalid := []PVPeriod{
		{End: end, Period: time.Hour, PVEstimateKW: -1},
		{End: end, Period: time.Hour, PVEstimateKW: math.NaN()},
		{End: end, Period: time.Hour, PVEstimateKW: math.Inf(1)},
		{End: end, Period: 0, PVEstimateKW: 1},
	}
	wh, reasons = BucketPV(invalid, slots[:1])
	assert.Equal(t, []float64{0}, wh)
	assert.Equal(t, []Reason{ReasonPVForecastMissing}, reasons)

	// Edge: no slots.
	wh, reasons = BucketPV(periods, nil)
	assert.Nil(t, wh)
	assert.Nil(t, reasons)

	// Edge: partial coverage of a slot is reported.
	wh, reasons = BucketPV(periods[:1], slots[:1])
	assert.Equal(t, []float64{1000}, wh)
	assert.Equal(t, []Reason{ReasonPVForecastMissing}, reasons)
}

func TestPVPeriodsFromSolcast(t *testing.T) {
	p, err := PVPeriodsFromSolcast("2026-10-02T10:30:00.0000000Z", "", 1.25)
	require.NoError(t, err)
	assert.True(t, p.End.Equal(time.Date(2026, 10, 2, 10, 30, 0, 0, time.UTC)))
	assert.Equal(t, time.UTC, p.End.Location())
	assert.Equal(t, DefaultPVPeriod, p.Period)
	assert.InDelta(t, 1.25, p.PVEstimateKW, 1e-12)

	for in, want := range map[string]time.Duration{
		"PT30M":   30 * time.Minute,
		"PT5M":    5 * time.Minute,
		"PT1H":    time.Hour,
		"PT1H30M": 90 * time.Minute,
		"pt15m":   15 * time.Minute,
		"PT90S":   90 * time.Second,
	} {
		t.Run(in, func(t *testing.T) {
			p, err := PVPeriodsFromSolcast("2026-10-02T10:30:00Z", in, 1)
			require.NoError(t, err)
			assert.Equal(t, want, p.Period)
		})
	}

	for _, bad := range []string{"30M", "PT", "P1D", "PTxM", "PT0M", "PT0.5H", "PT30M1H", "PT30M30M", "PT25H", "PT-5M"} {
		t.Run("period "+bad, func(t *testing.T) {
			_, err := PVPeriodsFromSolcast("2026-10-02T10:30:00Z", bad, 1)
			assert.ErrorIs(t, err, ErrPVPeriod)
		})
	}

	_, err = PVPeriodsFromSolcast("02/10/2026 10:30", "PT30M", 1)
	assert.Error(t, err)
	_, err = PVPeriodsFromSolcast("2026-10-02T10:30:00Z", "PT30M", -0.1)
	assert.Error(t, err)
	_, err = PVPeriodsFromSolcast("2026-10-02T10:30:00Z", "PT30M", math.NaN())
	assert.Error(t, err)
}
