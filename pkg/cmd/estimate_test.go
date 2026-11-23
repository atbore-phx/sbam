package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pw "sbam/pkg/power"
	"sbam/pkg/storage"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func withEstimateCacheTime(t *testing.T, cacheTime int32) {
	t.Helper()
	oldCacheTime := e_cache_time
	e_cache_time = cacheTime
	t.Cleanup(func() {
		e_cache_time = oldCacheTime
	})
}

func newEstimateTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	now := time.Now().UTC()
	pe0 := now.Format(time.RFC3339)
	pe1 := now.Add(30 * time.Minute).Format(time.RFC3339)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == storage.ReqURL {
			_, _ = fmt.Fprint(w, `{"Body":{"Data":{"0":{"Controller":{"Enable":1,"DesignedCapacity":10000,"StateOfCharge_Relative":50}}}},"Head":{"Status":{"Code":0,"Reason":"","UserMessage":""},"Timestamp":""}}`)
			return
		}

		_, _ = fmt.Fprintf(w, `{"forecasts":[{"period_end":"%s","pv_estimate":100},{"period_end":"%s","pv_estimate":150}]}`,
			pe0,
			pe1,
		)
	}))

	t.Cleanup(ts.Close)
	return ts
}

func TestCheckEstimate(t *testing.T) {
	tests := []struct {
		name      string
		apiKey    string
		url       string
		froniusIP string
		cacheTime int32
		wantErr   string
	}{
		{
			name:      "missing fronius ip",
			apiKey:    "api-key",
			url:       "https://example.test/forecast",
			froniusIP: "",
			cacheTime: 0,
			wantErr:   "--fronius_ip",
		},
		{
			name:      "missing api key",
			apiKey:    " ",
			url:       "https://example.test/forecast",
			froniusIP: "127.0.0.1",
			cacheTime: 0,
			wantErr:   "--apikey",
		},
		{
			name:      "missing url",
			apiKey:    "api-key",
			url:       "",
			froniusIP: "127.0.0.1",
			cacheTime: 0,
			wantErr:   "--url",
		},
		{
			name:      "cache time too low",
			apiKey:    "api-key",
			url:       "https://example.test/forecast",
			froniusIP: "127.0.0.1",
			cacheTime: -1,
			wantErr:   "cache_time",
		},
		{
			name:      "cache time too high",
			apiKey:    "api-key",
			url:       "https://example.test/forecast",
			froniusIP: "127.0.0.1",
			cacheTime: 86401,
			wantErr:   "cache_time",
		},
		{
			name:      "valid arguments",
			apiKey:    "api-key",
			url:       "https://example.test/forecast",
			froniusIP: "127.0.0.1",
			cacheTime: 86400,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			withEstimateCacheTime(t, tc.cacheTime)

			err := CheckEstimate(tc.apiKey, tc.url, tc.froniusIP, defPVSource)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// estimateSolcastSource is the solcast_api day source estimate builds from
// its flags.
func estimateSolcastSource(apiKey, url string) pvDaySource {
	return &solcastDaySource{apiKey: apiKey, url: url, cacheTime: 0}
}

func TestEstimate_ReturnsErrorWhenPowerHandlerReturnsError(t *testing.T) {
	assert.NotPanics(t, func() {
		err := estimate(context.Background(), estimateSolcastSource("api-key", "https://a.test,https://b.test,https://c.test"), false, "127.0.0.1", "default", time.Now())
		assert.Error(t, err)
	})
}

func TestEstimate_ReturnsErrorWhenStorageHandlerReturnsError(t *testing.T) {
	ts := newEstimateTestServer(t)

	assert.NotPanics(t, func() {
		err := estimate(context.Background(), estimateSolcastSource("api-key", ts.URL), false, "bad host", "default", time.Now())
		assert.Error(t, err)
	})
}

func TestEstimate_SucceedsWithValidForecastAndStorage(t *testing.T) {
	ts := newEstimateTestServer(t)
	froniusIP := strings.TrimPrefix(ts.URL, "http://")

	err := estimate(context.Background(), estimateSolcastSource("api-key", ts.URL), false, froniusIP, "default", time.Now())
	assert.NoError(t, err)
}

// estimate with the HA source: a failed or stale forecast is an error
// (exit 1, no panic); a forecast continues to the battery read.
func TestEstimate_HASource(t *testing.T) {
	oldS := newStorage
	t.Cleanup(func() { newStorage = oldS })

	t.Run("expected use", func(t *testing.T) {
		st := &fakeStorageClient{capacityMax: 10000}
		newStorage = func() storageClient { return st }
		src := &fakePVDay{wh: 18400, ok: true}
		require.NoError(t, estimate(context.Background(), src, true, "127.0.0.1", "today", time.Now()))
		assert.Equal(t, "today", src.horizon)
		assert.Equal(t, 1, st.calls)
	})

	t.Run("edge: horizon off is not an error", func(t *testing.T) {
		newStorage = func() storageClient { return &fakeStorageClient{} }
		require.NoError(t, estimate(context.Background(), &fakePVDay{}, true, "127.0.0.1", "off", time.Now()))
	})

	t.Run("failure: stale forecast", func(t *testing.T) {
		st := &fakeStorageClient{}
		newStorage = func() storageClient { return st }
		src := &fakePVDay{err: fmt.Errorf("pv forecast: %w", pw.ErrHAForecastStale)}
		var err error
		assert.NotPanics(t, func() { err = estimate(context.Background(), src, true, "127.0.0.1", "today", time.Now()) })
		require.Error(t, err)
		assert.ErrorIs(t, err, pw.ErrHAForecastStale)
		assert.Contains(t, err.Error(), "PV forecast from Home Assistant failed")
		assert.Zero(t, st.calls)
	})
}

// execEstimateCLI runs `sbam estimate <args>` in-process through rootCmd with
// estimateNow fixed to now. It returns the cobra output and the RunE error
// (a non-nil error is exit code 1).
func execEstimateCLI(t *testing.T, now time.Time, args ...string) (string, error) {
	t.Helper()
	resetViper(t)
	resetFlags := func() {
		estCmd.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	}
	oldNow := estimateNow
	estimateNow = func() time.Time { return now }
	t.Cleanup(func() {
		estimateNow = oldNow
		resetFlags()
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	resetFlags()
	var out bytes.Buffer
	rootCmd.SetArgs(append([]string{"estimate"}, args...))
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	err := rootCmd.Execute()
	return out.String(), err
}

// newPVTestDeadHA is an HA endpoint that drops every connection (transport
// error, as for an HA Core restart) without real network access.
func newPVTestDeadHA(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// T-0317 (AC-3, AC-7, AC-8, S-026 b): `sbam estimate --pv_source ha_solcast`
// through the cobra command against a mock HA and the Fronius storage mock.
func TestEstimate_HASolcast_T0317(t *testing.T) {
	now := pvTestNow(t)
	storageSrv := newEstimateTestServer(t)
	froniusIP := strings.TrimPrefix(storageSrv.URL, "http://")
	args := func(haURL string) []string {
		return []string{"--pv_source", pw.PVSourceHASolcast, "--ha_url", haURL, "--ha_token", pvTestHAToken,
			"-H", froniusIP, "--forecast_horizon", pw.ForecastHorizonToday}
	}

	t.Run("a: forecast OK exits 0 and logs the day total", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		ha := newPVTestHAServer(t)
		out, err := execEstimateCLI(t, now, args(ha.srv.URL)...)
		require.NoError(t, err, out)
		total := logs.FilterMessageSnippet("Total Forecast Solar Power").All()
		require.Len(t, total, 1)
		assert.Equal(t, "Total Forecast Solar Power for 26/10/2026: 19309 Wh", total[0].Message)
		assert.Equal(t, 1, ha.hitCount(pvTestToday), "estimate reads once")
	})

	t.Run("b: HA unreachable exits 1 without panic", func(t *testing.T) {
		pvTestObserveLogs(t)
		var err error
		assert.NotPanics(t, func() { _, err = execEstimateCLI(t, now, args(newPVTestDeadHA(t))...) })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PV forecast from Home Assistant failed")
	})

	t.Run("c: stale forecast exits 1 without panic", func(t *testing.T) {
		pvTestObserveLogs(t)
		ha := newPVTestHAServer(t)
		ha.setBody(pvTestLastPolled, []byte(`{"entity_id":"`+pvTestLastPolled+`","state":"`+now.Add(-25*time.Hour).UTC().Format(time.RFC3339)+`","attributes":{}}`))
		var err error
		assert.NotPanics(t, func() { _, err = execEstimateCLI(t, now, args(ha.srv.URL)...) })
		require.Error(t, err)
		assert.ErrorIs(t, err, pw.ErrHAForecastStale)
		assert.Zero(t, ha.hitCount(pvTestToday), "forecast entities are not read when stale")
	})

	t.Run("d: only tomorrow fails, today resolved: exit 0 with a Warn", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		ha := newPVTestHAServer(t)
		ha.script[pvTestTomorrow] = []int{http.StatusBadGateway}
		out, err := execEstimateCLI(t, now, args(ha.srv.URL)...)
		require.NoError(t, err, out)
		var warned bool
		for _, w := range logs.FilterLevelExact(zapcore.WarnLevel).All() {
			if w.ContextMap()["key"] == "pv_entity_tomorrow" {
				warned = true
				assert.Equal(t, pvTestTomorrow, w.ContextMap()["entity"])
			}
		}
		assert.True(t, warned, "a Warn names pv_entity_tomorrow")
		assert.Equal(t, 1, ha.hitCount(pvTestTomorrow), "no retry in estimate")
		require.Len(t, logs.FilterMessageSnippet("Total Forecast Solar Power").All(), 1)
	})
}
