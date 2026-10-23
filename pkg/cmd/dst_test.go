package cmd

import (
	"testing"
	"time"

	pw "sbam/pkg/power"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// madridDST loads a zone with a DST change; Europe leaves summer time on the
// last Sunday of October (2026-10-25 03:00 CEST -> 02:00 CET).
func madridDST(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skipf("tzdata not available: %v", err)
	}
	return loc
}

func TestNextWindowStartAcrossDSTEnd(t *testing.T) {
	loc := madridDST(t)
	r := NewRunner(RunnerConfig{
		Windows:        []pw.Window{{Name: "valle", Start: "00:00", End: "08:00", MaxCharge: 2500, Weekdays: "mon-fri"}},
		WeekdayFeature: true,
	}, nil)
	friday := time.Date(2026, 10, 23, 12, 21, 0, 0, loc)

	_, nextAt, ok := r.nextWindowStart(friday)

	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 10, 26, 0, 0, 0, 0, loc), nextAt, "Monday 00:00 local, not Monday 23:00 after the DST change")
}

func TestNextMatchingWeekdayKeepsWallClock(t *testing.T) {
	loc := madridDST(t)
	saturday := time.Date(2026, 10, 24, 0, 0, 0, 0, loc)

	got := nextMatchingWeekday(saturday, map[time.Weekday]bool{time.Monday: true})

	assert.Equal(t, time.Date(2026, 10, 26, 0, 0, 0, 0, loc), got)
}

func TestNextClockAtAcrossDSTEnd(t *testing.T) {
	loc := madridDST(t)
	evening := time.Date(2026, 10, 24, 23, 0, 0, 0, loc)

	got, err := nextClockAt(evening, "08:00")

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 25, 8, 0, 0, 0, loc), got, "08:00 local on the DST day, not 07:00")
}

func TestCooldownAcrossDSTEnd(t *testing.T) {
	loc := madridDST(t)
	// Cross-midnight window 22:00-08:00; at 23:00 the window ends tomorrow 08:00
	// local. 07:56 on the DST day is inside the 5-minute cooldown, 06:56 is not.
	in, err := isInCooldown(time.Date(2026, 10, 24, 23, 0, 0, 0, loc), "22:00", "08:00", 5)
	require.NoError(t, err)
	assert.False(t, in)

	in, err = isInCooldown(time.Date(2026, 10, 25, 7, 56, 0, 0, loc), "22:00", "08:00", 5)
	require.NoError(t, err)
	assert.True(t, in)
}
