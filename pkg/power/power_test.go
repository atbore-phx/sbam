package power_test

import (
	"fmt"
	"ha-fronius-bm/pkg/power"
	"net/http"
	"net/http/httptest"
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
	//tomorrow = tomorrow.AddDate(0, 0, 0)
	totalPower, err := power.GetTotalDayPowerEstimate(forecasts, day)
	assert.NoError(t, err)
	assert.Equal(t, 125000.0, totalPower)
}

func TestHandler(t *testing.T) {
	now := time.Now()
	pe := now.AddDate(0, 0, 1).Format(time.RFC3339)
	pe30 := now.AddDate(0, 0, 1).Add(time.Minute * 30).Format(time.RFC3339)
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
				}
			]
		}`)
	}))
	defer ts.Close()

	// Create a new Power object
	power := power.New()

	// Call the Handler function with the mock HTTP server's URL
	production, err := power.Handler("apiKey", ts.URL)
	assert.NoError(t, err)
	assert.Equal(t, 125000.0, production)
}
