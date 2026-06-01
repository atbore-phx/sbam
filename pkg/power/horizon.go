package power

import (
	"fmt"
	"time"
)

// ForecastHorizon selects which forecast window is used for solar
// production estimation.
type ForecastHorizon string

const (
	ForecastHorizonDefault        ForecastHorizon = "default"
	ForecastHorizonNextSolarDay   ForecastHorizon = "next_solar_day"
	ForecastHorizonRemainingToday ForecastHorizon = "remaining_today"
	ForecastHorizonToday          ForecastHorizon = "today"
	ForecastHorizonTomorrow       ForecastHorizon = "tomorrow"
	ForecastHorizonOff            ForecastHorizon = "off"
)

// ValidateForecastHorizon returns a ForecastHorizon parsed from s, or an
// error when s does not name a known mode.
func ValidateForecastHorizon(s string) (ForecastHorizon, error) {
	h := ForecastHorizon(s)
	switch h {
	case ForecastHorizonDefault,
		ForecastHorizonNextSolarDay,
		ForecastHorizonRemainingToday,
		ForecastHorizonToday,
		ForecastHorizonTomorrow,
		ForecastHorizonOff:
		return h, nil
	default:
		return "", fmt.Errorf("unknown forecast_horizon %q: must be one of default, next_solar_day, remaining_today, today, tomorrow, off", s)
	}
}

// ConsumptionHorizon selects how the daily consumption value is applied.
type ConsumptionHorizon string

const (
	ConsumptionHorizonFullDay        ConsumptionHorizon = "full_day"
	ConsumptionHorizonRemainingToday ConsumptionHorizon = "remaining_today"
)

// ValidateConsumptionHorizon returns a ConsumptionHorizon parsed from s, or
// an error when s does not name a known mode.
func ValidateConsumptionHorizon(s string) (ConsumptionHorizon, error) {
	h := ConsumptionHorizon(s)
	switch h {
	case ConsumptionHorizonFullDay,
		ConsumptionHorizonRemainingToday:
		return h, nil
	default:
		return "", fmt.Errorf("unknown consumption_horizon %q: must be one of full_day, remaining_today", s)
	}
}

// ResolveForecastDay maps a ForecastHorizon and the current time to the
// calendar day whose Solcast intervals should be summed, an optional
// "after" filter that excludes periods ending at or before that point
// (used by remaining_today), and a skip flag that signals the caller to
// bypass forecast retrieval entirely.
//
// The returned day is in the local timezone of now.
func ResolveForecastDay(h ForecastHorizon, now time.Time) (day time.Time, after *time.Time, skip bool) {
	switch h {
	case ForecastHorizonDefault, ForecastHorizonNextSolarDay:
		day = checkSun(now)
		return day, nil, false
	case ForecastHorizonRemainingToday:
		y, m, d := now.Date()
		day = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		return day, &now, false
	case ForecastHorizonToday:
		y, m, d := now.Date()
		day = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		return day, nil, false
	case ForecastHorizonTomorrow:
		y, m, d := now.Date()
		day = time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
		return day, nil, false
	case ForecastHorizonOff:
		return time.Time{}, nil, true
	default:
		// Unreachable when ValidateForecastHorizon is used; fall back to
		// default behaviour.
		day = checkSun(now)
		return day, nil, false
	}
}

// checkSun implements the original CheckSun noon-threshold logic: before
// 12:00 local time it returns today, at or after 12:00 it returns
// tomorrow. This is kept as a private helper and is exercised by the
// exported CheckSun wrapper for backward compatibility.
func checkSun(now time.Time) time.Time {
	if now.Hour() < 12 {
		return now
	}
	return now.AddDate(0, 0, 1)
}

// ResolveConsumption computes the effective daily consumption value from
// the static pwConsumption and the chosen ConsumptionHorizon at the given
// time now (local timezone).
//
//   - full_day returns pwConsumption unchanged.
//   - remaining_today returns pwConsumption scaled by the fraction of the
//     local day that remains (seconds remaining / 86400).
func ResolveConsumption(h ConsumptionHorizon, pwConsumption float64, now time.Time) float64 {
	switch h {
	case ConsumptionHorizonFullDay:
		return pwConsumption
	case ConsumptionHorizonRemainingToday:
		elapsed := now.Hour()*3600 + now.Minute()*60 + now.Second()
		remaining := 86400 - elapsed
		if remaining < 0 {
			remaining = 0
		}
		return pwConsumption * float64(remaining) / 86400.0
	default:
		return pwConsumption
	}
}
