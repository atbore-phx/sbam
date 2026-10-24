package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const optCfgTestSurcharge = "0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,2.04,2.04,2.04,2.04,0.78,0.78,0.78"

// optCfgNoEnv is a getenv that never finds SUPERVISOR_TOKEN.
func optCfgNoEnv(string) string { return "" }

// optCfgSupervisorEnv simulates an add-on with homeassistant_api: true.
func optCfgSupervisorEnv(k string) string {
	if k == "SUPERVISOR_TOKEN" {
		return "test-sup-token"
	}
	return ""
}

// optCfgValidBase sets a minimal valid optimize configuration in viper.
func optCfgValidBase(t *testing.T) {
	t.Helper()
	resetViper(t)
	viper.Set("ha_url", "http://homeassistant.local:8123")
	viper.Set("ha_token", "test-ha-token")
	viper.Set("opt_price_entity", "sensor.nordpool_kwh_dk2_dkk_3_10_025")
	viper.Set("opt_consumption_entity", "sensor.solarnet_power_load_consumed")
	viper.Set("pw_consumption", 12000.0)
}

func optCfgProblems(t *testing.T, err error) []string {
	t.Helper()
	require.Error(t, err)
	var ce *optimizeConfigError
	require.ErrorAs(t, err, &ce)
	return ce.Problems
}

// T-0120: accepted surcharge forms.
func TestParseSurcharge_T0120_Valid(t *testing.T) {
	want := [24]float64{0.52, 0.52, 0.52, 0.52, 0.52, 0.52, 0.78, 0.78, 0.78, 0.78, 0.78, 0.78, 0.78, 0.78, 0.78, 0.78, 0.78, 2.04, 2.04, 2.04, 2.04, 0.78, 0.78, 0.78}
	list := make([]any, 24)
	for i := range list {
		list[i] = want[i]
	}
	strs := strings.Split(optCfgTestSurcharge, ",")
	spaced := " " + strings.ReplaceAll(optCfgTestSurcharge, ",", " , ") + " "

	cases := []struct {
		name string
		in   any
	}{
		{"comma string", optCfgTestSurcharge},
		{"comma string with spaces", spaced},
		{"yaml list", list},
		{"string slice", strs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSurcharge(tc.in)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}

	t.Run("yaml list of ints and strings", func(t *testing.T) {
		mixed := make([]any, 24)
		for i := range mixed {
			if i%2 == 0 {
				mixed[i] = 1
			} else {
				mixed[i] = "-0.5"
			}
		}
		got, err := parseSurcharge(mixed)
		require.NoError(t, err)
		assert.Equal(t, 1.0, got[0])
		assert.Equal(t, -0.5, got[1])
	})

	t.Run("unset yields zeros", func(t *testing.T) {
		for _, in := range []any{nil, "", "  "} {
			got, err := parseSurcharge(in)
			require.NoError(t, err)
			assert.Equal(t, [24]float64{}, got)
		}
	})
}

// T-0120: wrong count, decimal comma, bad items, out-of-range (S-016).
func TestParseSurcharge_T0120_Errors(t *testing.T) {
	n := func(count int, v string) string {
		return strings.TrimSuffix(strings.Repeat(v+",", count), ",")
	}
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"23 values", n(23, "0.5"), "expected exactly 24 comma-separated numbers (hour 00..23), got 23; use '.' as decimal separator"},
		{"25 values", n(25, "0.5"), "got 25"},
		{"decimal comma", n(23, "0.5") + ",1,5", "got 25; use '.' as decimal separator"},
		{"empty item", n(22, "0.5") + ",,0.5", `item 23 ("") is not a number`},
		{"decimal comma inside 24 items", n(6, "0.5") + ",0,78x," + n(16, "0.5"), `item 8 ("78x") is not a number`},
		{"letters", n(23, "0.5") + ",abc", `item 24 ("abc") is not a number`},
		{"NaN", n(23, "0.5") + ",NaN", "must be a finite number"},
		{"Inf", "Inf," + n(23, "0.5"), "must be a finite number"},
		{"above 10", n(23, "0.5") + ",10.5", `item 24 ("10.5") must be a finite number between -5 and 10`},
		{"below -5", "-5.1," + n(23, "0.5"), "item 1"},
		{"list of 23", optCfgAnyList(23), "got 23"},
		{"list with bool", append(optCfgAnyList(23), true), "item 24 (true) is not a number"},
		{"unsupported type", 42, "unsupported value type int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSurcharge(tc.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "opt_purchase_surcharge: ")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// T-0120: the 7th item "0,78x" form from UX §2.2 is reported per item when
// the count happens to be 24.
func TestParseSurcharge_T0120_ItemMessage(t *testing.T) {
	items := strings.Split(optCfgTestSurcharge, ",")
	items[6] = "0.78x"
	_, err := parseSurcharge(strings.Join(items, ","))
	require.Error(t, err)
	assert.Equal(t, `opt_purchase_surcharge: item 7 ("0.78x") is not a number`, err.Error())
}

func TestLoadOptimizeConfig_Expected(t *testing.T) {
	optCfgValidBase(t)
	viper.Set("opt_ev_entity", "sensor.tesla_wall_connector_total_power")
	viper.Set("opt_purchase_surcharge", optCfgTestSurcharge)
	viper.Set("fronius_ip", "192.0.2.10")
	viper.Set("url", "https://example.invalid/a, https://example.invalid/b,")
	viper.Set("apikey", "test-api-key")
	viper.Set("cache_file_prefix", "pfx")
	viper.Set("cache_time", 172800)

	cfg, warns, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	assert.Empty(t, warns)

	assert.Equal(t, "sensor.nordpool_kwh_dk2_dkk_3_10_025", cfg.PriceEntity)
	assert.Equal(t, "sensor.tesla_wall_connector_total_power", cfg.EVEntity)
	assert.Equal(t, 0.52, cfg.Surcharge[0])
	assert.Equal(t, 2.04, cfg.Surcharge[17])
	assert.Equal(t, 12000.0, cfg.StaticDailyWh)
	assert.Equal(t, "192.0.2.10", cfg.FroniusIP)
	assert.Equal(t, []string{"https://example.invalid/a", "https://example.invalid/b"}, cfg.SolcastURLs)
	assert.Equal(t, "test-api-key", cfg.SolcastAPIKey)
	assert.Equal(t, "pfx", cfg.CacheFilePref)
	assert.Equal(t, int32(172800), cfg.CacheTime)
	assert.Equal(t, 48*time.Hour, cfg.ForecastMaxAge, "max(cache_time, 24h)")
	assert.Equal(t, time.Local, cfg.Location)
}

func TestLoadOptimizeConfig_Defaults(t *testing.T) {
	optCfgValidBase(t)

	cfg, _, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)

	assert.Equal(t, 10, cfg.HistoryDays)
	assert.Equal(t, 0.83, cfg.Efficiency)
	assert.Equal(t, 4500.0, cfg.MaxChargeW)
	assert.Equal(t, 4500.0, cfg.MaxDischargeW)
	assert.Equal(t, 7.0, cfg.MinSocPct)
	assert.Equal(t, 0.25, cfg.VATRate)
	assert.Equal(t, 0.0, cfg.SellMargin)
	assert.Equal(t, [24]float64{}, cfg.Surcharge)
	assert.True(t, cfg.DryRun, "opt_dry_run defaults to true for every target")
	assert.Equal(t, "", cfg.EVEntity)
	assert.Equal(t, "cached_forecast", cfg.CacheFilePref)
	assert.Equal(t, int32(7200), cfg.CacheTime)
	assert.Equal(t, 24*time.Hour, cfg.ForecastMaxAge, "cache_time below 24h is floored to 24h")
}

// Defaults are identical whether they come from registered flag defaults or
// from loadOptimizeConfig's own fallbacks.
func TestLoadOptimizeConfig_FlagDefaultsMatch(t *testing.T) {
	optCfgValidBase(t)
	c := &cobra.Command{Use: "probe", Run: func(*cobra.Command, []string) {}}
	registerOptimizeFlags(c)
	c.Flags().Int32("cache_time", 7200, "")
	c.Flags().String("cache_file_prefix", "cached_forecast", "")
	require.NoError(t, bindFlags(c))

	fromFlags, _, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)

	optCfgValidBase(t)
	fromFallback, _, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)

	assert.Equal(t, fromFallback, fromFlags)
}

// T-0121 / TC-016a/b (S-016): numeric bounds.
func TestLoadOptimizeConfig_T0121_Bounds(t *testing.T) {
	cases := []struct {
		key  string
		val  any
		want string
	}{
		{"opt_battery_efficiency", 0.0, "opt_battery_efficiency: must be greater than 0 and at most 1, got 0"},
		{"opt_battery_efficiency", 1.2, "opt_battery_efficiency: must be greater than 0 and at most 1, got 1.2"},
		{"opt_battery_efficiency", "NaN", "opt_battery_efficiency: must be a finite number"},
		{"opt_max_charge_rate_w", 0.0, "opt_max_charge_rate_w: must be greater than 0"},
		{"opt_max_charge_rate_w", -1.0, "opt_max_charge_rate_w: must be greater than 0"},
		{"opt_max_charge_rate_w", 1e9, "opt_max_charge_rate_w: must be greater than 0 and at most 30000 W, got 1e+09"},
		{"opt_max_discharge_rate_w", 0.0, "opt_max_discharge_rate_w: must be greater than 0"},
		{"opt_max_discharge_rate_w", 30001.0, "opt_max_discharge_rate_w"},
		{"opt_min_soc_pct", 100.0, "opt_min_soc_pct: must be at least 0 and below 100, got 100"},
		{"opt_min_soc_pct", -1.0, "opt_min_soc_pct"},
		{"opt_vat_rate", 25.0, "opt_vat_rate: must be a fraction between 0 and 1, got 25; did you mean 0.25?"},
		{"opt_vat_rate", -0.1, "opt_vat_rate: must be a fraction between 0 and 1"},
		{"opt_sell_margin", 11.0, "opt_sell_margin: must be between -5 and 10, got 11"},
		{"opt_sell_margin", -5.5, "opt_sell_margin"},
		{"opt_sell_margin", "1,5", `opt_sell_margin: "1,5" is not a number; use '.' as decimal separator`},
		{"opt_history_days", 0, "opt_history_days: must be between 1 and 30, got 0"},
		{"opt_history_days", 31, "opt_history_days: must be between 1 and 30, got 31"},
		{"opt_history_days", "ten", `opt_history_days: "ten" is not a whole number`},
		{"pw_consumption", 0.0, "pw_consumption: must be greater than 0"},
		{"opt_purchase_surcharge", "1,5", "opt_purchase_surcharge: expected exactly 24 comma-separated numbers (hour 00..23), got 2"},
		{"opt_dry_run", "maybe", `opt_dry_run: "maybe" is not true or false`},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			optCfgValidBase(t)
			viper.Set(tc.key, tc.val)

			cfg, _, err := optCfgLoad(optCfgNoEnv)
			problems := optCfgProblems(t, err)
			require.Len(t, problems, 1)
			assert.Contains(t, problems[0], tc.want)
			assert.True(t, cfg.DryRun, "dry run must stay on when validation fails")
		})
	}
}

// T-0018 (AC-11b): opt_min_gain defaults to 0.50, accepts [0, 10] and
// rejects values outside it or non-numbers.
func TestLoadOptimizeConfig_T0018_MinGain(t *testing.T) {
	optCfgValidBase(t)
	cfg, _, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	assert.Equal(t, 0.50, cfg.MinGain, "default")
	assert.Equal(t, 0.50, optParams(cfg).MinGain, "passed to the solver")

	for _, ok := range []any{0.0, 0.5, 10.0} {
		optCfgValidBase(t)
		viper.Set("opt_min_gain", ok)
		cfg, _, err = optCfgLoad(optCfgNoEnv)
		require.NoError(t, err, "value %v", ok)
		assert.Equal(t, ok, cfg.MinGain)
	}
	for _, bad := range []any{-1.0, 11.0, "NaN", "0,5"} {
		optCfgValidBase(t)
		viper.Set("opt_min_gain", bad)
		_, _, err = optCfgLoad(optCfgNoEnv)
		problems := optCfgProblems(t, err)
		require.Len(t, problems, 1, "value %v", bad)
		assert.Contains(t, problems[0], "opt_min_gain")
	}
}

// T-0097 (AC-11b): opt_wear_cost defaults to 0, accepts [0, 5] and rejects
// values outside it or non-numbers.
func TestLoadOptimizeConfig_T0097_WearCost(t *testing.T) {
	optCfgValidBase(t)
	cfg, _, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	assert.Equal(t, 0.0, cfg.WearCost, "default")

	for _, ok := range []any{0.0, 0.15, "0.15", 5.0} {
		optCfgValidBase(t)
		viper.Set("opt_wear_cost", ok)
		cfg, _, err = optCfgLoad(optCfgNoEnv)
		require.NoError(t, err, "value %v", ok)
		assert.Contains(t, []float64{0, 0.15, 5}, cfg.WearCost)
	}
	assert.Equal(t, 5.0, optParams(cfg).WearCost, "passed to the solver")

	for _, bad := range []any{-0.1, 5.5, "NaN", "0,15"} {
		optCfgValidBase(t)
		viper.Set("opt_wear_cost", bad)
		_, _, err = optCfgLoad(optCfgNoEnv)
		problems := optCfgProblems(t, err)
		require.Len(t, problems, 1, "value %v", bad)
		assert.Contains(t, problems[0], "opt_wear_cost")
	}
}

// T-0211 (SPEC-0002 AC-11): opt_ev_guard_w defaults to 1000 W, accepts
// [0, 50000] and rejects values outside it or non-numbers.
func TestLoadOptimizeConfig_T0211_EVGuardW(t *testing.T) {
	optCfgValidBase(t)
	cfg, _, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	assert.Equal(t, 1000.0, cfg.EVGuardW, "default")

	for in, want := range map[any]float64{0.0: 0, 1500.0: 1500, 50000.0: 50000, "2500": 2500} {
		optCfgValidBase(t)
		viper.Set("opt_ev_guard_w", in)
		cfg, _, err = optCfgLoad(optCfgNoEnv)
		require.NoError(t, err, "value %v", in)
		assert.Equal(t, want, cfg.EVGuardW)
	}
	for _, bad := range []any{-1.0, 50001.0, "NaN", "1,5"} {
		optCfgValidBase(t)
		viper.Set("opt_ev_guard_w", bad)
		_, _, err = optCfgLoad(optCfgNoEnv)
		problems := optCfgProblems(t, err)
		require.Len(t, problems, 1, "value %v", bad)
		assert.Contains(t, problems[0], "opt_ev_guard_w")
	}
}

// T-0121 edge: inclusive bounds are accepted.
func TestLoadOptimizeConfig_T0121_InclusiveBounds(t *testing.T) {
	optCfgValidBase(t)
	viper.Set("opt_battery_efficiency", 1.0)
	viper.Set("opt_max_charge_rate_w", 30000.0)
	viper.Set("opt_min_soc_pct", 0.0)
	viper.Set("opt_vat_rate", 1.0)
	viper.Set("opt_sell_margin", -5.0)
	viper.Set("opt_history_days", 30)
	viper.Set("opt_dry_run", "false")

	cfg, _, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	assert.False(t, cfg.DryRun)
	assert.Equal(t, 30, cfg.HistoryDays)
}

// T-0122 (AC-20): required entities and HA access.
func TestLoadOptimizeConfig_T0122_Required(t *testing.T) {
	t.Run("empty price entity", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("opt_price_entity", "")
		_, _, err := optCfgLoad(optCfgNoEnv)
		assert.Equal(t, []string{"opt_price_entity: required when scheduler_mode=optimize (e.g. sensor.nordpool_kwh_dk2_dkk_3_10_025)"}, optCfgProblems(t, err))
	})
	t.Run("empty consumption entity", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("opt_consumption_entity", "  ")
		_, _, err := optCfgLoad(optCfgNoEnv)
		problems := optCfgProblems(t, err)
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0], "opt_consumption_entity: required")
	})
	t.Run("non-sensor domain", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("opt_price_entity", "input_number.price")
		_, _, err := optCfgLoad(optCfgNoEnv)
		assert.Contains(t, optCfgProblems(t, err)[0], `opt_price_entity: "input_number.price" must match ^sensor\.[a-z0-9_]+$`)
	})
	t.Run("no HA access", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("ha_url", "")
		viper.Set("ha_token", "")
		_, _, err := optCfgLoad(optCfgNoEnv)
		problems := optCfgProblems(t, err)
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0], "home assistant access: no Supervisor token found and ha_url/ha_token are not set")
	})
	t.Run("ha_url without token", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("ha_token", "")
		_, _, err := optCfgLoad(optCfgNoEnv)
		assert.Equal(t, []string{"ha_token: required when ha_url is set"}, optCfgProblems(t, err))
	})
	t.Run("supervisor token suffices", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("ha_url", "")
		viper.Set("ha_token", "")
		cfg, _, err := optCfgLoad(optCfgSupervisorEnv)
		require.NoError(t, err)
		assert.Empty(t, cfg.HAToken, "the Supervisor token must never be copied into the config")
	})
	t.Run("empty EV entity is OK", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("opt_ev_entity", "")
		_, _, err := optCfgLoad(optCfgNoEnv)
		assert.NoError(t, err)
	})
	t.Run("invalid EV entity", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("opt_ev_entity", "sensor.Tesla")
		_, _, err := optCfgLoad(optCfgNoEnv)
		assert.Contains(t, optCfgProblems(t, err)[0], "opt_ev_entity")
	})
}

// UX §5.2: all problems are aggregated into one message.
func TestLoadOptimizeConfig_AggregatesProblems(t *testing.T) {
	resetViper(t)
	viper.Set("opt_purchase_surcharge", strings.Repeat("0.5,", 22)+"0.5")
	viper.Set("opt_battery_efficiency", 0.0)
	viper.Set("pw_consumption", 1000.0)

	_, _, err := optCfgLoad(optCfgNoEnv)
	problems := optCfgProblems(t, err)
	assert.Len(t, problems, 5)
	msg := err.Error()
	assert.True(t, strings.HasPrefix(msg, "invalid configuration for scheduler_mode=optimize (5 problems):\n  - opt_price_entity: required"))
	assert.Contains(t, msg, "\n  - opt_purchase_surcharge: expected exactly 24 comma-separated numbers (hour 00..23), got 23")
	assert.Contains(t, msg, "\n  - opt_battery_efficiency: must be greater than 0 and at most 1, got 0")
	assert.Contains(t, msg, "\n  - home assistant access:")
}

func TestOptimizeConfigError_SingularNoun(t *testing.T) {
	err := &optimizeConfigError{Problems: []string{"ha_token: required when ha_url is set"}}
	assert.Equal(t, "invalid configuration for scheduler_mode=optimize (1 problem):\n  - ha_token: required when ha_url is set", err.Error())
}

// T-0123 (TC-006c): min SoC below 5 % warns but does not fail.
func TestLoadOptimizeConfig_T0123_MinSocWarning(t *testing.T) {
	optCfgValidBase(t)
	viper.Set("opt_min_soc_pct", 2.0)

	cfg, warns, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	assert.Equal(t, 2.0, cfg.MinSocPct)
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0], "opt_min_soc_pct is 2")
}

// TC-014a (S-014): unsafe ha_url values fail validation without echoing secrets.
func TestLoadOptimizeConfig_TC014a_HAURL(t *testing.T) {
	cases := []string{"ftp://x", "http://user:test-pw@ha:8123", "http://ha:8123/?token=x", "http://ha:8123/#frag", "not a url"}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			optCfgValidBase(t)
			viper.Set("ha_url", in)
			_, _, err := optCfgLoad(optCfgNoEnv)
			problems := optCfgProblems(t, err)
			require.Len(t, problems, 1)
			assert.True(t, strings.HasPrefix(problems[0], "ha_url: "), problems[0])
			assert.NotContains(t, err.Error(), "test-pw")
			assert.NotContains(t, err.Error(), "test-ha-token")
		})
	}

	t.Run("entity id injection (TC-014c)", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set("opt_price_entity", "sensor.a&end_time=2000")
		_, _, err := optCfgLoad(optCfgNoEnv)
		assert.Contains(t, optCfgProblems(t, err)[0], "opt_price_entity")
	})
}

// S-014: plain http to a public host is allowed but warned.
func TestLoadOptimizeConfig_PlainHTTPPublicHostWarning(t *testing.T) {
	optCfgValidBase(t)
	viper.Set("ha_url", "http://ha.example.com:8123")

	_, warns, err := optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0], "plain http")
	assert.NotContains(t, warns[0], "test-ha-token")

	optCfgValidBase(t)
	viper.Set("ha_url", "https://ha.example.com")
	_, warns, err = optCfgLoad(optCfgNoEnv)
	require.NoError(t, err)
	assert.Empty(t, warns)
}

// loadOptimizeConfig reads SUPERVISOR_TOKEN from the real environment.
func TestLoadOptimizeConfig_UsesProcessEnv(t *testing.T) {
	optCfgValidBase(t)
	viper.Set("ha_url", "")
	viper.Set("ha_token", "")
	t.Setenv("SUPERVISOR_TOKEN", "test-sup-token")

	_, _, err := loadOptimizeConfig()
	require.NoError(t, err)
	assert.NotContains(t, viper.AllKeys(), "supervisor_token")
}

// optCfgRegisteredKeys enumerates the ha_*/opt_* keys that
// registerOptimizeFlags registers, from a fresh cobra command, so tests never
// rely on a hand-written key list (T-0127, T-0172).
func optCfgRegisteredKeys(t *testing.T) []*pflag.Flag {
	t.Helper()
	c := &cobra.Command{Use: "probe"}
	registerOptimizeFlags(c)
	var out []*pflag.Flag
	c.Flags().VisitAll(func(f *pflag.Flag) { out = append(out, f) })
	require.NotEmpty(t, out)
	return out
}

// T-0127 (UX §2.4): on the real `sbam schedule` and `sbam optimize` commands
// there are no short-flag collisions (local or inherited), every ha_*/opt_*
// flag exists on both and is long-only, and --dry-run never becomes a viper
// key.
func TestRegisterOptimizeFlags_T0127(t *testing.T) {
	registered := optCfgRegisteredKeys(t)
	commands := []*cobra.Command{scdCmd, optCmd}

	for _, c := range commands {
		t.Run(c.Name()+"/no short-flag collisions", func(t *testing.T) {
			owner := map[string]string{}
			check := func(f *pflag.Flag) {
				if f.Shorthand == "" {
					return
				}
				if prev, dup := owner[f.Shorthand]; dup && prev != f.Name {
					t.Errorf("-%s is used by both --%s and --%s", f.Shorthand, prev, f.Name)
				}
				owner[f.Shorthand] = f.Name
			}
			c.LocalFlags().VisitAll(check)
			c.InheritedFlags().VisitAll(check)
			// cobra adds -h lazily; it must not be taken by any flag either.
			if name, taken := owner["h"]; taken {
				assert.Equal(t, "help", name, "-h is reserved for --help")
			}
		})

		t.Run(c.Name()+"/every opt_* flag present and long-only", func(t *testing.T) {
			for _, want := range registered {
				got := c.Flags().Lookup(want.Name)
				if !assert.NotNil(t, got, "--%s missing on %s", want.Name, c.Name()) {
					continue
				}
				assert.Empty(t, got.Shorthand, "--%s must be long-only", want.Name)
				assert.Equal(t, want.DefValue, got.DefValue, "--%s default", want.Name)
				assert.Equal(t, want.Value.Type(), got.Value.Type(), "--%s type", want.Name)
			}
			// No stray optimize-looking flags beyond the registered set
			// (dry-run excepted: command-local to optimize).
			names := map[string]bool{}
			for _, f := range registered {
				names[f.Name] = true
			}
			c.Flags().VisitAll(func(f *pflag.Flag) {
				if optCfgIsOptimizeFlag(f.Name) && f.Name != "dry-run" {
					assert.True(t, names[f.Name], "unexpected optimize flag --%s on %s", f.Name, c.Name())
				}
			})
		})

		t.Run(c.Name()+"/dry-run is not a viper key", func(t *testing.T) {
			resetViper(t)
			require.NoError(t, bindFlags(c))
			keys := viper.AllKeys()
			assert.NotContains(t, keys, "dry-run")
			assert.NotContains(t, keys, "dry_run")
			assert.Contains(t, keys, keyOptDryRun)
		})
	}

	t.Run("dry-run only on optimize", func(t *testing.T) {
		assert.Nil(t, scdCmd.Flags().Lookup("dry-run"), "schedule must not expose --dry-run")
		f := optCmd.Flags().Lookup("dry-run")
		require.NotNil(t, f)
		assert.Contains(t, f.Annotations, flagAnnotationNoViper)
	})

	t.Run("registered set", func(t *testing.T) {
		c := &cobra.Command{Use: "probe"}
		registerOptimizeFlags(c)
		assert.Nil(t, c.Flags().Lookup("dry-run"))
		assert.Equal(t, "true", c.Flags().Lookup("opt_dry_run").DefValue)
		assert.Equal(t, "string", c.Flags().Lookup("opt_purchase_surcharge").Value.Type())
		for _, f := range registered {
			assert.True(t, optCfgIsOptimizeFlag(f.Name), "--%s must use the ha_/opt_ prefix", f.Name)
		}
	})
}

func optCfgAnyList(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = 0.5
	}
	return out
}

func optCfgIsOptimizeFlag(name string) bool {
	return strings.HasPrefix(name, "opt_") || strings.HasPrefix(name, "ha_") || name == "dry-run"
}
