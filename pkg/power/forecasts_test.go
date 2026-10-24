package power

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	u "sbam/src/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const testForecastBody = `{"forecasts":[{"period_end":"2026-10-02T12:00:00Z","pv_estimate":1.5,"period":"PT30M"}]}`

// solcastStub returns a server that counts requests and answers with the
// given status and body.
func solcastStub(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	return ts, &hits
}

func writeCache(t *testing.T, name string, pv float64, mtime time.Time) {
	t.Helper()
	f := testForecasts{Forecasts: []testForecast{{PeriodEnd: "2026-10-02T12:00:00Z", PVEstimate: pv}}}
	createTestCacheFile(t, f, name, mtime)
	require.NoError(t, os.Chtimes(name, mtime, mtime))
}

// T-0090: hourly loads with cache_time = 7200 hit Solcast at most 12 times
// in 24 hours (spec §Solcast quota).
func TestLoadForecasts_T0090_HourlyRespectsCacheTime(t *testing.T) {
	ts, hits := solcastStub(t, http.StatusOK, testForecastBody)
	defer ts.Close()

	prefix := filepath.Join(t.TempDir(), "solcast")
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	for i := 0; i < 24; i++ {
		fc, meta, err := LoadForecasts(context.Background(), "test-api-key", []string{ts.URL}, prefix, 7200, 24*time.Hour, now)
		require.NoError(t, err)
		require.Len(t, fc, 1)
		assert.Len(t, fc[0], 1)
		assert.False(t, meta.AnyStale())
		assert.False(t, meta.AnyMissing())
		clock = clock.Add(time.Hour)
	}
	got := atomic.LoadInt32(hits)
	assert.LessOrEqual(t, got, int32(12))
	assert.GreaterOrEqual(t, got, int32(1))
}

// T-0091 (TC-009b, S-009): a 3-day-old cache with a failing download yields
// no forecast and a stale meta, without an error.
func TestLoadForecasts_T0091_StaleCache(t *testing.T) {
	ts, hits := solcastStub(t, http.StatusInternalServerError, `{"error":"boom"}`)
	defer ts.Close()

	prefix := filepath.Join(t.TempDir(), "solcast")
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	writeCache(t, forecastCacheFileName(prefix, 0), 2.0, clock.Add(-72*time.Hour))

	fc, meta, err := LoadForecasts(context.Background(), "test-api-key", []string{ts.URL}, prefix, 3600, 24*time.Hour, func() time.Time { return clock })
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(hits))
	require.Len(t, fc, 1)
	assert.Nil(t, fc[0])
	require.Len(t, meta.Sources, 1)
	assert.True(t, meta.Sources[0].Stale)
	assert.False(t, meta.Sources[0].Missing)
	assert.Equal(t, 72*time.Hour, meta.Sources[0].Age)
	assert.True(t, meta.AnyStale())
}

func TestLoadForecasts_Scenarios(t *testing.T) {
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		status     int
		body       string
		cacheAge   *time.Duration
		wantHits   int32
		wantPV     float64 // 0 means no forecast expected
		wantMeta   ForecastSourceMeta
		wantCached float64 // expected pv in cache file after the call; 0 = no check
	}{
		{
			name: "fresh cache is used without download", status: http.StatusOK, body: testForecastBody,
			cacheAge: durPtr(30 * time.Minute), wantHits: 0, wantPV: 2.0,
			wantMeta: ForecastSourceMeta{Age: 30 * time.Minute},
		},
		{
			name: "expired cache triggers download and rewrite", status: http.StatusOK, body: testForecastBody,
			cacheAge: durPtr(2 * time.Hour), wantHits: 1, wantPV: 1.5,
			wantMeta: ForecastSourceMeta{Downloaded: true}, wantCached: 1.5,
		},
		{
			name: "no cache downloads", status: http.StatusOK, body: testForecastBody,
			wantHits: 1, wantPV: 1.5, wantMeta: ForecastSourceMeta{Downloaded: true}, wantCached: 1.5,
		},
		{
			name: "failed download falls back to cache within max age", status: http.StatusServiceUnavailable, body: "",
			cacheAge: durPtr(3 * time.Hour), wantHits: 1, wantPV: 2.0,
			wantMeta: ForecastSourceMeta{Age: 3 * time.Hour},
		},
		{
			name: "cache exactly at max age is still usable", status: http.StatusServiceUnavailable, body: "",
			cacheAge: durPtr(24 * time.Hour), wantHits: 1, wantPV: 2.0,
			wantMeta: ForecastSourceMeta{Age: 24 * time.Hour},
		},
		{
			name: "failed download without cache is missing", status: http.StatusUnauthorized, body: `{"response_status":{"error_code":"Unauthorized"}}`,
			wantHits: 1, wantMeta: ForecastSourceMeta{Missing: true},
		},
		{
			name: "empty download without cache is missing", status: http.StatusOK, body: `{"forecasts":[]}`,
			wantHits: 1, wantMeta: ForecastSourceMeta{Missing: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts, hits := solcastStub(t, tc.status, tc.body)
			defer ts.Close()

			prefix := filepath.Join(t.TempDir(), "solcast")
			cacheFile := forecastCacheFileName(prefix, 0)
			if tc.cacheAge != nil {
				writeCache(t, cacheFile, 2.0, clock.Add(-*tc.cacheAge))
			}

			fc, meta, err := LoadForecasts(context.Background(), "test-api-key", []string{ts.URL}, prefix, 3600, 24*time.Hour, func() time.Time { return clock })
			require.NoError(t, err)
			assert.Equal(t, tc.wantHits, atomic.LoadInt32(hits))
			require.Len(t, meta.Sources, 1)
			assert.Equal(t, tc.wantMeta, meta.Sources[0])
			if tc.wantPV == 0 {
				assert.Nil(t, fc[0])
			} else {
				require.Len(t, fc[0], 1)
				assert.Equal(t, tc.wantPV, fc[0][0].PVEstimate)
			}
			if tc.wantCached != 0 {
				cached, ok, err := ReadForecastCache(cacheFile)
				require.NoError(t, err)
				require.True(t, ok)
				assert.Equal(t, tc.wantCached, cached.Forecasts[0].PVEstimate)
				info, err := os.Stat(cacheFile)
				require.NoError(t, err)
				assert.True(t, info.ModTime().Equal(clock), "cache mtime should follow the injected clock")
			}
		})
	}
}

func TestLoadForecasts_TwoURLsUseSeparateCacheFiles(t *testing.T) {
	ok, okHits := solcastStub(t, http.StatusOK, testForecastBody)
	defer ok.Close()
	bad, badHits := solcastStub(t, http.StatusInternalServerError, "")
	defer bad.Close()

	prefix := filepath.Join(t.TempDir(), "solcast")
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	fc, meta, err := LoadForecasts(context.Background(), "test-api-key", []string{" " + ok.URL + " ", bad.URL}, prefix, 3600, 24*time.Hour, func() time.Time { return clock })
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(okHits))
	assert.Equal(t, int32(1), atomic.LoadInt32(badHits))
	require.Len(t, fc, 2)
	assert.Len(t, fc[0], 1)
	assert.Nil(t, fc[1])
	assert.True(t, meta.Sources[0].Downloaded)
	assert.True(t, meta.Sources[1].Missing)
	assert.True(t, meta.AnyMissing())
	assert.FileExists(t, forecastCacheFileName(prefix, 0))
	assert.NoFileExists(t, forecastCacheFileName(prefix, 1))
}

func TestLoadForecasts_InvalidArguments(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "solcast")
	tests := []struct {
		name      string
		urls      []string
		cacheTime int32
		maxAge    time.Duration
	}{
		{"no urls", nil, 3600, 24 * time.Hour},
		{"too many urls", []string{"a", "b", "c"}, 3600, 24 * time.Hour},
		{"negative cache time", []string{"a"}, -1, 24 * time.Hour},
		{"zero max age", []string{"a"}, 3600, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := LoadForecasts(context.Background(), "test-api-key", tc.urls, prefix, tc.cacheTime, tc.maxAge, nil)
			assert.Error(t, err)
		})
	}
}

func TestLoadForecasts_CancelledContext(t *testing.T) {
	ts, hits := solcastStub(t, http.StatusOK, testForecastBody)
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := LoadForecasts(ctx, "test-api-key", []string{ts.URL}, filepath.Join(t.TempDir(), "solcast"), 3600, 24*time.Hour, nil)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(0), atomic.LoadInt32(hits))
}

func durPtr(d time.Duration) *time.Duration { return &d }

// observeLogs replaces the package logger for one test.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	old := u.Log
	u.Log = zap.New(core).Sugar()
	t.Cleanup(func() { u.Log = old })
	return logs
}

// T-0220: after a 429 (daily quota exhausted) the gate stops further
// downloads until 00:00 UTC, uses the valid cache without a Warn, and logs
// the exhaustion once at Info. At the reset, downloads are tried again.
func TestLoadForecastsGated_T0220_QuotaGate(t *testing.T) {
	logs := observeLogs(t)
	ts, hits := solcastStub(t, http.StatusTooManyRequests, "")
	defer ts.Close()
	prefix := filepath.Join(t.TempDir(), "solcast")
	writeCache(t, forecastCacheFileName(prefix, 0), 2.0, time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	gate := NewQuotaGate()
	load := func(at time.Time) ([][]Forecast, ForecastMeta) {
		fc, meta, err := LoadForecastsGated(context.Background(), gate, "test-api-key", []string{ts.URL}, prefix, 3600, 24*time.Hour,
			func() time.Time { return at })
		require.NoError(t, err)
		return fc, meta
	}

	fc, meta := load(time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC))
	assert.Equal(t, int32(1), atomic.LoadInt32(hits))
	require.Len(t, fc[0], 1)
	assert.Equal(t, 2.0, fc[0][0].PVEstimate, "the valid cache is used")
	assert.Equal(t, ForecastSourceMeta{Age: 3 * time.Hour}, meta.Sources[0])
	info := logs.FilterMessage("Solcast daily quota exhausted; using the cached forecast until the quota resets").All()
	require.Len(t, info, 1)
	assert.Equal(t, "2026-10-03T00:00:00Z", info[0].ContextMap()["retry_at"])

	// Later the same evening: no request, no Warn.
	fc, _ = load(time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC))
	assert.Equal(t, int32(1), atomic.LoadInt32(hits), "no download while the quota is exhausted")
	assert.Equal(t, 2.0, fc[0][0].PVEstimate)
	assert.Equal(t, 0, logs.FilterLevelExact(zap.WarnLevel).Len(), "an exhausted quota with a valid cache is not a warning")

	// At the reset the download is tried again.
	load(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, int32(2), atomic.LoadInt32(hits))
}

// T-0221: edge and failure cases of the gate. Without a gate (sbam optimize,
// legacy callers) every attempt is made and warned about as before; with the
// quota blocked and no cache, the forecast is Missing and still warned about.
func TestLoadForecastsGated_T0221_NoGateAndMissingCache(t *testing.T) {
	logs := observeLogs(t)
	ts, hits := solcastStub(t, http.StatusTooManyRequests, "")
	defer ts.Close()
	prefix := filepath.Join(t.TempDir(), "solcast")
	writeCache(t, forecastCacheFileName(prefix, 0), 2.0, time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))
	at := func() time.Time { return time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC) }

	for i := 0; i < 2; i++ {
		_, _, err := LoadForecasts(context.Background(), "test-api-key", []string{ts.URL}, prefix, 3600, 24*time.Hour, at)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), atomic.LoadInt32(hits), "no gate: every expired cache is downloaded")
	assert.Equal(t, 2, logs.FilterMessage("forecast download failed, using cache").Len())

	gate := NewQuotaGate()
	empty := filepath.Join(t.TempDir(), "none")
	for i := 0; i < 2; i++ {
		_, meta, err := LoadForecastsGated(context.Background(), gate, "test-api-key", []string{ts.URL}, empty, 3600, 24*time.Hour, at)
		require.NoError(t, err)
		assert.Equal(t, ForecastSourceMeta{Missing: true}, meta.Sources[0])
	}
	assert.Equal(t, int32(3), atomic.LoadInt32(hits), "blocked after the first 429")
	assert.Equal(t, 2, logs.FilterMessage("forecast download failed and no cache is available").Len(), "a missing forecast is always a warning")
}

func TestNextQuotaReset(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	assert.Equal(t, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), NextQuotaReset(time.Date(2026, 10, 2, 23, 59, 0, 0, time.UTC)))
	assert.Equal(t, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), NextQuotaReset(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)), "exactly at the reset: the next one")
	assert.Equal(t, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), NextQuotaReset(time.Date(2026, 10, 2, 19, 0, 0, 0, cest)), "19:00 CEST is 17:00 UTC")
	assert.Equal(t, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), NextQuotaReset(time.Date(2026, 10, 3, 1, 30, 0, 0, cest)), "01:30 CEST is still before the reset")
}
