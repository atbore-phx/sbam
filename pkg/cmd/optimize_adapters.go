package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sbam/pkg/homeassistant"
	pw "sbam/pkg/power"
)

// haClient is the read-only Home Assistant API runOptimization needs
// (ADR-0001). It matches homeassistant.Client so the production client can be
// used directly; tests inject fakes via newHAClient.
type haClient interface {
	State(ctx context.Context, entityID string) (homeassistant.State, error)
	History(ctx context.Context, entityID string, start, end time.Time) ([]homeassistant.HistoryPoint, error)
}

// forecastLoader loads the Solcast forecasts for optimize mode. It always uses
// the forecast cache path (power.LoadForecasts); cache_forecast is ignored so
// hourly runs never raise the Solcast call rate (design §Solcast, T-0133).
type forecastLoader interface {
	Load(ctx context.Context, cfg OptimizeConfig, now time.Time) ([][]pw.Forecast, pw.ForecastMeta, error)
}

// batteryReader reads the battery state of charge (%) and capacity (Wh).
type batteryReader interface {
	Read(ctx context.Context, froniusIP string) (socPct, capacityWh float64, err error)
}

// optimizeDeps bundles the I/O dependencies of runOptimization. The runner
// and the `sbam optimize` CLI build it once with newOptimizeDeps.
type optimizeDeps struct {
	HA        haClient
	Forecasts forecastLoader
	Battery   batteryReader
}

// newHAClient resolves Home Assistant access (Supervisor token via os.Getenv
// or ha_url + ha_token) and returns the GET-only client. Package-level so
// tests can swap it, mirroring newStorage/newBatteryWriter. It delegates to
// newHAAccessClient, shared with the PV source (SPEC-0003).
var newHAClient = func(cfg OptimizeConfig) (haClient, error) {
	return newHAAccessClient(cfg.HAURL, cfg.HAToken)
}

// newForecastLoader returns the production cache-path forecast loader.
var newForecastLoader = func() forecastLoader {
	return &cachedForecastLoader{quota: pw.NewQuotaGate()}
}

// newBatteryReader returns the production battery reader, built on the same
// storage client factory the schedule command uses.
var newBatteryReader = func() batteryReader {
	return storageBatteryReader{client: newStorage()}
}

// newOptimizeDeps builds all optimizer dependencies. It fails only when Home
// Assistant access cannot be resolved, which startup validation reports.
func newOptimizeDeps(cfg OptimizeConfig) (optimizeDeps, error) {
	ha, err := newHAClient(cfg)
	if err != nil {
		return optimizeDeps{}, fmt.Errorf("home assistant client: %w", err)
	}
	// One HA client per process, shared by the optimizer and the PV reads
	// with pv_source=ha_solcast (S-025 d).
	var forecasts forecastLoader
	if cfg.PV.isHA() {
		forecasts = newHAForecastLoader(ha, cfg.PV.Entities)
	} else {
		forecasts = newForecastLoader()
	}
	return optimizeDeps{
		HA:        ha,
		Forecasts: forecasts,
		Battery:   newBatteryReader(),
	}, nil
}

// optForecastMinMaxAge is the lower bound of the forecast staleness threshold:
// max(cache_time, 24 h) (S-009).
const optForecastMinMaxAge = 24 * time.Hour

// cachedForecastLoader is the production forecastLoader. It keeps a Solcast
// quota gate for the runner's lifetime, so after a "daily limit exceeded"
// answer no download is attempted until the 00:00 UTC reset (see
// power.QuotaGate for why).
type cachedForecastLoader struct {
	quota *pw.QuotaGate
}

// Load implements forecastLoader via power.LoadForecastsGated.
func (l *cachedForecastLoader) Load(ctx context.Context, cfg OptimizeConfig, now time.Time) ([][]pw.Forecast, pw.ForecastMeta, error) {
	maxAge := cfg.ForecastMaxAge
	if minAge := max(time.Duration(cfg.CacheTime)*time.Second, optForecastMinMaxAge); maxAge < minAge {
		maxAge = minAge
	}
	return pw.LoadForecastsGated(ctx, l.quota, cfg.SolcastAPIKey, cfg.SolcastURLs, cfg.CacheFilePref, cfg.CacheTime, maxAge, func() time.Time { return now })
}

// storageBatteryReader adapts the Fronius Solar API storage client.
type storageBatteryReader struct {
	client storageClient
}

// Read implements batteryReader. The storage client has its own 10 s HTTP
// timeout; ctx is checked before the call.
func (s storageBatteryReader) Read(ctx context.Context, froniusIP string) (float64, float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if s.client == nil {
		return 0, 0, errors.New("storage client not configured")
	}
	_, capacityWh, socPct, err := s.client.Handler(froniusIP)
	if err != nil {
		return 0, 0, fmt.Errorf("read battery state: %w", err)
	}
	return socPct, capacityWh, nil
}
