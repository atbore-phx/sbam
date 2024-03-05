package power

import (
	u "sbam/src/utils"
	"time"
)

func New() *Power {
	return &Power{}
}

func (power *Power) Handler(apiKey string, url string) (float64, error) {
	production := 0.0
	forecasts, err := GetForecast(apiKey, url)
	if err != nil {
		u.Log.Errorln("Error getting forecast:", err)
		return production, err
	}

	day := CheckSun(time.Now())
	production, err = GetTotalDayPowerEstimate(forecasts, day)
	if err != nil {
		u.Log.Errorln("Error getting total power estimate:", err)
		return production, err
	}

	return production, nil

}
