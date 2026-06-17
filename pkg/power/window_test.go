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
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_EmptyList(t *testing.T) {
	err := ValidateWindows([]Window{}, true)
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
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 4, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "night", active.Name)
	_ = base

	// 13:00 → midday window.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 13, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "midday", active.Name)

	// 09:00 → no window active.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 9, 0, 0, 0, time.Local), true)
	assert.Nil(t, active)
}

func TestResolveActiveWindow_CrossMidnightPlusDaytime(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "22:00", End: "04:00", MaxCharge: 3000},
		{Name: "day", Start: "10:00", End: "14:00", MaxCharge: 2000},
	}

	// 23:00 → night window.
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 23, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "night", active.Name)

	// 03:00 (next day still night, cross-midnight).
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 4, 3, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "night", active.Name)

	// 12:00 → day window.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 12, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "day", active.Name)

	// 08:00 → no window.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 8, 0, 0, 0, time.Local), true)
	assert.Nil(t, active)
}

func TestResolveActiveWindow_BoundaryTickInclusive(t *testing.T) {
	windows := []Window{
		{Name: "morning", Start: "02:00", End: "06:00", MaxCharge: 3500},
	}

	// Exactly at start → active.
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 2, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "morning", active.Name)

	// Exactly at end → active (inclusive end).
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 6, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "morning", active.Name)

	// One minute after end → not active.
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 3, 6, 1, 0, 0, time.Local), true)
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

	active := ResolveActiveWindow(windows, time.Date(2026, 6, 3, 6, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "A", active.Name)
}

// --- Overlap detection tests ---

func TestValidateWindows_OverlappingRejected(t *testing.T) {
	windows := []Window{
		{Name: "A", Start: "02:00", End: "06:00", MaxCharge: 3500},
		{Name: "B", Start: "04:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlaps")
}

func TestValidateWindows_CrossMidnightOverlapRejected(t *testing.T) {
	windows := []Window{
		{Name: "night", Start: "22:00", End: "04:00", MaxCharge: 3000},
		{Name: "early", Start: "03:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overlaps")
}

func TestValidateWindows_AdjacentNonOverlapping(t *testing.T) {
	// Adjacent windows sharing a boundary point are now rejected (R6).
	windows := []Window{
		{Name: "A", Start: "02:00", End: "06:00", MaxCharge: 1000},
		{Name: "B", Start: "06:00", End: "10:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "end 06:00 equals window \"B\" start 06:00")
}

func TestValidateWindows_CrossMidnightAdjacentNonOverlapping(t *testing.T) {
	// Adjacent windows sharing a boundary point across midnight are now rejected (R6).
	windows := []Window{
		{Name: "night", Start: "22:00", End: "04:00", MaxCharge: 3000},
		{Name: "early", Start: "04:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "end 04:00 equals window \"early\" start 04:00")
}

func TestValidateWindows_EqualBoundaryBetweenFirstAndSecond(t *testing.T) {
	windows := []Window{
		{Name: "A", Start: "06:00", End: "12:00", MaxCharge: 1000},
		{Name: "B", Start: "12:00", End: "18:00", MaxCharge: 2000},
		{Name: "C", Start: "18:01", End: "22:00", MaxCharge: 1500},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "equals")
}

func TestValidateWindows_AdjacentNonEqualBoundariesPasses(t *testing.T) {
	windows := []Window{
		{Name: "day", Start: "06:00", End: "21:59", MaxCharge: 0},
		{Name: "night", Start: "22:00", End: "05:59", MaxCharge: 2000},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_SingleWindowPasses(t *testing.T) {
	windows := []Window{
		{Name: "only", Start: "06:00", End: "22:00", MaxCharge: 3000},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_EqualBoundaryNonAdjacentRejected(t *testing.T) {
	// Non-adjacent windows in list order can still share a boundary.
	// A and C share a boundary; B sits between them in list order with no overlap.
	windows := []Window{
		{Name: "A", Start: "06:00", End: "10:00", MaxCharge: 1000},
		{Name: "B", Start: "14:00", End: "18:00", MaxCharge: 2000},
		{Name: "C", Start: "10:00", End: "12:00", MaxCharge: 1500},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "equals")
}

// --- Individual field validation tests ---

func TestValidateWindows_ZeroWidthRejected(t *testing.T) {
	windows := []Window{
		{Name: "zero", Start: "02:00", End: "02:00", MaxCharge: 3500},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero-width")
}

func TestValidateWindows_InvalidTimeFormat(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "25:00", End: "26:00", MaxCharge: 3500},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid start time")
}

func TestValidateWindows_InvalidForecastHorizon(t *testing.T) {
	windows := []Window{
		{Name: "bad-fh", Start: "02:00", End: "06:00", MaxCharge: 3500, ForecastHorizon: "bogus"},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forecast_horizon")
}

func TestValidateWindows_InvalidConsumptionHorizon(t *testing.T) {
	windows := []Window{
		{Name: "bad-ch", Start: "02:00", End: "06:00", MaxCharge: 3500, ConsumptionHorizon: "bogus"},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consumption_horizon")
}

func TestValidateWindows_NegativeMaxCharge(t *testing.T) {
	windows := []Window{
		{Name: "neg", Start: "02:00", End: "06:00", MaxCharge: -100},
	}
	err := ValidateWindows(windows, true)
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
	err := ValidateWindows(windows, true)
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
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

// --- Windows-mode field validation tests ---

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func TestValidateWindows_TickMinutesValid(t *testing.T) {
	windows := []Window{
		{Name: "fast", Start: "06:00", End: "07:00", MaxCharge: 3500, TickMinutes: intPtr(30)},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_TickMinutesZeroRejected(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "06:00", End: "07:00", MaxCharge: 3500, TickMinutes: intPtr(0)},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tick_minutes")
}

func TestValidateWindows_TickMinutesNegativeRejected(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "06:00", End: "07:00", MaxCharge: 3500, TickMinutes: intPtr(-1)},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tick_minutes")
}

func TestValidateWindows_DefaultsAndBeforeEndValid(t *testing.T) {
	windows := []Window{
		{Name: "with-defaults", Start: "06:00", End: "07:00", MaxCharge: 3500,
			Defaults: boolPtr(true), BeforeEndDefaults: intPtr(10)},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_BeforeEndDefaultsZeroValid(t *testing.T) {
	windows := []Window{
		{Name: "at-end", Start: "06:00", End: "07:00", MaxCharge: 3500,
			Defaults: boolPtr(true), BeforeEndDefaults: intPtr(0)},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_BeforeEndDefaultsNegativeRejected(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "06:00", End: "07:00", MaxCharge: 3500,
			Defaults: boolPtr(true), BeforeEndDefaults: intPtr(-1)},
	}
	err := ValidateWindows(windows, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before_end_defaults")
}

func TestValidateWindows_NewFieldsOptional(t *testing.T) {
	// A window without any of the new fields should still validate.
	windows := []Window{
		{Name: "plain", Start: "06:00", End: "07:00", MaxCharge: 3500},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

// --- Weekday parsing tests ---

func TestParseWeekdays_SingleDay(t *testing.T) {
	set, err := parseWeekdays("mon")
	assert.NoError(t, err)
	assert.True(t, set[time.Monday])
	assert.Len(t, set, 1)
}

func TestParseWeekdays_CommaList(t *testing.T) {
	set, err := parseWeekdays("mon,fri")
	assert.NoError(t, err)
	assert.True(t, set[time.Monday])
	assert.True(t, set[time.Friday])
	assert.Len(t, set, 2)
}

func TestParseWeekdays_Range(t *testing.T) {
	set, err := parseWeekdays("mon-fri")
	assert.NoError(t, err)
	for wd := time.Monday; wd <= time.Friday; wd++ {
		assert.True(t, set[wd], "expected %s to be in set", wd.String())
	}
	assert.Len(t, set, 5)
}

func TestParseWeekdays_RangeAndSingle(t *testing.T) {
	set, err := parseWeekdays("mon-fri,sun")
	assert.NoError(t, err)
	assert.Len(t, set, 6)
	assert.True(t, set[time.Sunday])
}

func TestParseWeekdays_OverlappingRangesFlattened(t *testing.T) {
	set, err := parseWeekdays("mon-wed,tue-thu")
	assert.NoError(t, err)
	assert.Len(t, set, 4)
}

func TestParseWeekdays_SingleDayRange(t *testing.T) {
	set, err := parseWeekdays("mon-mon")
	assert.NoError(t, err)
	assert.Len(t, set, 1)
	assert.True(t, set[time.Monday])
}

func TestParseWeekdays_EmptyString(t *testing.T) {
	set, err := parseWeekdays("")
	assert.NoError(t, err)
	assert.Nil(t, set)
}

func TestParseWeekdays_UnknownToken(t *testing.T) {
	_, err := parseWeekdays("mon,xyz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown token")
}

func TestParseWeekdays_UnknownTokenInRange(t *testing.T) {
	_, err := parseWeekdays("mon-xyz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown token")
}

func TestParseWeekdays_EmptyElement(t *testing.T) {
	_, err := parseWeekdays("mon,,fri")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty element")
}

func TestParseWeekdays_CaseSensitiveRejected(t *testing.T) {
	_, err := parseWeekdays("MON")
	assert.Error(t, err)
}

func TestParseWeekdays_MixedCaseRangeRejected(t *testing.T) {
	_, err := parseWeekdays("mon-FRI")
	assert.Error(t, err)
}

// --- Weekday-aware ValidateWindows tests ---

func TestValidateWindows_DisjointWeekdaysNoOverlap(t *testing.T) {
	windows := []Window{
		{Name: "weekday", Start: "22:00", End: "04:00", MaxCharge: 3500, Weekdays: "mon-fri"},
		{Name: "weekend", Start: "22:00", End: "04:00", MaxCharge: 5000, Weekdays: "sat,sun"},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_OverlappingWeekdaysStillRejected(t *testing.T) {
	windows := []Window{
		{Name: "A", Start: "22:00", End: "04:00", MaxCharge: 3500, Weekdays: "mon-fri"},
		{Name: "B", Start: "22:00", End: "04:00", MaxCharge: 5000, Weekdays: "fri,sat"},
	}
	err := ValidateWindows(windows, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "overlaps")
}

func TestValidateWindows_DisjointWeekdaysAdjacentBoundaryPasses(t *testing.T) {
	windows := []Window{
		{Name: "weekday", Start: "22:00", End: "06:00", MaxCharge: 3500, Weekdays: "mon-fri"},
		{Name: "weekend", Start: "06:00", End: "22:00", MaxCharge: 5000, Weekdays: "sat,sun"},
	}
	err := ValidateWindows(windows, true)
	assert.NoError(t, err)
}

func TestValidateWindows_InvalidWeekdaysRejectedWithFlag(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "02:00", End: "06:00", MaxCharge: 3500, Weekdays: "xyz"},
	}
	err := ValidateWindows(windows, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "weekdays")
}

func TestValidateWindows_InvalidWeekdaysAcceptedWhenFlagDisabled(t *testing.T) {
	windows := []Window{
		{Name: "bad", Start: "02:00", End: "06:00", MaxCharge: 3500, Weekdays: "xyz"},
	}
	err := ValidateWindows(windows, false)
	assert.NoError(t, err)
}

func TestValidateWindows_OneEmptyWeekdaysAlwaysOverlaps(t *testing.T) {
	windows := []Window{
		{Name: "A", Start: "22:00", End: "04:00", MaxCharge: 3500, Weekdays: "mon-fri"},
		{Name: "B", Start: "22:00", End: "04:00", MaxCharge: 5000},
	}
	err := ValidateWindows(windows, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "overlaps")
}

// --- Weekday-aware ResolveActiveWindow tests ---

func TestResolveActiveWindow_WeekdayMatch(t *testing.T) {
	windows := []Window{
		{Name: "weekday", Start: "10:00", End: "14:00", MaxCharge: 3500, Weekdays: "mon-fri"},
	}
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 1, 12, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "weekday", active.Name)
}

func TestResolveActiveWindow_WeekdayNoMatch(t *testing.T) {
	windows := []Window{
		{Name: "weekday", Start: "10:00", End: "14:00", MaxCharge: 3500, Weekdays: "mon-fri"},
	}
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 6, 12, 0, 0, 0, time.Local), true)
	assert.Nil(t, active)
}

func TestResolveActiveWindow_CrossMidnightStartDayModel(t *testing.T) {
	windows := []Window{
		{Name: "fri-night", Start: "22:00", End: "04:00", MaxCharge: 3500, Weekdays: "fri"},
	}
	// Friday 23:00 — start day is Friday
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 5, 23, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "fri-night", active.Name)

	// Saturday 03:00 — post-midnight, start day is still Friday
	active = ResolveActiveWindow(windows, time.Date(2026, 6, 6, 3, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "fri-night", active.Name)
}

func TestResolveActiveWindow_CrossMidnightNotActiveNextDayStart(t *testing.T) {
	windows := []Window{
		{Name: "fri-night", Start: "22:00", End: "04:00", MaxCharge: 3500, Weekdays: "fri"},
	}
	// Saturday 23:00 — start day is Saturday, not Friday, should NOT be active
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 6, 23, 0, 0, 0, time.Local), true)
	assert.Nil(t, active)
}

func TestResolveActiveWindow_EmptyWeekdaysAlwaysActive(t *testing.T) {
	windows := []Window{
		{Name: "always", Start: "10:00", End: "14:00", MaxCharge: 3500},
	}
	// Saturday with no weekday filter
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 6, 12, 0, 0, 0, time.Local), true)
	require.NotNil(t, active)
	assert.Equal(t, "always", active.Name)
}

func TestResolveActiveWindow_FeatureDisabledIgnoresWeekdays(t *testing.T) {
	windows := []Window{
		{Name: "weekday", Start: "10:00", End: "14:00", MaxCharge: 3500, Weekdays: "mon-fri"},
	}
	// Saturday with feature disabled — should still be active
	active := ResolveActiveWindow(windows, time.Date(2026, 6, 6, 12, 0, 0, 0, time.Local), false)
	require.NotNil(t, active)
	assert.Equal(t, "weekday", active.Name)
}

func TestValidateWindows_FeatureDisabledOverlapPasses(t *testing.T) {
	windows := []Window{
		{Name: "A", Start: "02:00", End: "06:00", MaxCharge: 3500},
		{Name: "B", Start: "04:00", End: "08:00", MaxCharge: 2000},
	}
	err := ValidateWindows(windows, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "overlaps")
}
