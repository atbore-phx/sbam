package power_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sbam/pkg/power"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
	forecasts, err := power.GetForecast("apiKey", ts.URL)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(forecasts.Forecasts))
	assert.Equal(t, 100.0, forecasts.Forecasts[0].PVEstimate)
	assert.Equal(t, 150.0, forecasts.Forecasts[1].PVEstimate)
}

func TestGetForecastError1(t *testing.T) {

	_, err := power.GetForecast("apiKey", "url")
	assert.Error(t, err)
}

func TestGetForecastError2(t *testing.T) {
	// Create a mock HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "")
	}))
	defer ts.Close()
	_, err := power.GetForecast("apiKey", ts.URL)
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

	_, err := power.GetForecast("apiKey", ts.URL)
	assert.Error(t, err)
}

func TestGetForecastError3(t *testing.T) {
	_, err := power.GetForecast("apiKey", "http://|")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "|")
}

func TestGetTotalDayPowerEstimate(t *testing.T) {
	forecasts := power.Forecasts{
		Forecasts: []power.Forecast{
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
	totalPower, err := power.GetTotalDayPowerEstimate(forecasts, day)
	assert.NoError(t, err)
	assert.Equal(t, 125000.0, totalPower)
}

func TestErrorGetTotalDayPowerEstimate(t *testing.T) {
	forecasts := power.Forecasts{
		Forecasts: []power.Forecast{
			{
				PeriodEnd:  "InvalidTime",
				PVEstimate: 100,
			},
		},
	}

	day, _ := time.Parse("2006-01-02", "2023-06-29")
	_, err := power.GetTotalDayPowerEstimate(forecasts, day)
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
	power := power.New()

	// Call the Handler function with the mock HTTP server's URL
	production, err := power.Handler("apiKey", ts.URL+", "+ts.URL)
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
	power := power.New()

	// Call the Handler function with the mock HTTP server's URL
	_, err := power.Handler("apiKey", ts.URL)
	assert.Error(t, err)
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
	power := power.New()

	// Call the Handler function with the mock HTTP server's URL
	_, err := power.Handler("apiKey", ts.URL)
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
	power := power.New()

	// Call the Handler function with the mock HTTP server's URL
	_, err := power.Handler("apiKey", ts.URL+", "+ts.URL+", ")
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
			actualTime := power.CheckSun(test.time)
			assert.Equal(t, test.expectedTime, actualTime)
		})
	}
}
