package power

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ValidateWindows tests ---

func TestValidateWindows_TwoNonOverlapping(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "02:00", End: "06:00", MaxCharge: 3500},
		{Name: "midday", Start: "12:00", End: "15:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}

func TestValidateWindows_EmptyList(t *testing.T) {
	err := ValidateWindows([]Window{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one entry")
}

// --- ResolveActiveWindow tests ---

func TestResolveActiveWindow_TwoWindows(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "02:00", End: "06:00", MaxCharge: 3500},
		{Name: "midday", Start: "12:00", End: "15:00", MaxCharge: 2000},
	}

	// Use a fixed date so time resolution is deterministic.
	base := time.Date(2026, 6, 3, 0, 0, 0, 0, time.Local)

	// 04:00 → night window.
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 4, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "night", active.Name)
	_ = base

	// 13:00 → midday window.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 13, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "midday", active.Name)

	// 09:00 → no window active.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 9, 0, 0, 0, time.Local))
	assert.Nil(t, active)
}

func TestResolveActiveWindow_CrossMidnightPlusDaytime(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "22:00", End: "04:00", MaxCharge: 3000},
		{Name: "day", Start: "10:00", End: "14:00", MaxCharge: 2000},
	}

	// 23:00 → night window.
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 23, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "night", active.Name)

	// 03:00 (next day still night, cross-midnight).
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 4, 3, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "night", active.Name)

	// 12:00 → day window.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 12, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "day", active.Name)

	// 08:00 → no window.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 8, 0, 0, 0, time.Local))
	assert.Nil(t, active)
}

func TestResolveActiveWindow_BoundaryTickInclusive(t *testing.T) {
	windows := []Window{
		{Name: "morning", Start: "02:00", End: "06:00", MaxCharge: 3500},
	}

	// Exactly at start → active.
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 2, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "morning", active.Name)

	// Exactly at end → active (inclusive end).
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 6, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "morning", active.Name)

	// One minute after end → not active.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 6, 1, 0, 0, time.Local))
	assert.Nil(t, active)
}

func TestResolveActiveWindow_FirstWindowWinsAtSharedBoundary(t *testing.T) {
	// Adjacent windows share a boundary at 06:00. The first window (A)
	// should be returned because both include 06:00 (inclusive end/start)
	// and windows are evaluated in list order.
	windows := []Window{
		{Name: "A", Start: "02:00", End: "06:00", MaxCharge: 1000},
		{Name: "B", Start: "06:00", End: "10:00", MaxCharge: 2000},
	}

	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 6, 0, 0, 0, time.Local))
	require.NotNil(t, active)
	assert.Equal(t, "A", active.Name)
}

// --- Overlap detection tests ---

func TestValidateWindows_OverlappingRejected(t *testing.T) {
	windows := []Window{
		{Name: "A", Start: "02:00", End: "06:00", MaxCharge: 3500},
		{Name: "B", Start: "04:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlaps")
}

func TestValidateWindows_CrossMidnightOverlapRejected(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "22:00", End: "04:00", MaxCharge: 3000},
		{Name: "early", Start: "03:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlaps")
}

func TestValidateWindows_AdjacentNonOverlapping(t *testing.T) {
	// Adjacent windows sharing a boundary point are NOT overlapping
	// because half-open intervals are used for overlap detection.
	windows := []Window{
		{Name: "A", Start: "02:00", End: "06:00", MaxCharge: 1000},
		{Name: "B", Start: "06:00", End: "10:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}

func TestValidateWindows_CrossMidnightAdjacentNonOverlapping(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "22:00", End: "04:00", MaxCharge: 3000},
		{Name: "early", Start: "04:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}

// --- Individual field validation tests ---

func TestValidateWindows_ZeroWidthRejected(t *testing.T) {
	windows := []Window{
		{Name: "zero", Start: "02:00", End: "02:00", MaxCharge: 3500},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero-width")
}

func TestValidateWindows_InvalidTimeFormat(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "25:00", End: "26:00", MaxCharge: 3500},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start time")
}

func TestValidateWindows_InvalidForecastHorizon(t *testing.T) {
	windows := []Window{
		{Name: "bad-fh", Start: "02:00", End: "06:00", MaxCharge: 3500, ForecastHorizon: "bogus"},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forecast_horizon")
}

func TestValidateWindows_InvalidConsumptionHorizon(t *testing.T) {
	windows := []Window{
		{Name: "bad-ch", Start: "02:00", End: "06:00", MaxCharge: 3500, ConsumptionHorizon: "bogus"},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consumption_horizon")
}

func TestValidateWindows_NegativeMaxCharge(t *testing.T) {
	windows := []Window{
		{Name: "neg", Start: "02:00", End: "06:00", MaxCharge: -100},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_charge")
}

// --- Auto-generated names tests ---

func TestValidateWindows_AutoNameForErrorMessages(t *testing.T) {
	// Empty names are auto-generated for error messages only. Overlapping
	// windows with empty names should produce an error that mentions the
	// synthetic names.
	windows := []Window{
		{Start: "02:00", End: "06:00", MaxCharge: 3500},
		{Start: "04:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "window-1")
	assert.Contains(t, err.Error(), "window-2")
	assert.Contains(t, err.Error(), "overlaps")
}

// WindowNameOrDefault returns the window's Name if non-empty, or a
// synthetic label "window-N" based on its 0-based index.
func TestWindowNameOrDefault(t *testing.T) {
	assert.Equal(t, "night", WindowNameOrDefault(Window{Name: "night"}, 0))
	assert.Equal(t, "window-1", WindowNameOrDefault(Window{}, 0))
	assert.Equal(t, "window-3", WindowNameOrDefault(Window{}, 2))
}

// --- SyntheticLegacyWindow test ---

func TestSyntheticLegacyWindow(t *testing.T) {
	w := SyntheticLegacyWindow("02:00", "06:00", 3500, "default", "full_day")
	assert.Equal(t, "legacy", w.Name)
	assert.Equal(t, "02:00", w.Start)
	assert.Equal(t, "06:00", w.End)
	assert.Equal(t, 3500.0, w.MaxCharge)
	assert.Equal(t, "default", w.ForecastHorizon)
	assert.Equal(t, "full_day", w.ConsumptionHorizon)
}

// --- Cross-midnight window accepted by ValidateWindows ---

func TestValidateWindows_CrossMidnightValid(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "22:00", End: "04:00", MaxCharge: 3000},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}

// --- Windows-mode field validation tests ---

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func TestValidateWindows_TickMinutesValid(t *testing.T) {
	windows := []Window{
		{Name: "fast", Start: "06:00", End: "07:00", MaxCharge: 3500, TickMinutes: intPtr(30)},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}

func TestValidateWindows_TickMinutesZeroRejected(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "06:00", End: "07:00", MaxCharge: 3500, TickMinutes: intPtr(0)},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tick_minutes")
}

func TestValidateWindows_TickMinutesNegativeRejected(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "06:00", End: "07:00", MaxCharge: 3500, TickMinutes: intPtr(-1)},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tick_minutes")
}

func TestValidateWindows_DefaultsAndBeforeEndValid(t *testing.T) {
	windows := []Window{
		{Name: "with-defaults", Start: "06:00", End: "07:00", MaxCharge: 3500,
			Defaults: boolPtr(true), BeforeEndDefaults: intPtr(10)},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}

func TestValidateWindows_BeforeEndDefaultsZeroValid(t *testing.T) {
	windows := []Window{
		{Name: "at-end", Start: "06:00", End: "07:00", MaxCharge: 3500,
			Defaults: boolPtr(true), BeforeEndDefaults: intPtr(0)},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}

func TestValidateWindows_BeforeEndDefaultsNegativeRejected(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "06:00", End: "07:00", MaxCharge: 3500,
			Defaults: boolPtr(true), BeforeEndDefaults: intPtr(-1)},
	}
	err := ValidateWindows(windows)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before_end_defaults")
}

func TestValidateWindows_NewFieldsOptional(t *testing.T) {
	// A window without any of the new fields should still validate.
	windows := []Window{
		{Name: "plain", Start: "06:00", End: "07:00", MaxCharge: 3500},
	}
	err := ValidateWindows(windows)
	assert.NoError(t, err)
}
