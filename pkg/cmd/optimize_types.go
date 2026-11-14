package cmd

import "time"

// OptimizeConfig holds the validated ha_* and opt_* settings for
// scheduler_mode: optimize and the `sbam optimize` command (SPEC-0001).
// It is populated by loadOptimizeConfig (optimize_config.go) and consumed by
// runOptimization (optimize_run.go).
type OptimizeConfig struct {
	// Home Assistant access. HAURL/HAToken are the raw config values; the
	// resolved access (Supervisor token vs. URL) is built by the adapters.
	HAURL   string
	HAToken string

	PriceEntity       string
	ConsumptionEntity string
	EVEntity          string  // optional; "" disables EV subtraction and the EV guard
	EVGuardW          float64 // opt_ev_guard_w; EV power that engages the guard, 0 disables (SPEC-0002)

	HistoryDays    int
	Efficiency     float64
	MaxChargeW     float64
	MaxDischargeW  float64
	MinSocPct      float64
	VATRate        float64
	SellMargin     float64
	MinGain        float64 // opt_min_gain, currency per hour (AC-11b)
	WearCost       float64 // opt_wear_cost, currency per kWh moved by a forced charge or discharge (AC-11b)
	Surcharge      [24]float64
	DryRun         bool
	StaticDailyWh  float64 // pw_consumption; AC-2 fallback is StaticDailyWh/24 per hour
	FroniusIP      string
	SolcastURLs    []string
	SolcastAPIKey  string
	CacheFilePref  string
	CacheTime      int32
	ForecastMaxAge time.Duration // max(cache_time, 24 h)
	Location       *time.Location
}
