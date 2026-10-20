package power

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var madrid = time.FixedZone("CEST", 2*3600)

// periods builds a forecast with 30-minute periods ending at the given local
// clock times on 2026-10-20, with PV estimates in kW.
func periods(pv map[string]float64) Forecasts {
	var f Forecasts
	for clock, kw := range pv {
		t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-20 "+clock, madrid)
		f.Forecasts = append(f.Forecasts, Forecast{PVEstimate: kw, PeriodEnd: t.UTC().Format(time.RFC3339), Period: "PT30M"})
	}
	return f
}

func at(clock string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-20 "+clock, madrid)
	return t
}

func TestSolarGapEnergy(t *testing.T) {
	site := periods(map[string]float64{
		"08:00": 0.0, "08:30": 0.1, "09:00": 0.3, "09:30": 0.6, "10:00": 1.2,
	})

	gap, err := SolarGapEnergy([]Forecasts{site}, at("08:00"), 500)

	require.NoError(t, err)
	assert.True(t, gap.Covered)
	assert.Equal(t, at("09:00"), gap.CoveredAt, "the 09:00-09:30 period is the first above 500 W")
	// (500-100)*0.5 + (500-300)*0.5
	assert.InDelta(t, 300, gap.EnergyWh, 0.01)
}

func TestSolarGapEnergySumsSites(t *testing.T) {
	east := periods(map[string]float64{"08:30": 0.2, "09:00": 0.3})
	west := periods(map[string]float64{"08:30": 0.1, "09:00": 0.3})

	gap, err := SolarGapEnergy([]Forecasts{east, west}, at("08:00"), 500)

	require.NoError(t, err)
	assert.True(t, gap.Covered)
	assert.Equal(t, at("08:30"), gap.CoveredAt, "0.3 + 0.3 kW covers 500 W in the 08:30-09:00 period")
	assert.InDelta(t, 100, gap.EnergyWh, 0.01, "(500-300)*0.5 for 08:00-08:30")
}

func TestSolarGapEnergyPartialFirstPeriod(t *testing.T) {
	site := periods(map[string]float64{"08:30": 0.0, "09:00": 1.0})

	gap, err := SolarGapEnergy([]Forecasts{site}, at("08:15"), 400)

	require.NoError(t, err)
	assert.InDelta(t, 100, gap.EnergyWh, 0.01, "only 08:15-08:30 of the first period counts")
	assert.Equal(t, at("08:30"), gap.CoveredAt)
}

func TestSolarGapEnergyNeverCovered(t *testing.T) {
	site := periods(map[string]float64{"08:30": 0.1, "09:00": 0.1})

	gap, err := SolarGapEnergy([]Forecasts{site}, at("08:00"), 500)

	require.NoError(t, err)
	assert.False(t, gap.Covered)
	assert.True(t, gap.CoveredAt.IsZero())
	assert.InDelta(t, 400, gap.EnergyWh, 0.01)
}

func TestSolarGapEnergyEdgeCases(t *testing.T) {
	gap, err := SolarGapEnergy(nil, at("08:00"), 0)
	require.NoError(t, err)
	assert.True(t, gap.Covered, "no base load needs no reserve")
	assert.Zero(t, gap.EnergyWh)

	_, err = SolarGapEnergy([]Forecasts{{Forecasts: []Forecast{{PeriodEnd: "not-a-time"}}}}, at("08:00"), 500)
	assert.ErrorContains(t, err, "parse period_end")
}

func TestUntilSolarReserve(t *testing.T) {
	assert.InDelta(t, 1280+300, UntilSolarReserve(5120, 25, SolarGap{EnergyWh: 300}), 0.01)
	assert.InDelta(t, 300, UntilSolarReserve(5120, 0, SolarGap{EnergyWh: 300}), 0.01)
}

func TestValidateReserveHorizon(t *testing.T) {
	got, err := ValidateReserveHorizon("until_solar")
	assert.NoError(t, err)
	assert.Equal(t, ReserveHorizonUntilSolar, got)

	_, err = ValidateReserveHorizon("full_day")
	assert.ErrorContains(t, err, "unknown reserve_horizon")
}

func TestValidateWindowsReserveHorizon(t *testing.T) {
	ok := []Window{{Name: "night", Start: "00:00", End: "08:00", MaxCharge: 2500, ReserveHorizon: "until_solar"}}
	assert.NoError(t, ValidateWindows(ok, true))

	bad := []Window{{Name: "night", Start: "00:00", End: "08:00", MaxCharge: 2500, ReserveHorizon: "sunrise"}}
	assert.ErrorContains(t, ValidateWindows(bad, true), "unknown reserve_horizon")
}
