package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Copenhagen without system tzdata

	"sbam/pkg/homeassistant"
	"sbam/pkg/optimize"
	pw "sbam/pkg/power"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	optRunPriceEntity = "sensor.nordpool_test"
	optRunLoadEntity  = "sensor.load_test"
	optRunEVEntity    = "sensor.ev_test"
)

// --- fakes ---------------------------------------------------------------

type optRunHistoryCall struct {
	entity string
	start  time.Time
	end    time.Time
}

type optRunFakeHA struct {
	mu         sync.Mutex
	states     map[string]homeassistant.State
	stateErr   map[string]error
	historyErr map[string]error
	// historyFn returns the points for one chunk; nil means a constant
	// 500 W (load) / 0 kW (EV) at the chunk start.
	historyFn    func(entity string, start, end time.Time) []homeassistant.HistoryPoint
	historyCalls []optRunHistoryCall
}

func (f *optRunFakeHA) State(_ context.Context, entity string) (homeassistant.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.stateErr[entity]; err != nil {
		return homeassistant.State{}, err
	}
	st, ok := f.states[entity]
	if !ok {
		return homeassistant.State{}, fmt.Errorf("GET /api/states/%s: status 404: %w", entity, homeassistant.ErrNotFound)
	}
	return st, nil
}

func (f *optRunFakeHA) History(_ context.Context, entity string, start, end time.Time) ([]homeassistant.HistoryPoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyCalls = append(f.historyCalls, optRunHistoryCall{entity, start, end})
	if err := f.historyErr[entity]; err != nil {
		return nil, err
	}
	if f.historyFn != nil {
		return f.historyFn(entity, start, end), nil
	}
	v := "500"
	if entity == optRunEVEntity {
		v = "0"
	}
	return []homeassistant.HistoryPoint{{State: v, LastChanged: start.UTC()}}, nil
}

func (f *optRunFakeHA) historyCount(entity string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.historyCalls {
		if c.entity == entity {
			n++
		}
	}
	return n
}

type optRunFakeForecasts struct {
	forecasts [][]pw.Forecast
	meta      pw.ForecastMeta
	err       error
	calls     int
}

func (f *optRunFakeForecasts) Load(context.Context, OptimizeConfig, time.Time) ([][]pw.Forecast, pw.ForecastMeta, error) {
	f.calls++
	return f.forecasts, f.meta, f.err
}

type optRunFakeBattery struct {
	soc, capWh float64
	err        error
}

func (f optRunFakeBattery) Read(context.Context, string) (float64, float64, error) {
	return f.soc, f.capWh, f.err
}

// --- fixtures ------------------------------------------------------------

func optRunLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	require.NoError(t, err)
	return loc
}

func optRunCfg(loc *time.Location) OptimizeConfig {
	return OptimizeConfig{
		PriceEntity:       optRunPriceEntity,
		ConsumptionEntity: optRunLoadEntity,
		EVEntity:          optRunEVEntity,
		HistoryDays:       10,
		Efficiency:        0.83,
		MaxChargeW:        4500,
		MaxDischargeW:     4500,
		MinSocPct:         7,
		VATRate:           0.25,
		SellMargin:        0.1,
		StaticDailyWh:     12000,
		FroniusIP:         "192.0.2.10",
		SolcastURLs:       []string{"https://solcast.example.invalid/forecasts"},
		SolcastAPIKey:     "test-api-key",
		CacheTime:         3600,
		ForecastMaxAge:    24 * time.Hour,
		Location:          loc,
	}
}

// optRunPrice is the quarter-hour price incl. VAT per kWh: cheap at night,
// peak 17–20.
func optRunPrice(local time.Time) float64 {
	switch h := local.Hour(); {
	case h >= 17 && h < 20:
		return 3.0
	case h < 6:
		return 0.8
	default:
		return 1.5
	}
}

// optRunPriceState returns a Nordpool state with raw_today (and raw_tomorrow)
// in quarter-hour resolution for the local day of now. valueFn may return nil
// for an unpriced entry. scale multiplies every value (1000 for /MWh).
func optRunPriceState(now time.Time, loc *time.Location, attrs map[string]any, valueFn func(time.Time) any, scale float64) homeassistant.State {
	if valueFn == nil {
		valueFn = func(t time.Time) any { return optRunPrice(t.In(loc)) * scale }
	}
	day := func(add int) []any {
		l := now.In(loc)
		start := time.Date(l.Year(), l.Month(), l.Day()+add, 0, 0, 0, 0, loc)
		end := time.Date(l.Year(), l.Month(), l.Day()+add+1, 0, 0, 0, 0, loc)
		var out []any
		for t := start; t.Before(end); t = t.Add(15 * time.Minute) {
			out = append(out, map[string]any{
				"start": t.Format(time.RFC3339),
				"end":   t.Add(15 * time.Minute).Format(time.RFC3339),
				"value": valueFn(t),
			})
		}
		return out
	}
	a := map[string]any{
		"unit_of_measurement": "DKK/kWh",
		"currency":            "DKK",
		"region":              "DK2",
		"raw_today":           day(0),
		"raw_tomorrow":        day(1),
		"tomorrow_valid":      true,
	}
	for k, v := range attrs {
		if v == nil {
			delete(a, k)
			continue
		}
		a[k] = v
	}
	state := "unknown"
	if v, ok := valueFn(now.Truncate(15 * time.Minute)).(float64); ok {
		state = strconv.FormatFloat(v, 'f', 4, 64)
	}
	return homeassistant.State{
		EntityID:    optRunPriceEntity,
		State:       state,
		Attributes:  a,
		LastUpdated: now.Add(-30 * time.Minute).UTC(),
	}
}

// optRunForecasts covers [now−2h, now+50h) with 30-min Solcast periods.
func optRunForecasts(now time.Time, loc *time.Location) [][]pw.Forecast {
	var list []pw.Forecast
	start := now.Truncate(30 * time.Minute).Add(-2 * time.Hour)
	for t := start; t.Before(now.Add(50 * time.Hour)); t = t.Add(30 * time.Minute) {
		end := t.Add(30 * time.Minute)
		kw := 0.0
		if h := t.In(loc).Hour(); h >= 9 && h < 16 {
			kw = 2.0
		}
		list = append(list, pw.Forecast{PVEstimate: kw, PeriodEnd: end.UTC().Format(time.RFC3339), Period: "PT30M"})
	}
	return [][]pw.Forecast{list}
}

type optRunEnv struct {
	cfg   OptimizeConfig
	ha    *optRunFakeHA
	fc    *optRunFakeForecasts
	bat   optRunFakeBattery
	now   time.Time
	loc   *time.Location
	cache *profileCache
}

func newOptRunEnv(t *testing.T) *optRunEnv {
	t.Helper()
	loc := optRunLoc(t)
	now := time.Date(2026, 10, 5, 10, 20, 0, 0, loc)
	return &optRunEnv{
		cfg: optRunCfg(loc),
		ha: &optRunFakeHA{
			states: map[string]homeassistant.State{
				optRunPriceEntity: optRunPriceState(now, loc, nil, nil, 1),
				optRunLoadEntity:  {EntityID: optRunLoadEntity, State: "500", Attributes: map[string]any{"unit_of_measurement": "W"}},
				optRunEVEntity:    {EntityID: optRunEVEntity, State: "0", Attributes: map[string]any{"unit_of_measurement": "kW"}},
			},
			stateErr:   map[string]error{},
			historyErr: map[string]error{},
		},
		fc:    &optRunFakeForecasts{forecasts: optRunForecasts(now, loc), meta: pw.ForecastMeta{Sources: []pw.ForecastSourceMeta{{}}}},
		bat:   optRunFakeBattery{soc: 50, capWh: 10000},
		now:   now,
		loc:   loc,
		cache: &profileCache{},
	}
}

func (e *optRunEnv) deps() optimizeDeps {
	return optimizeDeps{HA: e.ha, Forecasts: e.fc, Battery: e.bat}
}

func (e *optRunEnv) run(ctx context.Context) optimizeResult {
	return runOptimization(ctx, e.deps(), e.cfg, e.now, e.cache)
}

func optRunIsActionReason(r optimize.Reason) bool {
	_, ranked := optReasonRank[r]
	return !ranked
}

// --- happy path ----------------------------------------------------------

func TestRunOptimization_ProducesValidatedPlan(t *testing.T) {
	e := newOptRunEnv(t)
	res := e.run(context.Background())

	require.Nil(t, res.Fallback, "unexpected fallback: %+v", res.Fallback)
	require.NotEmpty(t, res.Plan.Hours)
	assert.Equal(t, res.Grid.Hours[0].Start, res.Plan.Hours[0].Start)
	assert.True(t, res.Plan.Hours[0].Start.Equal(time.Date(2026, 10, 5, 10, 0, 0, 0, e.loc)))
	assert.Equal(t, "DKK", res.Currency)
	assert.True(t, res.HasSoc)
	assert.Equal(t, 50.0, res.StartSocPct)
	assert.Equal(t, res.Plan.TerminalValue, res.TerminalValue)
	require.Len(t, res.Reasons, 1, "no degraded inputs: only the action code")
	assert.True(t, optRunIsActionReason(res.Reasons[0]))
	assert.Equal(t, res.Plan.Reason, res.reason())
	assert.InDelta(t, 12000, res.ProfileSumWh, 1)
	assert.InDelta(t, 0.8, res.PriceMin, 1e-9)
	assert.InDelta(t, 3.0, res.PriceMax, 1e-9)
	// Horizon: today 10:00 … tomorrow 23:00 = 38 priced hours, capped at 36.
	assert.Len(t, res.Plan.Hours, optimize.DefaultMaxHours)
	assert.Equal(t, 2, res.Grid.Dropped)
	assert.True(t, e.cache.hasProfile(), "cold start builds the profile before solving")
}

func TestRunOptimization_Deterministic(t *testing.T) {
	e := newOptRunEnv(t)
	a := e.run(context.Background())
	b := e.run(context.Background())
	require.Nil(t, a.Fallback)
	assert.Equal(t, a.Plan, b.Plan)
	assert.Equal(t, a.Reasons, b.Reasons)
}

// --- T-0130: each fallback cause → its exact reason code (AC-16, UX §4.1) --

func TestRunOptimization_T0130_FallbackCodes(t *testing.T) {
	errSolver := errors.New("solver exploded")
	cases := []struct {
		name   string
		setup  func(e *optRunEnv)
		deps   func(e *optRunEnv) optimizeDeps
		solve  func(context.Context, optimize.Grid, optimize.Battery, optimize.Params) (optimize.Plan, error)
		reason optimize.Reason
		input  string
	}{
		{name: "HA connection error", reason: optimize.ReasonFallbackHAUnreachable, input: optInputPrice,
			setup: func(e *optRunEnv) { e.ha.stateErr[optRunPriceEntity] = errors.New("dial tcp: connection refused") }},
		{name: "HA 5xx", reason: optimize.ReasonFallbackHAUnreachable, input: optInputPrice,
			setup: func(e *optRunEnv) {
				e.ha.stateErr[optRunPriceEntity] = fmt.Errorf("status 502: %w", homeassistant.ErrStatus)
			}},
		{name: "HA client missing", reason: optimize.ReasonFallbackHAUnreachable, input: optInputHAClient,
			deps: func(e *optRunEnv) optimizeDeps { return optimizeDeps{Forecasts: e.fc, Battery: e.bat} }},
		{name: "HA 401", reason: optimize.ReasonFallbackHAAuthFailed, input: optInputPrice,
			setup: func(e *optRunEnv) {
				e.ha.stateErr[optRunPriceEntity] = fmt.Errorf("status 401: %w", homeassistant.ErrAuth)
			}},
		{name: "price entity 404", reason: optimize.ReasonFallbackPriceEntityNotFound, input: optInputPrice,
			setup: func(e *optRunEnv) { delete(e.ha.states, optRunPriceEntity) }},
		{name: "price entity unavailable", reason: optimize.ReasonFallbackPriceEntityNotFound, input: optInputPrice,
			setup: func(e *optRunEnv) {
				st := e.ha.states[optRunPriceEntity]
				st.State = "unavailable"
				e.ha.states[optRunPriceEntity] = st
			}},
		{name: "price unparseable", reason: optimize.ReasonFallbackPriceFormat, input: optInputPrice,
			setup: func(e *optRunEnv) {
				e.ha.states[optRunPriceEntity] = optRunPriceState(e.now, e.loc, map[string]any{"raw_today": nil, "raw_tomorrow": nil}, nil, 1)
			}},
		{name: "current hour unpriced", reason: optimize.ReasonFallbackMissingPrices, input: optInputPrice,
			setup: func(e *optRunEnv) {
				st := optRunPriceState(e.now, e.loc, nil, func(t time.Time) any {
					if t.In(e.loc).Hour() == 10 && t.In(e.loc).Day() == 5 {
						return nil
					}
					return optRunPrice(t.In(e.loc))
				}, 1)
				st.State = "1.5000" // numeric state, but no entry prices the current hour
				e.ha.states[optRunPriceEntity] = st
			}},
		{name: "price entity stale", reason: optimize.ReasonFallbackMissingPrices, input: optInputPrice,
			setup: func(e *optRunEnv) {
				st := e.ha.states[optRunPriceEntity]
				st.LastUpdated = e.now.Add(-27 * time.Hour)
				e.ha.states[optRunPriceEntity] = st
			}},
		{name: "price state inconsistent", reason: optimize.ReasonFallbackPriceInconsistent, input: optInputPrice,
			setup: func(e *optRunEnv) {
				st := e.ha.states[optRunPriceEntity]
				st.State = "9.99"
				e.ha.states[optRunPriceEntity] = st
			}},
		{name: "price implausible (TC-004c)", reason: optimize.ReasonFallbackImplausibleInput, input: optInputPrice,
			setup: func(e *optRunEnv) {
				e.ha.states[optRunPriceEntity] = optRunPriceState(e.now, e.loc, nil, func(time.Time) any { return 1e308 }, 1)
			}},
		{name: "grid implausible (surcharge)", reason: optimize.ReasonFallbackImplausibleInput, input: optInputGrid,
			setup: func(e *optRunEnv) { e.cfg.Surcharge[12] = 100 }},
		{name: "SoC read error", reason: optimize.ReasonFallbackSocUnavailable, input: optInputBattery,
			setup: func(e *optRunEnv) { e.bat.err = errors.New("solar api timeout") }},
		{name: "SoC 150 (TC-004f)", reason: optimize.ReasonFallbackSocUnavailable, input: optInputBattery,
			setup: func(e *optRunEnv) { e.bat.soc = 150 }},
		{name: "capacity 0 (TC-004f)", reason: optimize.ReasonFallbackSocUnavailable, input: optInputBattery,
			setup: func(e *optRunEnv) { e.bat.capWh = 0 }},
		{name: "battery reader missing", reason: optimize.ReasonFallbackSocUnavailable, input: optInputBattery,
			deps: func(e *optRunEnv) optimizeDeps { return optimizeDeps{HA: e.ha, Forecasts: e.fc} }},
		{name: "solver error", reason: optimize.ReasonFallbackSolverError, input: optInputSolver,
			solve: func(context.Context, optimize.Grid, optimize.Battery, optimize.Params) (optimize.Plan, error) {
				return optimize.Plan{}, errSolver
			}},
		{name: "solver deadline", reason: optimize.ReasonFallbackSolverError, input: optInputSolver,
			solve: func(ctx context.Context, g optimize.Grid, b optimize.Battery, p optimize.Params) (optimize.Plan, error) {
				<-ctx.Done() // the run's own solve deadline fires
				return optimize.Plan{}, ctx.Err()
			}},
		{name: "invalid plan", reason: optimize.ReasonFallbackInvalidPlan, input: optInputPlan,
			solve: func(context.Context, optimize.Grid, optimize.Battery, optimize.Params) (optimize.Plan, error) {
				return optimize.Plan{}, nil // empty horizon
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newOptRunEnv(t)
			if tc.setup != nil {
				tc.setup(e)
			}
			if tc.solve != nil {
				old := optSolve
				optSolve = tc.solve
				t.Cleanup(func() { optSolve = old })
			}
			deps := e.deps()
			if tc.deps != nil {
				deps = tc.deps(e)
			}
			ctx := context.Background()
			if tc.name == "solver deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			res := runOptimization(ctx, deps, e.cfg, e.now, e.cache)

			require.NotNil(t, res.Fallback)
			assert.Equal(t, tc.reason, res.Fallback.Reason)
			assert.Equal(t, tc.input, res.Fallback.Input)
			assert.Error(t, res.Fallback.Err)
			assert.Equal(t, tc.reason, res.reason(), "fallback has the highest precedence")
			assert.Empty(t, res.Plan.Hours, "no plan on fallback")
			for _, r := range res.Reasons {
				assert.False(t, optRunIsActionReason(r), "no action code on fallback: %s", r)
			}
		})
	}
}

// --- T-0131: degraded inputs still yield a plan, reasons in order ---------

func TestRunOptimization_T0131_DegradedInputs(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *optRunEnv)
		want  []optimize.Reason // degraded part, in precedence order
	}{
		{
			name: "history fails without cached profile + PV loader error",
			setup: func(e *optRunEnv) {
				e.ha.historyErr[optRunLoadEntity] = fmt.Errorf("status 500: %w", homeassistant.ErrStatus)
				e.fc.err = errors.New("load forecasts: cancelled")
			},
			want: []optimize.Reason{optimize.ReasonConsumptionStaticFallback, optimize.ReasonPVForecastMissing},
		},
		{
			name: "PV stale (missing suppressed) + history fails",
			setup: func(e *optRunEnv) {
				e.ha.historyErr[optRunLoadEntity] = errors.New("timeout")
				e.fc.forecasts = [][]pw.Forecast{nil}
				e.fc.meta = pw.ForecastMeta{Sources: []pw.ForecastSourceMeta{{Stale: true, Age: 30 * time.Hour}}}
			},
			want: []optimize.Reason{optimize.ReasonConsumptionStaticFallback, optimize.ReasonPVForecastStale},
		},
		{
			name: "EV entity not found + PV missing",
			setup: func(e *optRunEnv) {
				delete(e.ha.states, optRunEVEntity)
				e.fc.forecasts = [][]pw.Forecast{nil}
				e.fc.meta = pw.ForecastMeta{Sources: []pw.ForecastSourceMeta{{Missing: true}}}
			},
			want: []optimize.Reason{optimize.ReasonEVEntityNotFound, optimize.ReasonPVForecastMissing},
		},
		{
			name: "PV forecast does not cover the horizon",
			setup: func(e *optRunEnv) {
				e.fc.forecasts = [][]pw.Forecast{e.fc.forecasts[0][:20]}
			},
			want: []optimize.Reason{optimize.ReasonPVForecastMissing},
		},
		{
			name:  "consumption entity not found",
			setup: func(e *optRunEnv) { delete(e.ha.states, optRunLoadEntity) },
			want:  []optimize.Reason{optimize.ReasonConsumptionEntityNotFound},
		},
		{
			name: "no EV entity configured (AC-3)",
			setup: func(e *optRunEnv) {
				e.cfg.EVEntity = ""
			},
			want: nil,
		},
		{
			name: "fewer than 3 days of history (AC-2)",
			setup: func(e *optRunEnv) {
				e.ha.historyFn = func(entity string, start, _ time.Time) []homeassistant.HistoryPoint {
					if entity == optRunEVEntity {
						return []homeassistant.HistoryPoint{{State: "0", LastChanged: start.UTC()}}
					}
					if start.Before(e.now.Add(-48 * time.Hour)) {
						return []homeassistant.HistoryPoint{{State: "unavailable", LastChanged: start.UTC()}}
					}
					return []homeassistant.HistoryPoint{{State: "500", LastChanged: start.UTC()}}
				}
			},
			want: []optimize.Reason{optimize.ReasonConsumptionStaticFallback},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newOptRunEnv(t)
			tc.setup(e)
			res := e.run(context.Background())

			require.Nil(t, res.Fallback, "degraded inputs must not fall back: %+v", res.Fallback)
			require.NotEmpty(t, res.Plan.Hours)
			require.Len(t, res.Reasons, len(tc.want)+1)
			assert.Equal(t, tc.want, append([]optimize.Reason(nil), res.Reasons[:len(tc.want)]...))
			last := res.Reasons[len(res.Reasons)-1]
			assert.True(t, optRunIsActionReason(last), "action code last, got %s", last)
			assert.Equal(t, res.Plan.Reason, last)
		})
	}
}

func TestOptimizeResult_ReasonsWithOverride(t *testing.T) {
	ok := optimizeResult{Reasons: []optimize.Reason{optimize.ReasonPVForecastMissing, optimize.ReasonHoldForLaterPeak}}
	assert.Equal(t, []optimize.Reason{optimize.ReasonPaused, optimize.ReasonPVForecastMissing, optimize.ReasonHoldForLaterPeak},
		ok.reasonsWithOverride(optimize.ReasonPaused))
	assert.Equal(t, ok.Reasons, ok.reasonsWithOverride(""), "no override: unchanged")

	fb := optimizeResult{Reasons: []optimize.Reason{optimize.ReasonFallbackMissingPrices}}
	assert.Equal(t, []optimize.Reason{optimize.ReasonFallbackMissingPrices, optimize.ReasonManualOverride},
		fb.reasonsWithOverride(optimize.ReasonManualOverride), "fallback outranks override")

	assert.Equal(t, optimize.Reason(""), optimizeResult{}.reason())
	assert.Equal(t, []optimize.Reason{optimize.ReasonPVForecastMissing}, optSortReasons([]optimize.Reason{"", optimize.ReasonPVForecastMissing, optimize.ReasonPVForecastMissing}))
}

// --- T-0132: history fetched once per entity per day; rebuild after date change

func TestRunOptimization_T0132_DailyProfileRebuild(t *testing.T) {
	e := newOptRunEnv(t)
	day0 := time.Date(2026, 10, 5, 0, 0, 5, 0, e.loc)
	days := e.cfg.HistoryDays

	for h := 0; h < 24; h++ {
		e.now = day0.Add(time.Duration(h) * time.Hour)
		e.ha.states[optRunPriceEntity] = optRunPriceState(e.now, e.loc, nil, nil, 1)
		e.fc.forecasts = optRunForecasts(e.now, e.loc)

		res := e.run(context.Background())
		require.Nil(t, res.Fallback, "hour %d: %+v", h, res.Fallback)
		built, err := e.cache.rebuildIfStale(context.Background(), e.deps(), e.cfg, e.now)
		require.NoError(t, err)
		assert.False(t, built, "hour %d: no rebuild within the same local day", h)
	}
	assert.Equal(t, days, e.ha.historyCount(optRunLoadEntity), "load history: one request per history day, once per day")
	assert.Equal(t, days, e.ha.historyCount(optRunEVEntity), "EV history: one request per history day, once per day")
	for _, c := range e.ha.historyCalls {
		assert.LessOrEqual(t, c.end.Sub(c.start), 25*time.Hour, "chunks are at most one local day")
	}

	// First run of the next local date: solve with the cached profile, then
	// rebuild after dispatch.
	e.now = day0.Add(24 * time.Hour)
	e.ha.states[optRunPriceEntity] = optRunPriceState(e.now, e.loc, nil, nil, 1)
	e.fc.forecasts = optRunForecasts(e.now, e.loc)
	res := e.run(context.Background())
	require.Nil(t, res.Fallback)
	assert.Equal(t, days, e.ha.historyCount(optRunLoadEntity), "the run itself never fetches history once a profile exists")

	built, err := e.cache.rebuildIfStale(context.Background(), e.deps(), e.cfg, e.now)
	require.NoError(t, err)
	assert.True(t, built)
	assert.Equal(t, 2*days, e.ha.historyCount(optRunLoadEntity))
	assert.Equal(t, 2*days, e.ha.historyCount(optRunEVEntity))
	assert.Equal(t, "2026-10-06", e.cache.date)
	assert.Equal(t, e.now, e.cache.builtAt)

	// The newest chunk ends at local midnight of the new date.
	last := e.ha.historyCalls[len(e.ha.historyCalls)-1]
	assert.True(t, last.end.Equal(time.Date(2026, 10, 6, 0, 0, 0, 0, e.loc)))
}

func TestProfileCache_FailedRebuildRetriesHourlyAndExpires(t *testing.T) {
	e := newOptRunEnv(t)
	res := e.run(context.Background()) // cold build succeeds
	require.Nil(t, res.Fallback)
	builtAt := e.cache.builtAt

	// Next day the rebuild fails: the old profile is kept.
	e.ha.historyErr[optRunLoadEntity] = errors.New("timeout")
	next := e.now.Add(24 * time.Hour)
	built, err := e.cache.rebuildIfStale(context.Background(), e.deps(), e.cfg, next)
	assert.True(t, built)
	assert.Error(t, err)
	assert.Equal(t, builtAt, e.cache.builtAt)
	_, reasons := e.cache.current(e.cfg, next)
	assert.Empty(t, reasons, "profile ≤ 26 h old is still used")

	// No retry within the retry interval.
	built, _ = e.cache.rebuildIfStale(context.Background(), e.deps(), e.cfg, next.Add(30*time.Minute))
	assert.False(t, built)

	// After 26 h the profile is static.
	p, reasons := e.cache.current(e.cfg, e.now.Add(27*time.Hour))
	assert.Equal(t, []optimize.Reason{optimize.ReasonConsumptionStaticFallback}, reasons)
	assert.Equal(t, optStaticProfile(e.cfg), p)

	// Retry after an hour succeeds.
	delete(e.ha.historyErr, optRunLoadEntity)
	built, err = e.cache.rebuildIfStale(context.Background(), e.deps(), e.cfg, next.Add(time.Hour))
	assert.True(t, built)
	assert.NoError(t, err)
	assert.False(t, e.cache.lastFailed)

	var nilCache *profileCache
	_, err = nilCache.rebuildIfStale(context.Background(), e.deps(), e.cfg, next)
	assert.Error(t, err)
}

func TestRunOptimization_ColdStartFailureNotRetriedInRun(t *testing.T) {
	e := newOptRunEnv(t)
	e.ha.historyErr[optRunLoadEntity] = errors.New("timeout")
	res := e.run(context.Background())
	require.Nil(t, res.Fallback)
	calls := e.ha.historyCount(optRunLoadEntity)

	e.now = e.now.Add(time.Hour)
	e.ha.states[optRunPriceEntity] = optRunPriceState(e.now, e.loc, nil, nil, 1)
	res = e.run(context.Background())
	require.Nil(t, res.Fallback)
	assert.Equal(t, calls, e.ha.historyCount(optRunLoadEntity), "retries happen in rebuildIfStale, after dispatch")
	assert.Equal(t, optimize.ReasonConsumptionStaticFallback, res.Reasons[0])
}

// --- T-0133: optimize mode always uses the forecast cache path -----------

func TestCachedForecastLoader_T0133_UsesCachePath(t *testing.T) {
	loc := optRunLoc(t)
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, loc)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pw.Forecasts{Forecasts: optRunForecasts(now, loc)[0]})
	}))
	defer server.Close()

	// OptimizeConfig has no cache_forecast field: the cache path is the only
	// path, whatever cache_forecast is set to (design §Solcast).
	cfg := optRunCfg(loc)
	cfg.SolcastURLs = []string{server.URL}
	cfg.CacheFilePref = filepath.Join(t.TempDir(), "forecast")
	cfg.ForecastMaxAge = 0 // the adapter applies max(cache_time, 24 h)

	loader := newForecastLoader()
	for i := 0; i < 3; i++ {
		fc, meta, err := loader.Load(context.Background(), cfg, now.Add(time.Duration(i)*20*time.Minute))
		require.NoError(t, err)
		require.Len(t, fc, 1)
		assert.NotEmpty(t, fc[0])
		assert.False(t, meta.AnyStale())
		assert.False(t, meta.AnyMissing())
	}
	assert.Equal(t, int32(1), calls.Load(), "hourly loads within cache_time reuse the cache")

	// After cache_time the forecast is downloaded again.
	_, _, err := loader.Load(context.Background(), cfg, now.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())

	// The production loader drives runOptimization too.
	e := newOptRunEnv(t)
	e.now, e.cfg = now.Add(2*time.Hour+10*time.Minute), cfg
	e.ha.states[optRunPriceEntity] = optRunPriceState(e.now, loc, nil, nil, 1)
	res := runOptimization(context.Background(), optimizeDeps{HA: e.ha, Forecasts: loader, Battery: e.bat}, cfg, e.now, e.cache)
	require.Nil(t, res.Fallback)
	assert.Equal(t, int32(2), calls.Load())
	assert.NotContains(t, res.Reasons, optimize.ReasonPVForecastMissing)
}

func TestCachedForecastLoader_NoURLIsDegraded(t *testing.T) {
	e := newOptRunEnv(t)
	e.cfg.SolcastURLs = nil
	res := runOptimization(context.Background(), optimizeDeps{HA: e.ha, Forecasts: newForecastLoader(), Battery: e.bat}, e.cfg, e.now, e.cache)
	require.Nil(t, res.Fallback)
	assert.Contains(t, res.Reasons, optimize.ReasonPVForecastMissing)

	res = runOptimization(context.Background(), optimizeDeps{HA: e.ha, Battery: e.bat}, e.cfg, e.now, e.cache)
	require.Nil(t, res.Fallback)
	assert.Contains(t, res.Reasons, optimize.ReasonPVForecastMissing)
}

// --- TC-004d: price currency / unit check ---------------------------------

func TestOptCheckPriceCurrency_TC004d(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
		ok    bool
	}{
		{"DKK/kWh in DK2", map[string]any{"unit_of_measurement": "DKK/kWh", "currency": "DKK", "region": "DK2"}, true},
		{"DKK/MWh in DK2 (divided by 1000)", map[string]any{"unit_of_measurement": "DKK/MWh", "currency": "DKK", "region": "DK2"}, true},
		{"EUR/MWh in FI is local", map[string]any{"unit_of_measurement": "EUR/MWh", "currency": "EUR", "region": "FI"}, true},
		{"SEK/MWh via country", map[string]any{"unit_of_measurement": "SEK/MWh", "country": "Sweden"}, true},
		{"unit from currency + unit attrs", map[string]any{"currency": "DKK", "unit": "MWh", "region": "DK1"}, true},
		{"EUR/kWh in DK2 accepted as configured", map[string]any{"unit_of_measurement": "EUR/kWh", "currency": "EUR", "region": "DK2"}, true},
		{"non-price unit left to the parser", map[string]any{"unit_of_measurement": "kVA"}, true},
		{"EUR/MWh in DK2", map[string]any{"unit_of_measurement": "EUR/MWh", "currency": "EUR", "region": "DK2"}, false},
		{"EUR/MWh with currency DKK", map[string]any{"unit_of_measurement": "EUR/MWh", "currency": "DKK", "region": "DK2"}, false},
		{"EUR/MWh without region", map[string]any{"unit_of_measurement": "EUR/MWh"}, false},
		{"DKK/MWh with unknown region", map[string]any{"unit_of_measurement": "DKK/MWh", "region": "XX9"}, false},
		{"øre/kWh with currency DKK", map[string]any{"unit_of_measurement": "øre/kWh", "currency": "DKK", "region": "DK2"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := optCheckPriceCurrency(tc.attrs)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, optimize.ErrPriceFormat)
		})
	}
}

func TestRunOptimization_TC004d_PriceUnitFallback(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
		scale float64
		ok    bool
	}{
		{"EUR/MWh in DK2 rejected", map[string]any{"unit_of_measurement": "EUR/MWh", "currency": "EUR"}, 1000, false},
		{"EUR/MWh without area rejected", map[string]any{"unit_of_measurement": "EUR/MWh", "currency": "EUR", "region": nil}, 1000, false},
		{"Wh unit rejected by parser", map[string]any{"unit_of_measurement": "DKK/Wh"}, 1, false},
		{"DKK/MWh accepted and scaled", map[string]any{"unit_of_measurement": "DKK/MWh"}, 1000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newOptRunEnv(t)
			e.ha.states[optRunPriceEntity] = optRunPriceState(e.now, e.loc, tc.attrs, nil, tc.scale)
			res := e.run(context.Background())
			if tc.ok {
				require.Nil(t, res.Fallback, "%+v", res.Fallback)
				assert.InDelta(t, 3.0, res.PriceMax, 1e-9, "/MWh divided by 1000")
				assert.Equal(t, "DKK", res.Currency)
				return
			}
			require.NotNil(t, res.Fallback)
			assert.Equal(t, optimize.ReasonFallbackPriceFormat, res.Fallback.Reason)
		})
	}
}

// --- error mapping and adapters ------------------------------------------

func TestOptHAErrReason(t *testing.T) {
	assert.Equal(t, optimize.ReasonFallbackHAAuthFailed, optHAErrReason(fmt.Errorf("x: %w", homeassistant.ErrAuth)))
	assert.Equal(t, optimize.ReasonFallbackPriceEntityNotFound, optHAErrReason(fmt.Errorf("x: %w", homeassistant.ErrNotFound)))
	assert.Equal(t, optimize.ReasonFallbackPriceFormat, optHAErrReason(fmt.Errorf("x: %w", homeassistant.ErrTooLarge)))
	assert.Equal(t, optimize.ReasonFallbackHAUnreachable, optHAErrReason(context.DeadlineExceeded))
}

func TestOptGridErrReason(t *testing.T) {
	assert.Equal(t, optimize.ReasonFallbackMissingPrices, optGridErrReason(fmt.Errorf("x: %w", optimize.ErrNoPricedHours)))
	assert.Equal(t, optimize.ReasonFallbackSocUnavailable, optGridErrReason(fmt.Errorf("x: %w", optimize.ErrInvalidBattery)))
	assert.Equal(t, optimize.ReasonFallbackImplausibleInput, optGridErrReason(fmt.Errorf("x: %w", optimize.ErrInvalidGrid)))
	assert.Equal(t, optimize.ReasonFallbackImplausibleInput, optGridErrReason(errors.New("other")))
}

type optRunFakeStorage struct {
	capWh, soc float64
	err        error
}

func (f optRunFakeStorage) Handler(string) (float64, float64, float64, error) {
	return 0, f.capWh, f.soc, f.err
}

func TestStorageBatteryReader(t *testing.T) {
	r := storageBatteryReader{client: optRunFakeStorage{capWh: 10240, soc: 42.5}}
	soc, capWh, err := r.Read(context.Background(), "192.0.2.10")
	require.NoError(t, err)
	assert.Equal(t, 42.5, soc)
	assert.Equal(t, 10240.0, capWh)

	_, _, err = storageBatteryReader{client: optRunFakeStorage{err: errors.New("disabled")}}.Read(context.Background(), "192.0.2.10")
	assert.Error(t, err)

	_, _, err = storageBatteryReader{}.Read(context.Background(), "192.0.2.10")
	assert.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = r.Read(ctx, "192.0.2.10")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNewBatteryReader_UsesStorageFactory(t *testing.T) {
	old := newStorage
	newStorage = func() storageClient { return optRunFakeStorage{capWh: 5000, soc: 80} }
	t.Cleanup(func() { newStorage = old })

	soc, capWh, err := newBatteryReader().Read(context.Background(), "192.0.2.10")
	require.NoError(t, err)
	assert.Equal(t, 80.0, soc)
	assert.Equal(t, 5000.0, capWh)
}

func TestNewHAClientAndDeps(t *testing.T) {
	t.Setenv("SUPERVISOR_TOKEN", "")
	cfg := OptimizeConfig{HAURL: "http://127.0.0.1:8123", HAToken: "test-token"}
	c, err := newHAClient(cfg)
	require.NoError(t, err)
	assert.NotNil(t, c)

	deps, err := newOptimizeDeps(cfg)
	require.NoError(t, err)
	assert.NotNil(t, deps.HA)
	assert.NotNil(t, deps.Forecasts)
	assert.NotNil(t, deps.Battery)

	_, err = newHAClient(OptimizeConfig{})
	assert.Error(t, err, "no ha_url and no Supervisor token")
	_, err = newOptimizeDeps(OptimizeConfig{HAURL: "http://127.0.0.1:8123"})
	assert.Error(t, err, "ha_url without ha_token")

	t.Setenv("SUPERVISOR_TOKEN", "test-supervisor-token")
	c, err = newHAClient(OptimizeConfig{})
	require.NoError(t, err)
	assert.NotNil(t, c)
}
