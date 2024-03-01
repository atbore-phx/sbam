package power

import (
	"fmt"
	"time"
)

func New() *Power {
	return &Power{}
}

func (power *Power) Handler(apiKey string, url string) (float64, error) {
	production := 0.0
	forecasts, err := GetForecast(apiKey, url)
	if err != nil {
		fmt.Println("Error getting forecast:", err)
		return production, err
	}

	tomorrow := time.Now().AddDate(0, 0, 1)
	production, err = GetTotalDayPowerEstimate(forecasts, tomorrow)
	if err != nil {
		fmt.Println("Error getting total power estimate:", err)
		return production, err
	}

	return production, nil

}
