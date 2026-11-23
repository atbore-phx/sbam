package cmd

import (
	"fmt"
	"os"
	"strings"

	"sbam/pkg/homeassistant"
	pw "sbam/pkg/power"
	u "sbam/src/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Viper keys for the PV forecast source (SPEC-0003, UX §2.1).
const (
	keyPVSource           = "pv_source"
	keyPVEntityToday      = "pv_entity_today"
	keyPVEntityTomorrow   = "pv_entity_tomorrow"
	keyPVEntityLastPolled = "pv_entity_last_polled"
)

// Defaults of the pv_* keys: the integration's English entity IDs.
const (
	defPVSource           = pw.PVSourceSolcastAPI
	defPVEntityToday      = "sensor.solcast_pv_forecast_forecast_today"
	defPVEntityTomorrow   = "sensor.solcast_pv_forecast_forecast_tomorrow"
	defPVEntityLastPolled = "sensor.solcast_pv_forecast_api_last_polled"
)

// Shared help texts (UX §2.1) so schedule, estimate and optimize cannot drift.
const (
	helpSolcastURL    = "Solcast forecast URL(s), comma-separated (required with pv_source=solcast_api)"
	helpSolcastAPIKey = "Solcast API key (required with pv_source=solcast_api)"
	// sbam optimize never required url/apikey (AC-1), so its help says "used".
	helpOptimizeSolcastURL    = "Solcast forecast URL(s), comma-separated (used with pv_source=solcast_api)"
	helpOptimizeSolcastAPIKey = "Solcast API key (used with pv_source=solcast_api)"
	helpHAURL                 = "Home Assistant base URL for scheduler_mode=optimize or pv_source=ha_solcast (not needed in the HA add-on)"
	helpHAToken               = "HA long-lived access token (prefer HA_TOKEN env var)"
)

// Startup messages (UX §3.1).
const (
	msgPVSourceStartup = "PV forecast source"
	msgPVKeysIgnored   = "url and apikey are ignored with pv_source=ha_solcast; sbam makes no Solcast requests"
)

// HA access messages, shared word for word by optimize mode and the PV
// source (UX §5.2, AC-6).
const (
	msgHAAccessMissing  = "home assistant access: no Supervisor token found and " + keyHAURL + "/" + keyHAToken + " are not set; set HA_URL and HA_TOKEN (Docker/CLI) or enable homeassistant_api (add-on)"
	msgHATokenRequired  = keyHAToken + ": required when " + keyHAURL + " is set"
	msgHAPlainHTTPWarn  = keyHAURL + " uses plain http to a non-private host; the HA token is sent unencrypted, use https"
	haAccessSupervisor  = homeassistant.SourceSupervisor
	haAccessURL         = homeassistant.SourceURL
	pvLastPolledOffText = "off"
)

// PVSourceConfig is the validated PV forecast source configuration.
type PVSourceConfig struct {
	// Source is pw.PVSourceSolcastAPI or pw.PVSourceHASolcast. It is empty
	// only when pv_source failed validation.
	Source string
	// Entities are the integration's entities (validated only for ha_solcast).
	Entities pw.HAForecastEntities
	// HAURL and HAToken are the raw ha_url/ha_token values.
	HAURL   string
	HAToken string
	// Sites is the number of configured Solcast URLs.
	Sites int
	// IgnoredKeys names url/apikey when they are set with ha_solcast (AC-5).
	// Only key names are kept, never values (S-027 d).
	IgnoredKeys []string
}

// isHA reports whether the forecast comes from the HA Solcast integration.
func (c PVSourceConfig) isHA() bool {
	return c.Source == pw.PVSourceHASolcast
}

// haAccess returns how HA is reached: "url" when ha_url is set, otherwise
// "supervisor" (the token and URL are never logged, S-025 c).
func (c PVSourceConfig) haAccess() string {
	if strings.TrimSpace(c.HAURL) != "" {
		return haAccessURL
	}
	return haAccessSupervisor
}

// registerHAAccessFlags registers --ha_url and --ha_token, bound to the
// nested optimizer.ha_url / optimizer.ha_token keys (HA_URL / HA_TOKEN in the
// environment). estimate gets only these two, not the whole opt_* set
// (UX §2.1); schedule and optimize get them through registerOptimizeFlags.
func registerHAAccessFlags(cmd *cobra.Command) {
	bindOptimizeEnv()
	f := cmd.Flags()
	for _, k := range []struct{ key, help string }{{keyHAURL, helpHAURL}, {keyHAToken, helpHAToken}} {
		spec, _ := optSpecByKey(k.key)
		f.String(spec.flag, "", k.help)
		_ = f.SetAnnotation(spec.flag, flagAnnotationViperKey, []string{spec.key})
	}
}

// registerPVSourceFlags registers the four long-only pv_* flags on
// schedule, estimate and optimize (UX §2.1).
func registerPVSourceFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String(keyPVSource, defPVSource, "PV forecast source: solcast_api (download from Solcast with url/apikey) or ha_solcast (read the Home Assistant Solcast PV Forecast integration)")
	f.String(keyPVEntityToday, defPVEntityToday, "Solcast integration sensor with today's forecast (pv_source=ha_solcast)")
	f.String(keyPVEntityTomorrow, defPVEntityTomorrow, "Solcast integration sensor with tomorrow's forecast (pv_source=ha_solcast)")
	f.String(keyPVEntityLastPolled, defPVEntityLastPolled, "Solcast integration 'API last polled' sensor; forecasts older than 24 h are not used; empty disables the check (pv_source=ha_solcast)")
}

// pvSourceConfigError aggregates the PV configuration problems of classic
// modes and estimate into one message (UX §5.2, AC-11).
type pvSourceConfigError struct {
	Problems []string
}

func (e *pvSourceConfigError) Error() string {
	noun := "problems"
	if len(e.Problems) == 1 {
		noun = "problem"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "invalid PV forecast configuration (%d %s):", len(e.Problems), noun)
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	return b.String()
}

// haAccessProblems resolves Home Assistant access (Supervisor token via
// getenv, or ha_url + ha_token). It is the one place that produces the access
// problems and the plain-HTTP warning, for optimize mode and for
// pv_source=ha_solcast (S-025 a, UX A-2).
func haAccessProblems(haURL, haToken string, getenv func(string) string) (homeassistant.Config, []string, []string) {
	haURL, haToken = strings.TrimSpace(haURL), strings.TrimSpace(haToken)
	access, err := homeassistant.ResolveAccess(haURL, haToken, getenv)
	switch {
	case err == nil:
		if access.PlainHTTPToPublicHost() {
			return access, nil, []string{msgHAPlainHTTPWarn}
		}
		return access, nil, nil
	case haURL != "" && haToken == "":
		return access, []string{msgHATokenRequired}, nil
	case haURL == "":
		return access, []string{msgHAAccessMissing}, nil
	default:
		return access, []string{keyHAURL + ": " + strings.TrimPrefix(err.Error(), "homeassistant: ")}, nil
	}
}

// loadPVSourceConfig reads the PV source configuration from viper. It
// returns the problems (empty when valid) and the warnings to log at Warn.
//
// checkHAAccess is false in optimize mode, where optCfgLoad reports the HA
// access problem already, so it appears once (UX A-2). The pv_entity_* keys
// are validated only for ha_solcast, so a solcast_api user is never blocked
// by them (UX decision 2).
func loadPVSourceConfig(getenv func(string) string, checkHAAccess bool) (PVSourceConfig, []string, []string) {
	var problems, warnings []string
	cfg := PVSourceConfig{
		HAURL:   optGetString(keyHAURL, nil),
		HAToken: optGetString(keyHAToken, nil),
		Entities: pw.HAForecastEntities{
			Today:      pvCfgString(keyPVEntityToday, defPVEntityToday),
			Tomorrow:   pvCfgString(keyPVEntityTomorrow, defPVEntityTomorrow),
			LastPolled: pvCfgString(keyPVEntityLastPolled, defPVEntityLastPolled),
		},
		Sites: len(optCfgSplitList(viper.GetString("url"))),
	}

	raw := pvCfgString(keyPVSource, defPVSource)
	if raw == "" {
		raw = defPVSource
	}
	src, err := pw.ValidatePVSource(raw)
	if err != nil {
		problems = append(problems, fmt.Sprintf("%s: must be %s or %s, got %q", keyPVSource, pw.PVSourceSolcastAPI, pw.PVSourceHASolcast, raw))
	}
	cfg.Source = src

	if cfg.isHA() {
		problems = append(problems, pvCfgCheckEntity(keyPVEntityToday, cfg.Entities.Today, defPVEntityToday, true)...)
		problems = append(problems, pvCfgCheckEntity(keyPVEntityTomorrow, cfg.Entities.Tomorrow, defPVEntityTomorrow, true)...)
		problems = append(problems, pvCfgCheckEntity(keyPVEntityLastPolled, cfg.Entities.LastPolled, defPVEntityLastPolled, false)...)
		if checkHAAccess {
			_, accessProblems, accessWarnings := haAccessProblems(cfg.HAURL, cfg.HAToken, getenv)
			problems = append(problems, accessProblems...)
			warnings = append(warnings, accessWarnings...)
		}
		if strings.TrimSpace(viper.GetString("url")) != "" {
			cfg.IgnoredKeys = append(cfg.IgnoredKeys, "url")
		}
		if strings.TrimSpace(viper.GetString("apikey")) != "" {
			cfg.IgnoredKeys = append(cfg.IgnoredKeys, "apikey")
		}
	}
	return cfg, problems, warnings
}

// loadPVSourceConfigStandalone is loadPVSourceConfig for classic schedule
// modes and estimate: the HA access check is included, problems are
// returned as one pvSourceConfigError and warnings are logged at Warn.
func loadPVSourceConfigStandalone(getenv func(string) string) (PVSourceConfig, error) {
	cfg, problems, warnings := loadPVSourceConfig(getenv, true)
	for _, w := range warnings {
		u.Log.Warn(w)
	}
	if len(problems) > 0 {
		return cfg, &pvSourceConfigError{Problems: problems}
	}
	return cfg, nil
}

// loadPVSourceConfigEnv is loadPVSourceConfigStandalone with os.Getenv. The
// Supervisor token is read only via os.Getenv, never via viper (S-013).
func loadPVSourceConfigEnv() (PVSourceConfig, error) {
	return loadPVSourceConfigStandalone(os.Getenv)
}

// pvCfgString returns the trimmed value of key, or def when the key has no
// value at all (no flag bound, no env, no yaml). An explicitly empty value
// stays empty.
func pvCfgString(key, def string) string {
	if viper.Get(key) == nil {
		return def
	}
	return strings.TrimSpace(viper.GetString(key))
}

// pvCfgCheckEntity validates a pv_entity_* value with the same rules as the
// opt_*_entity keys (S-027 b).
func pvCfgCheckEntity(key, id, example string, required bool) []string {
	if id == "" {
		if required {
			return []string{fmt.Sprintf("%s: required when pv_source=ha_solcast (e.g. %s)", key, example)}
		}
		return nil
	}
	if !optCfgEntityPattern.MatchString(id) {
		if required {
			return []string{fmt.Sprintf("%s: %q must match %s", key, id, optCfgEntityPattern.String())}
		}
		return []string{fmt.Sprintf("%s: %q must match %s or be empty", key, id, optCfgEntityPattern.String())}
	}
	if err := homeassistant.ValidateEntityID(id); err != nil {
		return []string{fmt.Sprintf("%s: %q is not a valid entity id", key, id)}
	}
	return nil
}

// logPVSourceStartup writes the AC-10 startup Info line and, with
// ha_solcast, the AC-5 "ignored" line naming set keys only (UX §3.1).
func logPVSourceStartup(cfg PVSourceConfig) {
	if !cfg.isHA() {
		u.Log.Infow(msgPVSourceStartup, "pv_source", pw.PVSourceSolcastAPI, "sites", cfg.Sites)
		return
	}
	lastPolled := cfg.Entities.LastPolled
	if lastPolled == "" {
		lastPolled = pvLastPolledOffText
	}
	u.Log.Infow(msgPVSourceStartup,
		"pv_source", pw.PVSourceHASolcast,
		keyPVEntityToday, cfg.Entities.Today,
		keyPVEntityTomorrow, cfg.Entities.Tomorrow,
		keyPVEntityLastPolled, lastPolled,
		"ha_access", cfg.haAccess(),
	)
	if len(cfg.IgnoredKeys) > 0 {
		u.Log.Infow(msgPVKeysIgnored, "ignored", strings.Join(cfg.IgnoredKeys, ","))
	}
}
