package cmd

import (
	"testing"

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
		nil)

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
			name:      "start and end cannot be equal",
			prepare:   func(a *scheduleValidationArgs) { a.startHr = "12:00"; a.endHr = "12:00" },
			errSubstr: "must not be equal",
		},
		{
			name:      "invalid start format",
			prepare:   func(a *scheduleValidationArgs) { a.startHr = "bad" },
			errSubstr: "invalid start_hr",
		},
		{
			name:      "invalid end format",
			prepare:   func(a *scheduleValidationArgs) { a.endHr = "bad" },
			errSubstr: "invalid end_hr",
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
			name:      "reserve start and end cannot be equal",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "06:00", "06:00", 0, 0, 0) },
			errSubstr: "must not be equal",
		},
		{
			name:      "invalid reserve start format",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "bad", "06:00", 0, 0, 0) },
			errSubstr: "invalid batt_reserve_start_hr",
		},
		{
			name:      "invalid reserve end format",
			prepare:   func(a *scheduleValidationArgs) {},
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "05:00", "bad", 0, 0, 0) },
			errSubstr: "invalid batt_reserve_end_hr",
		},
		{
			name:      "reserve window must be contained in charge window",
			prepare:   func(a *scheduleValidationArgs) { a.startHr = "22:00"; a.endHr = "06:00" },
			setup:     func(t *testing.T) { withScheduleValidationGlobals(t, "02:00", "08:00", 0, 0, 0) },
			errSubstr: "must be contained",
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
				nil)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSubstr)
		})
	}
}

func TestCheckScheduleScheduleCrossMidnightChargeWindowValid(t *testing.T) {
	withScheduleValidationGlobals(t, "23:00", "05:00", 0, 0, 0)
	args := validScheduleValidationArgs()
	args.startHr = "22:00"
	args.endHr = "06:00"

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
		nil)

	require.NoError(t, err)
}

func TestCheckScheduleScheduleReserveWindowContainment(t *testing.T) {
	tests := []struct {
		name         string
		startHr      string
		endHr        string
		reserveStart string
		reserveEnd   string
		wantErr      bool
	}{
		{
			name:         "cross-midnight reserve contained",
			startHr:      "22:00",
			endHr:        "06:00",
			reserveStart: "23:00",
			reserveEnd:   "05:00",
			wantErr:      false,
		},
		{
			name:         "same-day reserve segment contained in cross-midnight outer",
			startHr:      "22:00",
			endHr:        "06:00",
			reserveStart: "02:00",
			reserveEnd:   "05:00",
			wantErr:      false,
		},
		{
			name:         "reserve exceeds cross-midnight outer end",
			startHr:      "22:00",
			endHr:        "06:00",
			reserveStart: "02:00",
			reserveEnd:   "08:00",
			wantErr:      true,
		},
		{
			name:         "cross-midnight reserve outside same-day outer",
			startHr:      "08:00",
			endHr:        "18:00",
			reserveStart: "17:00",
			reserveEnd:   "07:00",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			withScheduleValidationGlobals(t, tt.reserveStart, tt.reserveEnd, 0, 0, 0)
			args := validScheduleValidationArgs()
			args.startHr = tt.startHr
			args.endHr = tt.endHr

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
				nil)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "must be contained")
				return
			}

			require.NoError(t, err)
		})
	}
}
