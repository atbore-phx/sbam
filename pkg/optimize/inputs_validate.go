package optimize

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Power sample bounds in W, applied after unit conversion (S-004).
const (
	MinPowerSampleW = -1000.0
	MaxPowerSampleW = 50000.0
)

// ErrUnsupportedUnit is returned when a power entity's unit_of_measurement is
// neither W nor kW.
var ErrUnsupportedUnit = errors.New("unsupported power unit")

// ParseHAFloat parses a Home Assistant numeric state. It returns false for
// "unavailable", "unknown", the empty string, non-numeric text, NaN, ±Inf and
// values that overflow float64. A false result is a gap, never a zero.
func ParseHAFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "unavailable", "unknown", "none", "null":
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// SampleFromHAState converts one HA history state into a Sample. A state that
// ParseHAFloat rejects becomes a gap (Valid=false) at the same instant.
func SampleFromHAState(at time.Time, state string) Sample {
	v, ok := ParseHAFloat(state)
	return Sample{At: at, Value: v, Valid: ok}
}

// PowerUnitFactor returns the multiplier that converts a power entity's unit
// to W. Only "W" and "kW" are accepted (case-sensitive on the k/W, as HA
// reports them; surrounding whitespace is ignored).
func PowerUnitFactor(unit string) (float64, error) {
	switch strings.TrimSpace(unit) {
	case "W":
		return 1, nil
	case "kW":
		return 1000, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnsupportedUnit, unit)
	}
}

// SanitizePowerSamples converts samples to W and turns every non-finite or
// out-of-bounds value ([MinPowerSampleW, MaxPowerSampleW]) into a gap. The
// input slice is not modified. An unsupported unit returns ErrUnsupportedUnit.
func SanitizePowerSamples(samples []Sample, unit string) ([]Sample, error) {
	factor, err := PowerUnitFactor(unit)
	if err != nil {
		return nil, err
	}
	out := make([]Sample, len(samples))
	for i, s := range samples {
		out[i] = Sample{At: s.At}
		if !s.Valid {
			continue
		}
		w := s.Value * factor
		if math.IsNaN(w) || math.IsInf(w, 0) || w < MinPowerSampleW || w > MaxPowerSampleW {
			continue
		}
		out[i].Value = w
		out[i].Valid = true
	}
	return out, nil
}
