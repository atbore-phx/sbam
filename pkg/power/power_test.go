package power

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Lightweight forecast types used by cache-related tests where only
// PeriodEnd and PVEstimate matter. The package-level Forecast / Forecasts
// types carry extra fields (PVEstimate10, PVEstimate90, Period) that are
// noise for these tests.
type testForecast struct {
	PeriodEnd  string  `json:"period_end"`
	PVEstimate float64 `json:"pv_estimate"`
}

type testForecasts struct {
	Forecasts []testForecast `json:"forecasts"`
}

func createTestCacheFile(t *testing.T, forecasts testForecasts, filename string, modTime time.Time) {
	data, err := json.MarshalIndent(forecasts, "", "  ")
	assert.NoError(t, err)
	err = os.WriteFile(filename, data, 0644)
	assert.NoError(t, err)
	// Set mod time
	os.Chtimes(filename, modTime, modTime)
}

func TestGetForecast(t *testing.T) {
	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{
					"period_end": "2023-06-29T00:00:00Z",
					"pv_estimate": 100
				},
				{
					"period_end": "2023-06-29T00:30:00Z",
					"pv_estimate": 150
				}
			]
		}`)
	}))
	defer ts.Close()

	// Call the getForecast function with the mock HTTP server's URL
	forecasts, err := GetForecast("apiKey", ts.URL)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(forecasts.Forecasts))
	assert.Equal(t, 100.0, forecasts.Forecasts[0].PVEstimate)
	assert.Equal(t, 150.0, forecasts.Forecasts[1].PVEstimate)
}

func TestGetForecastError1(t *testing.T) {

	_, err := GetForecast("apiKey", "url")
	assert.Error(t, err)
}

func TestGetForecastError2(t *testing.T) {
	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "")
	}))
	defer ts.Close()
	_, err := GetForecast("apiKey", ts.URL)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "EOF")
}

func TestGetForecastError429(t *testing.T) {
	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests) // Set status code to 429
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "")
	}))
	defer ts.Close()

	_, err := GetForecast("apiKey", ts.URL)
	assert.Error(t, err)
}

func TestGetForecastError3(t *testing.T) {
	_, err := GetForecast("apiKey", "http://|")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "|")
}

func TestGetDayPowerEstimate_LegacyWrapper(t *testing.T) {
	forecasts := Forecasts{
		Forecasts: []Forecast{
			{
				PeriodEnd:  "2023-06-29T00:00:00Z",
				PVEstimate: 100,
			},
			{
				PeriodEnd:  "2023-06-29T00:30:00Z",
				PVEstimate: 150,
			},
			{
				PeriodEnd:  "2023-06-30T00:00:00Z",
				PVEstimate: 200,
			},
		},
	}

	day, _ := time.Parse("2006-01-02", "2023-06-29")
	totalPower, err := GetDayPowerEstimate(forecasts, day, nil)
	assert.NoError(t, err)
	assert.Equal(t, 125000.0, totalPower)
}

func TestErrorGetDayPowerEstimate(t *testing.T) {
	forecasts := Forecasts{
		Forecasts: []Forecast{
			{
				PeriodEnd:  "InvalidTime",
				PVEstimate: 100,
			},
		},
	}

	day, _ := time.Parse("2006-01-02", "2023-06-29")
	_, err := GetDayPowerEstimate(forecasts, day, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing time \"InvalidTime\"")

}

func TestHandler(t *testing.T) {
	now := time.Now()
	tomorrow := now.AddDate(0, 0, 1)
	pe := now.Format(time.RFC3339)
	pe30 := now.Add(time.Minute * 30).Format(time.RFC3339)
	pet := tomorrow.Format(time.RFC3339)
	pet30 := tomorrow.Add(time.Minute * 30).Format(time.RFC3339)

	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{
					"period_end": "`+pe+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pe30+`",
					"pv_estimate": 150
				},
				{
					"period_end": "`+pet+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pet30+`",
					"pv_estimate": 150
				}
			]
		}`)
	}))
	defer ts.Close()

	// Create a new Power object
	p := New()

	// Call the Handler function with the mock HTTP server's URL
	production, _, err := p.Handler("apiKey", ts.URL+", "+ts.URL, false, "cached_forecast", 7200, ForecastHorizonDefault, now)
	assert.NoError(t, err)
	assert.Equal(t, 250000.0, production)
}

func TestHandlerCache(t *testing.T) {
	now := time.Now()
	tomorrow := now.AddDate(0, 0, 1)
	pe := now.Format(time.RFC3339)
	pe30 := now.Add(time.Minute * 30).Format(time.RFC3339)
	pet := tomorrow.Format(time.RFC3339)
	pet30 := tomorrow.Add(time.Minute * 30).Format(time.RFC3339)

	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{
					"period_end": "`+pe+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pe30+`",
					"pv_estimate": 150
				},
				{
					"period_end": "`+pet+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pet30+`",
					"pv_estimate": 150
				}
			]
		}`)
	}))
	defer ts.Close()

	// Create a new Power object
	p := New()

	// Call the Handler function with the mock HTTP server's URL
	production, _, err := p.Handler("apiKey", ts.URL+", "+ts.URL, true, "gotest_cached_forecast.json", 7200, ForecastHorizonDefault, now)
	assert.NoError(t, err)
	assert.Equal(t, 250000.0, production)
}

func TestHandlerError(t *testing.T) {
	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{
					"period_end": "2023-06-29T00:00:00Z",
					"pv_estimate": 100
				},
				{
					"period_end": "2023-06-29T00:30:00Z",
					"pv_estimate": 150
				}
			]
		}`)
	}))
	ts.Close()

	// Create a new Power object
	p := New()

	// Call the Handler function with the mock HTTP server's URL
	_, test_forecast_retrieved, err := p.Handler("apiKey", ts.URL+", "+ts.URL, false, "cached_forecast", 7200, ForecastHorizonDefault, time.Now())
	assert.NoError(t, err)
	assert.Equal(t, test_forecast_retrieved, false)
}

func TestHandlerError2(t *testing.T) {
	now := time.Now()
	tomorrow := now.AddDate(0, 0, 1)
	pe := now.Format(time.ANSIC)
	pe30 := now.Add(time.Minute * 30).Format(time.ANSIC)
	pet := tomorrow.Format(time.ANSIC)
	pet30 := tomorrow.Add(time.Minute * 30).Format(time.ANSIC)

	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{
					"period_end": "`+pe+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pe30+`",
					"pv_estimate": 150
				},
				{
					"period_end": "`+pet+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pet30+`",
					"pv_estimate": 150
				}
			]
		}`)
	}))
	defer ts.Close()

	// Create a new Power object
	p := New()

	// Call the Handler function with the mock HTTP server's URL
	_, _, err := p.Handler("apiKey", ts.URL+", "+ts.URL, false, "cached_forecast", 7200, ForecastHorizonDefault, time.Now())
	assert.Error(t, err)

}

func TestHandlerError3(t *testing.T) {
	now := time.Now()
	tomorrow := now.AddDate(0, 0, 1)
	pe := now.Format(time.RFC3339)
	pe30 := now.Add(time.Minute * 30).Format(time.RFC3339)
	pet := tomorrow.Format(time.RFC3339)
	pet30 := tomorrow.Add(time.Minute * 30).Format(time.RFC3339)

	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{
					"period_end": "`+pe+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pe30+`",
					"pv_estimate": 150
				},
				{
					"period_end": "`+pet+`",
					"pv_estimate": 100
				},
				{
					"period_end": "`+pet30+`",
					"pv_estimate": 150
				}
			]
		}`)
	}))
	defer ts.Close()

	// Create a new Power object
	p := New()

	// Call the Handler function with the mock HTTP server's URL
	_, _, err := p.Handler("apiKey", ts.URL+", "+ts.URL+", ", false, "cached_forecast", 7200, ForecastHorizonDefault, time.Now())
	assert.Error(t, err)
}

func TestCheckSun(t *testing.T) {
	tests := []struct {
		name         string
		time         time.Time
		expectedTime time.Time
	}{
		{
			name:         "Check time before noon",
			time:         time.Date(2022, 1, 1, 10, 0, 0, 0, time.UTC),
			expectedTime: time.Date(2022, 1, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			name:         "Check time after noon",
			time:         time.Date(2022, 1, 1, 14, 0, 0, 0, time.UTC),
			expectedTime: time.Date(2022, 1, 2, 14, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualTime := checkSun(test.time)
			assert.Equal(t, test.expectedTime, actualTime)
		})
	}
}

func TestReadForecastCache_ValidFile(t *testing.T) {
	filename := "gotest_cache_valid.json"
	defer os.Remove(filename)
	f := testForecasts{Forecasts: []testForecast{{PeriodEnd: time.Now().Format(time.RFC3339), PVEstimate: 1.23}}}
	createTestCacheFile(t, f, filename, time.Now())
	result, hit, err := ReadForecastCache(filename)
	assert.True(t, hit)
	assert.NoError(t, err)
	assert.Equal(t, f.Forecasts[0].PVEstimate, result.Forecasts[0].PVEstimate)
}

func TestReadForecastCache_FileNotExist(t *testing.T) {
	filename := "gotest_cache_not_exist.json"
	os.Remove(filename)
	_, hit, err := ReadForecastCache(filename)
	assert.False(t, hit)
	assert.Error(t, err)
}

func TestReadForecastCache_InvalidJSON(t *testing.T) {
	filename := "gotest_cache_invalid.json"
	defer os.Remove(filename)
	_ = os.WriteFile(filename, []byte("not json"), 0644)
	_, hit, err := ReadForecastCache(filename)
	assert.False(t, hit)
	assert.Error(t, err)
}

func TestGetForecastChache_UsesCache(t *testing.T) {
	filename := "gotest_cache_recent.json"
	defer os.Remove(filename)
	f := testForecasts{Forecasts: []testForecast{{PeriodEnd: time.Now().Format(time.RFC3339), PVEstimate: 2.34}}}
	createTestCacheFile(t, f, filename, time.Now())
	// cache_time = 3600 seconds (1 hour)
	result, err := GetForecastChache("", "", filename, 3600)
	assert.NoError(t, err)
	assert.Equal(t, f.Forecasts[0].PVEstimate, result.Forecasts[0].PVEstimate)
}

func TestGetForecastChache_ExpiredCache1(t *testing.T) {
	filename := "gotest_cache_expired.json"
	defer os.Remove(filename)
	oldTime := time.Now().Add(-2 * time.Hour)
	f := testForecasts{Forecasts: []testForecast{{PeriodEnd: oldTime.Format(time.RFC3339), PVEstimate: 3.45}}}
	createTestCacheFile(t, f, filename, oldTime)
	// cache_time = 3600 seconds (1 hour), so cache is expired
	// GetForecast will fail, so should fallback to cache
	result, err := GetForecastChache("", "", filename, 3600)
	assert.NoError(t, err)
	assert.Equal(t, f.Forecasts[0].PVEstimate, result.Forecasts[0].PVEstimate)
}

func TestGetForecastChache_ExpiredCache2(t *testing.T) {
	filename := "gotest_cache_expired.json"
	defer os.Remove(filename)
	oldTime := time.Now().Add(-2 * time.Hour)
	f := testForecasts{Forecasts: []testForecast{{PeriodEnd: oldTime.Format(time.RFC3339), PVEstimate: 3.45}}}
	createTestCacheFile(t, f, filename, oldTime)
	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{
					"period_end": "`+oldTime.Format(time.RFC3339)+`",
					"pv_estimate": 3.45
				}
			]
		}`)
	}))
	defer ts.Close()
	// cache_time = 3600 seconds (1 hour), so cache is expired
	// fallback to cache url method, GetForecast will not fail and update the cache file with the new value.
	result, err := GetForecastChache("apikey", ts.URL, filename, 3600)
	assert.NoError(t, err)
	assert.Equal(t, f.Forecasts[0].PVEstimate, result.Forecasts[0].PVEstimate)
}

func TestGetForecastChache_NoCacheFile(t *testing.T) {
	filename := "gotest_cache_missing.json"
	os.Remove(filename)
	// Cache file is missing
	// GetForecast will fail, so should return error
	_, err := GetForecastChache("", "", filename, 3600)
	assert.Error(t, err)
}

func TestGetDayPowerEstimate_AllPeriods(t *testing.T) {
	forecasts := Forecasts{
		Forecasts: []Forecast{
			{PeriodEnd: "2023-06-29T00:00:00Z", PVEstimate: 100},
			{PeriodEnd: "2023-06-29T00:30:00Z", PVEstimate: 150},
			{PeriodEnd: "2023-06-30T00:00:00Z", PVEstimate: 200},
		},
	}

	day, _ := time.Parse("2006-01-02", "2023-06-29")
	day = day.In(time.UTC)
	totalPower, err := GetDayPowerEstimate(forecasts, day, nil)
	assert.NoError(t, err)
	// (100 + 150) * 0.5 * 1000 = 125000
	assert.Equal(t, 125000.0, totalPower)
}

func TestGetDayPowerEstimate_WithAfter(t *testing.T) {
	forecasts := Forecasts{
		Forecasts: []Forecast{
			{PeriodEnd: "2023-06-29T00:00:00Z", PVEstimate: 100},
			{PeriodEnd: "2023-06-29T00:30:00Z", PVEstimate: 150},
			{PeriodEnd: "2023-06-29T01:00:00Z", PVEstimate: 200},
		},
	}

	day, _ := time.Parse("2006-01-02", "2023-06-29")
	day = day.In(time.UTC)
	// Exclude the first period (00:00 UTC).
	after := time.Date(2023, 6, 29, 0, 15, 0, 0, time.UTC)
	totalPower, err := GetDayPowerEstimate(forecasts, day, &after)
	assert.NoError(t, err)
	// (150 + 200) * 0.5 * 1000 = 175000
	assert.Equal(t, 175000.0, totalPower)
}

func TestGetDayPowerEstimate_AfterAtBoundary(t *testing.T) {
	forecasts := Forecasts{
		Forecasts: []Forecast{
			{PeriodEnd: "2023-06-29T00:30:00Z", PVEstimate: 150},
		},
	}

	day, _ := time.Parse("2006-01-02", "2023-06-29")
	day = day.In(time.UTC)
	// after exactly at period_end → excluded (must be strictly after).
	after := time.Date(2023, 6, 29, 0, 30, 0, 0, time.UTC)
	totalPower, err := GetDayPowerEstimate(forecasts, day, &after)
	assert.NoError(t, err)
	assert.Equal(t, 0.0, totalPower)
}

func TestGetDayPowerEstimate_InvalidPeriodEnd(t *testing.T) {
	forecasts := Forecasts{
		Forecasts: []Forecast{
			{PeriodEnd: "InvalidTime", PVEstimate: 100},
		},
	}

	day, _ := time.Parse("2006-01-02", "2023-06-29")
	_, err := GetDayPowerEstimate(forecasts, day, nil)
	assert.Error(t, err)
}

func TestHandler_ForecastOff(t *testing.T) {
	// When forecast_horizon=off, Handler should return (0, false, nil)
	// without making any HTTP request.
	p := New()
	production, retrieved, err := p.Handler("", "", false, "", 0, ForecastHorizonOff, time.Now())
	assert.NoError(t, err)
	assert.Equal(t, 0.0, production)
	assert.False(t, retrieved)
}

func TestHandler_RemainingToday(t *testing.T) {
	loc := time.UTC
	now := time.Date(2023, 6, 29, 14, 0, 0, 0, loc)

	// Mock server returns forecasts for the whole day.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"forecasts": [
				{"period_end": "2023-06-29T10:00:00Z", "pv_estimate": 100},
				{"period_end": "2023-06-29T10:30:00Z", "pv_estimate": 100},
				{"period_end": "2023-06-29T15:00:00Z", "pv_estimate": 200},
				{"period_end": "2023-06-29T15:30:00Z", "pv_estimate": 200}
			]
		}`)
	}))
	defer ts.Close()

	p := New()
	// remaining_today with now=14:00 UTC should only include periods after 14:00.
	production, retrieved, err := p.Handler("apiKey", ts.URL, false, "", 0, ForecastHorizonRemainingToday, now)
	assert.NoError(t, err)
	assert.True(t, retrieved)
	// Only 15:00 and 15:30 periods: (200 + 200) * 0.5 * 1000 = 200000
	assert.Equal(t, 200000.0, production)
}
