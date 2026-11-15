package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/homeassistant"
	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"
	pw "sbam/pkg/power"
	u "sbam/src/utils"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Entities of the synthetic pkg/power fixtures (the add-on defaults).
const (
	pvTestToday      = defPVEntityToday
	pvTestTomorrow   = defPVEntityTomorrow
	pvTestLastPolled = defPVEntityLastPolled
	pvTestHAToken    = "test-ha-token"
)

// pvTestEntities are the default entities.
var pvTestEntities = pw.HAForecastEntities{Today: pvTestToday, Tomorrow: pvTestTomorrow, LastPolled: pvTestLastPolled}

// pvTestNow is within the fixtures' window: the today fixture covers
// 2026-10-26 (+01:00) and the last poll is 2026-10-26T05:12:03Z.
func pvTestNow(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 10, 26, 10, 0, 0, 0, optRunLoc(t))
}

// pvTestFixture reads a recorded-shape HA state from pkg/power/testdata.
func pvTestFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "power", "testdata", name))
	require.NoError(t, err)
	return b
}

// pvTestObserveLogs routes u.Log to an observer for the test.
func pvTestObserveLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	old := u.Log
	u.Log = zap.New(core).Sugar()
	t.Cleanup(func() { u.Log = old })
	return logs
}

// pvTestHAServer is a mock HA serving /api/states/<entity> from the
// fixtures. script holds per-entity status codes answered before the
// fixture (e.g. 502 then 200).
type pvTestHAServer struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	bodies map[string][]byte
	script map[string][]int
	hits   map[string]int
	auth   []string
	paths  []string
}

func newPVTestHAServer(t *testing.T) *pvTestHAServer {
	t.Helper()
	s := &pvTestHAServer{
		t: t,
		bodies: map[string][]byte{
			pvTestToday:      pvTestFixture(t, "ha_solcast_today.json"),
			pvTestTomorrow:   pvTestFixture(t, "ha_solcast_tomorrow.json"),
			pvTestLastPolled: pvTestFixture(t, "ha_solcast_last_polled.json"),
		},
		script: map[string][]int{},
		hits:   map[string]int{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *pvTestHAServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entity := strings.TrimPrefix(r.URL.Path, "/api/states/")
	s.hits[entity]++
	s.paths = append(s.paths, r.URL.Path)
	s.auth = append(s.auth, r.Method+" "+r.Header.Get("Authorization"))
	if codes := s.script[entity]; len(codes) > 0 {
		s.script[entity] = codes[1:]
		if codes[0] != http.StatusOK {
			w.WriteHeader(codes[0])
			return
		}
	}
	body, ok := s.bodies[entity]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (s *pvTestHAServer) hitCount(entity string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[entity]
}

// requestPaths returns the request paths in arrival order.
func (s *pvTestHAServer) requestPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// authHeaders returns "METHOD Authorization" per request.
func (s *pvTestHAServer) authHeaders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auth...)
}

// setBody replaces the state served for entity.
func (s *pvTestHAServer) setBody(entity string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies[entity] = body
}

// pvTestNoSleep replaces the retry delay and records the requested waits.
func pvTestNoSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	old := pvRetrySleep
	pvRetrySleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	t.Cleanup(func() { pvRetrySleep = old })
	return &waits
}

// pvTestHAConfig is a valid ha_solcast configuration against srv.
func pvTestHAConfig(srvURL string) PVSourceConfig {
	return PVSourceConfig{Source: pw.PVSourceHASolcast, Entities: pvTestEntities, HAURL: srvURL, HAToken: pvTestHAToken}
}

// --- haEntityReader --------------------------------------------------------

type pvTestStateReader struct {
	states map[string]homeassistant.State
	errs   map[string]error
	calls  int
}

func (f *pvTestStateReader) State(_ context.Context, entity string) (homeassistant.State, error) {
	f.calls++
	if err := f.errs[entity]; err != nil {
		return homeassistant.State{}, err
	}
	return f.states[entity], nil
}

func TestHAEntityReader(t *testing.T) {
	notFound := fmt.Errorf("homeassistant: GET /api/states/sensor.x: status 404: %w", homeassistant.ErrNotFound)
	auth := fmt.Errorf("homeassistant: GET /api/states/sensor.y: status 401: %w", homeassistant.ErrAuth)
	c := &pvTestStateReader{
		states: map[string]homeassistant.State{"sensor.ok": {State: "1.5", Attributes: map[string]any{"a": 1.0}}},
		errs:   map[string]error{"sensor.x": notFound, "sensor.y": auth},
	}
	r := haEntityReader{c: c}

	t.Run("expected use maps state and attributes", func(t *testing.T) {
		st, err := r.ReadEntity(context.Background(), "sensor.ok")
		require.NoError(t, err)
		assert.Equal(t, pw.EntityState{State: "1.5", Attributes: map[string]any{"a": 1.0}}, st)
	})
	t.Run("404 wraps power.ErrEntityNotFound and keeps the HA error", func(t *testing.T) {
		_, err := r.ReadEntity(context.Background(), "sensor.x")
		require.Error(t, err)
		assert.ErrorIs(t, err, pw.ErrEntityNotFound)
		assert.ErrorIs(t, err, homeassistant.ErrNotFound)
	})
	t.Run("other errors pass through", func(t *testing.T) {
		_, err := r.ReadEntity(context.Background(), "sensor.y")
		assert.ErrorIs(t, err, homeassistant.ErrAuth)
		assert.NotErrorIs(t, err, pw.ErrEntityNotFound)
	})
	t.Run("no client is an error, not a panic", func(t *testing.T) {
		_, err := haEntityReader{}.ReadEntity(context.Background(), "sensor.ok")
		assert.ErrorIs(t, err, errHAClientMissing)
	})
}

// --- retry -----------------------------------------------------------------

type pvTestScriptReader struct {
	errs  []error // consumed per call; nil entries (or exhaustion) succeed
	calls int
}

func (f *pvTestScriptReader) ReadEntity(context.Context, string) (pw.EntityState, error) {
	f.calls++
	if len(f.errs) == 0 {
		return pw.EntityState{State: "ok"}, nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	if err != nil {
		return pw.EntityState{}, err
	}
	return pw.EntityState{State: "ok"}, nil
}

func pvTestStatusErr(code int) error {
	sentinel := homeassistant.ErrStatus
	switch code {
	case 401, 403:
		sentinel = homeassistant.ErrAuth
	case 404:
		sentinel = homeassistant.ErrNotFound
	}
	return fmt.Errorf("homeassistant: GET /api/states/sensor.x: status %d: %w", code, sentinel)
}

func TestHAReadRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"502", pvTestStatusErr(502), true},
		{"503", pvTestStatusErr(503), true},
		{"transport", errors.New("homeassistant: GET /api/states/sensor.x: connection refused"), true},
		{"request timeout", fmt.Errorf("homeassistant: GET /api/states/sensor.x: %w", context.DeadlineExceeded), true},
		{"401", pvTestStatusErr(401), false},
		{"403", pvTestStatusErr(403), false},
		{"404", pvTestStatusErr(404), false},
		{"404 via adapter", fmt.Errorf("%w: %w", pw.ErrEntityNotFound, pvTestStatusErr(404)), false},
		{"400", pvTestStatusErr(400), false},
		{"429", pvTestStatusErr(429), false},
		{"redirect", fmt.Errorf("homeassistant: GET /x: status 302 (redirect not followed): %w", homeassistant.ErrStatus), false},
		{"too large", fmt.Errorf("homeassistant: GET /x: body exceeds 1 bytes: %w", homeassistant.ErrTooLarge), false},
		{"invalid json", errors.New("homeassistant: GET /api/states/sensor.x: decode response: invalid JSON"), false},
		{"no client", errHAClientMissing, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, haReadRetryable(context.Background(), tc.err))
		})
	}

	t.Run("cancelled context is never retried", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.False(t, haReadRetryable(ctx, pvTestStatusErr(502)))
	})
}

func TestRetryingEntityReader(t *testing.T) {
	e502 := pvTestStatusErr(502)
	cases := []struct {
		name      string
		errs      []error
		wantCalls int
		wantErr   bool
		wantWaits int
	}{
		{"success first try", nil, 1, false, 0},
		{"502 then ok", []error{e502}, 2, false, 1},
		{"502 twice then ok", []error{e502, e502}, 3, false, 2},
		{"502 three times gives up", []error{e502, e502, e502}, 3, true, 2},
		{"401 not retried", []error{pvTestStatusErr(401)}, 1, true, 0},
		{"404 not retried", []error{pvTestStatusErr(404)}, 1, true, 0},
		{"invalid data not retried", []error{errors.New("homeassistant: GET /x: decode response: invalid JSON")}, 1, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := &pvTestScriptReader{errs: tc.errs}
			var waits []time.Duration
			r := retryingEntityReader{next: next, retries: pvHARetries, delay: pvHARetryDelay,
				sleep: func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }}
			_, err := r.ReadEntity(context.Background(), "sensor.x")
			assert.Equal(t, tc.wantErr, err != nil, "err=%v", err)
			assert.Equal(t, tc.wantCalls, next.calls)
			require.Len(t, waits, tc.wantWaits)
			for _, w := range waits {
				assert.Equal(t, 15*time.Second, w)
			}
		})
	}

	t.Run("no retry that cannot finish before the deadline", func(t *testing.T) {
		next := &pvTestScriptReader{errs: []error{e502, e502}}
		r := retryingEntityReader{next: next, retries: pvHARetries, delay: pvHARetryDelay,
			sleep: func(context.Context, time.Duration) error { t.Fatal("must not wait"); return nil }}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := r.ReadEntity(ctx, "sensor.x")
		assert.Error(t, err)
		assert.Equal(t, 1, next.calls)
	})

	t.Run("a cancelled wait stops the retries", func(t *testing.T) {
		next := &pvTestScriptReader{errs: []error{e502, e502}}
		r := retryingEntityReader{next: next, retries: pvHARetries, delay: pvHARetryDelay,
			sleep: func(context.Context, time.Duration) error { return context.Canceled }}
		_, err := r.ReadEntity(context.Background(), "sensor.x")
		assert.ErrorIs(t, err, homeassistant.ErrStatus, "the last read error is returned")
		assert.Equal(t, 1, next.calls)
	})

	t.Run("default sleep honours the context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.ErrorIs(t, pvRetrySleep(ctx, time.Hour), context.Canceled)
	})
}

// --- day sources -------------------------------------------------------------

type pvTestCapturePower struct {
	calls   int
	apiKey  string
	url     string
	cache   bool
	prefix  string
	ttl     int32
	horizon string
	now     time.Time
}

func (f *pvTestCapturePower) Handler(apiKey string, url string, cacheForecast bool, cachePrefix string, cacheTime int32, horizon string, now time.Time) (float64, bool, error) {
	f.calls++
	f.apiKey, f.url, f.cache, f.prefix, f.ttl, f.horizon, f.now = apiKey, url, cacheForecast, cachePrefix, cacheTime, horizon, now
	return 1234, true, nil
}

func (f *pvTestCapturePower) LastForecasts() []pw.Forecasts {
	return []pw.Forecasts{{Forecasts: []pw.Forecast{{PVEstimate: 1, PeriodEnd: "2026-06-15T12:00:00Z", Period: "PT30M"}}}}
}

// pvTestForbidHAClient fails the test if an HA client is built.
func pvTestForbidHAClient(t *testing.T) {
	t.Helper()
	old := newHAAccessClient
	newHAAccessClient = func(string, string) (haClient, error) {
		t.Fatal("no Home Assistant client may be built with pv_source=solcast_api")
		return nil, nil
	}
	t.Cleanup(func() { newHAAccessClient = old })
}

// T-0315 (AC-1): with the default source the runner calls newPower().Handler
// with exactly the RunnerConfig values, and no HA client is built.
func TestPVSource_DefaultUnchanged_T0315(t *testing.T) {
	pvTestForbidHAClient(t)

	t.Run("config default is solcast_api", func(t *testing.T) {
		resetViper(t)
		cfg, problems, warnings := loadPVSourceConfig(optCfgNoEnv, true)
		assert.Empty(t, problems)
		assert.Empty(t, warnings)
		assert.Equal(t, pw.PVSourceSolcastAPI, cfg.Source)
		assert.Empty(t, cfg.IgnoredKeys)
	})

	t.Run("newPVDaySource returns the solcast source untouched", func(t *testing.T) {
		in := solcastDaySource{apiKey: "k", url: "https://solcast.example.invalid/a", cacheForecast: true, cachePrefix: "p", cacheTime: 60}
		src, err := newPVDaySource(PVSourceConfig{Source: pw.PVSourceSolcastAPI}, in, true)
		require.NoError(t, err)
		assert.Equal(t, &in, src)
	})

	t.Run("runner Tick calls Handler with the configured arguments", func(t *testing.T) {
		fp := &pvTestCapturePower{}
		oldP, oldS, oldF := newPower, newStorage, newFronius
		newPower = func() powerClient { return fp }
		newStorage = func() storageClient { return &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40} }
		tf := &trackingFroniusClient{decision: fronius.DecisionIdle, reason: "fake"}
		newFronius = func(fronius.SunSpecType) froniusClient { return tf }
		t.Cleanup(func() { newPower, newStorage, newFronius = oldP, oldS, oldF })

		r := newRunnerForTests(newFakeClient())
		now := r.now()
		require.NoError(t, r.Tick(context.Background(), now))

		require.Equal(t, 1, fp.calls)
		assert.Equal(t, "key", fp.apiKey)
		assert.Equal(t, "https://example.test/forecast", fp.url)
		assert.False(t, fp.cache)
		assert.Equal(t, "cached_forecast", fp.prefix)
		assert.Equal(t, int32(7200), fp.ttl)
		assert.Equal(t, now, fp.now)
		assert.Equal(t, 1234.0, tf.lastForecastWh)
		assert.True(t, tf.lastForecastEnabled)
	})

	t.Run("apikey and url stay required with the existing messages", func(t *testing.T) {
		withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 0)
		a := validScheduleValidationArgs()
		err := checkScheduleschedule(a.crontab, "", a.url, a.froniusIP, a.pwConsumption, a.maxCharge, a.pwBattReserve, a.startHr, a.endHr, nil, a.schedulerMode, true, defPVSource)
		require.EqualError(t, err, "the --apikey flag must be set")
		err = checkScheduleschedule(a.crontab, a.apiKey, "", a.froniusIP, a.pwConsumption, a.maxCharge, a.pwBattReserve, a.startHr, a.endHr, nil, a.schedulerMode, true, defPVSource)
		require.EqualError(t, err, "the --url flag must be set")
		withEstimateCacheTime(t, 0)
		require.EqualError(t, CheckEstimate("", "u", "127.0.0.1", defPVSource), "the --apikey flag must be set")
		require.EqualError(t, CheckEstimate("k", "", "127.0.0.1", defPVSource), "the --url flag must be set")
	})
}

func TestNewPVDaySource_HA(t *testing.T) {
	t.Run("builds one client and wires retries for classic runs", func(t *testing.T) {
		calls := 0
		old := newHAAccessClient
		newHAAccessClient = func(haURL, haToken string) (haClient, error) {
			calls++
			assert.Equal(t, "http://ha.local:8123", haURL)
			assert.Equal(t, pvTestHAToken, haToken)
			return &optRunFakeHA{}, nil
		}
		t.Cleanup(func() { newHAAccessClient = old })

		src, err := newPVDaySource(pvTestHAConfig("http://ha.local:8123"), solcastDaySource{}, true)
		require.NoError(t, err)
		ds, ok := src.(haDaySource)
		require.True(t, ok)
		assert.Equal(t, pvHAClassicTimeout, ds.timeout)
		assert.Equal(t, 1, calls)

		src, err = newPVDaySource(pvTestHAConfig("http://ha.local:8123"), solcastDaySource{}, false)
		require.NoError(t, err)
		assert.Equal(t, pvHASingleReadTimeout, src.(haDaySource).timeout)
	})

	t.Run("client error is returned", func(t *testing.T) {
		t.Setenv("SUPERVISOR_TOKEN", "")
		_, err := newPVDaySource(PVSourceConfig{Source: pw.PVSourceHASolcast, Entities: pvTestEntities}, solcastDaySource{}, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "home assistant client")
	})

	t.Run("no HA request at construction (S-023 c)", func(t *testing.T) {
		srv := newPVTestHAServer(t)
		_, err := newPVDaySource(pvTestHAConfig(srv.srv.URL), solcastDaySource{}, true)
		require.NoError(t, err)
		assert.Zero(t, srv.hitCount(pvTestToday)+srv.hitCount(pvTestTomorrow)+srv.hitCount(pvTestLastPolled))
	})
}

type pvTestDeadlineReader struct {
	deadline time.Time
	ok       bool
}

func (f *pvTestDeadlineReader) ReadEntity(ctx context.Context, _ string) (pw.EntityState, error) {
	f.deadline, f.ok = ctx.Deadline()
	return pw.EntityState{}, errors.New("unreachable")
}

func TestHADaySource_Timeout(t *testing.T) {
	r := &pvTestDeadlineReader{}
	s := haDaySource{reader: pw.NewHAForecastReader(r, pw.HAForecastEntities{Today: pvTestToday, Tomorrow: pvTestTomorrow}), timeout: pvHAClassicTimeout}
	start := time.Now()
	_, ok, err := s.DayProduction(context.Background(), pw.ForecastHorizonToday, pvTestNow(t))
	assert.False(t, ok)
	assert.Error(t, err)
	require.True(t, r.ok, "reads must run under a deadline")
	assert.WithinDuration(t, start.Add(pvHAClassicTimeout), r.deadline, 5*time.Second)

	_, ok, err = haDaySource{}.DayProduction(context.Background(), pw.ForecastHorizonToday, pvTestNow(t))
	assert.False(t, ok)
	assert.ErrorIs(t, err, errHAClientMissing)
}

// T-0312 (S-023): classic runs retry 5xx/transport errors only; estimate
// and optimize runs read once. Uses the real HA client against a mock HA.
func TestHADaySource_Retry_T0312(t *testing.T) {
	now := pvTestNow(t)
	cases := []struct {
		name      string
		script    []int
		wantHits  int
		wantOK    bool
		wantWaits int
	}{
		{"a: 502 then 200", []int{502}, 2, true, 1},
		{"b: 502, 502, 200", []int{502, 502}, 3, true, 2},
		{"c: 502 x3", []int{502, 502, 502}, 3, false, 2},
		{"d: 401", []int{401}, 1, false, 0},
		{"e: 404", []int{404}, 1, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pvTestObserveLogs(t)
			waits := pvTestNoSleep(t)
			srv := newPVTestHAServer(t)
			srv.script[pvTestToday] = tc.script

			src, err := newPVDaySource(pvTestHAConfig(srv.srv.URL), solcastDaySource{}, true)
			require.NoError(t, err)
			wh, ok, err := src.DayProduction(context.Background(), pw.ForecastHorizonToday, now)

			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantOK, err == nil, "err=%v", err)
			if tc.wantOK {
				assert.InDelta(t, 19309.1, wh, 0.5)
			} else {
				assert.ErrorIs(t, err, pw.ErrForecastDayNotCovered, "the tomorrow entity alone does not cover today")
			}
			assert.Equal(t, tc.wantHits, srv.hitCount(pvTestToday))
			assert.Len(t, *waits, tc.wantWaits)
			var total time.Duration
			for _, w := range *waits {
				total += w
			}
			assert.LessOrEqual(t, total, 50*time.Second)
		})
	}

	t.Run("f: optimize loader reads once", func(t *testing.T) {
		pvTestObserveLogs(t)
		waits := pvTestNoSleep(t)
		srv := newPVTestHAServer(t)
		srv.script[pvTestToday] = []int{502}
		c, err := newHAAccessClient(srv.srv.URL, pvTestHAToken)
		require.NoError(t, err)

		forecasts, meta, err := newHAForecastLoader(c, pvTestEntities).Load(context.Background(), OptimizeConfig{Location: now.Location()}, now)
		require.NoError(t, err)
		assert.Equal(t, 1, srv.hitCount(pvTestToday))
		assert.Empty(t, *waits)
		require.Len(t, meta.Sources, 1)
		assert.False(t, meta.Sources[0].Missing)
		require.Len(t, forecasts, 1)
		assert.Len(t, forecasts[0], 48, "the tomorrow entity's periods are used")
	})

	t.Run("g: estimate reads once", func(t *testing.T) {
		pvTestObserveLogs(t)
		waits := pvTestNoSleep(t)
		srv := newPVTestHAServer(t)
		srv.script[pvTestToday] = []int{502}
		src, err := newPVDaySource(pvTestHAConfig(srv.srv.URL), solcastDaySource{}, false)
		require.NoError(t, err)
		_, ok, err := src.DayProduction(context.Background(), pw.ForecastHorizonToday, now)
		assert.False(t, ok)
		assert.Error(t, err)
		assert.Equal(t, 1, srv.hitCount(pvTestToday))
		assert.Empty(t, *waits)
	})

	t.Run("requests are GET with the bearer token", func(t *testing.T) {
		pvTestObserveLogs(t)
		srv := newPVTestHAServer(t)
		src, err := newPVDaySource(pvTestHAConfig(srv.srv.URL), solcastDaySource{}, true)
		require.NoError(t, err)
		_, ok, err := src.DayProduction(context.Background(), pw.ForecastHorizonToday, now)
		require.NoError(t, err)
		require.True(t, ok)
		for _, a := range srv.auth {
			assert.Equal(t, "GET Bearer "+pvTestHAToken, a)
		}
		assert.Equal(t, 1, srv.hitCount(pvTestLastPolled))
	})
}

// --- optimize loader -------------------------------------------------------------

func pvTestState(t *testing.T, name string) homeassistant.State {
	t.Helper()
	srv := newPVTestHAServer(t)
	_ = srv // fixtures are decoded through the real client below
	c, err := newHAAccessClient(srv.srv.URL, pvTestHAToken)
	require.NoError(t, err)
	st, err := c.State(context.Background(), name)
	require.NoError(t, err)
	return st
}

func TestHAForecastLoader(t *testing.T) {
	now := pvTestNow(t)
	cfg := OptimizeConfig{Location: now.Location()}
	full := map[string]homeassistant.State{
		pvTestToday:      pvTestState(t, pvTestToday),
		pvTestTomorrow:   pvTestState(t, pvTestTomorrow),
		pvTestLastPolled: pvTestState(t, pvTestLastPolled),
	}
	polled := time.Date(2026, 10, 26, 5, 12, 3, 0, time.UTC)

	t.Run("expected use: one source with both days and the last poll", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		c := &pvTestStateReader{states: full}
		forecasts, meta, err := newHAForecastLoader(c, pvTestEntities).Load(context.Background(), cfg, now)
		require.NoError(t, err)
		require.Len(t, forecasts, 1)
		assert.Len(t, forecasts[0], 96)
		require.Len(t, meta.Sources, 1)
		assert.False(t, meta.AnyMissing())
		assert.False(t, meta.AnyStale())
		assert.True(t, polled.Equal(meta.Sources[0].LastPolled))

		fp := logs.FilterMessage("PV forecast input fingerprint").All()
		require.Len(t, fp, 1, "one fingerprint line per run (AC-13)")
		f := fp[0].ContextMap()
		assert.Equal(t, pw.PVSourceHASolcast, f["pv_source"])
		assert.EqualValues(t, 96, f["periods"])
		assert.EqualValues(t, 38618, f["total_wh"])
		assert.EqualValues(t, 76, f["covered"], "28 half-hours left today + 48 tomorrow")
		assert.EqualValues(t, 76, f["expected"])
	})

	t.Run("edge: both entities fail -> Missing, no error", func(t *testing.T) {
		pvTestObserveLogs(t)
		c := &pvTestStateReader{states: map[string]homeassistant.State{pvTestLastPolled: full[pvTestLastPolled]}, errs: map[string]error{
			pvTestToday:    pvTestStatusErr(404),
			pvTestTomorrow: pvTestStatusErr(502),
		}}
		forecasts, meta, err := newHAForecastLoader(c, pvTestEntities).Load(context.Background(), cfg, now)
		require.NoError(t, err)
		assert.True(t, meta.AnyMissing())
		require.Len(t, forecasts, 1)
		assert.Empty(t, forecasts[0])

		periods, reasons, _ := optLoadPV(context.Background(), newHAForecastLoader(c, pvTestEntities), cfg, now)
		assert.Empty(t, periods)
		assert.Equal(t, []optimize.Reason{optimize.ReasonPVForecastMissing}, reasons)
	})

	t.Run("stale -> optimizer_pv_forecast_stale", func(t *testing.T) {
		pvTestObserveLogs(t)
		c := &pvTestStateReader{states: map[string]homeassistant.State{
			pvTestLastPolled: {State: now.Add(-25 * time.Hour).Format(time.RFC3339)},
		}}
		periods, reasons, lastPolled := optLoadPV(context.Background(), newHAForecastLoader(c, pvTestEntities), cfg, now)
		assert.Empty(t, periods)
		assert.Equal(t, []optimize.Reason{optimize.ReasonPVForecastStale}, reasons)
		assert.False(t, lastPolled.IsZero())
		assert.Equal(t, 1, c.calls, "forecast entities are not read when stale")
	})

	t.Run("full forecast maps to PV periods without a PV reason", func(t *testing.T) {
		pvTestObserveLogs(t)
		periods, reasons, lastPolled := optLoadPV(context.Background(), newHAForecastLoader(&pvTestStateReader{states: full}, pvTestEntities), cfg, now)
		assert.Len(t, periods, 96)
		assert.Empty(t, reasons)
		assert.True(t, polled.Equal(lastPolled))
	})

	t.Run("failure: cancelled context is returned", func(t *testing.T) {
		pvTestObserveLogs(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := newHAForecastLoader(&pvTestStateReader{states: full}, pvTestEntities).Load(ctx, cfg, now)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// newOptimizeDeps picks the HA loader for ha_solcast and shares the HA
// client (S-025 d); solcast_api keeps the cache loader.
func TestNewOptimizeDeps_PVSource(t *testing.T) {
	origHA, origFC := newHAClient, newForecastLoader
	t.Cleanup(func() { newHAClient, newForecastLoader = origHA, origFC })
	ha := &optRunFakeHA{}
	builds := 0
	newHAClient = func(OptimizeConfig) (haClient, error) { builds++; return ha, nil }
	cacheLoader := &optRunFakeForecasts{}
	newForecastLoader = func() forecastLoader { return cacheLoader }

	deps, err := newOptimizeDeps(OptimizeConfig{})
	require.NoError(t, err)
	assert.Same(t, cacheLoader, deps.Forecasts)

	deps, err = newOptimizeDeps(OptimizeConfig{PV: PVSourceConfig{Source: pw.PVSourceHASolcast, Entities: pvTestEntities}})
	require.NoError(t, err)
	hl, ok := deps.Forecasts.(*haForecastLoader)
	require.True(t, ok)
	require.NotNil(t, hl.reader)
	assert.Equal(t, 2, builds, "one HA client per newOptimizeDeps call")
}

// --- wave 3: HTTP end to end and security ------------------------------------

// pvTestSolcastShape converts the detailedForecast of the given fixtures to
// the Solcast API shape (period_end = period_start + 30 min in UTC, PT30M)
// without the HA reader, so the HA path can be compared with the Solcast path.
func pvTestSolcastShape(t *testing.T, names ...string) []pw.Forecast {
	t.Helper()
	var out []pw.Forecast
	for _, n := range names {
		var st struct {
			Attributes struct {
				DetailedForecast []struct {
					PeriodStart string  `json:"period_start"`
					PVEstimate  float64 `json:"pv_estimate"`
				} `json:"detailedForecast"`
			} `json:"attributes"`
		}
		require.NoError(t, json.Unmarshal(pvTestFixture(t, n), &st))
		for _, el := range st.Attributes.DetailedForecast {
			start, err := time.Parse(time.RFC3339, el.PeriodStart)
			require.NoError(t, err)
			out = append(out, pw.Forecast{PVEstimate: el.PVEstimate, PeriodEnd: start.Add(30 * time.Minute).UTC().Format(time.RFC3339), Period: "PT30M"})
		}
	}
	return out
}

// pvTestSolcastServer is a mock Solcast API that counts its requests.
type pvTestSolcastServer struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newPVTestSolcastServer(t *testing.T, forecasts []pw.Forecast) *pvTestSolcastServer {
	t.Helper()
	body, err := json.Marshal(pw.Forecasts{Forecasts: forecasts})
	require.NoError(t, err)
	s := &pvTestSolcastServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// pvTestLogLines renders every captured entry as "message key=value ...".
func pvTestLogLines(logs *observer.ObservedLogs) []string {
	var out []string
	for _, e := range logs.All() {
		var b strings.Builder
		b.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			fmt.Fprintf(&b, " %s=%v", k, v)
		}
		out = append(out, b.String())
	}
	return out
}

// pvTestAssertNotLogged fails when any captured entry contains secret.
func pvTestAssertNotLogged(t *testing.T, logs *observer.ObservedLogs, secret string) {
	t.Helper()
	for _, l := range pvTestLogLines(logs) {
		assert.NotContains(t, l, secret)
	}
}

// T-0311 (AC-2, AC-6): mock HA over HTTP -> real homeassistant.HTTPClient ->
// haEntityReader -> haDaySource.DayProduction and haForecastLoader.Load.
func TestHAForecast_HTTPEndToEnd_T0311(t *testing.T) {
	now := pvTestNow(t)
	wantPaths := []string{"/api/states/" + pvTestLastPolled, "/api/states/" + pvTestToday, "/api/states/" + pvTestTomorrow}
	solcastShape := pvTestSolcastShape(t, "ha_solcast_today.json", "ha_solcast_tomorrow.json")

	for _, horizon := range []string{pw.ForecastHorizonToday, pw.ForecastHorizonTomorrow, pw.ForecastHorizonRemainingToday, pw.ForecastHorizonDefault} {
		t.Run("day total equals the Solcast path: "+horizon, func(t *testing.T) {
			logs := pvTestObserveLogs(t)
			ha := newPVTestHAServer(t)
			src, err := newPVDaySource(pvTestHAConfig(ha.srv.URL), solcastDaySource{}, true)
			require.NoError(t, err)
			_, isHA := src.(haDaySource)
			require.True(t, isHA)

			got, ok, err := src.DayProduction(context.Background(), horizon, now)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Greater(t, got, 0.0)

			// The same half-hours through the unchanged Solcast API path.
			solcast := newPVTestSolcastServer(t, solcastShape)
			want, wantOK, err := (&solcastDaySource{apiKey: "test-api-key", url: solcast.srv.URL}).DayProduction(context.Background(), horizon, now)
			require.NoError(t, err)
			require.True(t, wantOK)
			assert.InDelta(t, want, got, 1e-6)
			assert.EqualValues(t, 1, solcast.hits.Load())

			assert.Equal(t, wantPaths, ha.requestPaths(), "last_polled first, then today and tomorrow")
			for _, a := range ha.authHeaders() {
				assert.Equal(t, "GET Bearer "+pvTestHAToken, a)
			}
			total := logs.FilterMessageSnippet("Total Forecast Solar Power").All()
			require.Len(t, total, 2, "one line from each path")
			assert.Equal(t, total[1].Message, total[0].Message, "same Info line format for both sources")
		})
	}

	t.Run("haForecastLoader.Load returns one source, not missing or stale", func(t *testing.T) {
		pvTestObserveLogs(t)
		ha := newPVTestHAServer(t)
		c, err := newHAAccessClient(ha.srv.URL, pvTestHAToken)
		require.NoError(t, err)
		_, isHTTP := c.(*homeassistant.HTTPClient)
		require.True(t, isHTTP, "the real HA client is used")

		forecasts, meta, err := newHAForecastLoader(c, pvTestEntities).Load(context.Background(), OptimizeConfig{Location: now.Location()}, now)
		require.NoError(t, err)
		require.Len(t, forecasts, 1)
		require.Len(t, meta.Sources, 1)
		assert.False(t, meta.Sources[0].Missing)
		assert.False(t, meta.Sources[0].Stale)
		assert.Equal(t, solcastShape, forecasts[0], "the merged periods equal the Solcast-shaped periods")
		assert.Equal(t, wantPaths, ha.requestPaths())
		for _, a := range ha.authHeaders() {
			assert.Equal(t, "GET Bearer "+pvTestHAToken, a)
		}
	})

	t.Run("failure: wrong token gives no forecast", func(t *testing.T) {
		pvTestObserveLogs(t)
		ha := newPVTestHAServer(t)
		ha.script[pvTestToday] = []int{401}
		ha.script[pvTestTomorrow] = []int{401}
		src, err := newPVDaySource(pvTestHAConfig(ha.srv.URL), solcastDaySource{}, true)
		require.NoError(t, err)
		_, ok, err := src.DayProduction(context.Background(), pw.ForecastHorizonToday, now)
		assert.False(t, ok)
		assert.ErrorIs(t, err, homeassistant.ErrAuth)
	})
}

// T-0313 (AC-5, S-027 d): with pv_source=ha_solcast no Solcast request is
// made and no cache file is written in a classic Tick, estimate and an
// optimize run, even with url, apikey and cache_forecast set; the "ignored"
// Info line names the keys only.
func TestHASolcast_NoSolcastTraffic_T0313(t *testing.T) {
	const apiKey = "solcast-secret-api-key-T0313"
	now := pvTestNow(t)
	solcast := newPVTestSolcastServer(t, pvTestSolcastShape(t, "ha_solcast_today.json", "ha_solcast_tomorrow.json"))
	cacheDir := t.TempDir()
	cachePrefix := filepath.Join(cacheDir, "cached_forecast")
	ha := newPVTestHAServer(t)

	setViper := func(t *testing.T) {
		resetViper(t)
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		viper.Set("url", solcast.srv.URL)
		viper.Set("apikey", apiKey)
		viper.Set("cache_forecast", true)
		viper.Set("cache_file_prefix", cachePrefix)
		viper.Set(keyHAURL, ha.srv.URL)
		viper.Set(keyHAToken, pvTestHAToken)
	}

	t.Run("startup: ignored line names keys only", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		setViper(t)
		cfg, err := loadPVSourceConfigStandalone(func(string) string { return "" })
		require.NoError(t, err)
		logPVSourceStartup(cfg)

		ignored := logs.FilterMessage(msgPVKeysIgnored).All()
		require.Len(t, ignored, 1)
		assert.Equal(t, "url,apikey", ignored[0].ContextMap()["ignored"])
		pvTestAssertNotLogged(t, logs, apiKey)
		pvTestAssertNotLogged(t, logs, solcast.srv.URL)
	})

	t.Run("classic Tick", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		setViper(t)
		cfg, err := loadPVSourceConfigStandalone(func(string) string { return "" })
		require.NoError(t, err)
		src, err := newPVDaySource(cfg, solcastDaySource{apiKey: apiKey, url: solcast.srv.URL, cacheForecast: true, cachePrefix: cachePrefix, cacheTime: 7200}, true)
		require.NoError(t, err)

		oldS, oldF := newStorage, newFronius
		newStorage = func() storageClient {
			return &fakeStorageClient{capacityToCharge: 5000, capacityMax: 10000, socPct: 50}
		}
		tf := &trackingFroniusClient{decision: fronius.DecisionIdle, reason: "fake"}
		newFronius = func(fronius.SunSpecType) froniusClient { return tf }
		t.Cleanup(func() { newStorage, newFronius = oldS, oldF })

		r := newRunnerForTests(newFakeClient())
		r.cfg.APIKey, r.cfg.URL = apiKey, solcast.srv.URL
		r.cfg.CacheForecast, r.cfg.CacheFilePrefix = true, cachePrefix
		r.cfg.ForecastHorizon = pw.ForecastHorizonToday
		r.cfg.PVDay = src
		require.NoError(t, r.Tick(context.Background(), now))

		assert.True(t, tf.lastForecastEnabled)
		assert.InDelta(t, 19309.1, tf.lastForecastWh, 0.5)
		assert.Empty(t, logs.FilterMessageSnippet("Index 0 URL").All(), "the Solcast 'Index %d URL %s' line must not run")
		pvTestAssertNotLogged(t, logs, apiKey)
	})

	t.Run("estimate", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		oldS := newStorage
		newStorage = func() storageClient { return &fakeStorageClient{capacityMax: 10000} }
		t.Cleanup(func() { newStorage = oldS })

		out, err := execEstimateCLI(t, now,
			"--pv_source", pw.PVSourceHASolcast,
			"-u", solcast.srv.URL, "-k", apiKey, "--cache_forecast", "-f", cachePrefix,
			"--ha_url", ha.srv.URL, "--ha_token", pvTestHAToken,
			"-H", "192.0.2.10", "--forecast_horizon", pw.ForecastHorizonToday)
		require.NoError(t, err, out)
		ignored := logs.FilterMessage(msgPVKeysIgnored).All()
		require.Len(t, ignored, 1)
		assert.Equal(t, "url,apikey", ignored[0].ContextMap()["ignored"])
		pvTestAssertNotLogged(t, logs, apiKey)
		assert.NotContains(t, out, apiKey)
	})

	t.Run("runOptimization", func(t *testing.T) {
		pvTestObserveLogs(t)
		setViper(t)
		pvCfg, err := loadPVSourceConfigStandalone(func(string) string { return "" })
		require.NoError(t, err)

		e := newOptRunEnv(t)
		e.now = now
		e.ha.states[optRunPriceEntity] = optRunPriceState(now, e.loc, nil, nil, 1)
		for _, ent := range []string{pvTestToday, pvTestTomorrow, pvTestLastPolled} {
			e.ha.states[ent] = pvTestState(t, ent)
		}
		cfg := e.cfg
		cfg.SolcastURLs = []string{solcast.srv.URL}
		cfg.SolcastAPIKey = apiKey
		cfg.CacheFilePref = cachePrefix
		cfg.PV = pvCfg

		origHA, origBat := newHAClient, newBatteryReader
		newHAClient = func(OptimizeConfig) (haClient, error) { return e.ha, nil }
		newBatteryReader = func() batteryReader { return e.bat }
		t.Cleanup(func() { newHAClient, newBatteryReader = origHA, origBat })

		deps, err := newOptimizeDeps(cfg)
		require.NoError(t, err)
		res := runOptimization(context.Background(), deps, cfg, now, &profileCache{})
		require.Nil(t, res.Fallback)
		assert.False(t, optHasReason(res.Reasons, optimize.ReasonPVForecastMissing))
		assert.False(t, optHasReason(res.Reasons, optimize.ReasonPVForecastStale))
	})

	assert.Zero(t, solcast.hits.Load(), "no Solcast request with pv_source=ha_solcast")
	entries, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "no forecast cache file is written")
}

// pvTestPoisonState is an HA state whose only detailedForecast element has
// the given period_start.
func pvTestPoisonState(t *testing.T, entity, periodStart string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"entity_id": entity,
		"state":     "12.5",
		"attributes": map[string]any{
			"detailedForecast": []any{map[string]any{"period_start": periodStart, "pv_estimate": 1.0}},
		},
	})
	require.NoError(t, err)
	return b
}

// T-0324 (S-025 b/c, S-026 a): tokens never reach the startup dump or the
// logs; a hostile period_start yields a bounded, single-line error.
func TestHASolcast_SecretsAndErrorBounds_T0324(t *testing.T) {
	now := pvTestNow(t)

	t.Run("a: estimate --ha_token is never logged", func(t *testing.T) {
		const token = "secret123"
		logs := pvTestObserveLogs(t)
		ha := newPVTestHAServer(t)
		oldS := newStorage
		newStorage = func() storageClient { return &fakeStorageClient{capacityMax: 10000} }
		t.Cleanup(func() { newStorage = oldS })

		out, err := execEstimateCLI(t, now, "--pv_source", pw.PVSourceHASolcast,
			"--ha_url", ha.srv.URL, "--ha_token", token, "-H", "192.0.2.10", "--forecast_horizon", pw.ForecastHorizonToday)
		require.NoError(t, err, out)
		require.NotEmpty(t, logs.FilterMessageSnippet("effective startup parameters").All(), "the startup dump is captured")
		for _, a := range ha.authHeaders() {
			assert.Equal(t, "GET Bearer "+token, a, "the token is used on the wire")
		}
		pvTestAssertNotLogged(t, logs, token)
		assert.NotContains(t, out, token)
	})

	t.Run("b: SUPERVISOR_TOKEN is never logged", func(t *testing.T) {
		const token = "sup-secret-456"
		t.Setenv("SUPERVISOR_TOKEN", token)
		logs := pvTestObserveLogs(t)
		ha := newPVTestHAServer(t)
		oldS, oldC := newStorage, newHAAccessClient
		newStorage = func() storageClient { return &fakeStorageClient{capacityMax: 10000} }
		var resolved homeassistant.Config
		// Resolve exactly as production does, then point the Supervisor
		// proxy URL at the mock HA.
		newHAAccessClient = func(haURL, haToken string) (haClient, error) {
			access, err := homeassistant.ResolveAccess(haURL, haToken, os.Getenv)
			if err != nil {
				return nil, err
			}
			resolved = access
			access.BaseURL = ha.srv.URL + "/api"
			return homeassistant.New(access)
		}
		t.Cleanup(func() { newStorage, newHAAccessClient = oldS, oldC })

		out, err := execEstimateCLI(t, now, "--pv_source", pw.PVSourceHASolcast, "-H", "192.0.2.10", "--forecast_horizon", pw.ForecastHorizonToday)
		require.NoError(t, err, out)
		assert.Equal(t, homeassistant.SourceSupervisor, resolved.Source)
		startup := logs.FilterMessage(msgPVSourceStartup).All()
		require.Len(t, startup, 1)
		assert.Equal(t, haAccessSupervisor, startup[0].ContextMap()["ha_access"])
		for _, a := range ha.authHeaders() {
			assert.Equal(t, "GET Bearer "+token, a)
		}
		pvTestAssertNotLogged(t, logs, token)
		assert.NotContains(t, out, token)
	})

	// c: a 10 KB period_start containing "\n" in a classic run.
	poison := "2026-10-26T10:00:00+01:00\ninjected log line " + strings.Repeat("A", 10*1024)
	cases := []struct {
		name     string
		poisoned []string
	}{
		{name: "c: today entity poisoned", poisoned: []string{pvTestToday}},
		{name: "c: both entities poisoned", poisoned: []string{pvTestToday, pvTestTomorrow}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := pvTestObserveLogs(t)
			ha := newPVTestHAServer(t)
			for _, ent := range tc.poisoned {
				ha.setBody(ent, pvTestPoisonState(t, ent, poison))
			}
			src, err := newPVDaySource(pvTestHAConfig(ha.srv.URL), solcastDaySource{}, true)
			require.NoError(t, err)

			oldS, oldF, oldP := newStorage, newFronius, newPower
			newStorage = func() storageClient {
				return &fakeStorageClient{capacityToCharge: 5000, capacityMax: 10000, socPct: 50}
			}
			tf := &trackingFroniusClient{decision: fronius.DecisionIdle, reason: "fake"}
			newFronius = func(fronius.SunSpecType) froniusClient { return tf }
			newPower = func() powerClient { t.Fatal("no Solcast call with ha_solcast"); return nil }
			t.Cleanup(func() { newStorage, newFronius, newPower = oldS, oldF, oldP })

			client := newFakeClient()
			r := newRunnerForTests(client)
			r.cfg.PVDay = src
			r.cfg.ForecastHorizon = pw.ForecastHorizonToday
			require.NoError(t, r.Tick(context.Background(), now))
			assert.False(t, tf.lastForecastEnabled)

			errMsg, published := findPublishedBySuffix(drainPublishes(client), "/error")
			require.True(t, published)
			var p mqtt.ErrorPayload
			require.NoError(t, json.Unmarshal(errMsg.payload, &p))
			assert.Equal(t, "power", p.Source)
			assert.LessOrEqual(t, len(p.Error), 256, "published error: %q", p.Error)
			assert.NotContains(t, p.Error, "\n", "published error: %q", p.Error)
			assert.NotContains(t, p.Error, strings.Repeat("A", 65))

			warns := logs.FilterMessageSnippet("power forecast retrieval failed; disabling forecast for this run").All()
			require.Len(t, warns, 1)
			logged := strings.TrimPrefix(warns[0].Message, "power forecast retrieval failed; disabling forecast for this run ")
			assert.LessOrEqual(t, len(logged), 256, "logged error: %q", logged)

			for _, line := range pvTestLogLines(logs) {
				assert.NotContains(t, line, "\n", "log line: %q", line)
				assert.NotContains(t, line, strings.Repeat("A", 65))
				assert.LessOrEqual(t, strings.Count(line, "injected log line"), 1, "at most one quoted value per line")
			}
			drops := logs.FilterMessage("PV forecast periods dropped").All()
			require.Len(t, drops, len(tc.poisoned))
			for _, d := range drops {
				example, _ := d.ContextMap()["example"].(string)
				quoted := strings.TrimSuffix(strings.TrimPrefix(example, "period_start="), "(truncated)")
				raw, err := strconv.Unquote(quoted)
				require.NoError(t, err, "example is one quoted value: %s", example)
				assert.LessOrEqual(t, len(raw), 64)
				assert.True(t, strings.HasSuffix(example, "(truncated)"))
			}
		})
	}
}

// reserve_horizon until_solar sizes the reserve from the periods of the
// configured PV source, so it works with pv_source=ha_solcast too.
func TestTick_UntilSolarReserveUsesPVDaySource(t *testing.T) {
	run := func(t *testing.T, src *fakePVDay) *trackingFroniusClient {
		t.Helper()
		oldStorage, oldPower, oldFronius := newStorage, newPower, newFronius
		t.Cleanup(func() { newStorage, newPower, newFronius = oldStorage, oldPower, oldFronius })
		newStorage = func() storageClient {
			return &fakeStorageClient{capacityToCharge: 3000, capacityMax: 5120, socPct: 41}
		}
		newPower = func() powerClient {
			t.Fatal("the Solcast API handler must not be used when a PV day source is set")
			return nil
		}
		tracker := &trackingFroniusClient{decision: fronius.DecisionIdle, reason: "test"}
		newFronius = func(fronius.SunSpecType) froniusClient { return tracker }

		r := reserveRunner(nightWindow, 1000)
		r.cfg.PVDay = src
		require.NoError(t, r.Tick(t.Context(), localAt("03:00")))
		require.Equal(t, 1, tracker.calls)
		return tracker
	}

	t.Run("periods from the source size the reserve", func(t *testing.T) {
		tracker := run(t, &fakePVDay{wh: 12000, ok: true, sites: morningForecast()})
		assert.InDelta(t, 1580, tracker.lastReserveWh, 0.01)
	})

	t.Run("no periods falls back to pw_batt_reserve", func(t *testing.T) {
		tracker := run(t, &fakePVDay{wh: 12000, ok: true})
		assert.InDelta(t, 1000, tracker.lastReserveWh, 0.01)
	})

	t.Run("failed source ignores stale periods", func(t *testing.T) {
		tracker := run(t, &fakePVDay{err: errors.New("ha down"), sites: morningForecast()})
		assert.InDelta(t, 1000, tracker.lastReserveWh, 0.01, "periods are used only when the forecast was retrieved")
	})
}

// The fingerprint counts the half-hours of the plan horizon only: before
// tomorrow's prices exist (for example the 00:00 run, before the
// integration rolls over) tomorrow is not reported as expected.
func TestHAForecastLoader_FingerprintBoundedByPlanHorizon(t *testing.T) {
	now := pvTestNow(t) // 10:00
	cfg := OptimizeConfig{Location: now.Location()}
	full := map[string]homeassistant.State{
		pvTestToday:      pvTestState(t, pvTestToday),
		pvTestTomorrow:   pvTestState(t, pvTestTomorrow),
		pvTestLastPolled: pvTestState(t, pvTestLastPolled),
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())

	for _, tc := range []struct {
		name              string
		until             time.Time
		covered, expected int
	}{
		{"unbounded: to the end of tomorrow", time.Time{}, 76, 76},
		{"prices end at midnight", midnight, 28, 28},
		{"prices end at 12:00", now.Add(2 * time.Hour), 4, 4},
		{"prices end tomorrow 13:00", midnight.Add(13 * time.Hour), 54, 54},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := pvTestObserveLogs(t)
			l := newHAForecastLoader(&pvTestStateReader{states: full}, pvTestEntities)
			if !tc.until.IsZero() {
				l.boundHorizon(tc.until)
			}
			_, _, err := l.Load(context.Background(), cfg, now)
			require.NoError(t, err)
			fp := logs.FilterMessage("PV forecast input fingerprint").All()
			require.Len(t, fp, 1)
			assert.EqualValues(t, tc.covered, fp[0].ContextMap()["covered"])
			assert.EqualValues(t, tc.expected, fp[0].ContextMap()["expected"])
			assert.EqualValues(t, 96, fp[0].ContextMap()["periods"], "periods stays the raw count")
		})
	}

	t.Run("missing tomorrow inside the horizon still shows", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		states := map[string]homeassistant.State{pvTestToday: full[pvTestToday], pvTestLastPolled: full[pvTestLastPolled]}
		l := newHAForecastLoader(&pvTestStateReader{states: states, errs: map[string]error{pvTestTomorrow: pvTestStatusErr(404)}}, pvTestEntities)
		l.boundHorizon(midnight.Add(13 * time.Hour))
		_, _, err := l.Load(context.Background(), cfg, now)
		require.NoError(t, err)
		fp := logs.FilterMessage("PV forecast input fingerprint").All()
		require.Len(t, fp, 1)
		assert.EqualValues(t, 28, fp[0].ContextMap()["covered"])
		assert.EqualValues(t, 54, fp[0].ContextMap()["expected"])
	})
}
