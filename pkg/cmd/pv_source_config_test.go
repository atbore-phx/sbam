package cmd

import (
	"strings"
	"testing"

	pw "sbam/pkg/power"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pvCfgSetHA sets pv_source=ha_solcast with HA access via ha_url/ha_token.
func pvCfgSetHA(t *testing.T) {
	t.Helper()
	resetViper(t)
	viper.Set(keyPVSource, pw.PVSourceHASolcast)
	viper.Set(keyHAURL, "http://homeassistant.local:8123")
	viper.Set(keyHAToken, pvTestHAToken)
}

func pvCfgProblems(t *testing.T, err error) []string {
	t.Helper()
	require.Error(t, err)
	var ce *pvSourceConfigError
	require.ErrorAs(t, err, &ce)
	return ce.Problems
}

// T-0319 (AC-11, AC-5, S-027): aggregated PV validation.
func TestPVSourceConfig_Validation_T0319(t *testing.T) {
	t.Run("a: misspelled pv_source", func(t *testing.T) {
		resetViper(t)
		viper.Set(keyPVSource, "HA-Solcast")
		cfg, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		assert.Equal(t, []string{`pv_source: must be solcast_api or ha_solcast, got "HA-Solcast"`}, pvCfgProblems(t, err))
		assert.Empty(t, cfg.Source)
		assert.Contains(t, err.Error(), "invalid PV forecast configuration (1 problem):")
	})

	t.Run("b-d: entity problems are listed together", func(t *testing.T) {
		pvCfgSetHA(t)
		viper.Set(keyPVEntityToday, "sensor.x/../../config")
		viper.Set(keyPVEntityTomorrow, "light.kitchen")
		viper.Set(keyPVEntityLastPolled, "binary_sensor.x")
		_, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		assert.Equal(t, []string{
			`pv_entity_today: "sensor.x/../../config" must match ^sensor\.[a-z0-9_]+$`,
			`pv_entity_tomorrow: "light.kitchen" must match ^sensor\.[a-z0-9_]+$`,
			`pv_entity_last_polled: "binary_sensor.x" must match ^sensor\.[a-z0-9_]+$ or be empty`,
		}, pvCfgProblems(t, err))
		assert.True(t, strings.HasPrefix(err.Error(), "invalid PV forecast configuration (3 problems):\n  - pv_entity_today"))
	})

	t.Run("d: empty today and tomorrow are required", func(t *testing.T) {
		pvCfgSetHA(t)
		viper.Set(keyPVEntityToday, " ")
		viper.Set(keyPVEntityTomorrow, "")
		_, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		assert.Equal(t, []string{
			"pv_entity_today: required when pv_source=ha_solcast (e.g. sensor.solcast_pv_forecast_forecast_today)",
			"pv_entity_tomorrow: required when pv_source=ha_solcast (e.g. sensor.solcast_pv_forecast_forecast_tomorrow)",
		}, pvCfgProblems(t, err))
	})

	t.Run("e: empty last_polled is accepted", func(t *testing.T) {
		pvCfgSetHA(t)
		viper.Set(keyPVEntityLastPolled, "")
		cfg, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		assert.Empty(t, cfg.Entities.LastPolled)
		assert.Equal(t, defPVEntityToday, cfg.Entities.Today)
		assert.Equal(t, defPVEntityTomorrow, cfg.Entities.Tomorrow)
	})

	t.Run("f: solcast_api keeps the apikey/url checks", func(t *testing.T) {
		withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 0)
		withEstimateCacheTime(t, 0)
		a := validScheduleValidationArgs()
		err := checkScheduleschedule(a.crontab, "", a.url, a.froniusIP, a.pwConsumption, a.maxCharge, a.pwBattReserve, a.startHr, a.endHr, nil, a.schedulerMode, true, pw.PVSourceSolcastAPI)
		assert.EqualError(t, err, "the --apikey flag must be set")
		assert.EqualError(t, CheckEstimate("", a.url, a.froniusIP, pw.PVSourceSolcastAPI), "the --apikey flag must be set")
		// An invalid source is not a reason to skip them.
		err = checkScheduleschedule(a.crontab, a.apiKey, "", a.froniusIP, a.pwConsumption, a.maxCharge, a.pwBattReserve, a.startHr, a.endHr, nil, a.schedulerMode, true, "")
		assert.EqualError(t, err, "the --url flag must be set")
	})

	t.Run("g: ha_solcast needs neither apikey nor url", func(t *testing.T) {
		withScheduleValidationGlobals(t, "05:00", "06:00", 0, 0, 0)
		withEstimateCacheTime(t, 0)
		a := validScheduleValidationArgs()
		for _, mode := range []string{"crontab", schedulerModeOptimize} {
			err := checkScheduleschedule(a.crontab, "", "", a.froniusIP, a.pwConsumption, a.maxCharge, a.pwBattReserve, a.startHr, a.endHr, nil, mode, true, pw.PVSourceHASolcast)
			assert.NoError(t, err, mode)
		}
		assert.NoError(t, CheckEstimate("", "", a.froniusIP, pw.PVSourceHASolcast))
		assert.EqualError(t, CheckEstimate("", "", "", pw.PVSourceHASolcast), "the --fronius_ip flag must be set")

		pvCfgSetHA(t)
		_, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		assert.NoError(t, err)
	})

	t.Run("h: solcast_api ignores the entity keys", func(t *testing.T) {
		resetViper(t)
		viper.Set(keyPVSource, pw.PVSourceSolcastAPI)
		viper.Set(keyPVEntityToday, "garbage")
		viper.Set(keyPVEntityLastPolled, "light.x")
		cfg, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		assert.Equal(t, pw.PVSourceSolcastAPI, cfg.Source)
	})

	t.Run("pv_source is trimmed; empty means the default", func(t *testing.T) {
		resetViper(t)
		viper.Set(keyPVSource, "  ")
		cfg, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		assert.Equal(t, pw.PVSourceSolcastAPI, cfg.Source)

		pvCfgSetHA(t)
		viper.Set(keyPVSource, " ha_solcast ")
		cfg, err = loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		assert.Equal(t, pw.PVSourceHASolcast, cfg.Source)
	})

	t.Run("optimize mode: PV problems join the optimize list", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		viper.Set(keyPVEntityToday, "light.kitchen")
		viper.Set(keyOptMinGain, -1.0)
		cfg, _, err := optCfgLoad(optCfgNoEnv)
		problems := optCfgProblems(t, err)
		require.Len(t, problems, 2)
		assert.Contains(t, problems[0], keyOptMinGain)
		assert.Equal(t, `pv_entity_today: "light.kitchen" must match ^sensor\.[a-z0-9_]+$`, problems[1])
		assert.Contains(t, err.Error(), "invalid configuration for scheduler_mode=optimize (2 problems):")
		assert.Equal(t, pw.PVSourceHASolcast, cfg.PV.Source)
	})

	t.Run("optimize mode fills cfg.PV", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		viper.Set(keyPVEntityLastPolled, "")
		cfg, _, err := optCfgLoad(optCfgNoEnv)
		require.NoError(t, err)
		assert.Equal(t, pw.HAForecastEntities{Today: defPVEntityToday, Tomorrow: defPVEntityTomorrow}, cfg.PV.Entities)
	})
}

// T-0318 (AC-6, S-023, S-025): HA access resolution for the PV source.
func TestPVSourceConfig_HAAccess_T0318(t *testing.T) {
	t.Run("a: Supervisor token", func(t *testing.T) {
		resetViper(t)
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		cfg, err := loadPVSourceConfigStandalone(optCfgSupervisorEnv)
		require.NoError(t, err)
		assert.Equal(t, "supervisor", cfg.haAccess())
	})

	t.Run("b: ha_url and ha_token", func(t *testing.T) {
		pvCfgSetHA(t)
		cfg, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		assert.Equal(t, "url", cfg.haAccess())
	})

	t.Run("c: no access names ha_url/ha_token", func(t *testing.T) {
		resetViper(t)
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		_, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		assert.Equal(t, []string{msgHAAccessMissing}, pvCfgProblems(t, err))
		assert.Contains(t, err.Error(), keyHAURL+"/"+keyHAToken)
	})

	t.Run("c: reported once in optimize mode", func(t *testing.T) {
		optCfgValidBase(t)
		viper.Set(keyHAURL, "")
		viper.Set(keyHAToken, "")
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		_, _, err := optCfgLoad(optCfgNoEnv)
		assert.Equal(t, []string{msgHAAccessMissing}, optCfgProblems(t, err))
	})

	t.Run("d: ha_url without ha_token", func(t *testing.T) {
		pvCfgSetHA(t)
		viper.Set(keyHAToken, "")
		_, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		assert.Equal(t, []string{msgHATokenRequired}, pvCfgProblems(t, err))
	})

	t.Run("e: plain http to a public host warns once", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		pvCfgSetHA(t)
		viper.Set(keyHAURL, "http://example.com")
		_, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		assert.Equal(t, 1, logs.FilterMessage(msgHAPlainHTTPWarn).Len())

		// Optimize mode: the warning comes from optCfgLoad only.
		optCfgValidBase(t)
		viper.Set(keyHAURL, "http://example.com")
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		_, warnings, err := optCfgLoad(optCfgNoEnv)
		require.NoError(t, err)
		assert.Equal(t, []string{msgHAPlainHTTPWarn}, warnings)
	})

	t.Run("solcast_api does not need HA access", func(t *testing.T) {
		resetViper(t)
		_, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		assert.NoError(t, err)
	})

	t.Run("haAccessProblems keeps the optimize messages", func(t *testing.T) {
		_, problems, _ := haAccessProblems("ftp://ha.local", "tok", optCfgNoEnv)
		require.Len(t, problems, 1)
		assert.True(t, strings.HasPrefix(problems[0], keyHAURL+": "))
	})

	// f (no startup probe) is covered by TestNewPVDaySource_HA.
}

// T-0320 (AC-10, UX §3.1): startup lines; the optimize fingerprint is in
// TestHAForecastLoader.
func TestPVSource_LogLines_T0320(t *testing.T) {
	t.Run("a: solcast_api with two sites", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		resetViper(t)
		viper.Set("url", "https://a.example.invalid, https://b.example.invalid")
		cfg, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		logPVSourceStartup(cfg)

		entries := logs.FilterMessage(msgPVSourceStartup).All()
		require.Len(t, entries, 1)
		assert.Equal(t, map[string]any{"pv_source": "solcast_api", "sites": int64(2)}, entries[0].ContextMap())
		assert.Zero(t, logs.FilterMessage(msgPVKeysIgnored).Len())
	})

	t.Run("b: ha_solcast via Supervisor, last_polled off, ignored keys named only", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		resetViper(t)
		viper.Set(keyPVSource, pw.PVSourceHASolcast)
		viper.Set(keyPVEntityLastPolled, "")
		viper.Set("apikey", "secret-api-key-value")
		viper.Set("url", "https://solcast.example.invalid/rooftop_sites/abc")
		cfg, err := loadPVSourceConfigStandalone(optCfgSupervisorEnv)
		require.NoError(t, err)
		logPVSourceStartup(cfg)

		entries := logs.FilterMessage(msgPVSourceStartup).All()
		require.Len(t, entries, 1)
		assert.Equal(t, map[string]any{
			"pv_source":             "ha_solcast",
			"pv_entity_today":       defPVEntityToday,
			"pv_entity_tomorrow":    defPVEntityTomorrow,
			"pv_entity_last_polled": "off",
			"ha_access":             "supervisor",
		}, entries[0].ContextMap())

		ignored := logs.FilterMessage(msgPVKeysIgnored).All()
		require.Len(t, ignored, 1)
		assert.Equal(t, map[string]any{"ignored": "url,apikey"}, ignored[0].ContextMap())

		for _, e := range logs.All() {
			line := e.Message + " " + strings.Join(pvTestFieldValues(e.ContextMap()), " ")
			assert.NotContains(t, line, "secret-api-key-value")
			assert.NotContains(t, line, "solcast.example.invalid")
			assert.NotContains(t, line, "test-sup-token")
		}
	})

	t.Run("b: only set keys are named", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		pvCfgSetHA(t)
		viper.Set("url", "https://solcast.example.invalid/x")
		cfg, err := loadPVSourceConfigStandalone(optCfgNoEnv)
		require.NoError(t, err)
		logPVSourceStartup(cfg)
		ignored := logs.FilterMessage(msgPVKeysIgnored).All()
		require.Len(t, ignored, 1)
		assert.Equal(t, "url", ignored[0].ContextMap()["ignored"])
		entries := logs.FilterMessage(msgPVSourceStartup).All()
		require.Len(t, entries, 1)
		assert.Equal(t, "url", entries[0].ContextMap()["ha_access"])
		assert.NotContains(t, pvTestFieldValues(entries[0].ContextMap()), "http://homeassistant.local:8123")
	})

	t.Run("c: optimize mode active reports forecast_cache per source", func(t *testing.T) {
		logs := pvTestObserveLogs(t)
		logOptimizeModeActive(OptimizeConfig{})
		logOptimizeModeActive(OptimizeConfig{PV: PVSourceConfig{Source: pw.PVSourceHASolcast}})
		entries := logs.FilterMessage("optimize mode active").All()
		require.Len(t, entries, 2)
		assert.Equal(t, true, entries[0].ContextMap()["forecast_cache"])
		assert.Equal(t, false, entries[1].ContextMap()["forecast_cache"])
	})
}

func pvTestFieldValues(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
