package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
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
	schedulerMode string
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
		schedulerMode: "crontab",
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
		nil,
		args.schedulerMode, true)

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
		{
			name:      "scheduler_mode auto rejected",
			prepare:   func(a *scheduleValidationArgs) { a.schedulerMode = "auto" },
			errSubstr: "#149",
		},
		{
			name:      "scheduler_mode unknown value",
			prepare:   func(a *scheduleValidationArgs) { a.schedulerMode = "invalid" },
			errSubstr: "unknown scheduler_mode",
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
				nil,
				args.schedulerMode, true)

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
		nil,
		args.schedulerMode, true)

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
				nil,
				args.schedulerMode, true)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "must be contained")
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestCheckScheduleScheduleOptimizeMode(t *testing.T) {
	withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 0)
	args := validScheduleValidationArgs()

	err := checkScheduleschedule(args.crontab, args.apiKey, args.url, args.froniusIP, args.pwConsumption,
		args.maxCharge, args.pwBattReserve, args.startHr, args.endHr, nil, "optimize", true)
	require.NoError(t, err, "optimize is a valid scheduler_mode")

	err = checkScheduleschedule(args.crontab, args.apiKey, args.url, args.froniusIP, args.pwConsumption,
		args.maxCharge, args.pwBattReserve, args.startHr, args.endHr, nil, "invalid", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "crontab, windows or optimize")
}

// setOptimizeStartupViper configures a live optimize-mode config whose only
// problem is the given surcharge value.
func setOptimizeStartupViper(t *testing.T, surcharge string, dryRun bool) {
	t.Helper()
	resetViper(t)
	viper.Set("ha_url", "http://homeassistant.local:8123")
	viper.Set("ha_token", "test-ha-token")
	viper.Set("opt_price_entity", "sensor.price")
	viper.Set("opt_consumption_entity", "sensor.load")
	viper.Set("pw_consumption", 12000.0)
	viper.Set("fronius_ip", schOptIP)
	viper.Set("opt_purchase_surcharge", surcharge)
	viper.Set("opt_dry_run", dryRun)
}

func withStartupWriter(t *testing.T) *schOptWriter {
	t.Helper()
	w := &schOptWriter{ev: &schOptEvents{}}
	old := newDispatchWriter
	newDispatchWriter = func() dispatchWriter { return w }
	t.Cleanup(func() { newDispatchWriter = old })
	return w
}

// T-0153 / TC-001c (S-001, ADR-0004 §3): a startup validation failure in live
// optimize mode attempts ordered defaults before failing; dry-run does not.
func TestPrepareOptimizeMode_T0153_StartupFailSafe(t *testing.T) {
	surcharge23 := strings.TrimSuffix(strings.Repeat("0.1,", 23), ",")
	surcharge24 := strings.TrimSuffix(strings.Repeat("0.1,", 24), ",")
	cases := []struct {
		name         string
		surcharge    string
		dryRun       bool
		froniusIP    *string
		baseErr      error
		wantErr      string
		wantDefaults int
	}{
		{name: "live, 23 surcharge values", surcharge: surcharge23, wantErr: "opt_purchase_surcharge", wantDefaults: 1},
		{name: "dry-run, 23 surcharge values", surcharge: surcharge23, dryRun: true, wantErr: "opt_purchase_surcharge", wantDefaults: 0},
		{name: "live, generic validation error", surcharge: surcharge24, baseErr: errors.New("the --apikey flag must be set"), wantErr: "--apikey", wantDefaults: 1},
		{name: "live, no fronius_ip", surcharge: surcharge23, froniusIP: new(string), wantErr: "opt_purchase_surcharge", wantDefaults: 0},
		{name: "live, valid", surcharge: surcharge24, wantDefaults: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setOptimizeStartupViper(t, tc.surcharge, tc.dryRun)
			if tc.froniusIP != nil {
				viper.Set("fronius_ip", *tc.froniusIP)
			}
			w := withStartupWriter(t)

			cfg, deps, err := prepareOptimizeMode(tc.baseErr)

			_, defaults, _ := w.counts()
			assert.Equal(t, tc.wantDefaults, defaults)
			if tc.wantErr == "" {
				require.NoError(t, err)
				assert.NotNil(t, deps.HA)
				assert.False(t, cfg.DryRun)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestOptStartupFailSafe_Timeout(t *testing.T) {
	old := optStartupDefaultsTimeout
	optStartupDefaultsTimeout = 20 * time.Millisecond
	t.Cleanup(func() { optStartupDefaultsTimeout = old })
	w := withStartupWriter(t)
	w.defaultsDelay = 200 * time.Millisecond

	start := time.Now()
	optStartupFailSafe(OptimizeConfig{FroniusIP: schOptIP})
	assert.Less(t, time.Since(start), 150*time.Millisecond, "the attempt is bounded")
	time.Sleep(250 * time.Millisecond) // let the background write finish
}

// T-0153: `sbam schedule` in live optimize mode returns an error (exit code 1
// via Execute) after the defaults attempt.
func TestScheduleCommand_T0153_OptimizeValidationFailureExits(t *testing.T) {
	snapshotScheduleGlobals(t)
	setOptimizeStartupViper(t, strings.TrimSuffix(strings.Repeat("0.1,", 23), ","), false)
	viper.Set("scheduler_mode", "optimize")
	viper.Set("apikey", "test-api-key")
	viper.Set("url", "https://solcast.example.invalid/forecasts")
	viper.Set("start_hr", "00:00")
	viper.Set("end_hr", "06:00")
	viper.Set("crontab", const_ct)
	viper.Set("cache_time", 7200)
	viper.Set("forecast_horizon", "default")
	viper.Set("consumption_horizon", "full_day")
	w := withStartupWriter(t)

	err := scdCmd.RunE(scdCmd, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "opt_purchase_surcharge")
	_, defaults, _ := w.counts()
	assert.Equal(t, 1, defaults)
	assert.True(t, scdCmd.SilenceUsage)
}

// snapshotScheduleGlobals restores the package variables scdCmd.RunE assigns.
func snapshotScheduleGlobals(t *testing.T) {
	t.Helper()
	strs := []*string{&s_apiKey, &s_url, &start_hr, &end_hr, &batt_reserve_start_hr, &batt_reserve_end_hr, &w_start_hr, &w_end_hr,
		&crontab, &s_cache_file_prefix, &mqtt_broker, &mqtt_client_id, &mqtt_username, &mqtt_password, &mqtt_tls_ca_file,
		&mqtt_tls_client_cert, &mqtt_tls_client_cert_key, &mqtt_topic_prefix, &mqtt_ha_discovery_prefix, &forecast_horizon,
		&consumption_horizon, &scheduler_mode, &fronius_ip}
	floats := []*float64{&pw_consumption, &max_charge, &pw_lwt, &pw_upt, &pw_batt_reserve}
	bools := []*bool{&s_defaults, &s_cache_forecast, &mqtt_enabled, &mqtt_ha_discovery, &mqtt_tls_insecure_skip, &weekday_feature}
	strVals := make([]string, len(strs))
	for i, p := range strs {
		strVals[i] = *p
	}
	floatVals := make([]float64, len(floats))
	for i, p := range floats {
		floatVals[i] = *p
	}
	boolVals := make([]bool, len(bools))
	for i, p := range bools {
		boolVals[i] = *p
	}
	cacheTime := s_cache_time
	silenceUsage, silenceErrors := scdCmd.SilenceUsage, scdCmd.SilenceErrors
	t.Cleanup(func() {
		for i, p := range strs {
			*p = strVals[i]
		}
		for i, p := range floats {
			*p = floatVals[i]
		}
		for i, p := range bools {
			*p = boolVals[i]
		}
		s_cache_time = cacheTime
		scdCmd.SilenceUsage, scdCmd.SilenceErrors = silenceUsage, silenceErrors
	})
}
