package cmd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"sbam/pkg/homeassistant"
	"sbam/pkg/optimize"
	u "sbam/src/utils"
)

// Time budgets of one optimizer run (design §Concurrency, S-007, S-012).
const (
	// optSolveTimeout is the solver deadline; exceeding it is a solver_error.
	optSolveTimeout = 2 * time.Second
	// optProfileRebuildTimeout bounds one consumption profile build.
	optProfileRebuildTimeout = 4 * time.Minute
	// optProfileMaxAge is how long a built profile is used; after that the
	// static profile applies until the next successful rebuild.
	optProfileMaxAge = 26 * time.Hour
	// optProfileRetryInterval spaces rebuild attempts after a failed build.
	optProfileRetryInterval = time.Hour
	// optPriceLookaheadHours requests more priced hours than the horizon cap so
	// BuildGrid can report the dropped slots (design Q1).
	optPriceLookaheadHours = 48
)

// optSolve is the solver entry point; a package variable so tests can inject
// solver errors and invalid plans (T-0130).
var optSolve = optimize.Solve

// Fallback input names (optimizeFallback.Input), for logs and publishing.
const (
	optInputBattery  = "battery"
	optInputPrice    = "price"
	optInputGrid     = "grid"
	optInputSolver   = "solver"
	optInputPlan     = "plan"
	optInputHAClient = "ha_client"
)

// optimizeFallback describes why a run must restore inverter defaults (AC-16).
type optimizeFallback struct {
	Reason optimize.Reason
	Input  string
	Err    error
}

// optimizeResult is the outcome of one runOptimization call. Exactly one of
// Fallback (non-nil) or a valid Plan is set. The runner decides about writes,
// override and pause; runOptimization never writes.
type optimizeResult struct {
	// Fallback is non-nil when the inverter must go to defaults.
	Fallback *optimizeFallback
	// Plan, Grid and Battery are set on success.
	Plan    optimize.Plan
	Grid    optimize.Grid
	Battery optimize.Battery
	Params  optimize.Params
	// Reasons are all applicable codes in precedence order
	// (fallback > degraded > action). On fallback the first entry is the
	// fallback reason, followed by any degraded reasons collected so far. Use
	// reasonsWithOverride to insert an override code (paused / manual
	// override) at its precedence position.
	Reasons []optimize.Reason
	// Currency is the price currency (e.g. "DKK"); empty when no price was read.
	Currency string
	// StartSocPct is the SoC read at the start of the run; HasSoc tells whether
	// it was read successfully.
	StartSocPct float64
	HasSoc      bool
	// TerminalValue mirrors Plan.TerminalValue for publishing.
	TerminalValue float64
	// Input fingerprint for the Info run log (S-010).
	PriceMin, PriceMax, PriceMean float64
	ProfileSumWh                  float64
	// TZMismatch is true when the price entries' offset differs from the local
	// offset (S-008); the runner may surface it.
	TZMismatch bool
}

// reason returns the highest-precedence reason (last_decision_reason).
func (r optimizeResult) reason() optimize.Reason {
	if len(r.Reasons) == 0 {
		return ""
	}
	return r.Reasons[0]
}

// reasonsWithOverride returns Reasons with an override code (optimizer_paused
// or optimizer_manual_override) inserted after any fallback code and before
// degraded and action codes (UX §4 precedence). An empty override returns a
// copy of Reasons.
func (r optimizeResult) reasonsWithOverride(override optimize.Reason) []optimize.Reason {
	out := append([]optimize.Reason(nil), r.Reasons...)
	if override == "" {
		return out
	}
	return optSortReasons(append(out, override))
}

// profileCache holds the daily consumption profile (design §Consumption
// profile). It is owned by the runner goroutine (or by one CLI invocation)
// and is not safe for concurrent use.
type profileCache struct {
	date    string // local date (YYYY-MM-DD) of the successful build
	profile optimize.Profile
	reasons []optimize.Reason
	builtAt time.Time // zero until the first successful build

	lastAttempt time.Time // last build attempt, successful or not
	lastFailed  bool
}

// hasProfile reports whether a profile has been built successfully.
func (c *profileCache) hasProfile() bool {
	return c != nil && !c.builtAt.IsZero()
}

// current returns the profile and its degraded reasons for a run at now.
// A profile older than optProfileMaxAge is replaced by the static profile.
func (c *profileCache) current(cfg OptimizeConfig, now time.Time) (optimize.Profile, []optimize.Reason) {
	if c.hasProfile() && now.Sub(c.builtAt) <= optProfileMaxAge {
		return c.profile, append([]optimize.Reason(nil), c.reasons...)
	}
	return optStaticProfile(cfg), []optimize.Reason{optimize.ReasonConsumptionStaticFallback}
}

// needsRebuild reports whether rebuildIfStale should build now: the profile
// belongs to an earlier local date, or the last attempt failed at least
// optProfileRetryInterval ago.
func (c *profileCache) needsRebuild(now time.Time, loc *time.Location) bool {
	if c.lastFailed {
		return now.Sub(c.lastAttempt) >= optProfileRetryInterval
	}
	return !c.hasProfile() || c.date != optLocalDate(now, loc)
}

// rebuildIfStale rebuilds the profile when needsRebuild says so. The runner
// calls it after the hourly write and publish, so the history fetch never
// delays the re-assertion. The build has its own 4-minute deadline. It returns
// whether a build was attempted and the build error, if any; on error the
// previous profile is kept (and expires after optProfileMaxAge).
func (c *profileCache) rebuildIfStale(ctx context.Context, deps optimizeDeps, cfg OptimizeConfig, now time.Time) (bool, error) {
	if c == nil {
		return false, errors.New("profile cache is nil")
	}
	if !c.needsRebuild(now, optLoc(cfg)) {
		return false, nil
	}
	return true, c.rebuild(ctx, deps, cfg, now)
}

// rebuild builds the profile and stores it on success.
func (c *profileCache) rebuild(ctx context.Context, deps optimizeDeps, cfg OptimizeConfig, now time.Time) error {
	bctx, cancel := context.WithTimeout(ctx, optProfileRebuildTimeout)
	defer cancel()

	c.lastAttempt = now
	profile, reasons, err := optBuildProfile(bctx, deps, cfg, now)
	if err != nil {
		c.lastFailed = true
		u.Log.Warnw("optimizer degraded input", "reason", optimize.ReasonConsumptionStaticFallback, "detail", "consumption profile build failed", "error", err)
		return err
	}
	c.lastFailed = false
	c.date = optLocalDate(now, optLoc(cfg))
	c.profile = profile
	c.reasons = reasons
	c.builtAt = now
	return nil
}

// runOptimization collects and validates the inputs, builds the grid and
// solves it (design §Interactions). It never writes to the inverter; the
// result is either a validated plan or a fallback decision. Only a cold start
// (no profile built yet) fetches history before solving.
func runOptimization(ctx context.Context, deps optimizeDeps, cfg OptimizeConfig, now time.Time, cache *profileCache) optimizeResult {
	var res optimizeResult
	loc := optLoc(cfg)

	// 1. Battery.
	if deps.Battery == nil {
		return optFail(res, optimize.ReasonFallbackSocUnavailable, optInputBattery, errors.New("battery reader not configured"))
	}
	socPct, capacityWh, err := deps.Battery.Read(ctx, cfg.FroniusIP)
	if err != nil {
		return optFail(res, optimize.ReasonFallbackSocUnavailable, optInputBattery, err)
	}
	if err := optimize.ValidateBattery(socPct, capacityWh); err != nil {
		return optFail(res, optimize.ReasonFallbackSocUnavailable, optInputBattery, err)
	}
	res.StartSocPct, res.HasSoc = socPct, true

	// 2. Price.
	if deps.HA == nil {
		return optFail(res, optimize.ReasonFallbackHAUnreachable, optInputHAClient, errors.New("home assistant client not configured"))
	}
	prices, meta, reason, err := optReadPrices(ctx, deps.HA, cfg, now, loc)
	res.Currency = meta.Currency
	res.TZMismatch = meta.TZMismatch
	if err != nil {
		return optFail(res, reason, optInputPrice, err)
	}
	if meta.TZMismatch {
		u.Log.Warnw("TZ mismatch", "detail", "price timestamps use a different UTC offset than the local zone; check the container TZ", "local_offset", optOffset(now, loc))
	}

	// 3. Consumption profile: cached, or cold-start build.
	if cache == nil {
		cache = &profileCache{}
	}
	if !cache.hasProfile() && cache.lastAttempt.IsZero() {
		_ = cache.rebuild(ctx, deps, cfg, now) // failure → static profile below
	}
	profile, degraded := cache.current(cfg, now)

	// 4. PV forecast (always via the cache path).
	periods, pvReasons := optLoadPV(ctx, deps.Forecasts, cfg, now)
	degraded = append(degraded, pvReasons...)
	pvStale := optHasReason(pvReasons, optimize.ReasonPVForecastStale)

	// 5. Grid.
	params := optParams(cfg)
	grid, battery, gridReasons, err := optimize.BuildGrid(optimize.GridInput{
		Prices:  prices,
		Profile: profile,
		PV: func(slots []optimize.Hour) ([]float64, []optimize.Reason) {
			starts := make([]time.Time, len(slots))
			for i, s := range slots {
				starts[i] = s.Start
			}
			pv, rs := optimize.BucketPV(periods, starts)
			if pvStale {
				rs = nil // stale already explains the missing PV
			}
			return pv, rs
		},
		SocPct:     socPct,
		CapacityWh: capacityWh,
		Params:     params,
		Now:        now,
		Loc:        loc,
	})
	degraded = append(degraded, gridReasons...)
	res.Reasons = optSortReasons(degraded)
	if err != nil {
		return optFail(res, optGridErrReason(err), optInputGrid, err)
	}
	res.Grid, res.Battery, res.Params = grid, battery, params
	res.PriceMin, res.PriceMax, res.PriceMean = optPriceFingerprint(grid)
	for _, v := range profile {
		res.ProfileSumWh += v
	}
	if grid.Dropped > 0 {
		u.Log.Debugw("priced hours beyond the horizon cap dropped", "dropped", grid.Dropped)
	}
	optLogDegraded(res.Reasons)

	// 6. Solve with its own deadline.
	sctx, cancel := context.WithTimeout(ctx, optSolveTimeout)
	plan, err := optSolve(sctx, grid, battery, params)
	cancel()
	if err != nil {
		return optFail(res, optimize.ReasonFallbackSolverError, optInputSolver, err)
	}

	// 7. Plan invariants (S-007).
	if err := optimize.ValidatePlan(plan, grid, battery, params, now); err != nil {
		return optFail(res, optimize.ReasonFallbackInvalidPlan, optInputPlan, err)
	}

	res.Plan = plan
	res.TerminalValue = plan.TerminalValue
	if plan.Reason != "" {
		res.Reasons = optSortReasons(append(res.Reasons, plan.Reason))
	}
	return res
}

// optFail turns res into a fallback result. Degraded reasons collected so far
// follow the fallback code; no plan is returned.
func optFail(res optimizeResult, reason optimize.Reason, input string, err error) optimizeResult {
	res.Fallback = &optimizeFallback{Reason: reason, Input: input, Err: err}
	res.Plan = optimize.Plan{}
	res.Reasons = optSortReasons(append(res.Reasons, reason))
	return res
}

// optReadPrices reads the price entity, checks its unit and currency, parses
// it and returns the hourly prices from the current hour. On error it returns
// the matching fallback reason.
func optReadPrices(ctx context.Context, ha haClient, cfg OptimizeConfig, now time.Time, loc *time.Location) ([]optimize.HourPrice, optimize.PriceMeta, optimize.Reason, error) {
	st, err := ha.State(ctx, cfg.PriceEntity)
	if err != nil {
		return nil, optimize.PriceMeta{}, optHAErrReason(err), err
	}
	switch strings.ToLower(strings.TrimSpace(st.State)) {
	case "unknown", "unavailable":
		return nil, optimize.PriceMeta{}, optimize.ReasonFallbackPriceEntityNotFound,
			fmt.Errorf("price entity state is %q", st.State)
	}
	if err := optCheckPriceCurrency(st.Attributes); err != nil {
		return nil, optimize.PriceMeta{}, optimize.ReasonFallbackPriceFormat, err
	}
	points, meta, err := optimize.ParseNordpool(st.Attributes, st.State, st.LastUpdated, now, loc)
	if err != nil {
		return nil, meta, optimize.ReasonForPriceError(err), err
	}
	hours, err := optimize.PriceHorizon(points, optSlot0(now, loc), optPriceLookaheadHours)
	if err != nil {
		return nil, meta, optimize.ReasonFallbackMissingPrices, err
	}
	return hours, meta, "", nil
}

// optHAErrReason maps a Home Assistant client error to its fallback reason.
// Connection errors, timeouts, 5xx and other unexpected statuses are
// ha_unreachable.
func optHAErrReason(err error) optimize.Reason {
	switch {
	case errors.Is(err, homeassistant.ErrAuth):
		return optimize.ReasonFallbackHAAuthFailed
	case errors.Is(err, homeassistant.ErrNotFound):
		return optimize.ReasonFallbackPriceEntityNotFound
	case errors.Is(err, homeassistant.ErrTooLarge):
		return optimize.ReasonFallbackPriceFormat
	default:
		return optimize.ReasonFallbackHAUnreachable
	}
}

// optGridErrReason maps a BuildGrid error to its fallback reason.
func optGridErrReason(err error) optimize.Reason {
	switch {
	case errors.Is(err, optimize.ErrNoPricedHours):
		return optimize.ReasonFallbackMissingPrices
	case errors.Is(err, optimize.ErrInvalidBattery):
		return optimize.ReasonFallbackSocUnavailable
	default: // ErrInvalidGrid, ErrInvalidParams, anything else
		return optimize.ReasonFallbackImplausibleInput
	}
}

// optLocalCurrencyByArea maps Nordpool bidding areas (the sensor's `region`
// attribute) to the area's local currency.
var optLocalCurrencyByArea = map[string]string{
	"DK1": "DKK", "DK2": "DKK",
	"SE1": "SEK", "SE2": "SEK", "SE3": "SEK", "SE4": "SEK",
	"NO1": "NOK", "NO2": "NOK", "NO3": "NOK", "NO4": "NOK", "NO5": "NOK",
	"OSLO": "NOK", "KR.SAND": "NOK", "BERGEN": "NOK", "MOLDE": "NOK", "TR.HEIM": "NOK", "TROMSØ": "NOK",
	"FI": "EUR", "EE": "EUR", "LV": "EUR", "LT": "EUR",
	"AT": "EUR", "BE": "EUR", "DE-LU": "EUR", "FR": "EUR", "NL": "EUR",
}

// optLocalCurrencyByCountry is the fallback when `region` is not recognised.
var optLocalCurrencyByCountry = map[string]string{
	"DK": "DKK", "DENMARK": "DKK",
	"SE": "SEK", "SWEDEN": "SEK",
	"NO": "NOK", "NORWAY": "NOK",
	"FI": "EUR", "FINLAND": "EUR", "EE": "EUR", "ESTONIA": "EUR",
	"LV": "EUR", "LATVIA": "EUR", "LT": "EUR", "LITHUANIA": "EUR",
	"AT": "EUR", "AUSTRIA": "EUR", "BE": "EUR", "BELGIUM": "EUR",
	"DE": "EUR", "GERMANY": "EUR", "FR": "EUR", "FRANCE": "EUR",
	"NL": "EUR", "NETHERLANDS": "EUR",
}

// optCheckPriceCurrency guards against prices in a currency other than the
// one the surcharges and sell margin are entered in (TC-004d, S-004).
//
//   - The currency in the unit (the part before "/") must equal the entity's
//     `currency` attribute when both are present (catches "øre/kWh" with
//     currency DKK, or "EUR/MWh" with currency DKK).
//   - A /MWh price is the raw exchange format and is only accepted when its
//     currency is the local currency of the sensor's bidding area (`region`,
//     else `country`): DKK/MWh in DK2 is accepted (÷1000), EUR/MWh in DK2 is
//     rejected. When the area is unknown the currency cannot be verified and
//     a /MWh price is rejected.
//   - /kWh prices are accepted as configured by the user (the Nordpool sensor
//     converts them to the chosen currency incl. VAT).
//
// Units that end in neither /kWh nor /MWh are left to ParseNordpool, which
// rejects them with the same price_format reason.
func optCheckPriceCurrency(attrs map[string]any) error {
	unit := strings.TrimSpace(optAttrString(attrs, "unit_of_measurement"))
	attrCur := strings.TrimSpace(optAttrString(attrs, "currency"))
	if unit == "" {
		if su := strings.TrimSpace(optAttrString(attrs, "unit")); attrCur != "" && su != "" {
			unit = attrCur + "/" + su
		}
	}
	slash := strings.LastIndex(unit, "/")
	if slash < 0 {
		return nil // ParseNordpool reports the unit error
	}
	unitCur := strings.TrimSpace(unit[:slash])
	per := strings.ToLower(strings.TrimSpace(unit[slash+1:]))
	if per != "kwh" && per != "mwh" {
		return nil
	}
	if unitCur != "" && attrCur != "" && !strings.EqualFold(unitCur, attrCur) {
		return fmt.Errorf("%w: unit %q does not match currency %q", optimize.ErrPriceFormat, unit, attrCur)
	}
	if per != "mwh" {
		return nil
	}
	cur := attrCur
	if cur == "" {
		cur = unitCur
	}
	expected := optLocalCurrency(attrs)
	if expected == "" {
		return fmt.Errorf("%w: unit %q: cannot verify the currency of a /MWh price without a known region; configure the sensor in local currency per kWh", optimize.ErrPriceFormat, unit)
	}
	if !strings.EqualFold(cur, expected) {
		return fmt.Errorf("%w: unit %q is not in the local currency %s; configure the sensor in %s/kWh", optimize.ErrPriceFormat, unit, expected, expected)
	}
	return nil
}

// optLocalCurrency returns the local currency of the sensor's bidding area,
// or "" when it is not known.
func optLocalCurrency(attrs map[string]any) string {
	if c, ok := optLocalCurrencyByArea[strings.ToUpper(strings.TrimSpace(optAttrString(attrs, "region")))]; ok {
		return c
	}
	if c, ok := optLocalCurrencyByCountry[strings.ToUpper(strings.TrimSpace(optAttrString(attrs, "country")))]; ok {
		return c
	}
	return ""
}

func optAttrString(attrs map[string]any, key string) string {
	s, _ := attrs[key].(string)
	return s
}

// optBuildProfile fetches the load (and EV) history in ≤ 1-day chunks and
// builds the consumption profile. A missing consumption entity or empty
// history yields the static profile with optimizer_consumption_entity_not_found
// (degraded, no error); transport errors are returned so the cache retries.
func optBuildProfile(ctx context.Context, deps optimizeDeps, cfg OptimizeConfig, now time.Time) (optimize.Profile, []optimize.Reason, error) {
	if deps.HA == nil {
		return optimize.Profile{}, nil, errors.New("home assistant client not configured")
	}
	loc := optLoc(cfg)
	staticWh := cfg.StaticDailyWh / 24

	loadUnit, loadSamples, err := optFetchEntity(ctx, deps.HA, cfg.ConsumptionEntity, cfg.HistoryDays, now, loc)
	if errors.Is(err, homeassistant.ErrNotFound) || (err == nil && len(loadSamples) == 0) {
		u.Log.Warnw("optimizer degraded input", "reason", optimize.ReasonConsumptionEntityNotFound, "detail", "consumption entity not found or without history")
		return optStaticProfile(cfg), []optimize.Reason{optimize.ReasonConsumptionEntityNotFound}, nil
	}
	if err != nil {
		return optimize.Profile{}, nil, fmt.Errorf("consumption history: %w", err)
	}

	var (
		evUnit    string
		evSamples []optimize.Sample
		reasons   []optimize.Reason
	)
	if strings.TrimSpace(cfg.EVEntity) != "" {
		evUnit, evSamples, err = optFetchEntity(ctx, deps.HA, cfg.EVEntity, cfg.HistoryDays, now, loc)
		switch {
		case errors.Is(err, homeassistant.ErrNotFound):
			evUnit, evSamples = "", nil
			reasons = append(reasons, optimize.ReasonEVEntityNotFound)
			u.Log.Warnw("optimizer degraded input", "reason", optimize.ReasonEVEntityNotFound, "detail", "EV entity not found; EV subtraction skipped")
		case err != nil:
			return optimize.Profile{}, nil, fmt.Errorf("EV history: %w", err)
		}
	}

	b, err := optimize.BuildProfileDetail(loadSamples, evSamples, loadUnit, evUnit, cfg.HistoryDays, now, loc, staticWh)
	if err != nil {
		return optimize.Profile{}, nil, fmt.Errorf("build profile: %w", err)
	}
	if len(b.StaticHours) > 0 {
		u.Log.Warnw("optimizer degraded input", "reason", optimize.ReasonConsumptionStaticFallback, "hours", b.StaticHours)
	}
	return b.Profile, optSortReasons(append(reasons, b.Reasons...)), nil
}

// optFetchEntity reads the entity's unit and its history over the last `days`
// complete local days, one request per local day (≤ 25 h on DST days).
func optFetchEntity(ctx context.Context, ha haClient, entity string, days int, now time.Time, loc *time.Location) (string, []optimize.Sample, error) {
	st, err := ha.State(ctx, entity)
	if err != nil {
		return "", nil, err
	}
	unit := optAttrString(st.Attributes, "unit_of_measurement")

	l := now.In(loc)
	var samples []optimize.Sample
	for d := days; d >= 1; d-- {
		start := time.Date(l.Year(), l.Month(), l.Day()-d, 0, 0, 0, 0, loc)
		end := time.Date(l.Year(), l.Month(), l.Day()-d+1, 0, 0, 0, 0, loc)
		points, err := ha.History(ctx, entity, start, end)
		if err != nil {
			return "", nil, err
		}
		for _, p := range points {
			samples = append(samples, optimize.SampleFromHAState(p.LastChanged, p.State))
		}
	}
	return unit, samples, nil
}

// optLoadPV loads the Solcast forecasts via the cache path and converts them
// to PV periods. Failures are degraded inputs, never a fallback.
func optLoadPV(ctx context.Context, fl forecastLoader, cfg OptimizeConfig, now time.Time) ([]optimize.PVPeriod, []optimize.Reason) {
	if fl == nil {
		return nil, []optimize.Reason{optimize.ReasonPVForecastMissing}
	}
	forecasts, meta, err := fl.Load(ctx, cfg, now)
	if err != nil {
		u.Log.Warnw("optimizer degraded input", "reason", optimize.ReasonPVForecastMissing, "error", err)
		return nil, []optimize.Reason{optimize.ReasonPVForecastMissing}
	}
	var reasons []optimize.Reason
	if meta.AnyStale() {
		reasons = append(reasons, optimize.ReasonPVForecastStale)
	} else if meta.AnyMissing() {
		reasons = append(reasons, optimize.ReasonPVForecastMissing)
	}
	var periods []optimize.PVPeriod
	dropped := 0
	for _, list := range forecasts {
		for _, f := range list {
			p, err := optimize.PVPeriodsFromSolcast(f.PeriodEnd, f.Period, f.PVEstimate)
			if err != nil {
				dropped++
				continue
			}
			periods = append(periods, p)
		}
	}
	if dropped > 0 {
		u.Log.Debugw("solcast periods dropped", "count", dropped)
	}
	return periods, reasons
}

// optParams maps the configuration to solver parameters.
func optParams(cfg OptimizeConfig) optimize.Params {
	return optimize.Params{
		Efficiency:    cfg.Efficiency,
		MaxChargeW:    cfg.MaxChargeW,
		MaxDischargeW: cfg.MaxDischargeW,
		MinSocPct:     cfg.MinSocPct,
		VATRate:       cfg.VATRate,
		SellMargin:    cfg.SellMargin,
		MinGain:       cfg.MinGain,
		WearCost:      cfg.WearCost,
		Surcharge:     cfg.Surcharge,
	}
}

// optStaticProfile is pw_consumption/24 for every hour (AC-2).
func optStaticProfile(cfg OptimizeConfig) optimize.Profile {
	var p optimize.Profile
	for h := range p {
		p[h] = cfg.StaticDailyWh / 24
	}
	return p
}

// optReasonRank orders reasons by UX §4 precedence: fallback > override >
// degraded > action. Within a group the UX table order applies.
var optReasonRank = func() map[optimize.Reason]int {
	order := []optimize.Reason{
		optimize.ReasonFallbackHAUnreachable,
		optimize.ReasonFallbackHAAuthFailed,
		optimize.ReasonFallbackPriceEntityNotFound,
		optimize.ReasonFallbackPriceFormat,
		optimize.ReasonFallbackMissingPrices,
		optimize.ReasonFallbackSocUnavailable,
		optimize.ReasonFallbackSolverError,
		optimize.ReasonFallbackModbusWriteFailed,
		optimize.ReasonFallbackInvalidPlan,
		optimize.ReasonFallbackPriceInconsistent,
		optimize.ReasonFallbackImplausibleInput,
		optimize.ReasonFallbackInverterLimits,
		optimize.ReasonWatchdogMinSoc,
		optimize.ReasonPaused,
		optimize.ReasonManualOverride,
		optimize.ReasonEVGuardHold,
		optimize.ReasonConsumptionStaticFallback,
		optimize.ReasonConsumptionEntityNotFound,
		optimize.ReasonEVEntityNotFound,
		optimize.ReasonPVForecastMissing,
		optimize.ReasonPVForecastStale,
	}
	m := make(map[optimize.Reason]int, len(order))
	for i, r := range order {
		m[r] = i
	}
	return m
}()

// optSortReasons deduplicates reasons and orders them by precedence. Codes not
// in the table (the action codes) sort last, keeping their relative order.
func optSortReasons(rs []optimize.Reason) []optimize.Reason {
	seen := make(map[optimize.Reason]bool, len(rs))
	out := make([]optimize.Reason, 0, len(rs))
	for _, r := range rs {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	rank := func(r optimize.Reason) int {
		if v, ok := optReasonRank[r]; ok {
			return v
		}
		return len(optReasonRank)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// optReplaceReason returns rs with old replaced by repl (appended when old is
// absent), keeping the order.
func optReplaceReason(rs []optimize.Reason, old, repl optimize.Reason) []optimize.Reason {
	out := make([]optimize.Reason, 0, len(rs)+1)
	replaced := false
	for _, r := range rs {
		if r == old && !replaced {
			out = append(out, repl)
			replaced = true
			continue
		}
		out = append(out, r)
	}
	if !replaced {
		out = append(out, repl)
	}
	return out
}

func optHasReason(rs []optimize.Reason, r optimize.Reason) bool {
	for _, x := range rs {
		if x == r {
			return true
		}
	}
	return false
}

// optLogDegraded logs each degraded reason once per run at Warn.
func optLogDegraded(rs []optimize.Reason) {
	for _, r := range rs {
		switch r {
		case optimize.ReasonPVForecastMissing, optimize.ReasonPVForecastStale:
			u.Log.Warnw("optimizer degraded input", "reason", r)
		}
	}
}

// optPriceFingerprint returns min/max/mean of the hourly spot prices (S-010).
func optPriceFingerprint(g optimize.Grid) (float64, float64, float64) {
	if len(g.Hours) == 0 {
		return 0, 0, 0
	}
	lo, hi, sum := math.Inf(1), math.Inf(-1), 0.0
	for _, h := range g.Hours {
		lo = math.Min(lo, h.SpotInclVAT)
		hi = math.Max(hi, h.SpotInclVAT)
		sum += h.SpotInclVAT
	}
	return lo, hi, sum / float64(len(g.Hours))
}

func optLoc(cfg OptimizeConfig) *time.Location {
	if cfg.Location != nil {
		return cfg.Location
	}
	return time.Local
}

// optSlot0 is the start of the wall-clock hour containing now (same rule as
// optimize.HourSlots).
func optSlot0(now time.Time, loc *time.Location) time.Time {
	l := now.In(loc)
	return l.Add(-time.Duration(l.Minute())*time.Minute - time.Duration(l.Second())*time.Second - time.Duration(l.Nanosecond()))
}

func optLocalDate(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(time.DateOnly)
}

func optOffset(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("-07:00")
}
