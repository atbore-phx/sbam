package optimize

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-0055 (TC-004a, S-004): HA numeric states; anything not a finite number is
// a gap.
func TestParseHAFloat(t *testing.T) {
	ok := map[string]float64{
		"1234.5": 1234.5,
		" -3 ":   -3,
		"0":      0,
		"1e3":    1000,
	}
	for in, want := range ok {
		t.Run("ok "+in, func(t *testing.T) {
			v, valid := ParseHAFloat(in)
			assert.True(t, valid)
			assert.InDelta(t, want, v, 1e-12)
		})
	}
	for _, in := range []string{"", "  ", "unavailable", "unknown", "Unknown", "None", "nan", "NaN", "inf", "-Inf", "+Infinity", "1e400", "abc", "12W", "0x10"} {
		t.Run("gap "+in, func(t *testing.T) {
			v, valid := ParseHAFloat(in)
			assert.False(t, valid)
			assert.Zero(t, v)
		})
	}
}

func TestSampleFromHAState(t *testing.T) {
	at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, Sample{At: at, Value: 42, Valid: true}, SampleFromHAState(at, "42"))
	assert.Equal(t, Sample{At: at}, SampleFromHAState(at, "unavailable"))
}

// T-0051/T-0056 (TC-004e): unit conversion and power bounds [−1000, 50000] W.
func TestSanitizePowerSamples(t *testing.T) {
	at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	in := []Sample{
		{At: at, Value: 3.7, Valid: true},
		{At: at, Value: -1.0, Valid: true},  // −1000 W: lower bound, kept
		{At: at, Value: 50.0, Valid: true},  // 50000 W: upper bound, kept
		{At: at, Value: 50.01, Valid: true}, // above bound
		{At: at, Value: -1.01, Valid: true}, // below bound
		{At: at, Value: 1.0, Valid: false},  // already a gap
	}
	out, err := SanitizePowerSamples(in, "kW")
	require.NoError(t, err)
	require.Len(t, out, len(in))
	assert.Equal(t, Sample{At: at, Value: 3700, Valid: true}, out[0])
	assert.True(t, out[1].Valid)
	assert.InDelta(t, -1000, out[1].Value, 1e-9)
	assert.True(t, out[2].Valid)
	assert.False(t, out[3].Valid)
	assert.False(t, out[4].Valid)
	assert.False(t, out[5].Valid)
	assert.InDelta(t, 3.7, in[0].Value, 1e-12, "input not modified")

	w, err := SanitizePowerSamples([]Sample{{At: at, Value: 60000, Valid: true}, {At: at, Value: 500, Valid: true}}, " W ")
	require.NoError(t, err)
	assert.False(t, w[0].Valid)
	assert.Equal(t, 500.0, w[1].Value)

	for _, unit := range []string{"kVA", "", "w", "MW"} {
		_, err := SanitizePowerSamples(in, unit)
		assert.ErrorIs(t, err, ErrUnsupportedUnit, unit)
	}
}
