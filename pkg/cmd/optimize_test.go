package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/homeassistant"
	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"
	pw "sbam/pkg/power"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// optCLIGoldenPath is the T-0160 table snapshot. Regenerate with
// SBAM_UPDATE_GOLDEN=1 go test ./pkg/cmd/ -run TestOptimizeCmd_T0160.
var optCLIGoldenPath = filepath.Join("testdata", "optimize_table.golden")

// optCLIWriterSpy counts invocations of every factory that could produce an
// inverter writer. `sbam optimize` must never call any of them (AC-19).
type optCLIWriterSpy struct {
	batteryWriter  int
	fronius        int
	dispatchWriter int // T-0160: optimize-mode dispatch writer (schedule_runner.go)
}

// optCLIEnv wires the run fakes into the package factories and seams.
type optCLIEnv struct {
	run *optRunEnv
	spy *optCLIWriterSpy
}

func newOptCLIEnv(t *testing.T) *optCLIEnv {
	t.Helper()
	resetViper(t)
	e := &optCLIEnv{run: newOptRunEnv(t), spy: &optCLIWriterSpy{}}

	origHA, origFC, origBat := newHAClient, newForecastLoader, newBatteryReader
	origBW, origFr, origDW := newBatteryWriter, newFronius, newDispatchWriter
	origNow, origLoc := optCLINow, optCLILocation
	t.Cleanup(func() {
		newHAClient, newForecastLoader, newBatteryReader = origHA, origFC, origBat
		newBatteryWriter, newFronius, newDispatchWriter = origBW, origFr, origDW
		optCLINow, optCLILocation = origNow, origLoc
		optCLIResetFlags()
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	newHAClient = func(OptimizeConfig) (haClient, error) { return e.run.ha, nil }
	newForecastLoader = func() forecastLoader { return e.run.fc }
	newBatteryReader = func() batteryReader { return e.run.bat }
	newBatteryWriter = func(fronius.SunSpecType) batteryWriter { e.spy.batteryWriter++; return nil }
	newFronius = func(fronius.SunSpecType) froniusClient { e.spy.fronius++; return nil }
	newDispatchWriter = func(fronius.SunSpecType) dispatchWriter { e.spy.dispatchWriter++; return nil }
	optCLINow = func() time.Time { return e.run.now }
	optCLILocation = func() *time.Location { return e.run.loc }
	return e
}

// optCLIResetFlags restores every optimize flag to its default so runs of
// the shared rootCmd do not leak flag values into each other.
func optCLIResetFlags() {
	optCmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

// optCLIValidArgs matches optRunCfg (placeholders only).
func optCLIValidArgs(extra ...string) []string {
	args := []string{
		"optimize",
		"--ha_url", "https://ha.example.invalid",
		"--ha_token", "test-token",
		"--opt_price_entity", optRunPriceEntity,
		"--opt_consumption_entity", optRunLoadEntity,
		"--opt_ev_entity", optRunEVEntity,
		"--opt_sell_margin", "0.1",
		"-c", "12000",
		"-H", "192.0.2.10",
		"-u", "https://solcast.example.invalid/forecasts",
		"-k", "test-api-key",
		"-l", "3600",
	}
	return append(args, extra...)
}

// execOptCLI runs the cobra command in-process and returns stdout and the
// command error (Execute maps a non-nil error to exit code 1).
func execOptCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	optCLIResetFlags()
	var out, errOut bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	err := rootCmd.Execute()
	return out.String(), err
}

// T-0160 (AC-19): the table matches the golden snapshot and no writer
// factory is invoked.
func TestOptimizeCmd_T0160_TableGolden(t *testing.T) {
	e := newOptCLIEnv(t)

	out, err := execOptCLI(t, optCLIValidArgs()...)
	require.NoError(t, err)

	if os.Getenv("SBAM_UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(optCLIGoldenPath), 0o755))
		require.NoError(t, os.WriteFile(optCLIGoldenPath, []byte(out), 0o644))
	}
	golden, err := os.ReadFile(optCLIGoldenPath)
	require.NoError(t, err, "golden missing; run with SBAM_UPDATE_GOLDEN=1")
	assert.Equal(t, strings.ReplaceAll(string(golden), "\r\n", "\n"), out)

	assert.Contains(t, out, "DRY RUN (nothing written to the inverter)")
	assert.Contains(t, out, "START             CONS Wh   PV Wh    BUY    SELL  ACTION         RATE W  SOC END %")
	assert.Contains(t, out, "Expected cost")
	assert.Contains(t, out, "Terminal value")
	assert.Equal(t, 0, e.spy.batteryWriter, "battery writer factory must never be invoked")
	assert.Equal(t, 0, e.spy.fronius, "fronius client factory must never be invoked")
	assert.Equal(t, 0, e.spy.dispatchWriter, "dispatch writer factory must never be invoked")
}

// T-0160 edge case: one row per plan hour, in the plan's order.
func TestOptimizeCmd_T0160_OneRowPerHour(t *testing.T) {
	e := newOptCLIEnv(t)
	out, err := execOptCLI(t, optCLIValidArgs()...)
	require.NoError(t, err)

	res := runOptimization(t.Context(), e.run.deps(), optCLIRunCfg(e), e.run.now, &profileCache{})
	require.Nil(t, res.Fallback)
	labels := optCLIStartLabels(res.Plan.Hours, e.run.loc)
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if rows < len(labels) && strings.HasPrefix(line, labels[rows]+" ") {
			rows++
		}
	}
	assert.Equal(t, len(res.Plan.Hours), rows)
}

// T-0160 failure case: a fallback prints the reason and the failing input,
// no table, and exits with code 2 (UX §6); 1 is reserved for config errors.
func TestOptimizeCmd_T0160_FallbackExitsTwo(t *testing.T) {
	e := newOptCLIEnv(t)
	e.run.bat = optRunFakeBattery{err: errors.New("solar api timeout")}

	out, err := execOptCLI(t, optCLIValidArgs()...)
	require.Error(t, err)
	var ec interface{ ExitCode() int }
	require.ErrorAs(t, err, &ec)
	assert.Equal(t, 2, ec.ExitCode())
	assert.Contains(t, out, "FALLBACK: optimizer_fallback_soc_unavailable - inverter defaults would be restored")
	assert.Contains(t, out, "Input       battery: solar api timeout")
	assert.NotContains(t, out, "START ")
	assert.Equal(t, 0, e.spy.batteryWriter)
	assert.Equal(t, 0, e.spy.fronius)
	assert.Equal(t, 0, e.spy.dispatchWriter)
}

// Config errors exit 1 with the aggregated validation message (UX §5.2).
func TestOptimizeCmd_ConfigErrorExitsOne(t *testing.T) {
	newOptCLIEnv(t)
	out, err := execOptCLI(t, "optimize", "--opt_battery_efficiency", "1.5")
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "invalid configuration")
	assert.Contains(t, msg, keyOptPriceEntity)
	assert.Contains(t, msg, keyOptBatteryEfficiency)
	assert.Empty(t, out)
}

// T-0161 (AC-19): --dry-run is a no-op; --dry-run=false exits 1 with the
// "only via sbam schedule" message and never runs the optimizer.
func TestOptimizeCmd_T0161_DryRunFlag(t *testing.T) {
	tests := []struct {
		name    string
		extra   []string
		wantErr bool
	}{
		{name: "absent", extra: nil},
		{name: "bare flag is a no-op", extra: []string{"--dry-run"}},
		{name: "explicit true", extra: []string{"--dry-run=true"}},
		{name: "false is rejected", extra: []string{"--dry-run=false"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newOptCLIEnv(t)
			out, err := execOptCLI(t, optCLIValidArgs(tc.extra...)...)
			if tc.wantErr {
				require.ErrorIs(t, err, errOptCLIWritesDisabled)
				assert.Contains(t, err.Error(), "only via `sbam schedule` with scheduler_mode: optimize")
				assert.Empty(t, out)
				assert.Equal(t, 0, e.run.fc.calls, "optimizer must not run")
			} else {
				require.NoError(t, err)
				assert.Contains(t, out, "DRY RUN")
			}
			assert.Equal(t, 0, e.spy.batteryWriter)
			assert.Equal(t, 0, e.spy.fronius)
			assert.Equal(t, 0, e.spy.dispatchWriter)
			assert.NotContains(t, viper.AllKeys(), "dry-run", "--dry-run must not become a viper key")
		})
	}
}

// T-0161 edge case: bindFlags skips the command-local --dry-run but binds the
// shared keys.
func TestBindFlags_SkipsCommandLocalDryRun(t *testing.T) {
	resetViper(t)
	require.NoError(t, bindFlags(optCmd))
	keys := viper.AllKeys()
	assert.NotContains(t, keys, "dry-run")
	assert.Contains(t, keys, "fronius_ip")
	assert.Contains(t, keys, keyOptDryRun)
}

// T-0162 (UX §2.4): --output json prints the PlanPayload.
func TestOptimizeCmd_T0162_JSONOutput(t *testing.T) {
	e := newOptCLIEnv(t)
	out, err := execOptCLI(t, optCLIValidArgs("--output", "json")...)
	require.NoError(t, err)

	var got mqtt.PlanPayload
	require.NoError(t, json.Unmarshal([]byte(out), &got), "output must be valid JSON")

	res := runOptimization(t.Context(), e.run.deps(), optCLIRunCfg(e), e.run.now, &profileCache{})
	require.Nil(t, res.Fallback)
	want, err := json.Marshal(optCLIPlanPayload(res, e.run.now))
	require.NoError(t, err)
	assert.JSONEq(t, string(want), out)

	assert.True(t, got.DryRun)
	assert.Equal(t, "DKK", got.Currency)
	assert.Len(t, got.Hours, got.HorizonH)
	require.NotNil(t, got.ExpectedCost)
	require.NotNil(t, got.TerminalValue)
	assert.Equal(t, 0, e.spy.batteryWriter)
	assert.Equal(t, 0, e.spy.dispatchWriter)
}

// T-0162 edge case: on fallback the JSON has empty hours and null costs.
func TestOptimizeCmd_T0162_JSONFallback(t *testing.T) {
	e := newOptCLIEnv(t)
	e.run.ha.stateErr[optRunPriceEntity] = errors.New("dial tcp: connection refused")

	out, err := execOptCLI(t, optCLIValidArgs("--output", "json")...)
	var ec interface{ ExitCode() int }
	require.ErrorAs(t, err, &ec, "a fallback still prints JSON, then exits 2")
	assert.Equal(t, 2, ec.ExitCode())
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &raw))
	assert.Equal(t, []any{}, raw["hours"])
	assert.Nil(t, raw["expected_cost"])
	assert.Nil(t, raw["terminal_value"])
	assert.Equal(t, string(optimize.ReasonFallbackHAUnreachable), raw["reason"])
	assert.InDelta(t, 50.0, raw["start_soc_pct"], 1e-9)
}

// T-0162 failure case: an unknown --output value exits 1.
func TestOptimizeCmd_T0162_UnknownOutput(t *testing.T) {
	e := newOptCLIEnv(t)
	_, err := execOptCLI(t, optCLIValidArgs("--output", "xml")...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--output")
	assert.Equal(t, 0, e.run.fc.calls)
}

// Short flags shared with schedule/estimate map to the same keys (UX §2.4).
func TestOptimizeCmd_SharedShortFlags(t *testing.T) {
	want := map[string]string{
		"u": "url", "k": "apikey", "H": "fronius_ip", "n": "cache_forecast",
		"f": "cache_file_prefix", "l": "cache_time", "c": "pw_consumption",
	}
	for short, long := range want {
		f := optCmd.Flags().ShorthandLookup(short)
		require.NotNil(t, f, "-%s", short)
		assert.Equal(t, long, f.Name)
	}
	assert.Nil(t, optCmd.Flags().ShorthandLookup("d"), "no -d short form for --dry-run")
}

func TestOptCLIStartLabels_DST(t *testing.T) {
	loc := optRunLoc(t)
	// 2026-10-25: Copenhagen falls back at 03:00 CEST -> 02:00 CET.
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC) // 02:00 CEST
	var hours []optimize.PlanHour
	for i := range 3 {
		s := start.Add(time.Duration(i) * time.Hour)
		hours = append(hours, optimize.PlanHour{Start: s, End: s.Add(time.Hour)})
	}
	labels := optCLIStartLabels(hours, loc)
	assert.Equal(t, []string{"Sun 10-25 02:00+02", "Sun 10-25 02:00+01", "Sun 10-25 03:00"}, labels)

	// Ordinary day: no offsets.
	plain := []optimize.PlanHour{{Start: time.Date(2026, 10, 5, 10, 0, 0, 0, loc)}, {Start: time.Date(2026, 10, 5, 11, 0, 0, 0, loc)}}
	assert.Equal(t, []string{"Mon 10-05 10:00", "Mon 10-05 11:00"}, optCLIStartLabels(plain, loc))
}

func TestOptCLIFormatting(t *testing.T) {
	assert.Equal(t, "10 000", optCLIThousands(10000))
	assert.Equal(t, "999", optCLIThousands(999.4))
	assert.Equal(t, "-1 234 567", optCLIThousands(-1234567))
	assert.Equal(t, "0", optCLIThousands(-0.2))
	assert.Equal(t, "1.38", optCLIPrice(1.375))
	assert.Equal(t, "0.004", optCLIPrice(0.004))
	assert.Equal(t, "0.00", optCLIPrice(0))
	assert.Equal(t, "-0.42", optCLIPrice(-0.42))
}

// optCLIRunCfg is the configuration the command builds from optCLIValidArgs.
func optCLIRunCfg(e *optCLIEnv) OptimizeConfig {
	cfg := e.run.cfg
	cfg.HAURL, cfg.HAToken = "https://ha.example.invalid", "test-token"
	cfg.CacheFilePref = optCfgDefaultCachePrefix
	cfg.DryRun = true
	cfg.MinGain = defOptMinGain // the CLI loads opt_min_gain with its default
	return cfg
}

// UX §6.1: PV row status with pv_source=ha_solcast (full table golden for
// the HA row is T-0321, wave 3).
func TestOptCLIPVStatusHA(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Copenhagen")
	require.NoError(t, err)
	now := time.Date(2026, 10, 26, 10, 20, 0, 0, loc)
	polled := time.Date(2026, 10, 26, 5, 15, 0, 0, loc)

	cases := []struct {
		name string
		res  optimizeResult
		want string
	}{
		{"last polled", optimizeResult{PVLastPolled: polled}, "last polled 2026-10-26 05:15, 5 h ago"},
		{"under an hour", optimizeResult{PVLastPolled: now.Add(-20 * time.Minute)}, "last polled 2026-10-26 10:00, <1 h ago"},
		{"not checked", optimizeResult{}, "last poll not checked"},
		{"stale", optimizeResult{PVLastPolled: polled, Reasons: []optimize.Reason{optimize.ReasonPVForecastStale}}, "stale, counted as 0 Wh - optimizer_pv_forecast_stale"},
		{"missing", optimizeResult{Reasons: []optimize.Reason{optimize.ReasonPVForecastMissing}}, "missing hours counted as 0 Wh - optimizer_pv_forecast_missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, optCLIPVStatusHA(tc.res, now, loc))
		})
	}
}

// AC-10: with pv_source=ha_solcast the PV row names the source and the
// entities; url/apikey are not needed. The forecast entities are absent in
// the fake HA, so the hours count as missing.
func TestOptimizeCmd_PVRowHA(t *testing.T) {
	newOptCLIEnv(t)
	pvTestObserveLogs(t)
	args := optCLIValidArgs("--pv_source", "ha_solcast", "--pv_entity_last_polled", "")
	// Drop -u/-k: they are not required with ha_solcast.
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-u" || args[i] == "-k" {
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}

	out, err := execOptCLI(t, filtered...)
	require.NoError(t, err)
	assert.Contains(t, out, "PV          Solcast via HA (missing hours counted as 0 Wh - optimizer_pv_forecast_missing)\n"+
		"            sensor.solcast_pv_forecast_forecast_today, sensor.solcast_pv_forecast_forecast_tomorrow\n")
	assert.NotContains(t, out, "Solcast (forecast cache)")
}

// optCLIHAGoldenPath is the T-0321 table snapshot for pv_source=ha_solcast.
// Regenerate with SBAM_UPDATE_GOLDEN=1 go test ./pkg/cmd/ -run TestOptimizeTable_PVRowHA.
var optCLIHAGoldenPath = filepath.Join("testdata", "optimize_table_ha.golden")

// optCLIHAPVStates expresses the env's Solcast-shaped forecast as the
// integration's today/tomorrow entities (period_start in local time, kW),
// plus a last-polled state.
func optCLIHAPVStates(t *testing.T, e *optCLIEnv, polled time.Time) map[string]homeassistant.State {
	t.Helper()
	loc := e.run.loc
	y, m, d := e.run.now.In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, loc)
	tomorrow := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	after := time.Date(y, m, d+2, 0, 0, 0, 0, loc)
	var td, tm []any
	for _, f := range e.run.fc.forecasts[0] {
		end, err := time.Parse(time.RFC3339, f.PeriodEnd)
		require.NoError(t, err)
		start := end.Add(-30 * time.Minute).In(loc)
		el := map[string]any{"period_start": start.Format(time.RFC3339), "pv_estimate": f.PVEstimate, "pv_estimate10": 0.0, "pv_estimate90": 0.0}
		switch {
		case !start.Before(today) && start.Before(tomorrow):
			td = append(td, el)
		case !start.Before(tomorrow) && start.Before(after):
			tm = append(tm, el)
		}
	}
	require.NotEmpty(t, td)
	require.NotEmpty(t, tm)
	return map[string]homeassistant.State{
		defPVEntityToday:      {EntityID: defPVEntityToday, State: "10.5", Attributes: map[string]any{pw.HAForecastAttribute: td}},
		defPVEntityTomorrow:   {EntityID: defPVEntityTomorrow, State: "14.0", Attributes: map[string]any{pw.HAForecastAttribute: tm}},
		defPVEntityLastPolled: {EntityID: defPVEntityLastPolled, State: polled.UTC().Format(time.RFC3339)},
	}
}

// optCLIHAArgs are optCLIValidArgs with pv_source=ha_solcast and without the
// Solcast url/apikey, which ha_solcast does not need.
func optCLIHAArgs(extra ...string) []string {
	args := optCLIValidArgs(append([]string{"--pv_source", "ha_solcast"}, extra...)...)
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-u" || args[i] == "-k" {
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}
	return filtered
}

// optCLIWithoutPVRow drops the PV row and its continuation line.
func optCLIWithoutPVRow(out string) []string {
	var lines []string
	skipCont := false
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "PV ") {
			skipCont = true
			continue
		}
		if skipCont && strings.HasPrefix(l, "            ") && strings.Contains(l, "sensor.") {
			continue
		}
		skipCont = false
		lines = append(lines, l)
	}
	return lines
}

// T-0321 (AC-10, UX §6.1): `sbam optimize` with pv_source=ha_solcast. The
// table matches optimize_table_ha.golden and differs from the solcast_api
// table only in the PV row; the JSON output is identical for the same plan.
func TestOptimizeTable_PVRowHA_T0321(t *testing.T) {
	e := newOptCLIEnv(t)
	pvTestObserveLogs(t)
	polled := time.Date(2026, 10, 5, 5, 15, 0, 0, e.run.loc)

	solcastOut, err := execOptCLI(t, optCLIValidArgs()...)
	require.NoError(t, err)
	solcastJSON, err := execOptCLI(t, optCLIValidArgs("--output", "json")...)
	require.NoError(t, err)

	for k, v := range optCLIHAPVStates(t, e, polled) {
		e.run.ha.states[k] = v
	}
	fcCalls := e.run.fc.calls

	t.Run("b: table golden with the HA PV row", func(t *testing.T) {
		out, err := execOptCLI(t, optCLIHAArgs()...)
		require.NoError(t, err)

		if os.Getenv("SBAM_UPDATE_GOLDEN") == "1" {
			require.NoError(t, os.WriteFile(optCLIHAGoldenPath, []byte(out), 0o644))
		}
		golden, err := os.ReadFile(optCLIHAGoldenPath)
		require.NoError(t, err, "golden missing; run with SBAM_UPDATE_GOLDEN=1")
		assert.Equal(t, strings.ReplaceAll(string(golden), "\r\n", "\n"), out)

		assert.Contains(t, out, "PV          Solcast via HA (last polled 2026-10-05 05:15, 5 h ago)\n"+
			"            sensor.solcast_pv_forecast_forecast_today, sensor.solcast_pv_forecast_forecast_tomorrow\n")
		assert.Equal(t, optCLIWithoutPVRow(solcastOut), optCLIWithoutPVRow(out), "only the PV row differs from solcast_api")
		assert.Equal(t, fcCalls, e.run.fc.calls, "the Solcast cache loader is not used")
		assert.Equal(t, 0, e.spy.batteryWriter)
		assert.Equal(t, 0, e.spy.dispatchWriter)
	})

	t.Run("a: solcast_api table still matches optimize_table.golden", func(t *testing.T) {
		golden, err := os.ReadFile(optCLIGoldenPath)
		require.NoError(t, err)
		assert.Equal(t, strings.ReplaceAll(string(golden), "\r\n", "\n"), solcastOut)
	})

	t.Run("c: last poll not checked", func(t *testing.T) {
		out, err := execOptCLI(t, optCLIHAArgs("--pv_entity_last_polled", "")...)
		require.NoError(t, err)
		assert.Contains(t, out, "PV          Solcast via HA (last poll not checked)\n")
	})

	t.Run("d: stale", func(t *testing.T) {
		e.run.ha.states[defPVEntityLastPolled] = homeassistant.State{State: e.run.now.Add(-25 * time.Hour).UTC().Format(time.RFC3339)}
		t.Cleanup(func() { e.run.ha.states[defPVEntityLastPolled] = optCLIHAPVStates(t, e, polled)[defPVEntityLastPolled] })
		out, err := execOptCLI(t, optCLIHAArgs()...)
		require.NoError(t, err)
		assert.Contains(t, out, "PV          Solcast via HA (stale, counted as 0 Wh - optimizer_pv_forecast_stale)\n")
	})

	t.Run("JSON output is identical to solcast_api (no new field)", func(t *testing.T) {
		out, err := execOptCLI(t, optCLIHAArgs("--output", "json")...)
		require.NoError(t, err)
		assert.JSONEq(t, solcastJSON, out)
	})
}
