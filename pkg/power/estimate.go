package power

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	u "sbam/src/utils"
	"time"
)

// maxForecastBodyBytes caps the Solcast response body (S-018).
const maxForecastBodyBytes = 4 << 20 // 4 MiB

// forecastHTTPTimeout is the per-request timeout for Solcast downloads.
const forecastHTTPTimeout = 10 * time.Second

// GetForecast downloads a Solcast forecast. It is a thin wrapper around
// GetForecastCtx using context.Background, kept for existing callers.
// ErrQuotaExceeded is returned for HTTP 429 from Solcast. On the free
// (hobbyist) plan this means the daily request limit is used up; it resets at
// 00:00 UTC. The message is unchanged from earlier releases.
var ErrQuotaExceeded = errors.New("you have exceeded your free daily limit, too many Request to the forecast API")

func GetForecast(apiKey string, url string) (Forecasts, error) {
	return GetForecastCtx(context.Background(), apiKey, url)
}

// GetForecastCtx downloads a Solcast forecast from url, authenticating with
// apiKey as a Bearer token. Only a 200 response is decoded, and the body is
// capped at 4 MiB. Returned errors never contain the API key or the URL.
func GetForecastCtx(ctx context.Context, apiKey string, url string) (Forecasts, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Forecasts{}, fmt.Errorf("build forecast request: %w", sanitizeURLError(err))
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: forecastHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return Forecasts{}, fmt.Errorf("fetch forecast: %w", sanitizeURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return Forecasts{}, ErrQuotaExceeded
	}
	if resp.StatusCode != http.StatusOK {
		return Forecasts{}, fmt.Errorf("fetch forecast: unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxForecastBodyBytes+1))
	if err != nil {
		return Forecasts{}, fmt.Errorf("read forecast body: %w", sanitizeURLError(err))
	}
	if len(body) > maxForecastBodyBytes {
		return Forecasts{}, fmt.Errorf("read forecast body: response exceeds %d bytes", maxForecastBodyBytes)
	}

	var forecasts Forecasts
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&forecasts); err != nil {
		return Forecasts{}, fmt.Errorf("decode forecast: %w", err)
	}
	return forecasts, nil
}

// sanitizeURLError strips the request URL from *url.Error values so that
// site IDs or query-string credentials never reach logs or MQTT errors.
func sanitizeURLError(err error) error {
	var uerr *neturl.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
	}
	return err
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
		u.Log.Errorf("Error getting file info: %v", err)
		if os.IsNotExist(err) {
			u.Log.Infof("Info: Cache File '%s' does not exist - fallthough to download", cache_file_name)
		}
	} else {
		modTime := fileInfo.ModTime()
		currentTime := time.Now()

		age := currentTime.Sub(modTime)

		u.Log.Debugf("File '%s' was last modified at: %s", cache_file_name, modTime.Format(time.RFC3339))
		u.Log.Debugf("Age of file '%s': %s", cache_file_name, age)

		cachedForecasts, cacheHit, _ = ReadForecastCache(cache_file_name)
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
	_ = writeForecastCache(cache_file_name, forecasts)

	return forecasts, nil
}

// writeForecastCache stores forecasts as indented JSON in cache_file_name.
// Failures are logged and returned; callers may treat them as non-fatal.
func writeForecastCache(cache_file_name string, forecasts Forecasts) error {
	jsonData, err := json.MarshalIndent(forecasts, "", "  ") // Use "  " for 2-space indentation
	if err != nil {
		u.Log.Errorf("Error: Unable to marshall forecast")
		return fmt.Errorf("marshal forecast cache: %w", err)
	}
	if err := os.WriteFile(cache_file_name, jsonData, 0644); err != nil {
		u.Log.Errorf("Error: Unable to write cachefile %s", cache_file_name)
		return fmt.Errorf("write forecast cache: %w", err)
	}
	u.Log.Infof("Cachefile %s written successfully", cache_file_name)
	return nil
}

func ReadForecastCache(cache_file_name string) (Forecasts, bool, error) {
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

// GetDayPowerEstimate sums PV estimates from forecasts for the calendar
// day day. When after is non-nil only periods with period_end strictly
// after that point (in the local timezone of day) are included.
//
// period_end values are parsed as RFC 3339 and converted to the local
// timezone of day before comparison, so day boundaries are evaluated in
// local time regardless of the original UTC offset.
func GetDayPowerEstimate(forecasts Forecasts, day time.Time, after *time.Time) (float64, error) {
	totalPower := 0.0
	loc := day.Location()
	for _, forecast := range forecasts.Forecasts {
		periodEnd, err := time.Parse(time.RFC3339, forecast.PeriodEnd)
		if err != nil {
			u.Log.Errorln("Error parsing time:", err)
			return totalPower, err
		}
		// Convert to local time so day-of-year comparisons are
		// consistent with the caller-supplied day.
		periodEndLocal := periodEnd.In(loc)
		if periodEndLocal.Year() == day.Year() && periodEndLocal.YearDay() == day.YearDay() {
			if after != nil && !periodEndLocal.After(*after) {
				continue
			}
			totalPower += forecast.PVEstimate * 0.5 // Multiply by 0.5 because data is obtained every 30min
		}
	}

	totalPower = totalPower * 1000 // The calculated totalPower is in Wh
	u.Log.Infof("Forecast Solar Power for %d/%d/%d: %d Wh", day.Day(), day.Month(), day.Year(), int(totalPower))
	return totalPower, nil
}
