package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type Forecast struct {
	PVEstimate   float64 `json:"pv_estimate"`
	PVEstimate10 float64 `json:"pv_estimate10"`
	PVEstimate90 float64 `json:"pv_estimate90"`
	PeriodEnd    string  `json:"period_end"`
	Period       string  `json:"period"`
}

type Forecasts struct {
	Forecasts []Forecast `json:"forecasts"`
}

func getForecast(apiKey string, url string) (Forecasts, error) {
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

	var forecasts Forecasts
	err = json.NewDecoder(resp.Body).Decode(&forecasts)
	if err != nil {
		return Forecasts{}, err
	}

	return forecasts, nil
}

func getTotalDayPowerEstimate(forecasts Forecasts, day time.Time) (float64, error) {
	totalPower := 0.0
	for _, forecast := range forecasts.Forecasts {
		periodEnd, err := time.Parse(time.RFC3339, forecast.PeriodEnd)
		if err != nil {
			fmt.Println("Error parsing time:", err)
			return 0, err
		}
		if periodEnd.Year() == day.Year() && periodEnd.YearDay() == day.YearDay() {
			totalPower += forecast.PVEstimate * 0.5 // Multiply by 0.5 because data is obtained every 30min
		}
	}

	// The calculated totalPower is in KWh
	return totalPower, nil
}

func main() {
	// init global vars
	url := os.Getenv("URL")
	apiKey := os.Getenv("API_KEY")

	forecasts, err := getForecast(apiKey, url)
	if err != nil {
		fmt.Println("Error getting forecast:", err)
		return
	}

	tomorrow := time.Now().AddDate(0, 0, 1)
	chargeKW, err := getTotalDayPowerEstimate(forecasts, tomorrow)
	if err != nil {
		fmt.Println("Error getting total power estimate:", err)
		return
	}

	fmt.Print(chargeKW)

}
