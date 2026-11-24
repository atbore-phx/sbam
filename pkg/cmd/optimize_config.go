package cmd

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"sbam/pkg/homeassistant"
	"sbam/pkg/optimize"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Viper keys for scheduler_mode: optimize and `sbam optimize` (UX §2.1).
const (
	// Nested keys of the optimizer: map (see optimize_keys.go); the flags
	// keep the flat --opt_* / --ha_* names.
	keyHAURL                 = "optimizer.ha_url"
	keyHAToken               = "optimizer.ha_token"
	keyOptPriceSource        = "optimizer.price_source"
	keyOptPriceEntity        = "optimizer.price_entity"
	keyOptStaticBuyPrice     = "optimizer.static.buy_price"
	keyOptStaticSellPrice    = "optimizer.static.sell_price"
	keyOptStaticCurrency     = "optimizer.static.currency"
	keyOptConsumptionEntity  = "optimizer.consumption_entity"
	keyOptEVEntity           = "optimizer.ev_entity"
	keyOptHistoryDays        = "optimizer.history_days"
	keyOptBatteryEfficiency  = "optimizer.battery_efficiency"
	keyOptMaxChargeRateW     = "optimizer.max_charge_rate_w"
	keyOptMaxDischargeRateW  = "optimizer.max_discharge_rate_w"
	keyOptMinSocPct          = "optimizer.min_soc_pct"
	keyOptVATRate            = "optimizer.vat_rate"
	keyOptSellMargin         = "optimizer.sell_margin"
	keyOptMinGain            = "optimizer.min_gain"
	keyOptWearCost           = "optimizer.wear_cost"
	keyOptPurchaseSurcharge  = "optimizer.purchase_surcharge"
	keyOptDryRun             = "optimizer.dry_run"
	keyOptEVGuardW           = "optimizer.ev_guard_w"
	optCfgMaxEVGuardW        = 50000.0
	optCfgMaxCurrencyLen     = 8
	optCfgSurchargeHours     = 24
	optCfgSurchargeMin       = -5.0
	optCfgSurchargeMax       = 10.0
	optCfgMaxRateW           = 30000.0
	optCfgMinSocWarnPct      = 5.0
	optCfgMinForecastMaxAge  = 24 * time.Hour
	optCfgDefaultCachePrefix = "cached_forecast"
	optCfgDefaultCacheTime   = int32(7200)
)

// Price sources (optimizer.price_source).
const (
	optPriceSourceNordpool = "nordpool"
	optPriceSourceStatic   = "static"
)

// Defaults (UX §2.1; opt_dry_run defaults to true for every target per the
// SPEC-0001 amendment, overriding UX note A).
const (
	defOptPriceSource       = optPriceSourceNordpool
	defOptHistoryDays       = 10
	defOptBatteryEfficiency = 0.83
	defOptMaxChargeRateW    = 4500.0
	defOptMaxDischargeRateW = 4500.0
	defOptMinSocPct         = 7.0
	defOptVATRate           = 0.25
	defOptSellMargin        = 0.0
	defOptMinGain           = 0.50
	defOptWearCost          = 0.0
	defOptDryRun            = true
	defOptEVGuardW          = 1000.0
)

// optCfgEntityPattern is the UX/add-on entity regex for opt_*_entity keys.
var optCfgEntityPattern = regexp.MustCompile(`^sensor\.[a-z0-9_]+$`)

// registerOptimizeFlags registers the long-only ha_* / opt_* flags shared by
// `sbam schedule` and `sbam optimize`. Values are read through viper (after
// bindFlags), so no package variables are bound. --dry-run is intentionally
// not registered here: it is command-local to `sbam optimize`.
func registerOptimizeFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	bindOptimizeEnv()
	flag := func(key string) string {
		spec, _ := optSpecByKey(key)
		return spec.flag
	}
	f.String(flag(keyHAURL), "", "Home Assistant base URL (not needed in the HA add-on)")
	f.String(flag(keyHAToken), "", "HA long-lived access token (prefer HA_TOKEN env var)")
	f.String(flag(keyOptPriceSource), defOptPriceSource, "Price source: nordpool (HA Nordpool sensor) or static (fixed tariff from opt_static_buy_price and opt_static_sell_price)")
	f.String(flag(keyOptPriceEntity), "", "Nordpool price sensor entity ID (prices incl. VAT), e.g. sensor.nordpool_kwh_dk2_dkk_3_10_025; required when opt_price_source is nordpool")
	f.String(flag(keyOptStaticBuyPrice), "", "Static tariff buy price per kWh incl. tax: 24 comma-separated values, hour 00..23")
	f.String(flag(keyOptStaticSellPrice), "", "Static tariff sell (feed-in) price per kWh: 24 comma-separated values, hour 00..23")
	f.String(flag(keyOptStaticCurrency), "", "Currency of the static tariff, e.g. AUD (optional)")
	f.String(flag(keyOptConsumptionEntity), "", "Household load power sensor entity ID")
	f.String(flag(keyOptEVEntity), "", "Optional EV charger power sensor entity ID")
	f.Int(flag(keyOptHistoryDays), defOptHistoryDays, "Days of consumption history used for the profile (1..30)")
	f.Float64(flag(keyOptBatteryEfficiency), defOptBatteryEfficiency, "Battery round-trip efficiency (0, 1]")
	f.Float64(flag(keyOptMaxChargeRateW), defOptMaxChargeRateW, "Maximum battery charge rate in W")
	f.Float64(flag(keyOptMaxDischargeRateW), defOptMaxDischargeRateW, "Maximum battery discharge rate in W")
	f.Float64(flag(keyOptMinSocPct), defOptMinSocPct, "Minimum battery SoC in percent [0, 100)")
	f.Float64(flag(keyOptVATRate), defOptVATRate, "VAT rate as a fraction (0.25 = 25 %)")
	f.Float64(flag(keyOptSellMargin), defOptSellMargin, "Sell margin in currency/kWh incl. VAT")
	f.Float64(flag(keyOptEVGuardW), defOptEVGuardW, "EV charger power in W above which the battery is held instead of discharging into the car in self_consume hours; 0 disables (needs opt_ev_entity)")
	f.Float64(flag(keyOptMinGain), defOptMinGain, "Minimum gain in currency per hour before a charge or discharge replaces self-consumption (hold and standby need a tenth); 0 disables")
	f.Float64(flag(keyOptWearCost), defOptWearCost, "Battery wear cost in currency per kWh a forced charge or discharge moves; 0 disables")
	f.String(flag(keyOptPurchaseSurcharge), "", "24 comma-separated values, hour 00..23 (default 24 x 0)")
	f.Bool(flag(keyOptDryRun), defOptDryRun, "In sbam schedule: compute and publish the plan without writing to the inverter (sbam optimize never writes)")
	for _, spec := range optKeySpecs {
		_ = f.SetAnnotation(spec.flag, flagAnnotationViperKey, []string{spec.key})
	}
}

// optimizeConfigError aggregates every validation problem into one message
// (UX §5.2) so the operator can fix them all in one restart.
type optimizeConfigError struct {
	Problems []string
}

func (e *optimizeConfigError) Error() string {
	noun := "problems"
	if len(e.Problems) == 1 {
		noun = "problem"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "invalid configuration for scheduler_mode=optimize (%d %s):", len(e.Problems), noun)
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

// loadOptimizeConfig reads and validates the optimize configuration from
// viper. Warnings are non-fatal and should be logged at Warn by the caller.
// The Supervisor token is read only via os.Getenv (S-013), never via viper.
func loadOptimizeConfig() (OptimizeConfig, []string, error) {
	return optCfgLoad(os.Getenv)
}

// optCfgLoad is loadOptimizeConfig with an injectable getenv for tests.
func optCfgLoad(getenv func(string) string) (OptimizeConfig, []string, error) {
	var problems, warnings []string
	seenDepr := map[string]bool{}
	deprecated := func(msg string) {
		if !seenDepr[msg] {
			seenDepr[msg] = true
			warnings = append(warnings, msg)
		}
	}
	warnings = append(warnings, optDeprecatedEnvWarnings(getenv)...)
	addErr := func(err error) {
		if err != nil {
			problems = append(problems, err.Error())
		}
	}

	cfg := OptimizeConfig{
		HAURL:             optGetString(keyHAURL, deprecated),
		HAToken:           optGetString(keyHAToken, deprecated),
		PriceEntity:       optGetString(keyOptPriceEntity, deprecated),
		ConsumptionEntity: optGetString(keyOptConsumptionEntity, deprecated),
		EVEntity:          optGetString(keyOptEVEntity, deprecated),
		FroniusIP:         strings.TrimSpace(viper.GetString("fronius_ip")),
		SolcastURLs:       optCfgSplitList(viper.GetString("url")),
		SolcastAPIKey:     viper.GetString("apikey"),
		CacheFilePref:     optCfgDefaultCachePrefix,
		CacheTime:         optCfgDefaultCacheTime,
		Location:          time.Local,
	}
	if viper.Get("cache_file_prefix") != nil {
		cfg.CacheFilePref = viper.GetString("cache_file_prefix")
	}
	if viper.Get("cache_time") != nil {
		ct, err := optCfgInt("cache_time", viper.Get("cache_time"))
		addErr(err)
		if err == nil {
			cfg.CacheTime = int32(ct)
		}
	}
	cfg.ForecastMaxAge = time.Duration(cfg.CacheTime) * time.Second
	if cfg.ForecastMaxAge < optCfgMinForecastMaxAge {
		cfg.ForecastMaxAge = optCfgMinForecastMaxAge
	}

	// Price source, then entity IDs (AC-20, S-014).
	sourceProblems, sourceWarnings := optCfgPriceSource(&cfg, deprecated)
	problems = append(problems, sourceProblems...)
	warnings = append(warnings, sourceWarnings...)
	addErr(optCfgCheckEntity(keyOptConsumptionEntity, cfg.ConsumptionEntity, true))
	addErr(optCfgCheckEntity(keyOptEVEntity, cfg.EVEntity, false))

	// HA access (AC-21, S-014).
	access, err := homeassistant.ResolveAccess(cfg.HAURL, cfg.HAToken, getenv)
	switch {
	case err == nil:
		if access.PlainHTTPToPublicHost() {
			warnings = append(warnings, keyHAURL+" uses plain http to a non-private host; the HA token is sent unencrypted, use https")
		}
	case cfg.HAURL != "" && cfg.HAToken == "":
		problems = append(problems, keyHAToken+": required when "+keyHAURL+" is set")
	case cfg.HAURL == "":
		problems = append(problems, "home assistant access: no Supervisor token found and "+keyHAURL+"/"+keyHAToken+" are not set; set HA_URL and HA_TOKEN (Docker/CLI) or enable homeassistant_api (add-on)")
	default:
		problems = append(problems, keyHAURL+": "+strings.TrimPrefix(err.Error(), "homeassistant: "))
	}

	// Numeric bounds (AC-20, S-016).
	var v float64
	if v, err = optCfgFloatKey(keyOptBatteryEfficiency, defOptBatteryEfficiency, deprecated); err == nil {
		cfg.Efficiency = v
		if !(v > 0 && v <= 1) {
			err = fmt.Errorf("%s: must be greater than 0 and at most 1, got %v", keyOptBatteryEfficiency, v)
		}
	}
	addErr(err)

	for _, k := range []struct {
		key string
		def float64
		dst *float64
	}{
		{keyOptMaxChargeRateW, defOptMaxChargeRateW, &cfg.MaxChargeW},
		{keyOptMaxDischargeRateW, defOptMaxDischargeRateW, &cfg.MaxDischargeW},
	} {
		if v, err = optCfgFloatKey(k.key, k.def, deprecated); err == nil {
			*k.dst = v
			if !(v > 0 && v <= optCfgMaxRateW) {
				err = fmt.Errorf("%s: must be greater than 0 and at most %v W, got %v", k.key, optCfgMaxRateW, v)
			}
		}
		addErr(err)
	}

	if v, err = optCfgFloatKey(keyOptMinSocPct, defOptMinSocPct, deprecated); err == nil {
		cfg.MinSocPct = v
		switch {
		case !(v >= 0 && v < 100):
			err = fmt.Errorf("%s: must be at least 0 and below 100, got %v", keyOptMinSocPct, v)
		case v < optCfgMinSocWarnPct:
			warnings = append(warnings, fmt.Sprintf("%s is %v; values below %v leave almost no reserve against deep discharge", keyOptMinSocPct, v, optCfgMinSocWarnPct))
		}
	}
	addErr(err)

	if v, err = optCfgFloatKey(keyOptVATRate, defOptVATRate, deprecated); err == nil {
		cfg.VATRate = v
		switch {
		case v > 1:
			err = fmt.Errorf("%s: must be a fraction between 0 and 1, got %v; did you mean %v?", keyOptVATRate, v, v/100)
		case !(v >= 0):
			err = fmt.Errorf("%s: must be a fraction between 0 and 1, got %v", keyOptVATRate, v)
		}
	}
	addErr(err)

	if v, err = optCfgFloatKey(keyOptSellMargin, defOptSellMargin, deprecated); err == nil {
		cfg.SellMargin = v
		if !(v >= optCfgSurchargeMin && v <= optCfgSurchargeMax) {
			err = fmt.Errorf("%s: must be between %v and %v, got %v", keyOptSellMargin, optCfgSurchargeMin, optCfgSurchargeMax, v)
		}
	}
	addErr(err)

	if v, err = optCfgFloatKey(keyOptMinGain, defOptMinGain, deprecated); err == nil {
		cfg.MinGain = v
		if !(v >= 0 && v <= optimize.MaxMinGain) {
			err = fmt.Errorf("%s: must be between 0 and %v currency per hour, got %v", keyOptMinGain, optimize.MaxMinGain, v)
		}
	}
	addErr(err)

	if v, err = optCfgFloatKey(keyOptWearCost, defOptWearCost, deprecated); err == nil {
		cfg.WearCost = v
		if !(v >= 0 && v <= optimize.MaxWearCost) {
			err = fmt.Errorf("%s: must be between 0 and %v currency per kWh, got %v", keyOptWearCost, optimize.MaxWearCost, v)
		}
	}
	addErr(err)

	if v, err = optCfgFloatKey(keyOptEVGuardW, defOptEVGuardW, deprecated); err == nil {
		cfg.EVGuardW = v
		if !(v >= 0 && v <= optCfgMaxEVGuardW) {
			err = fmt.Errorf("%s: must be between 0 and %v W, got %v", keyOptEVGuardW, optCfgMaxEVGuardW, v)
		}
	}
	addErr(err)

	cfg.HistoryDays = defOptHistoryDays
	if hd := optGet(keyOptHistoryDays, deprecated); hd != nil {
		var n int
		if n, err = optCfgInt(keyOptHistoryDays, hd); err == nil {
			cfg.HistoryDays = n
			if n < 1 || n > 30 {
				err = fmt.Errorf("%s: must be between 1 and 30, got %d", keyOptHistoryDays, n)
			}
		}
		addErr(err)
	}

	if v, err = optCfgFloatKey("pw_consumption", 0, deprecated); err == nil {
		cfg.StaticDailyWh = v
		if !(v > 0) {
			err = fmt.Errorf("pw_consumption: must be greater than 0 in scheduler_mode=optimize (static consumption fallback), got %v", v)
		}
	}
	addErr(err)

	cfg.Surcharge, err = parseSurcharge(optGet(keyOptPurchaseSurcharge, deprecated))
	addErr(err)
	warnings = append(warnings, optCfgStaticIgnored(cfg)...)

	cfg.DryRun = defOptDryRun
	if dr := optGet(keyOptDryRun, deprecated); dr != nil {
		cfg.DryRun, err = optCfgBool(keyOptDryRun, dr)
		if err != nil {
			cfg.DryRun = true // fail safe: never go live on a malformed value
		}
		addErr(err)
	}

	if len(problems) > 0 {
		return cfg, warnings, &optimizeConfigError{Problems: problems}
	}
	return cfg, warnings, nil
}

// optCfgPriceSource reads optimizer.price_source and validates the inputs of that source.
func optCfgPriceSource(cfg *OptimizeConfig, deprecated func(string)) (problems, warnings []string) {
	addErr := func(err error) {
		if err != nil {
			problems = append(problems, err.Error())
		}
	}
	cfg.PriceSource = optGetString(keyOptPriceSource, deprecated)
	if cfg.PriceSource == "" {
		cfg.PriceSource = defOptPriceSource
	}
	switch cfg.PriceSource {
	case optPriceSourceNordpool:
		addErr(optCfgCheckEntity(keyOptPriceEntity, cfg.PriceEntity, true))
	case optPriceSourceStatic:
		var err error
		cfg.Tariff.Buy, err = optCfgTariffPrices(keyOptStaticBuyPrice, deprecated)
		addErr(err)
		cfg.Tariff.Sell, err = optCfgTariffPrices(keyOptStaticSellPrice, deprecated)
		addErr(err)
		cfg.Tariff.Currency, err = optCfgCurrency(optGetString(keyOptStaticCurrency, deprecated))
		addErr(err)
		if len(problems) == 0 {
			warnings = optCfgTariffSwapped(cfg.Tariff)
		}
	default:
		problems = append(problems, fmt.Sprintf("%s: %q must be %s or %s", keyOptPriceSource, cfg.PriceSource, optPriceSourceNordpool, optPriceSourceStatic))
	}
	return problems, warnings
}

// optCfgTariffSwapped warns when the sell list averages above the buy list.
func optCfgTariffSwapped(t optimize.Tariff) []string {
	var buy, sell float64
	for h := range t.Buy {
		buy += t.Buy[h]
		sell += t.Sell[h]
	}
	if sell <= buy {
		return nil
	}
	return []string{fmt.Sprintf("%s is on average higher than %s; check that the two lists are not swapped", keyOptStaticSellPrice, keyOptStaticBuyPrice)}
}

// optCfgTariffPrices reads one required 24-value price list of the static tariff.
func optCfgTariffPrices(key string, deprecated func(string)) ([24]float64, error) {
	vals, set, err := parseHourly(key, optGet(key, deprecated), optimize.MinPriceKWh, optimize.MaxPriceKWh)
	if err == nil && !set {
		err = fmt.Errorf("%s: required when %s=%s (24 comma-separated numbers, hour 00..23)", key, keyOptPriceSource, optPriceSourceStatic)
	}
	return vals, err
}

// optCfgCurrency validates the optional currency label of the static tariff.
func optCfgCurrency(c string) (string, error) {
	if utf8.RuneCountInString(c) > optCfgMaxCurrencyLen || strings.ContainsFunc(c, unicode.IsSpace) {
		return "", fmt.Errorf("%s: %q must be at most %d characters without spaces, e.g. AUD", keyOptStaticCurrency, c, optCfgMaxCurrencyLen)
	}
	return c, nil
}

// optCfgStaticIgnored warns about spot-price settings that a static tariff does not use.
func optCfgStaticIgnored(cfg OptimizeConfig) []string {
	if cfg.PriceSource != optPriceSourceStatic {
		return nil
	}
	var out []string
	// vat_rate is left out: the add-on always sends its default.
	if cfg.SellMargin != 0 {
		out = append(out, fmt.Sprintf("%s is ignored when %s=%s; %s is the sell price", keyOptSellMargin, keyOptPriceSource, optPriceSourceStatic, keyOptStaticSellPrice))
	}
	if cfg.Surcharge != ([24]float64{}) {
		out = append(out, fmt.Sprintf("%s is ignored when %s=%s; include it in %s", keyOptPurchaseSurcharge, keyOptPriceSource, optPriceSourceStatic, keyOptStaticBuyPrice))
	}
	return out
}

// parseSurcharge normalizes opt_purchase_surcharge (UX §2.2) into 24 hourly
// values. nil or an empty string yields all zeros.
func parseSurcharge(v any) ([24]float64, error) {
	out, _, err := parseHourly(keyOptPurchaseSurcharge, v, optCfgSurchargeMin, optCfgSurchargeMax)
	return out, err
}

// parseHourly normalizes a 24-value hourly option, each value in [lo, hi]; set is false for nil or empty.
func parseHourly(key string, v any, lo, hi float64) (out [24]float64, set bool, err error) {
	var items []string
	switch t := v.(type) {
	case nil:
		return out, false, nil
	case string:
		if strings.TrimSpace(t) == "" {
			return out, false, nil
		}
		items = strings.Split(t, ",")
	case []string:
		items = t
	case []any:
		if len(t) != optCfgSurchargeHours {
			return out, true, optCfgHourlyCountErr(key, len(t))
		}
		items = make([]string, len(t))
		for i, e := range t {
			s, ok := optCfgScalarString(e)
			if !ok {
				return out, true, fmt.Errorf("%s: item %d (%v) is not a number", key, i+1, e)
			}
			items[i] = s
		}
	case []float64:
		items = make([]string, len(t))
		for i, e := range t {
			items[i] = strconv.FormatFloat(e, 'g', -1, 64)
		}
	default:
		return out, true, fmt.Errorf("%s: unsupported value type %T; use 24 comma-separated numbers or a YAML list", key, v)
	}

	if len(items) != optCfgSurchargeHours {
		return out, true, optCfgHourlyCountErr(key, len(items))
	}
	for i, raw := range items {
		s := strings.TrimSpace(raw)
		f, err := strconv.ParseFloat(s, 64)
		if s == "" || err != nil {
			return out, true, fmt.Errorf("%s: item %d (%q) is not a number", key, i+1, s)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || f < lo || f > hi {
			return out, true, fmt.Errorf("%s: item %d (%q) must be a finite number between %v and %v", key, i+1, s, lo, hi)
		}
		out[i] = f
	}
	return out, true, nil
}

func optCfgHourlyCountErr(key string, got int) error {
	return fmt.Errorf("%s: expected exactly 24 comma-separated numbers (hour 00..23), got %d; use '.' as decimal separator", key, got)
}

// optCfgEntityExample returns a typical entity ID for the given key, used in
// the "required" validation message.
func optCfgEntityExample(key string) string {
	if key == keyOptConsumptionEntity {
		return "sensor.solarnet_power_load_consumed"
	}
	return "sensor.nordpool_kwh_dk2_dkk_3_10_025"
}

// optCfgCheckEntity validates an opt_*_entity value.
func optCfgCheckEntity(key, id string, required bool) error {
	if id == "" {
		if required {
			return fmt.Errorf("%s: required when scheduler_mode=optimize (e.g. %s)", key, optCfgEntityExample(key))
		}
		return nil
	}
	if !optCfgEntityPattern.MatchString(id) {
		return fmt.Errorf("%s: %q must match %s", key, id, optCfgEntityPattern.String())
	}
	if err := homeassistant.ValidateEntityID(id); err != nil {
		return fmt.Errorf("%s: %q is not a valid entity id", key, id)
	}
	return nil
}

// optCfgFloatKey reads a float key, returning def when it has no value
// (neither flag default, env, yaml nor explicit set). Unlike
// viper.GetFloat64 it reports malformed and non-finite values.
func optCfgFloatKey(key string, def float64, deprecated func(string)) (float64, error) {
	v := optGet(key, deprecated)
	if v == nil {
		return def, nil
	}
	return optCfgFloat(key, v)
}

func optCfgFloat(key string, v any) (float64, error) {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case float32:
		f = float64(t)
	case int:
		f = float64(t)
	case int32:
		f = float64(t)
	case int64:
		f = float64(t)
	case uint64:
		f = float64(t)
	case string:
		p, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %q is not a number; use '.' as decimal separator", key, t)
		}
		f = p
	default:
		return 0, fmt.Errorf("%s: unsupported value type %T", key, v)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%s: must be a finite number", key)
	}
	return f, nil
}

func optCfgInt(key string, v any) (int, error) {
	switch t := v.(type) {
	case int:
		return t, nil
	case int32:
		return int(t), nil
	case int64:
		return int(t), nil
	case uint64:
		return int(t), nil
	case float64:
		if t != math.Trunc(t) || math.IsInf(t, 0) || math.IsNaN(t) {
			return 0, fmt.Errorf("%s: must be a whole number, got %v", key, t)
		}
		return int(t), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, fmt.Errorf("%s: %q is not a whole number", key, t)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s: unsupported value type %T", key, v)
	}
}

// optCfgBool parses strictly: a typo must not silently turn into false
// (which for opt_dry_run would mean live writes).
func optCfgBool(key string, v any) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		if err != nil {
			return false, fmt.Errorf("%s: %q is not true or false", key, t)
		}
		return b, nil
	default:
		return false, fmt.Errorf("%s: unsupported value type %T", key, v)
	}
}

// optCfgScalarString renders a YAML/JSON list element as text for parsing.
func optCfgScalarString(e any) (string, bool) {
	switch t := e.(type) {
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(t), 'g', -1, 32), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case uint64:
		return strconv.FormatUint(t, 10), true
	default:
		return "", false
	}
}

// optCfgSplitList splits a comma-separated list, trimming and dropping empties.
func optCfgSplitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
