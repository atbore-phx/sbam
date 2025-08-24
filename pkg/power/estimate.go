package power

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
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

func GetForecastChache(apiKey string, url string, cache_file_name string, cache_time int32) (Forecasts, error) {
	// Very simple cachefile - not generic in any way, we simply use a localfile to hold the json we got last time
	// Try getting the forecast from the local cachefile fist. If the cachefile is less than X hous old, then use it.
	// Else fall through to the oiginal code and download from the website.
	// Content downloaded from the website should then be saved to the local cachefile
	var cachedForecasts Forecasts
	var cacheHit bool

	u.Log.Debugf("cache_forecast is enabled")
	u.Log.Debugf("cache_forecast file %s cache_time %d", cache_file_name, cache_time)

	fileInfo, err := os.Stat(cache_file_name)
	if err != nil {
		if os.IsNotExist(err) {
			u.Log.Infof("Info: Cache File '%s' does not exist - fallthough to download", cache_file_name)
		} else {
			u.Log.Errorf("Error getting file info: %v", err)
		}
	}
	modTime := fileInfo.ModTime()
	currentTime := time.Now()

	age := currentTime.Sub(modTime)

	u.Log.Debugf("File '%s' was last modified at: %s", cache_file_name, modTime.Format(time.RFC3339))
	u.Log.Debugf("Age of file '%s': %s", cache_file_name, age)

	cachedForecasts, cacheHit, _ = readForecastCache(cache_file_name)
	if cacheHit {
		// Check if the file is older than a certain duration
		threshold := time.Duration(cache_time) * time.Second
		if age > threshold {
			u.Log.Debugf("File '%s' is older than %s - fall though to download", cache_file_name, threshold)
		} else {
			u.Log.Debugf("File '%s' is not older than %s - use cached forecast", cache_file_name, threshold)
			return cachedForecasts, nil
		}
	}
	u.Log.Debugf("Falling through to URL read for forecast")

	// Read the forecast from the URL
	forecasts, err := GetForecast(apiKey, url)
	if err != nil {
		if cacheHit {
			u.Log.Errorln("Error getting forecast for", url, ":", err)
			u.Log.Debugf("Using cached forecast")
			return cachedForecasts, nil
		} else {
			u.Log.Errorln("Error getting forecast for", url, " and there isn't a valid cache available:", err)
			return Forecasts{}, err
		}
	}

	// Write the json to the cachefile
	jsonData, err := json.MarshalIndent(forecasts, "", "  ") // Use "  " for 2-space indentation
	if err != nil {
		u.Log.Errorf("Error: Unable to marshall forecast")
	} else {
		err = os.WriteFile(cache_file_name, jsonData, 0644)
		if err != nil {
			u.Log.Errorf("Error: Unable to write cachefile %s", cache_file_name)
		} else {
			u.Log.Infof("Cachefile %s written successfully", cache_file_name)
		}
	}

	return forecasts, nil
}

func readForecastCache(cache_file_name string) (Forecasts, bool, error) {
	var cachedForecasts Forecasts

	// Read the cachefile
	cacheFile, err := os.Open(cache_file_name)
	if err != nil {
		u.Log.Errorf("Error opening file:", err)
		return Forecasts{}, false, err
	}
	defer cacheFile.Close() // Ensure the file is closed after function execution
	byteValue, err := io.ReadAll(cacheFile)
	if err != nil {
		u.Log.Errorf("Error reading file:", err)
		return Forecasts{}, false, err
	}

	err = json.Unmarshal(byteValue, &cachedForecasts)
	if err != nil {
		u.Log.Errorf("Error unmarshaling JSON:", err)
		return Forecasts{}, false, err
	}
	u.Log.Debugf("Cache File Read Successfully - returning cached forecast")
	return cachedForecasts, true, nil
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
