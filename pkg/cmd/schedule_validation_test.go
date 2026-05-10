package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scheduleValidationArgs struct {
	crontab       string
	apiKey        string
	url           string
	froniusIP     string
	pwConsumption float64
	maxCharge     float64
	pwBattReserve float64
	startHr       string
	endHr         string
}

func validScheduleValidationArgs() scheduleValidationArgs {
	return scheduleValidationArgs{
		crontab:       "0 0 * * *",
		apiKey:        "key",
		url:           "https://example.test/forecast",
		froniusIP:     "127.0.0.1",
		pwConsumption: 1000,
		maxCharge:     3500,
		pwBattReserve: 200,
		startHr:       "00:00",
		endHr:         "23:59",
	}
}

func withScheduleValidationGlobals(t *testing.T, reserveStart, reserveEnd string, lwt, upt float64, cacheTime int32) {
	t.Helper()
	oldReserveStart := batt_reserve_start_hr
	oldReserveEnd := batt_reserve_end_hr
	oldLwt := pw_lwt
	oldUpt := pw_upt
	oldCacheTime := s_cache_time

	batt_reserve_start_hr = reserveStart
	batt_reserve_end_hr = reserveEnd
	pw_lwt = lwt
	pw_upt = upt
	s_cache_time = cacheTime

	t.Cleanup(func() {
		batt_reserve_start_hr = oldReserveStart
		batt_reserve_end_hr = oldReserveEnd
		pw_lwt = oldLwt
		pw_upt = oldUpt
		s_cache_time = oldCacheTime
	})
}

func TestStartEndHelpers(t *testing.T) {
	assert.True(t, isStartBeforeEnd("01:00", "02:00"))
	assert.False(t, isStartBeforeEnd("02:00", "02:00"))
	assert.True(t, isStartAfterEnd("03:00", "02:00"))
	assert.False(t, isStartAfterEnd("01:00", "02:00"))
}

func TestStartEndHelpersPanicOnInvalidTime(t *testing.T) {
	assert.Panics(t, func() { _ = isStartBeforeEnd("bad", "02:00") })
	assert.Panics(t, func() { _ = isStartAfterEnd("01:00", "bad") })
}

func TestCheckTimeRangeBranchesAndPanic(t *testing.T) {
	now := time.Now()
	startInside := now.Add(-1 * time.Minute).Format("15:04")
	endInside := now.Add(1 * time.Minute).Format("15:04")
	assert.True(t, CheckTimeRange(startInside, endInside))

	startOutside := now.Add(-3 * time.Hour).Format("15:04")
	endOutside := now.Add(-2 * time.Hour).Format("15:04")
	assert.False(t, CheckTimeRange(startOutside, endOutside))

	assert.Panics(t, func() { _ = CheckTimeRange("xx", "yy") })
}

func TestCheckScheduleScheduleValid(t *testing.T) {
	withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 0)
	args := validScheduleValidationArgs()

	err := checkScheduleschedule(
		args.crontab,
		args.apiKey,
		args.url,
		args.froniusIP,
		args.pwConsumption,
		args.maxCharge,
		args.pwBattReserve,
		args.startHr,
		args.endHr,
	)

	require.NoError(t, err)
}

func TestCheckScheduleScheduleValidationErrors(t *testing.T) {
	cases := []struct {
		name      string
		prepare   func(*scheduleValidationArgs)
		setup     func(t *testing.T)
		errSubstr string
	}{
		{
			name:      "missing fronius ip",
			prepare:   func(a *scheduleValidationArgs) { a.froniusIP = "  " },
			errSubstr: "--fronius_ip",
		},
		{
			name:      "missing api key",
			prepare:   func(a *scheduleValidationArgs) { a.apiKey = "" },
			errSubstr: "--apikey",
		},
		{
			name:      "missing url",
			prepare:   func(a *scheduleValidationArgs) { a.url = "" },
			errSubstr: "--url",
		},
		{
			name:      "start must be before end",
			prepare:   func(a *scheduleValidationArgs) { a.startHr = "12:00"; a.endHr = "11:00" },
			errSubstr: "is not before end_hr",
		},
		{
			name:      "missing crontab",
			prepare:   func(a *scheduleValidationArgs) { a.crontab = "" },
			errSubstr: "crontab must to be integer > 0",
		},
		{
			name:      "negative pw_consumption",
			prepare:   func(a *scheduleValidationArgs) { a.pwConsumption = -1 },
			errSubstr: "pw_consumption",
		},
		{
			name:      "negative max_charge",
			prepare:   func(a *scheduleValidationArgs) { a.maxCharge = -1 },
			errSubstr: "max_charge",
		},
		{
			name:      "negative pw_lwt",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "05:00", "06:00", -1, 0, 0) },
			errSubstr: "pw_lwt",
		},
		{
			name:      "negative pw_upt",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "05:00", "06:00", 0, -1, 0) },
			errSubstr: "pw_upt",
		},
		{
			name:      "negative battery reserve",
			prepare:   func(a *scheduleValidationArgs) { a.pwBattReserve = -1 },
			errSubstr: "pw_batt_reserve",
		},
		{
			name:      "reserve range must be increasing",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "07:00", "06:00", 0, 0, 0) },
			errSubstr: "batt_reserve_start_hr",
		},
		{
			name:      "start must be before reserve start",
			prepare:   func(a *scheduleValidationArgs) { a.startHr = "07:00" },
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 0) },
			errSubstr: "start_hr",
		},
		{
			name:      "reserve end must be before end",
			prepare:   func(a *scheduleValidationArgs) { a.endHr = "06:30" },
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "05:00", "07:00", 0, 0, 0) },
			errSubstr: "batt_reserve_end_hr",
		},
		{
			name:      "cache_time upper bound",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 86401) },
			errSubstr: "cache_time",
		},
		{
			name:      "cache_time lower bound",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, -1) },
			errSubstr: "cache_time",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			} else {
				withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 0)
			}

			args := validScheduleValidationArgs()
			tc.prepare(&args)

			err := checkScheduleschedule(
				args.crontab,
				args.apiKey,
				args.url,
				args.froniusIP,
				args.pwConsumption,
				args.maxCharge,
				args.pwBattReserve,
				args.startHr,
				args.endHr,
			)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSubstr)
		})
	}
}
