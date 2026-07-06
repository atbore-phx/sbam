package power

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateForecastHorizon_Valid(t *testing.T) {
	valid := []string{"default", "next_solar_day", "remaining_today", "today", "tomorrow", "off"}
	for _, s := range valid {
		t.Run(s, func(t *testing.T) {
			h, err := ValidateForecastHorizon(s)
			assert.NoError(t, err)
			assert.Equal(t, s, h)
		})
	}
}

func TestValidateForecastHorizon_Invalid(t *testing.T) {
	_, err := ValidateForecastHorizon("bogus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown forecast_horizon")
}

func TestValidateConsumptionHorizon_Valid(t *testing.T) {
	valid := []string{"full_day", "remaining_today"}
	for _, s := range valid {
		t.Run(s, func(t *testing.T) {
			h, err := ValidateConsumptionHorizon(s)
			assert.NoError(t, err)
			assert.Equal(t, s, h)
		})
	}
}

func TestValidateConsumptionHorizon_Invalid(t *testing.T) {
	_, err := ValidateConsumptionHorizon("bogus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown consumption_horizon")
}

func TestResolveForecastDay_Default(t *testing.T) {
	loc := time.UTC

	// Before noon → today.
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)
	day, after, skip := ResolveForecastDay(ForecastHorizonDefault, now)
	assert.False(t, skip)
	assert.Nil(t, after)
	assert.Equal(t, 2026, day.Year())
	assert.Equal(t, time.June, day.Month())
	assert.Equal(t, 1, day.Day())

	// After noon → tomorrow.
	now = time.Date(2026, 6, 1, 14, 0, 0, 0, loc)
	day, after, skip = ResolveForecastDay(ForecastHorizonDefault, now)
	assert.False(t, skip)
	assert.Nil(t, after)
	assert.Equal(t, 2026, day.Year())
	assert.Equal(t, time.June, day.Month())
	assert.Equal(t, 2, day.Day())

	// Exactly noon → tomorrow.
	now = time.Date(2026, 6, 1, 12, 0, 0, 0, loc)
	day, after, skip = ResolveForecastDay(ForecastHorizonDefault, now)
	assert.False(t, skip)
	assert.Nil(t, after)
	assert.Equal(t, 2, day.Day())
}

func TestResolveForecastDay_NextSolarDay(t *testing.T) {
	loc := time.UTC

	// next_solar_day mirrors default.
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)
	day, after, skip := ResolveForecastDay(ForecastHorizonNextSolarDay, now)
	assert.False(t, skip)
	assert.Nil(t, after)
	assert.Equal(t, 1, day.Day())

	now = time.Date(2026, 6, 1, 14, 0, 0, 0, loc)
	day, after, skip = ResolveForecastDay(ForecastHorizonNextSolarDay, now)
	assert.False(t, skip)
	assert.Nil(t, after)
	assert.Equal(t, 2, day.Day())
}

func TestResolveForecastDay_RemainingToday(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 1, 14, 30, 0, 0, loc)

	day, after, skip := ResolveForecastDay(ForecastHorizonRemainingToday, now)
	assert.False(t, skip)
	assert.NotNil(t, after)
	assert.Equal(t, 1, day.Day())
	// after should point to the same instant as now.
	assert.True(t, after.Equal(now))
}

func TestResolveForecastDay_Today(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)

	day, after, skip := ResolveForecastDay(ForecastHorizonToday, now)
	assert.False(t, skip)
	assert.Nil(t, after)
	assert.Equal(t, 1, day.Day())
}

func TestResolveForecastDay_Tomorrow(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)

	day, after, skip := ResolveForecastDay(ForecastHorizonTomorrow, now)
	assert.False(t, skip)
	assert.Nil(t, after)
	assert.Equal(t, 2, day.Day())
}

func TestResolveForecastDay_Off(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)

	day, after, skip := ResolveForecastDay(ForecastHorizonOff, now)
	assert.True(t, skip)
	assert.Nil(t, after)
	assert.True(t, day.IsZero())
}

func TestResolveForecastDay_NearMidnight(t *testing.T) {
	// default at 23:30 → tomorrow (which is the next calendar day).
	loc := time.UTC
	now := time.Date(2026, 6, 1, 23, 30, 0, 0, loc)
	day, _, skip := ResolveForecastDay(ForecastHorizonDefault, now)
	assert.False(t, skip)
	assert.Equal(t, 2, day.Day())

	// remaining_today near midnight still returns today with after filter.
	day, after, skip := ResolveForecastDay(ForecastHorizonRemainingToday, now)
	assert.False(t, skip)
	assert.Equal(t, 1, day.Day())
	assert.True(t, after.Equal(now))
}

func TestResolveConsumption_FullDay(t *testing.T) {
	now := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	got := ResolveConsumption(ConsumptionHorizonFullDay, 10000, now)
	assert.Equal(t, 10000.0, got)
}

func TestResolveConsumption_RemainingToday(t *testing.T) {
	// Exactly noon → 12h remaining → 50%.
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	got := ResolveConsumption(ConsumptionHorizonRemainingToday, 12000, now)
	assert.InDelta(t, 6000.0, got, 1.0)

	// Start of day → full value.
	now = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	got = ResolveConsumption(ConsumptionHorizonRemainingToday, 12000, now)
	assert.InDelta(t, 12000.0, got, 1.0)

	// End of day → near zero.
	now = time.Date(2026, 6, 1, 23, 59, 59, 0, time.UTC)
	got = ResolveConsumption(ConsumptionHorizonRemainingToday, 12000, now)
	assert.InDelta(t, 0.0, got, 1.0)
}

func TestResolveConsumption_RemainingToday_ClampZero(t *testing.T) {
	// If seconds_remaining would be negative it clamps to 0.
	// This can't happen with a real clock, but test the safety clamp.
	now := time.Date(2026, 6, 2, 0, 0, 1, 0, time.UTC)
	got := ResolveConsumption(ConsumptionHorizonRemainingToday, 12000, now)
	// At 00:00:01 remaining = 86399, so this is not negative.
	// The clamp is a safety net, truly negative can't happen with valid inputs.
	assert.GreaterOrEqual(t, got, 0.0)
}

func TestCheckSun_BackwardCompatibility(t *testing.T) {
	loc := time.UTC

	// Before noon → today.
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, loc)
	got := checkSun(now)
	assert.Equal(t, 1, got.Day())

	// After noon → tomorrow.
	now = time.Date(2026, 6, 1, 14, 0, 0, 0, loc)
	got = checkSun(now)
	assert.Equal(t, 2, got.Day())
}
