package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sbam/pkg/homeassistant"
	pw "sbam/pkg/power"
	u "sbam/src/utils"
)

// Time budgets of the Home Assistant PV reads (SPEC-0003 AC-7, S-023).
const (
	// pvHARetries is the number of retries of a failed state read in a
	// classic schedule run (5xx and transport errors only).
	pvHARetries = 2
	// pvHARetryDelay spaces the retries.
	pvHARetryDelay = 15 * time.Second
	// pvHAClassicTimeout bounds one classic run's PV reads, retries
	// included, so the tick stays within its 50 s budget.
	pvHAClassicTimeout = 50 * time.Second
	// pvHASingleReadTimeout bounds the PV reads of `sbam estimate` (one read
	// per entity, at most three 10 s state reads).
	pvHASingleReadTimeout = 30 * time.Second
)

// errHAClientMissing is returned by haEntityReader without a client. It is
// never retried.
var errHAClientMissing = errors.New("home assistant client not configured")

// pvRetrySleep waits d or until ctx is done. A package variable so tests can
// skip the 15 s retry delay.
var pvRetrySleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// pvDaySource yields the classic-mode and `estimate` day total of PV
// production. Implementations log their own Info/Warn lines; err != nil
// means "no forecast this run, publish the error" (design §pkg/cmd seams).
type pvDaySource interface {
	DayProduction(ctx context.Context, horizon string, now time.Time) (wh float64, ok bool, err error)
	// LastForecasts returns the forecast periods behind the last successful
	// DayProduction call (nil before that). reserve_horizon until_solar
	// sizes the reserve from them, whichever source supplied them.
	LastForecasts() []pw.Forecasts
}

// solcastDaySource is the solcast_api day source. It calls
// newPower().Handler exactly as the runner did before SPEC-0003 (AC-1).
type solcastDaySource struct {
	apiKey        string
	url           string
	cacheForecast bool
	cachePrefix   string
	cacheTime     int32

	last []pw.Forecasts
}

// DayProduction implements pvDaySource via the Solcast API path.
func (s *solcastDaySource) DayProduction(_ context.Context, horizon string, now time.Time) (float64, bool, error) {
	h := newPower()
	wh, ok, err := h.Handler(s.apiKey, s.url, s.cacheForecast, s.cachePrefix, s.cacheTime, horizon, now)
	if err == nil && ok {
		s.last = h.LastForecasts()
	}
	return wh, ok, err
}

// LastForecasts implements pvDaySource: the per-site Solcast forecasts of
// the last successful DayProduction call.
func (s *solcastDaySource) LastForecasts() []pw.Forecasts {
	return s.last
}

// haDaySource is the ha_solcast day source. The reader is created once per
// process (it remembers the S-024 "Warn once" state).
type haDaySource struct {
	reader  *pw.HAForecastReader
	timeout time.Duration
}

// DayProduction implements pvDaySource via the HA Solcast integration,
// bounded by the source's timeout.
func (s haDaySource) DayProduction(ctx context.Context, horizon string, now time.Time) (float64, bool, error) {
	if s.reader == nil {
		return 0, false, fmt.Errorf("pv forecast: %w", errHAClientMissing)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	return s.reader.DayProduction(ctx, horizon, now)
}

// LastForecasts implements pvDaySource: the merged integration periods of
// the last successful DayProduction call, as one site.
func (s haDaySource) LastForecasts() []pw.Forecasts {
	if s.reader == nil {
		return nil
	}
	return s.reader.LastForecasts()
}

// haStateReader is the part of the HA client the PV adapter needs; haClient
// and homeassistant.Client satisfy it.
type haStateReader interface {
	State(ctx context.Context, entityID string) (homeassistant.State, error)
}

// haEntityReader adapts the HA client to power.EntityReader. A 404 wraps
// power.ErrEntityNotFound; the original error stays in the chain, so
// homeassistant sentinels still match.
type haEntityReader struct {
	c haStateReader
}

// ReadEntity implements power.EntityReader.
func (r haEntityReader) ReadEntity(ctx context.Context, entityID string) (pw.EntityState, error) {
	if r.c == nil {
		return pw.EntityState{}, errHAClientMissing
	}
	st, err := r.c.State(ctx, entityID)
	if err != nil {
		if errors.Is(err, homeassistant.ErrNotFound) {
			return pw.EntityState{}, fmt.Errorf("%w: %w", pw.ErrEntityNotFound, err)
		}
		return pw.EntityState{}, err
	}
	return pw.EntityState{State: st.State, Attributes: st.Attributes}, nil
}

// retryingEntityReader retries a failed read for classic schedule runs only
// (S-023): at most retries times, delay apart, on 5xx and transport errors.
// 401/403, 404, an oversized body and invalid data are not retried. A retry
// that cannot complete before the context deadline is not started.
type retryingEntityReader struct {
	next    pw.EntityReader
	retries int
	delay   time.Duration
	sleep   func(ctx context.Context, d time.Duration) error
}

// ReadEntity implements power.EntityReader.
func (r retryingEntityReader) ReadEntity(ctx context.Context, entityID string) (pw.EntityState, error) {
	st, err := r.next.ReadEntity(ctx, entityID)
	for attempt := 1; err != nil && attempt <= r.retries && haReadRetryable(ctx, err); attempt++ {
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) <= r.delay {
			break
		}
		u.Log.Debugw("PV forecast entity read failed; retrying", "entity", entityID, "retry", attempt, "delay", r.delay, "error", err)
		sleep := r.sleep
		if sleep == nil {
			sleep = pvRetrySleep
		}
		if sleep(ctx, r.delay) != nil {
			break
		}
		st, err = r.next.ReadEntity(ctx, entityID)
	}
	return st, err
}

// haStatusPattern extracts the HTTP status from a homeassistant error, which
// carries "status NNN" and the request path only (ADR-0001).
var haStatusPattern = regexp.MustCompile(`status (\d{3})`)

// haReadRetryable reports whether a failed state read is worth retrying:
// 5xx answers and transport errors (connection refused or reset, timeouts,
// a broken body). Auth failures, 404, an oversized body, other statuses and
// invalid JSON are not.
func haReadRetryable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	switch {
	case errors.Is(err, errHAClientMissing),
		errors.Is(err, pw.ErrEntityNotFound),
		errors.Is(err, homeassistant.ErrNotFound),
		errors.Is(err, homeassistant.ErrAuth),
		errors.Is(err, homeassistant.ErrTooLarge):
		return false
	case errors.Is(err, homeassistant.ErrStatus):
		m := haStatusPattern.FindStringSubmatch(err.Error())
		if m == nil {
			return false
		}
		code, convErr := strconv.Atoi(m[1])
		return convErr == nil && code >= 500 && code <= 599
	case strings.Contains(err.Error(), "decode response"):
		return false // invalid data
	default:
		return true
	}
}

// newHAAccessClient resolves Home Assistant access (Supervisor token via
// os.Getenv, or ha_url + ha_token) and returns the GET-only client. It does
// not contact HA (no startup probe, S-023 c). Package-level so tests can
// swap it.
var newHAAccessClient = func(haURL, haToken string) (haClient, error) {
	access, err := homeassistant.ResolveAccess(haURL, haToken, os.Getenv)
	if err != nil {
		return nil, err
	}
	c, err := homeassistant.New(access)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// newPVDaySource returns the day source for pv. solcast_api returns solcast
// without touching HA. ha_solcast builds the HA client once; retry enables
// the S-023 retries (classic schedule runs only; `estimate` reads once).
func newPVDaySource(pv PVSourceConfig, solcast solcastDaySource, retry bool) (pvDaySource, error) {
	if !pv.isHA() {
		return &solcast, nil
	}
	c, err := newHAAccessClient(pv.HAURL, pv.HAToken)
	if err != nil {
		return nil, fmt.Errorf("home assistant client: %w", err)
	}
	var reader pw.EntityReader = haEntityReader{c: c}
	timeout := pvHASingleReadTimeout
	if retry {
		reader = retryingEntityReader{next: reader, retries: pvHARetries, delay: pvHARetryDelay}
		timeout = pvHAClassicTimeout
	}
	return haDaySource{reader: pw.NewHAForecastReader(reader, pv.Entities), timeout: timeout}, nil
}

// haForecastLoader is the forecastLoader for optimize mode with
// pv_source=ha_solcast. It reads once per run (no retries, so the optimize
// run budget is unchanged) and presents the merged periods as one source.
type haForecastLoader struct {
	reader *pw.HAForecastReader
	// until bounds the fingerprint's covered/expected counts to the plan
	// horizon (end of the known prices); zero means the end of tomorrow.
	until time.Time
}

// pvHorizonBounder is implemented by forecast loaders whose fingerprint
// counts the half-hours of the plan horizon. The optimize run passes the end
// of the price horizon before it loads the forecast.
type pvHorizonBounder interface {
	boundHorizon(end time.Time)
}

// boundHorizon implements pvHorizonBounder.
func (l *haForecastLoader) boundHorizon(end time.Time) { l.until = end }

// newHAForecastLoader builds the loader on the process's HA client (S-025 d).
func newHAForecastLoader(c haStateReader, e pw.HAForecastEntities) *haForecastLoader {
	return &haForecastLoader{reader: pw.NewHAForecastReader(haEntityReader{c: c}, e)}
}

// Load implements forecastLoader. Read failures are degraded inputs: they
// are reported in the meta (Missing/Stale) and the error is dropped after the
// reader's Warn lines. Only a cancelled context is returned as an error.
func (l *haForecastLoader) Load(ctx context.Context, cfg OptimizeConfig, now time.Time) ([][]pw.Forecast, pw.ForecastMeta, error) {
	f, meta, err := l.reader.Load(ctx, now)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, pw.ForecastMeta{}, err
		}
		u.Log.Debugw("PV forecast from Home Assistant not used for this run", "error", err)
	}
	logHAForecastFingerprint(f.Forecasts, now.In(optLoc(cfg)), l.until)
	return [][]pw.Forecast{f.Forecasts}, pw.ForecastMeta{Sources: []pw.ForecastSourceMeta{meta}}, nil
}

// logHAForecastFingerprint logs the per-run input fingerprint in optimize
// mode (AC-13, S-028): periods, total Wh and the covered/expected half-hours
// from now to until, the end of the plan horizon (zero: the end of
// tomorrow). Bounding by the plan horizon keeps the 00:00 run, before the
// integration has rolled over and before tomorrow's prices exist, from
// reporting tomorrow's half-hours as expected.
func logHAForecastFingerprint(periods []pw.Forecast, now, until time.Time) {
	totalWh := 0.0
	for _, p := range periods {
		totalWh += p.PVEstimate * pw.HAForecastPeriod.Hours() * 1000
	}
	f := pw.Forecasts{Forecasts: periods}
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	tomorrow := time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
	covered := pw.CountDayPeriods(f, today, &now) + pw.CountDayPeriods(f, tomorrow, nil)
	expected := pw.ExpectedDayPeriods(today, &now) + pw.ExpectedDayPeriods(tomorrow, nil)
	if !until.IsZero() {
		until = until.In(now.Location())
		// Remove the half-hours that end after until (counts are by end).
		covered -= pw.CountDayPeriods(f, today, &until) + pw.CountDayPeriods(f, tomorrow, &until)
		expected -= pw.ExpectedDayPeriods(today, &until) + pw.ExpectedDayPeriods(tomorrow, &until)
	}
	u.Log.Infow("PV forecast input fingerprint", "pv_source", pw.PVSourceHASolcast, "periods", len(periods),
		"total_wh", int(totalWh), "covered", covered, "expected", expected)
}
