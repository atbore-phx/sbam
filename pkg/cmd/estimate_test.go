package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sbam/pkg/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			err := CheckEstimate(tc.apiKey, tc.url, tc.froniusIP)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestEstimate_PanicsWhenPowerHandlerReturnsError(t *testing.T) {
	assert.Panics(t, func() {
		estimate("api-key", "https://a.test,https://b.test,https://c.test", "127.0.0.1", false, "", 0, "default")
	})
}

func TestEstimate_PanicsWhenStorageHandlerReturnsError(t *testing.T) {
	ts := newEstimateTestServer(t)

	assert.Panics(t, func() {
		estimate("api-key", ts.URL, "bad host", false, "", 0, "default")
	})
}

func TestEstimate_SucceedsWithValidForecastAndStorage(t *testing.T) {
	ts := newEstimateTestServer(t)
	froniusIP := strings.TrimPrefix(ts.URL, "http://")

	assert.NotPanics(t, func() {
		estimate("api-key", ts.URL, froniusIP, false, "", 0, "default")
	})
}
