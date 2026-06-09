package power

import (
	"fmt"
	"sort"
	"time"
)

// scheduleClockLayout is the expected wall-clock format ("HH:MM").
const scheduleClockLayout = "15:04"

// clockSegment represents a half-open minute-of-day interval [startMinute, endMinute).
// endMinute is exclusive; a segment with startMinute == endMinute is zero-width.
type clockSegment struct {
	startMinute int
	endMinute   int
}

// Window describes a single charge window entry from the windows: list in
// config.yaml (or the equivalent CLI/env source).
//
// Name is optional and used for MQTT active-window identification and log
// messages. When empty, the caller should auto-generate a label such as
// "window-N".
//
// Start and End are wall-clock strings in "HH:MM" layout (local timezone).
// End may be earlier than Start to express a window that crosses midnight.
//
// MaxCharge is the maximum charge power in watts for this window. It must
// be ≥ 0 and is enforced by ValidateWindows.
//
// ForecastHorizon and ConsumptionHorizon are optional per-window overrides.
// When empty the top-level forecast_horizon / consumption_horizon is used.
type Window struct {
	Name               string  `json:"name" yaml:"name" mapstructure:"name"`
	Start              string  `json:"start" yaml:"start" mapstructure:"start"`
	End                string  `json:"end" yaml:"end" mapstructure:"end"`
	MaxCharge          float64 `json:"max_charge" yaml:"max_charge" mapstructure:"max_charge"`
	ForecastHorizon    string  `json:"forecast_horizon,omitempty" yaml:"forecast_horizon,omitempty" mapstructure:"forecast_horizon,omitempty"`
	ConsumptionHorizon string  `json:"consumption_horizon,omitempty" yaml:"consumption_horizon,omitempty" mapstructure:"consumption_horizon,omitempty"`
}

// ValidateWindows checks a non-empty ordered list of charge windows and
// returns nil when every window passes individual field validation and no
// two windows overlap. Overlap detection supports cross-midnight windows.
func ValidateWindows(windows []Window) error {
	if len(windows) == 0 {
		return fmt.Errorf("windows must contain at least one entry")
	}

	type namedSegment struct {
		seg     clockSegment
		windowN int // 0-based index into windows
	}

	var allSegments []namedSegment

	for i, w := range windows {
		if err := validateWindowFields(w); err != nil {
			return fmt.Errorf("window %q (#%d): %w", WindowNameOrDefault(w, i), i+1, err)
		}

		startTime, _ := parseClock(w.Start)
		endTime, _ := parseClock(w.End)

		startMin := clockMinute(startTime)
		endMin := clockMinute(endTime)

		if startMin == endMin {
			return fmt.Errorf("window %q (#%d): zero-width window (start and end are both %s)", WindowNameOrDefault(w, i), i+1, w.Start)
		}

		for _, seg := range expandClockMinutes(startMin, endMin) {
			allSegments = append(allSegments, namedSegment{seg: seg, windowN: i})
		}
	}

	// Sort segments by startMinute for overlap detection.
	sort.Slice(allSegments, func(i, j int) bool {
		return allSegments[i].seg.startMinute < allSegments[j].seg.startMinute
	})

	// Detect overlaps: adjacent segments [a_start, a_end) and [b_start, b_end)
	// overlap when a_start < b_end and b_start < a_end (half-open intervals).
	for i := 0; i < len(allSegments); i++ {
		for j := i + 1; j < len(allSegments); j++ {
			a := allSegments[i].seg
			b := allSegments[j].seg

			// Since segments are sorted by startMinute, if a.endMinute <= b.startMinute
			// then a cannot overlap with b nor any later segment.
			if a.endMinute <= b.startMinute {
				break
			}

			if a.startMinute < b.endMinute && b.startMinute < a.endMinute {
				wa := windows[allSegments[i].windowN]
				wb := windows[allSegments[j].windowN]
				na := WindowNameOrDefault(wa, allSegments[i].windowN)
				nb := WindowNameOrDefault(wb, allSegments[j].windowN)
				return fmt.Errorf("window %q overlaps with window %q", na, nb)
			}
		}
	}

	return nil
}

// validateWindowFields checks individual fields of a single Window and
// returns an error describing the first validation failure.
func validateWindowFields(w Window) error {
	if _, err := parseClock(w.Start); err != nil {
		return fmt.Errorf("invalid start time %q: %w", w.Start, err)
	}
	if _, err := parseClock(w.End); err != nil {
		return fmt.Errorf("invalid end time %q: %w", w.End, err)
	}
	if w.MaxCharge < 0 {
		return fmt.Errorf("max_charge must be >= 0, got %.2f", w.MaxCharge)
	}
	if w.ForecastHorizon != "" {
		if _, err := ValidateForecastHorizon(w.ForecastHorizon); err != nil {
			return err
		}
	}
	if w.ConsumptionHorizon != "" {
		if _, err := ValidateConsumptionHorizon(w.ConsumptionHorizon); err != nil {
			return err
		}
	}
	return nil
}

// ResolveActiveWindow returns a pointer to the first Window in the ordered
// list whose time range contains now. Start and end are inclusive (matching
// the existing checkTimeRangeAt semantics).
//
// Returns nil when no window contains now.
func ResolveActiveWindow(windows []Window, now time.Time) *Window {
	for i := range windows {
		startTime, err := parseClock(windows[i].Start)
		if err != nil {
			continue
		}
		endTime, err := parseClock(windows[i].End)
		if err != nil {
			continue
		}

		startAt := time.Date(now.Year(), now.Month(), now.Day(),
			startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
		endAt := time.Date(now.Year(), now.Month(), now.Day(),
			endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

		if isCrossMidnightWindow(startTime, endTime) {
			inRange := (now.After(startAt) || now.Equal(startAt)) ||
				(now.Before(endAt) || now.Equal(endAt))
			if inRange {
				return &windows[i]
			}
		} else {
			inRange := (now.After(startAt) || now.Equal(startAt)) &&
				(now.Before(endAt) || now.Equal(endAt))
			if inRange {
				return &windows[i]
			}
		}
	}
	return nil
}

// SyntheticLegacyWindow builds a single Window from the legacy top-level
// config keys (start_hr, end_hr, max_charge) and the top-level horizon
// defaults. It is used when the windows: list is absent so the runner
// behaves identically to pre-multi-window releases.
func SyntheticLegacyWindow(startHR, endHR string, maxCharge float64, forecastHorizon, consumptionHorizon string) Window {
	return Window{
		Name:               "legacy",
		Start:              startHR,
		End:                endHR,
		MaxCharge:          maxCharge,
		ForecastHorizon:    forecastHorizon,
		ConsumptionHorizon: consumptionHorizon,
	}
}

// WindowNameOrDefault returns w.Name if non-empty, or a synthetic label
// "window-N" where N is the 0-based index + 1.
func WindowNameOrDefault(w Window, index int) string {
	if w.Name != "" {
		return w.Name
	}
	return fmt.Sprintf("window-%d", index+1)
}

// parseClock parses a wall-clock string in "HH:MM" layout and returns a
// time.Time whose date portion is set by Go's parser. The returned value is
// intended only for hour/minute comparisons, not absolute timestamps.
func parseClock(value string) (time.Time, error) {
	t, err := time.Parse(scheduleClockLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid clock time %q: must be HH:MM", value)
	}
	return t, nil
}

// clockMinute returns the minute-of-day (0..1439) for the provided time.
func clockMinute(t time.Time) int {
	return t.Hour()*60 + t.Minute()
}

// expandClockMinutes converts a start and end minute-of-day into one or two
// half-open clockSegment entries.
//
// When startMinute < endMinute the window is within a single day and a
// single segment [startMinute, endMinute) is returned.
//
// When startMinute >= endMinute the window crosses midnight; two segments
// are returned: [startMinute, 1440) and [0, endMinute).
func expandClockMinutes(startMinute, endMinute int) []clockSegment {
	if startMinute < endMinute {
		return []clockSegment{{startMinute: startMinute, endMinute: endMinute}}
	}

	return []clockSegment{
		{startMinute: startMinute, endMinute: 1440},
		{startMinute: 0, endMinute: endMinute},
	}
}

// isCrossMidnightWindow returns true when the start clock time is strictly
// after the end clock time, indicating a window that spans midnight.
func isCrossMidnightWindow(startTime, endTime time.Time) bool {
	return startTime.After(endTime)
}
