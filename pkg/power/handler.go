package power

import (
	"encoding/json"
	"errors"
	"io/ioutil"
  "fmt"
	"os"
	u "sbam/src/utils"
	"strings"
	"time"

	"github.com/spf13/viper"
)

func New() *Power {
	return &Power{}
}

func readForecastCache(cache_file_name string) (Forecasts, error) {
	var cachedForecasts Forecasts

	// Read the cachefile
	cacheFile, err := os.Open(cache_file_name)
	if err != nil {
		u.Log.Errorf("Error opening file:", err)
		return Forecasts{}, err
	}
	defer cacheFile.Close() // Ensure the file is closed after function execution
	byteValue, err := ioutil.ReadAll(cacheFile)
	if err != nil {
		u.Log.Errorf("Error reading file:", err)
		return Forecasts{}, err
	}

	err = json.Unmarshal(byteValue, &cachedForecasts)
	if err != nil {
		u.Log.Errorf("Error unmarshaling JSON:", err)
		return Forecasts{}, err
	}
	u.Log.Debugf("Cache File Read Successfully - returning cached forecast")
	return cachedForecasts, nil
}

// Very simple cachefile - not generic in any way, we simply use a localfile to hold the json we got last time
// Try getting the forecast from the local cachefile fist. If the cachefile is less than X hous old, then use it.
// Else fall through and download from the website.
// Content downloaded from the website should then be saved to the local cachefile
//
// RETURN:
//
//		 TotalDailyProduction float64
//	  forecast_retrieved bool
//	  err error
func (power *Power) Handler(apiKey string, urls string) (float64, bool, error) {
	var cachedForecasts Forecasts
	var cacherr error

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
	// read the cache control variables
	cache_forecast := viper.GetBool("cache_forecast")
	cache_file_prefix := viper.GetString("cache_file_prefix")
	cache_time := viper.GetInt32("cache_time")

  var forecasts Forecasts
  var err error
  var cache_read bool = false

	day := CheckSun(time.Now())
	for pvn, url := range urlList {
    u.Log.Infof("Index %d URL %s", pvn, url)
		if cache_forecast {
			// Caching is enabled. As we have multiple URL's we need a separate file for each. Use the cache_file_name as a base
			// and add a suffix to it. Use the pvn as the suffix
			cache_file_name := fmt.Sprintf("%s.%d", cache_file_prefix, pvn)
			u.Log.Debugf("cache_forecast is enabled")
			u.Log.Debugf("cache_forecast file %s cache_time %d", cache_file_name, cache_time)

			fileInfo, err := os.Stat(cache_file_name)
			if err != nil {
				if os.IsNotExist(err) {
					u.Log.Infof("Info: Cache File '%s' does not exist - fallthough to download", cache_file_name)
				} else {
					u.Log.Errorf("Error getting file info: %v", err)
				}
			} else {
				modTime := fileInfo.ModTime()
				currentTime := time.Now()

				age := currentTime.Sub(modTime)

				u.Log.Debugf("File '%s' was last modified at: %s", cache_file_name, modTime.Format(time.RFC3339))
				u.Log.Debugf("Age of file '%s': %s", cache_file_name, age)

				cachedForecasts, cacherr = readForecastCache(cache_file_name)
				if cacherr == nil {
					// Check if the file is older than a certain duration

					threshold := time.Duration(cache_time) * time.Second
					if age > threshold {
						u.Log.Debugf("File '%s' is older than %s - fall though to download", cache_file_name, threshold)
            cacherr=errors.New("cache is too old")
					} else {
						u.Log.Infof("File '%s' (%s) is newer than %s - use cached forecast", cache_file_name, age, threshold)
            cache_read=true
					}
				}
			}
		}
    if(cache_read) {
      u.Log.Debugf("Using cached forecast")
      forecasts = cachedForecasts
    } else {
      u.Log.Debugf("No forecast from cache - retrieve from upstream")
		  forecasts, err = GetForecast(apiKey, url)
      if cache_forecast {
        // Write the json to the cachefile
        jsonData, err := json.MarshalIndent(forecasts, "", "  ") // Use "  " for 2-space indentation
        if err != nil {
          u.Log.Errorf("Error: Unable to marshall forecast")
        } else {
          cache_file_name := fmt.Sprintf("%s.%d", cache_file_prefix, pvn)
  
          err = os.WriteFile(cache_file_name, jsonData, 0644)
          if err != nil {
            u.Log.Errorf("Error: Unable to write cachefile %s", cache_file_name)
          } else {
            u.Log.Infof("Cachefile %s for '%s' written successfully", cache_file_name, url)
          }
        }
      }
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
