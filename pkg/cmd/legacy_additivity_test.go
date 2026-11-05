package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"sbam/pkg/fronius"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingFronius records the forecast inputs the legacy decision got.
type recordingFronius struct {
	calls           int
	forecastWh      float64
	forecastEnabled bool
}

func (s *recordingFronius) Handler(_ context.Context, pw_forecast float64, _ float64, _ float64, _ float64, _ float64, _ float64, _ string, _ string, _ string, _ bool, _ float64, _ float64, forecast_charge_enabled bool, _ bool, _ ...string) (int16, fronius.Decision, fronius.Reason, fronius.PowerState, error) {
	s.calls++
	s.forecastWh = pw_forecast
	s.forecastEnabled = forecast_charge_enabled
	return 0, fronius.DecisionIdle, "stub", fronius.PowerState{}, nil
}

// legacyTickWithSolcast runs one legacy (crontab-mode) tick against a Solcast
// stub through the real power handler and returns the recorded decision inputs.
func legacyTickWithSolcast(t *testing.T, status int, body string) *recordingFronius {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(ts.Close)

	oldStorage := newStorage
	newStorage = func() storageClient {
		return &fakeStorageClient{capacityToCharge: 500, capacityMax: 10000, socPct: 40}
	}
	t.Cleanup(func() { newStorage = oldStorage })

	rec := &recordingFronius{}
	oldFronius := newFronius
	newFronius = func(fronius.SunSpecType) froniusClient { return rec }
	t.Cleanup(func() { newFronius = oldFronius })

	runner := newRunnerForTests(newFakeClient())
	runner.cfg.URL = ts.URL
	runner.cfg.ForecastHorizon = "today"

	require.NoError(t, runner.Tick(context.Background(), runner.now()))
	require.Equal(t, 1, rec.calls, "the legacy decision still runs")
	return rec
}

// Expected use: a 200 response feeds the forecast into the legacy decision.
func TestLegacyTick_SolcastOKUsesForecast(t *testing.T) {
	body := `{"forecasts":[{"pv_estimate":2.0,"period_end":"2026-05-10T14:00:00.0000000Z","period":"PT30M"}]}`
	rec := legacyTickWithSolcast(t, http.StatusOK, body)
	assert.True(t, rec.forecastEnabled)
	assert.Greater(t, rec.forecastWh, 0.0)
}

// Intentional change, legacy path: a Solcast 429 or other non-200 response is
// a failed fetch. The power handler logs it and the run continues with
// forecast charging disabled (reserve logic only); an error body is never
// decoded into a zero forecast that would trigger forecast charging.
func TestLegacyTick_SolcastNon200DisablesForecast(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"429 quota exhausted":   {http.StatusTooManyRequests, `{"forecasts":[],"response_status":{"error_code":"TooManyRequests"}}`},
		"500 with JSON body":    {http.StatusInternalServerError, `{"forecasts":[]}`},
		"401 with forecastlike": {http.StatusUnauthorized, `{"forecasts":[{"pv_estimate":0,"period_end":"2026-05-10T14:00:00.0000000Z","period":"PT30M"}]}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := legacyTickWithSolcast(t, tc.status, tc.body)

			assert.False(t, rec.forecastEnabled, "forecast charging disabled for this run")
			assert.Equal(t, 0.0, rec.forecastWh)
		})
	}
}
