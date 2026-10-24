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

	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"

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
	newBatteryWriter = func() batteryWriter { e.spy.batteryWriter++; return nil }
	newFronius = func() froniusClient { e.spy.fronius++; return nil }
	newDispatchWriter = func() dispatchWriter { e.spy.dispatchWriter++; return nil }
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
