package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"
	u "sbam/src/utils"

	"github.com/spf13/cobra"
)

// `sbam optimize` (SPEC-0001 AC-19, UX §2.4/§6): compute the cost-optimal
// battery plan once and print it. The command is always a dry run: it never
// constructs a battery or dispatch writer and never calls pkg/fronius write
// functions. Inverter writes only happen from `sbam schedule` with
// scheduler_mode: optimize.
const (
	optCLIFlagDryRun = "dry-run"
	optCLIFlagOutput = "output"
	optCLIOutTable   = "table"
	optCLIOutJSON    = "json"
	// optCLIRunTimeout bounds the whole one-shot run (inputs, profile, solve).
	optCLIRunTimeout = 60 * time.Second
)

// errOptCLIWritesDisabled is returned for --dry-run=false.
var errOptCLIWritesDisabled = errors.New("sbam optimize is always a dry run: inverter writes happen only via `sbam schedule` with scheduler_mode: optimize")

// optCLINow and optCLILocation are seams for tests (deterministic output).
var (
	optCLINow      = time.Now
	optCLILocation = func() *time.Location { return time.Local }
)

var optCmd = &cobra.Command{
	Use:   "optimize",
	Short: "Compute and print the cost-optimal battery plan (dry run)",
	Long: `Compute the cost-optimal battery plan for the current price horizon and print it.
The command is always a dry run: nothing is written to the inverter.
Inverter writes only happen via 'sbam schedule' with scheduler_mode: optimize.`,
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd)
	},
	RunE: runOptimizeCmd,
}

func registerOptCmd() {
	f := optCmd.Flags()
	f.Bool(optCLIFlagDryRun, true, "print the plan only; write nothing to the inverter (always on; --dry-run=false is rejected)")
	// Command-local: never bound to viper, never in the startup dump.
	_ = f.SetAnnotation(optCLIFlagDryRun, flagAnnotationNoViper, []string{"true"})
	f.StringP("url", "u", "", "Solcast forecast URL(s), comma-separated")
	f.StringP("apikey", "k", "", "Solcast API key")
	f.StringP("fronius_ip", "H", "", "Fronius inverter LAN IP")
	f.BoolP("cache_forecast", "n", false, "accepted for compatibility; optimize always uses the forecast cache")
	f.StringP("cache_file_prefix", "f", optCfgDefaultCachePrefix, "CACHE_FILE_PREFIX")
	f.Int32P("cache_time", "l", optCfgDefaultCacheTime, "CACHE_TIME in seconds")
	f.Float64P("pw_consumption", "c", 0, "daily consumption Wh; static fallback for hours without history")
	registerOptimizeFlags(optCmd)
	f.String(optCLIFlagOutput, optCLIOutTable, "output format: table | json")

	rootCmd.AddCommand(optCmd)
}

// runOptimizeCmd validates the flags and config, runs the optimizer once and
// prints the result. A fallback result is a successful run (exit 0); config
// errors return an error (exit 1).
func runOptimizeCmd(cmd *cobra.Command, _ []string) error {
	dryRun, err := cmd.Flags().GetBool(optCLIFlagDryRun)
	if err != nil {
		return err
	}
	if !dryRun {
		return errOptCLIWritesDisabled
	}
	output, err := cmd.Flags().GetString(optCLIFlagOutput)
	if err != nil {
		return err
	}
	output = strings.ToLower(strings.TrimSpace(output))
	if output != optCLIOutTable && output != optCLIOutJSON {
		return fmt.Errorf("--output must be %q or %q, got %q", optCLIOutTable, optCLIOutJSON, output)
	}

	u.LogStartupParams(cmd, nil)

	cfg, warnings, err := loadOptimizeConfig()
	for _, w := range warnings {
		u.Log.Warn(w)
	}
	if err != nil {
		return err
	}
	cfg.Location = optCLILocation()
	cfg.DryRun = true // the CLI never writes, whatever opt_dry_run says

	deps, err := newOptimizeDeps(cfg)
	if err != nil {
		return err
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, optCLIRunTimeout)
	defer cancel()
	now := optCLINow()
	res := runOptimization(ctx, deps, cfg, now, &profileCache{})
	if res.Fallback != nil {
		u.Log.Warnw("optimizer fallback", "reason", res.Fallback.Reason, "input", res.Fallback.Input, "error", res.Fallback.Err)
	}

	out := cmd.OutOrStdout()
	if output == optCLIOutJSON {
		err = writeOptCLIJSON(out, optCLIPlanPayload(res, now))
	} else {
		err = renderOptCLITable(out, res, cfg, now)
	}
	if err != nil {
		return err
	}
	if res.Fallback != nil {
		return optCLIFallbackError{reason: string(res.Fallback.Reason)}
	}
	return nil
}

// optCLIFallbackError makes `sbam optimize` exit with code 2 when the run
// fell back (UX §6): the command worked, but the optimizer had no usable
// plan. Exit code 1 stays reserved for configuration errors.
type optCLIFallbackError struct{ reason string }

func (e optCLIFallbackError) Error() string { return "optimizer fallback: " + e.reason }

// ExitCode is read by Execute to choose the process exit code.
func (e optCLIFallbackError) ExitCode() int { return 2 }

// optCLIPlanPayload converts a run result into the <prefix>/plan shape
// (UX §7.2), rounded like mqtt.PublishPlan. On fallback, Hours is empty and
// the cost fields are null.
func optCLIPlanPayload(res optimizeResult, now time.Time) mqtt.PlanPayload {
	p := mqtt.PlanPayload{
		Timestamp: now.UTC(),
		DryRun:    true,
		Currency:  res.Currency,
		Reason:    string(res.reason()),
		Reasons:   make([]string, 0, len(res.Reasons)),
		Hours:     []mqtt.PlanEntry{},
	}
	for _, r := range res.Reasons {
		p.Reasons = append(p.Reasons, string(r))
	}
	if res.HasSoc {
		p.StartSocPct = optCLIPtr(optCLIRound(res.StartSocPct, 1))
	}
	if res.Fallback != nil || len(res.Plan.Hours) == 0 {
		return p
	}
	hours := res.Plan.Hours
	p.HorizonH = len(hours)
	end := hours[len(hours)-1].End
	p.HorizonEnd = &end
	p.ExpectedCost = optCLIPtr(optCLIRound(res.Plan.ExpectedCost, 4))
	p.TerminalValue = optCLIPtr(optCLIRound(res.Plan.TerminalValue, 4))
	for _, h := range hours {
		p.Hours = append(p.Hours, mqtt.PlanEntry{
			Start:         h.Start,
			Action:        string(h.Action),
			RateW:         optCLIRound(h.RateW, 0),
			SocEndPct:     optCLIRound(h.SocEndPct, 1),
			Buy:           optCLIRound(h.Buy, 4),
			Sell:          optCLIRound(h.Sell, 4),
			ConsumptionWh: optCLIRound(h.ConsumptionWh, 0),
			PvWh:          optCLIRound(h.PVWh, 0),
		})
	}
	return p
}

func writeOptCLIJSON(w io.Writer, p mqtt.PlanPayload) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return fmt.Errorf("write plan json: %w", err)
	}
	return nil
}

// renderOptCLITable prints the UX §6 table: header, one row per hour and the
// cost footer. On fallback it prints the fallback reason and input instead
// of the table.
func renderOptCLITable(w io.Writer, res optimizeResult, cfg OptimizeConfig, now time.Time) error {
	loc := optLoc(cfg)
	var b strings.Builder
	b.WriteString("sbam optimize - DRY RUN (nothing written to the inverter)\n")

	runAt := now.In(loc).Format("2006-01-02 15:04 -07:00")
	if res.Fallback != nil || len(res.Plan.Hours) == 0 {
		fmt.Fprintf(&b, "%-12s%s\n", "Run at", runAt)
		optCLIFallback(&b, res)
		_, err := io.WriteString(w, b.String())
		return err
	}

	hours := res.Plan.Hours
	until := hours[len(hours)-1].End.Add(-time.Minute).In(loc).Format("2006-01-02 15:04")
	fmt.Fprintf(&b, "%-12s%s     Horizon  %d h (until %s)\n", "Run at", runAt, len(hours), until)
	fmt.Fprintf(&b, "%-12sSoC %.1f %%  capacity %s Wh  min SoC %s %%  usable %s Wh\n", "Battery",
		res.StartSocPct, optCLIThousands(res.Battery.CapacityWh), optCLINum(cfg.MinSocPct), optCLIThousands(res.Battery.E0Wh))
	fmt.Fprintf(&b, "%-12sefficiency %.2f  max charge %s W  max discharge %s W\n", "Model",
		cfg.Efficiency, optCLIThousands(cfg.MaxChargeW), optCLIThousands(cfg.MaxDischargeW))
	fmt.Fprintf(&b, "%-12s%s (%s/kWh, incl. VAT)  VAT %.2f  sell margin %.2f\n", "Prices",
		cfg.PriceEntity, optCLICurrency(res.Currency), cfg.VATRate, cfg.SellMargin)
	consumption := cfg.ConsumptionEntity
	if cfg.EVEntity != "" {
		consumption += " - EV " + cfg.EVEntity
	}
	fmt.Fprintf(&b, "%-12s%s\n", "Consumption", consumption)
	fmt.Fprintf(&b, "%-12shistory %d days; static fallback hours: %s\n", "", cfg.HistoryDays, optCLIStaticStatus(res.Reasons))
	fmt.Fprintf(&b, "%-12sSolcast (%s)\n", "PV", optCLIPVStatus(res.Reasons))
	b.WriteString("\n")

	fmt.Fprintf(&b, "%-18s%7s%8s%7s%8s  %-13s%8s%11s\n", "START", "CONS Wh", "PV Wh", "BUY", "SELL", "ACTION", "RATE W", "SOC END %")
	labels := optCLIStartLabels(hours, loc)
	var importCost, exportCredit float64
	for i, h := range hours {
		fmt.Fprintf(&b, "%-18s%7d%8d%7s%8s  %-13s%8d%11.1f\n", labels[i],
			optCLIInt(h.ConsumptionWh), optCLIInt(h.PVWh), optCLIPrice(h.Buy), optCLIPrice(h.Sell),
			h.Action, optCLIInt(h.RateW), h.SocEndPct)
		if h.Cost >= 0 {
			importCost += h.Cost
		} else {
			exportCredit -= h.Cost
		}
	}
	b.WriteString("\n")

	cur := optCLICurrency(res.Currency)
	plan := res.Plan
	fmt.Fprintf(&b, "%-16s%6.2f %s  (import %.2f - export %.2f - terminal value %.2f)\n", "Expected cost",
		plan.ExpectedCost, cur, importCost, exportCredit, plan.TerminalValue)
	meanBuy := 0.0
	if cfg.Efficiency > 0 {
		meanBuy = res.Grid.TerminalPrice / cfg.Efficiency
	}
	fmt.Fprintf(&b, "%-16s%6.2f %s  (%s Wh left above min SoC x %.2f x mean buy %.2f)\n", "Terminal value",
		plan.TerminalValue, cur, optCLIThousands(hours[len(hours)-1].EndWh), cfg.Efficiency, meanBuy)
	fmt.Fprintf(&b, "%-16shour 0: %s %d W - %s\n", "Decision", hours[0].Action, optCLIInt(hours[0].RateW), plan.Reason)
	fmt.Fprintf(&b, "%-16s%s\n", "Reasons", optCLIJoinReasons(res.Reasons))

	_, err := io.WriteString(w, b.String())
	return err
}

// optCLIFallback prints the fallback block (UX §6): the reason, the failing
// input and all reasons. No table is printed.
func optCLIFallback(b *strings.Builder, res optimizeResult) {
	reason, input, detail := optimize.Reason("unknown"), "unknown", "no plan"
	if fb := res.Fallback; fb != nil {
		reason, input = fb.Reason, fb.Input
		if fb.Err != nil {
			detail = fb.Err.Error()
		}
	}
	fmt.Fprintf(b, "\nFALLBACK: %s - inverter defaults would be restored\n", reason)
	fmt.Fprintf(b, "%-12s%s: %s\n", "Input", input, detail)
	if res.HasSoc {
		fmt.Fprintf(b, "%-12sSoC %.1f %%\n", "Battery", res.StartSocPct)
	}
	fmt.Fprintf(b, "%-12s%s\n", "Reasons", optCLIJoinReasons(res.Reasons))
}

// optCLIStartLabels formats the START column in local time. Labels that
// repeat (DST fall-back) or follow a skipped hour (DST spring-forward) get
// the UTC offset appended, e.g. "Sun 10-26 02:00+02".
func optCLIStartLabels(hours []optimize.PlanHour, loc *time.Location) []string {
	const layout = "Mon 01-02 15:04"
	labels := make([]string, len(hours))
	count := make(map[string]int, len(hours))
	for i, h := range hours {
		labels[i] = h.Start.In(loc).Format(layout)
		count[labels[i]]++
	}
	for i, h := range hours {
		l := h.Start.In(loc)
		_, off := l.Zone()
		offChanged := i > 0 && func() bool { _, p := hours[i-1].Start.In(loc).Zone(); return p != off }()
		if count[labels[i]] > 1 || offChanged {
			labels[i] += l.Format("-07")
		}
	}
	return labels
}

func optCLIStaticStatus(rs []optimize.Reason) string {
	switch {
	case optHasReason(rs, optimize.ReasonConsumptionEntityNotFound):
		return "all (" + string(optimize.ReasonConsumptionEntityNotFound) + ")"
	case optHasReason(rs, optimize.ReasonConsumptionStaticFallback):
		return "some, see log (" + string(optimize.ReasonConsumptionStaticFallback) + ")"
	default:
		return "none"
	}
}

func optCLIPVStatus(rs []optimize.Reason) string {
	switch {
	case optHasReason(rs, optimize.ReasonPVForecastStale):
		return "stale, counted as 0 Wh - " + string(optimize.ReasonPVForecastStale)
	case optHasReason(rs, optimize.ReasonPVForecastMissing):
		return "missing hours counted as 0 Wh - " + string(optimize.ReasonPVForecastMissing)
	default:
		return "forecast cache"
	}
}

func optCLIJoinReasons(rs []optimize.Reason) string {
	if len(rs) == 0 {
		return "none"
	}
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}

func optCLICurrency(c string) string {
	if c == "" {
		return "?"
	}
	return c
}

// optCLIPrice renders a price in the fixed 7-char column: 2 decimals, or 3
// when the value is non-zero but rounds to 0.00.
func optCLIPrice(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	if v != 0 && (s == "0.00" || s == "-0.00") {
		s = strconv.FormatFloat(v, 'f', 3, 64)
	}
	return s
}

func optCLIInt(v float64) int {
	return int(optCLIRound(v, 0))
}

// optCLIThousands renders a rounded integer with a space as the thousands
// separator ("10 000").
func optCLIThousands(v float64) string {
	n := optCLIInt(v)
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func optCLINum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func optCLIRound(v float64, decimals int) float64 {
	f := math.Pow(10, float64(decimals))
	r := math.Round(v*f) / f
	if r == 0 {
		return 0 // avoid "-0"
	}
	return r
}

func optCLIPtr(v float64) *float64 { return &v }
