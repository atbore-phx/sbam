package power

import (
	"errors"
	u "sbam/src/utils"
	"strings"
	"time"
)

func New() *Power {
	return &Power{}
}

func (power *Power) Handler(apiKey string, urls string) (float64, error) {
    production := 0.0
    urlList := strings.Split(urls, ",")
		for i := range urlList {
				urlList[i] = strings.TrimSpace(urlList[i])
		}
		if len(urlList) > 2 {
			err := errors.New("urlList contains more than 2 elements")
			u.Log.Errorln("Error:", err)
			return production, err
		}

	  day := CheckSun(time.Now())
    for pvn, url := range urlList {
        forecasts, err := GetForecast(apiKey, url)
        if err != nil {
            u.Log.Errorln("Error getting forecast for", url, ":", err)
            return production, err
        }
				u.Log.Infof("Starting Calculate PV production for solar System %d", pvn)

        dailyProduction, err := GetTotalDayPowerEstimate(forecasts, day)
        if err != nil {
            u.Log.Errorln("Error getting total power estimate for", url, ":", err)
            return production, err
        }

        production += dailyProduction
    }
		u.Log.Infof("Total Forecast Solar Power for %d/%d/%d: %d Wh", day.Day(), day.Month(), day.Year(), int(production))
    return production, nil
}
