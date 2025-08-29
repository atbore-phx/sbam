package power

import (
	"errors"
	"fmt"
	u "sbam/src/utils"
	"strings"
	"time"
)

var forecasts Forecasts
var err error

func New() *Power {
	return &Power{}
}

func (power *Power) Handler(apiKey string, urls string, cache_forecast bool, cache_file_prefix string, cache_time int32) (float64, bool, error) {
	production := 0.0
	urlList := strings.Split(urls, ",")
	for i := range urlList {
		urlList[i] = strings.TrimSpace(urlList[i])
	}
	if len(urlList) > 2 {
		err := errors.New("urlList contains more than 2 elements")
		u.Log.Errorln("Error:", err)
		return production, false, err
	}

	day := CheckSun(time.Now())
	for pvn, url := range urlList {
		u.Log.Infof("Index %d URL %s", pvn, url)
		if !cache_forecast {
			u.Log.Debugf("cache_forecast is disabled")
			forecasts, err = GetForecast(apiKey, url)
		} else {
			cache_file_name := fmt.Sprintf("%s.%d", cache_file_prefix, pvn)
			forecasts, err = GetForecastChache(apiKey, url, cache_file_name, cache_time)
		}
		if err != nil {
			u.Log.Errorln("Error getting forecast for", url, ":", err)
			u.Log.Errorln("Forecast charging will be disabled")
			return production, false, nil
		}
		u.Log.Infof("Starting Calculate PV production for solar System %d", pvn)

		dailyProduction, err := GetTotalDayPowerEstimate(forecasts, day)
		if err != nil {
			u.Log.Errorln("Error getting total power estimate for", url, ":", err)
			return production, false, err
		}

		production += dailyProduction
	}
	u.Log.Infof("Total Forecast Solar Power for %d/%d/%d: %d Wh", day.Day(), day.Month(), day.Year(), int(production))
	return production, true, nil
}
