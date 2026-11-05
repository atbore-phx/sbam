package power

import (
	"context"
	"errors"
	"fmt"
	"os"
	u "sbam/src/utils"
	"strings"
	"sync"
	"time"
)

// maxForecastURLs mirrors the limit enforced by Handler.
const maxForecastURLs = 2

// ForecastSourceMeta describes how the forecast for one Solcast URL index
// was obtained.
type ForecastSourceMeta struct {
	// Age is the age of the forecast that was used (0 for a fresh download).
	// For a stale cache it is the age of the rejected cache file.
	Age time.Duration
	// Downloaded is true when the forecast was fetched from Solcast in this call.
	Downloaded bool
	// Stale is true when the download failed and the cache was older than
	// maxAge; no forecast is returned for this index (S-009).
	Stale bool
	// Missing is true when no usable forecast exists: no cache, an unreadable
	// cache, or an empty forecast, together with a failed download.
	Missing bool
}

// ForecastMeta holds per-URL metadata in the same order as the urls passed
// to LoadForecasts.
type ForecastMeta struct {
	Sources []ForecastSourceMeta
}

// AnyStale reports whether any URL index was rejected as stale.
func (m ForecastMeta) AnyStale() bool {
	for _, s := range m.Sources {
		if s.Stale {
			return true
		}
	}
	return false
}

// AnyMissing reports whether any URL index has no forecast at all.
func (m ForecastMeta) AnyMissing() bool {
	for _, s := range m.Sources {
		if s.Missing {
			return true
		}
	}
	return false
}

// forecastCacheFileName returns the cache file used for URL index idx. It
// matches the naming used by Handler so both paths share one cache.
func forecastCacheFileName(prefix string, idx int) string {
	return fmt.Sprintf("%s.%d", prefix, idx)
}

// LoadForecasts returns the Solcast forecast periods for each URL, always via
// the cache path regardless of cache_forecast:
//
//   - a cache file younger than cacheTime seconds (and not older than maxAge)
//     is used without contacting Solcast;
//   - otherwise the forecast is downloaded and the cache rewritten, with its
//     mtime set to now() so the injected clock and the cache age agree;
//   - if the download fails, a cache no older than maxAge is used; an older
//     cache is reported as Stale and an absent one as Missing, and the slice
//     for that index is nil.
//
// Staleness and missing data are reported only through ForecastMeta. An error
// is returned only for invalid arguments or a cancelled context. The caller
// sets maxAge to max(cache_time, 24 h). now defaults to time.Now when nil.
func LoadForecasts(ctx context.Context, apiKey string, urls []string, cacheFilePrefix string, cacheTime int32, maxAge time.Duration, now func() time.Time) ([][]Forecast, ForecastMeta, error) {
	return LoadForecastsGated(ctx, nil, apiKey, urls, cacheFilePrefix, cacheTime, maxAge, now)
}

// QuotaGate remembers, per forecast URL index, that Solcast answered "daily
// limit exceeded" and until when no further download should be tried.
//
// Why: the hourly optimizer re-downloads whenever the cache is older than
// cache_time. On the free plan (10 requests per site and day) the last
// successful refresh happens in the afternoon, and every later hour until the
// 00:00 UTC reset got a 429 and logged a Warn (about 18 Warns a day in the
// first live week) although the cached forecast was still valid. Each of
// those requests is pointless: Solcast will not answer before the reset. The
// gate skips them and logs the exhaustion once, at Info.
//
// A QuotaGate is safe for concurrent use. A nil *QuotaGate never blocks.
type QuotaGate struct {
	mu    sync.Mutex
	until map[int]time.Time
}

// NewQuotaGate returns an empty gate.
func NewQuotaGate() *QuotaGate {
	return &QuotaGate{until: map[int]time.Time{}}
}

// blockedUntil returns the reset time while downloads for idx are blocked.
func (g *QuotaGate) blockedUntil(idx int, now time.Time) (time.Time, bool) {
	if g == nil {
		return time.Time{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.until[idx]
	if !ok || !now.Before(until) {
		delete(g.until, idx)
		return time.Time{}, false
	}
	return until, true
}

// block stops downloads for idx until the next Solcast quota reset.
func (g *QuotaGate) block(idx int, now time.Time) time.Time {
	until := NextQuotaReset(now)
	g.mu.Lock()
	g.until[idx] = until
	g.mu.Unlock()
	return until
}

// NextQuotaReset returns the next 00:00 UTC after now, when Solcast resets
// the daily request limit. Observed live: after an evening of 429 answers
// the first successful request was the one at 00:00 UTC (02:00 CEST).
func NextQuotaReset(now time.Time) time.Time {
	t := now.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
}

// LoadForecastsGated is LoadForecasts with a quota gate that persists across
// calls (the optimizer runner keeps one for its lifetime). A nil gate behaves
// like LoadForecasts: every expired cache leads to a download attempt.
func LoadForecastsGated(ctx context.Context, gate *QuotaGate, apiKey string, urls []string, cacheFilePrefix string, cacheTime int32, maxAge time.Duration, now func() time.Time) ([][]Forecast, ForecastMeta, error) {
	if now == nil {
		now = time.Now
	}
	if len(urls) == 0 {
		return nil, ForecastMeta{}, errors.New("load forecasts: no forecast URL configured")
	}
	if len(urls) > maxForecastURLs {
		return nil, ForecastMeta{}, fmt.Errorf("load forecasts: %d URLs configured, at most %d supported", len(urls), maxForecastURLs)
	}
	if cacheTime < 0 {
		return nil, ForecastMeta{}, fmt.Errorf("load forecasts: cache_time must not be negative, got %d", cacheTime)
	}
	if maxAge <= 0 {
		return nil, ForecastMeta{}, fmt.Errorf("load forecasts: maxAge must be positive, got %s", maxAge)
	}

	result := make([][]Forecast, len(urls))
	meta := ForecastMeta{Sources: make([]ForecastSourceMeta, len(urls))}
	for idx, url := range urls {
		if err := ctx.Err(); err != nil {
			return nil, ForecastMeta{}, fmt.Errorf("load forecasts: %w", err)
		}
		cacheFile := forecastCacheFileName(cacheFilePrefix, idx)
		result[idx], meta.Sources[idx] = loadOneForecast(ctx, gate, apiKey, strings.TrimSpace(url), idx, cacheFile, cacheTime, maxAge, now)
	}
	return result, meta, nil
}

// loadOneForecast resolves the forecast for a single URL index.
func loadOneForecast(ctx context.Context, gate *QuotaGate, apiKey, url string, idx int, cacheFile string, cacheTime int32, maxAge time.Duration, now func() time.Time) ([]Forecast, ForecastSourceMeta) {
	threshold := time.Duration(cacheTime) * time.Second
	cached, cacheAge, cacheOK := readCacheWithAge(cacheFile, now())

	if cacheOK && cacheAge <= threshold && cacheAge <= maxAge {
		u.Log.Debugw("forecast cache hit", "index", idx, "age", cacheAge)
		return cached.Forecasts, ForecastSourceMeta{Age: cacheAge}
	}

	// While the daily quota is exhausted a download cannot succeed, so it is
	// skipped and the cache is used as if the download had failed. A stale or
	// missing cache still gets its Warn below: that is a real problem.
	var err error
	quotaExpected := false
	if until, blocked := gate.blockedUntil(idx, now()); blocked {
		u.Log.Debugw("forecast download skipped: Solcast daily quota exhausted", "index", idx, "retry_at", until.Format(time.RFC3339))
		err, quotaExpected = ErrQuotaExceeded, true
	} else {
		var downloaded Forecasts
		downloaded, err = GetForecastCtx(ctx, apiKey, url)
		if err == nil && len(downloaded.Forecasts) == 0 {
			err = errors.New("empty forecast")
		}
		if err == nil {
			storeForecastCache(cacheFile, downloaded, now(), idx)
			return downloaded.Forecasts, ForecastSourceMeta{Downloaded: true}
		}
		if gate != nil && errors.Is(err, ErrQuotaExceeded) {
			until := gate.block(idx, now())
			u.Log.Infow("Solcast daily quota exhausted; using the cached forecast until the quota resets",
				"index", idx, "cache_age", cacheAge, "retry_at", until.Format(time.RFC3339))
			quotaExpected = true
		}
	}

	switch {
	case cacheOK && cacheAge <= maxAge && quotaExpected:
		// Normal on the free plan every afternoon; the Info line that set the
		// gate already says so.
		return cached.Forecasts, ForecastSourceMeta{Age: cacheAge}
	case cacheOK && cacheAge <= maxAge:
		u.Log.Warnw("forecast download failed, using cache", "index", idx, "age", cacheAge, "error", err)
		return cached.Forecasts, ForecastSourceMeta{Age: cacheAge}
	case cacheOK:
		u.Log.Warnw("forecast download failed and cache is stale", "index", idx, "age", cacheAge, "max_age", maxAge, "error", err)
		return nil, ForecastSourceMeta{Age: cacheAge, Stale: true}
	default:
		u.Log.Warnw("forecast download failed and no cache is available", "index", idx, "error", err)
		return nil, ForecastSourceMeta{Missing: true}
	}
}

// readCacheWithAge reads a cache file and returns its contents and age. ok is
// false when the file is absent, unreadable, or holds no forecast periods.
func readCacheWithAge(cacheFile string, now time.Time) (Forecasts, time.Duration, bool) {
	info, err := os.Stat(cacheFile)
	if err != nil {
		return Forecasts{}, 0, false
	}
	cached, ok, err := ReadForecastCache(cacheFile)
	if err != nil || !ok || len(cached.Forecasts) == 0 {
		return Forecasts{}, 0, false
	}
	age := now.Sub(info.ModTime())
	if age < 0 {
		age = 0
	}
	return cached, age, true
}

// storeForecastCache writes the cache and stamps its mtime with ts. Failures
// are logged only: the downloaded forecast is still usable.
func storeForecastCache(cacheFile string, forecasts Forecasts, ts time.Time, idx int) {
	if err := writeForecastCache(cacheFile, forecasts); err != nil {
		u.Log.Warnw("forecast cache not written", "index", idx, "error", err)
		return
	}
	if err := os.Chtimes(cacheFile, ts, ts); err != nil {
		u.Log.Warnw("forecast cache mtime not set", "index", idx, "error", err)
	}
}
