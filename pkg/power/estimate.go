package power

import (
	"encoding/json"
	"errors"
	"net/http"
	u "sbam/src/utils"
	"time"
)

func GetForecast(apiKey string, url string) (Forecasts, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return Forecasts{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Forecasts{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		return Forecasts{}, errors.New("you have exceeded your free daily limit, too many Request to the forecast API")
  }

	var forecasts Forecasts
	err = json.NewDecoder(resp.Body).Decode(&forecasts)
	if err != nil {
		return Forecasts{}, err
	}
	return forecasts, nil
}

func GetTotalDayPowerEstimate(forecasts Forecasts, day time.Time) (float64, error) {
	totalPower := 0.0
	for _, forecast := range forecasts.Forecasts {
		periodEnd, err := time.Parse(time.RFC3339, forecast.PeriodEnd)
		if err != nil {
			u.Log.Errorln("Error parsing time:", err)
			return totalPower, err
		}
		if periodEnd.Year() == day.Year() && periodEnd.YearDay() == day.YearDay() {
			totalPower += forecast.PVEstimate * 0.5 // Multiply by 0.5 because data is obtained every 30min
		}
	}

	// The calculated totalPower is in Wh
	totalPower = totalPower * 1000
	u.Log.Infof("Forecast Solar Power for %d/%d/%d: %d Wh", day.Day(), day.Month(), day.Year(), int(totalPower))
	return totalPower, nil
}

func CheckSun(now time.Time) time.Time {

	switch time := now; {
	case time.Hour() < 12:
		return now
	default:
		return now.AddDate(0, 0, 1)
	}
}
