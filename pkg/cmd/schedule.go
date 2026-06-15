package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	pw "sbam/pkg/power"
	"sbam/pkg/storage"
	u "sbam/src/utils"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v3"
)

var s_apiKey, s_url, start_hr, end_hr, batt_reserve_start_hr, batt_reserve_end_hr, w_start_hr, w_end_hr,
	crontab, s_cache_file_prefix, mqtt_tls_ca_file, mqtt_tls_client_cert, mqtt_tls_client_cert_key,
	forecast_horizon, consumption_horizon, scheduler_mode string
var pw_consumption, max_charge, pw_lwt, pw_upt, pw_batt_reserve float64
var s_cache_time int32
var s_defaults, s_cache_forecast, mqtt_enabled, mqtt_tls_insecure_skip bool

// mqttOptionalConfig carries the nested MQTT parameters from the
// mqtt_optional_config config key / CLI flag / env var. Fields use both
// yaml (for flag/env unmarshalling) and mapstructure (for viper.UnmarshalKey)
// tags.
type mqttOptionalConfig struct {
	Broker            string `yaml:"broker" mapstructure:"broker"`
	ClientID          string `yaml:"client_id" mapstructure:"client_id"`
	Username          string `yaml:"username" mapstructure:"username"`
	Password          string `yaml:"password" mapstructure:"password"`
	TopicPrefix       string `yaml:"topic_prefix" mapstructure:"topic_prefix"`
	HADiscovery       bool   `yaml:"ha_discovery" mapstructure:"ha_discovery"`
	HADiscoveryPrefix string `yaml:"ha_discovery_prefix" mapstructure:"ha_discovery_prefix"`
}

// mqttOptsDefaults returns an mqttOptionalConfig populated with the
// same default values as the current config.json.
func mqttOptsDefaults() mqttOptionalConfig {
	return mqttOptionalConfig{
		TopicPrefix:       "sbam",
		HADiscovery:       true,
		HADiscoveryPrefix: "homeassistant",
	}
}

const (
	const_pc                  = 0.0
	const_sh                  = "00:00"
	const_eh                  = "00:55"
	const_mc                  = 3500
	const_plwt                = 0
	const_pupt                = 0
	const_pbr                 = 0
	const_br_sh               = ""
	const_br_eh               = ""
	const_ct                  = "0 0 0 0 0"
	const_mqtt_topic_prefix   = "sbam"
	const_ha_discovery_prefix = "homeassistant"
	const_mqtt_op_timeout     = 5 * time.Second
	const_forecast_horizon    = "default"
	const_consumption_horizon = "full_day"
	const_scheduler_mode      = "crontab"
	scheduleClockLayout       = "15:04"
)

type clockSegment struct {
	startMinute int
	endMinute   int
}

// froniusClient abstracts the subset of Fronius behavior used by the
// scheduler. It's defined here so tests can inject a fake implementation
// without changing production logic.
type froniusClient interface {
	Handler(pw_forecast, pw_batt2charge, pw_batt_max, pw_consumption, max_charge, pw_batt_reserve float64,
		start_hr, end_hr, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt, pw_upt float64,
		forecast_charge_enabled bool, fronius_port ...string) (int16, fronius.Decision, fronius.Reason, fronius.PowerState, error)
}

var newFronius = func() froniusClient {
	return fronius.New()
}

type storageClient interface {
	Handler(fronius_ip string) (float64, float64, float64, error)
}

var newStorage = func() storageClient {
	return storage.New()
}

type powerClient interface {
	Handler(apiKey string, url string, cache_forecast bool, cache_file_prefix string, cache_time int32, forecastHorizon string, now time.Time) (float64, bool, error)
}

var newPower = func() powerClient {
	return pw.New()
}

var scdCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule Battery Storage Charge",
	Long:  `Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd)
	},
	Run: func(cmd *cobra.Command, args []string) {
		s_url = viper.GetString("url")
		s_apiKey = viper.GetString("apikey")
		fronius_ip = viper.GetString("fronius_ip")
		pw_consumption = viper.GetFloat64("pw_consumption")
		start_hr = viper.GetString("start_hr")
		end_hr = viper.GetString("end_hr")
		max_charge = viper.GetFloat64("max_charge")
		pw_lwt = viper.GetFloat64("pw_lwt")
		pw_upt = viper.GetFloat64("pw_upt")
		pw_batt_reserve = viper.GetFloat64("pw_batt_reserve")
		s_cache_forecast = viper.GetBool("cache_forecast")
		s_cache_file_prefix = viper.GetString("cache_file_prefix")
		s_cache_time = viper.GetInt32("cache_time")
		mqtt_enabled = viper.GetBool("mqtt_enabled")
		mqtt_tls_ca_file = viper.GetString("mqtt_tls_ca_file")
		mqtt_tls_client_cert = viper.GetString("mqtt_tls_client_cert")
		mqtt_tls_client_cert_key = viper.GetString("mqtt_tls_client_cert_key")
		mqtt_tls_insecure_skip = viper.GetBool("mqtt_tls_insecure_skip")

		// Resolve mqtt_optional_config with flag > env > config.yaml precedence
		// (same pattern as --windows).
		mqttOpts := mqttOptsDefaults()
		if f := cmd.Flags().Lookup("mqtt_optional_config"); f != nil && f.Changed {
			if err := yaml.Unmarshal([]byte(f.Value.String()), &mqttOpts); err != nil {
				u.Log.Errorf("invalid --mqtt_optional_config flag: %v", err)
				return
			}
		} else if env, ok := os.LookupEnv("MQTT_OPTIONAL_CONFIG"); ok {
			if err := yaml.Unmarshal([]byte(env), &mqttOpts); err != nil {
				u.Log.Errorf("invalid MQTT_OPTIONAL_CONFIG env var: %v", err)
				return
			}
		} else if viper.InConfig("mqtt_optional_config") {
			if err := viper.UnmarshalKey("mqtt_optional_config", &mqttOpts); err != nil {
				u.Log.Errorf("invalid mqtt_optional_config in config.yaml: %v", err)
				return
			}
		}
		forecast_horizon = viper.GetString("forecast_horizon")
		consumption_horizon = viper.GetString("consumption_horizon")
		scheduler_mode = viper.GetString("scheduler_mode")

		// Resolve windows with flag > env > config.yaml precedence.
		var windows []pw.Window
		if f := cmd.Flags().Lookup("windows"); f != nil && f.Changed {
			if err := yaml.Unmarshal([]byte(f.Value.String()), &windows); err != nil {
				u.Log.Errorf("invalid --windows flag: %v", err)
				return
			}
		} else if env, ok := os.LookupEnv("WINDOWS"); ok {
			if err := yaml.Unmarshal([]byte(env), &windows); err != nil {
				u.Log.Errorf("invalid WINDOWS env var: %v", err)
				return
			}
		} else if viper.InConfig("windows") {
			if err := viper.UnmarshalKey("windows", &windows); err != nil {
				u.Log.Errorf("invalid windows in config.yaml: %v", err)
				return
			}
		}
		for i := range windows {
			if windows[i].Name == "" {
				windows[i].Name = fmt.Sprintf("window-%d", i+1)
			}
		}
		if len(windows) > 0 {
			w_start_hr = windows[0].Start
			w_end_hr = windows[len(windows)-1].End
		} else {
			w_start_hr = start_hr
			w_end_hr = end_hr
		}
		if len(viper.GetString("batt_reserve_start_hr")) == 0 {
			batt_reserve_start_hr = w_start_hr
		} else {
			batt_reserve_start_hr = viper.GetString("batt_reserve_start_hr")
		}
		if len(viper.GetString("batt_reserve_end_hr")) == 0 {
			batt_reserve_end_hr = w_end_hr
		} else {
			batt_reserve_end_hr = viper.GetString("batt_reserve_end_hr")
		}
		crontab = viper.GetString("crontab")
		s_defaults = viper.GetBool("defaults")

		mqttCfg := mqtt.Config{
			Enabled:           mqtt_enabled,
			Broker:            mqttOpts.Broker,
			ClientID:          mqttOpts.ClientID,
			Username:          mqttOpts.Username,
			Password:          mqttOpts.Password,
			TLSCAFile:         mqtt_tls_ca_file,
			TLSClientCert:     mqtt_tls_client_cert,
			TLSClientCertKey:  mqtt_tls_client_cert_key,
			TLSInsecureSkip:   mqtt_tls_insecure_skip,
			TopicPrefix:       mqttOpts.TopicPrefix,
			HADiscovery:       mqttOpts.HADiscovery,
			HADiscoveryPrefix: mqttOpts.HADiscoveryPrefix,
			FroniusIP:         fronius_ip,
		}
		mqttClient, mqttCleanup, err := mqtt.InitWithCleanup(mqttCfg, appVersion)
		defer mqttCleanup()
		if err != nil {
			u.HandleError(err, "mqtt client setup failed")
		}

		extras := map[string]interface{}{
			"effective_start_hr":              w_start_hr,
			"effective_end_hr":                w_end_hr,
			"effective_batt_reserve_start_hr": batt_reserve_start_hr,
			"effective_batt_reserve_end_hr":   batt_reserve_end_hr,
		}
		if len(windows) > 0 {
			extras["window_count"] = len(windows)
		}
		u.LogStartupParams(cmd, extras)

		err = checkScheduleschedule(crontab, s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, w_start_hr, w_end_hr, windows, scheduler_mode)
		if err != nil {
			u.Log.Error(err)
			return
		}

		runnerCfg := RunnerConfig{
			APIKey:             s_apiKey,
			URL:                s_url,
			FroniusIP:          fronius_ip,
			PWConsumption:      pw_consumption,
			MaxCharge:          max_charge,
			PWBattReserve:      pw_batt_reserve,
			StartHR:            start_hr,
			EndHR:              end_hr,
			BattReserveStartHR: batt_reserve_start_hr,
			BattReserveEndHR:   batt_reserve_end_hr,
			PWLWT:              pw_lwt,
			PWUPT:              pw_upt,
			CacheForecast:      s_cache_forecast,
			Windows:            windows,
			CacheFilePrefix:    s_cache_file_prefix,
			CacheTime:          s_cache_time,
			Defaults:           s_defaults,
			ForecastHorizon:    forecast_horizon,
			ConsumptionHorizon: consumption_horizon,
			SchedulerMode:      scheduler_mode,
			MQTT:               mqttCfg,
			Now:                time.Now,
		}

		runner := NewRunner(runnerCfg, mqttClient)
		runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		runDone := make(chan error, 1)
		go func() {
			runDone <- runner.Run(runCtx)
		}()

		if mqttCfg.Enabled && mqttClient != nil {
			mqttClient.OnConnect(func() {
				u.Log.Debug("mqtt onConnect: subscribing to command topics")
				if err := subscribeScheduleCommands(runCtx, mqttClient, mqttCfg, runner); err != nil {
					u.HandleError(err, "mqtt command subscription failed")
				}
			})
		}

		switch scheduler_mode {
		case "crontab":
			u.Log.Warn("scheduler_mode=crontab is deprecated and will be removed in v3.0.0; migrate to scheduler_mode=windows")
			if mqttCfg.Enabled && mqttClient != nil {
				deprecationPayload := mqtt.StatePayload{
					SchedulerMode:      strPtr("crontab"),
					DeprecationWarning: strPtr("scheduler_mode=crontab is deprecated and will be removed in v3.0.0; migrate to scheduler_mode=windows"),
					Timestamp:          time.Now().UTC(),
				}
				publishStateSnapshot(mqttClient, mqttCfg, deprecationPayload)
			}

			if crontab != const_ct {
				if err := crontabSchedule(runCtx, runner, crontab, s_defaults, w_end_hr); err != nil {
					u.Log.Error(err)
					_ = runner.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown})
					stop()
					if waitErr := waitForRunnerDone(runDone); waitErr != nil {
						u.Log.Error(waitErr)
					}
					return
				}
			}

		case "windows":
			if crontab != const_ct {
				u.Log.Warn("crontab is set but scheduler_mode=windows; crontab will be ignored")
			}
			u.Log.Info("scheduler_mode=windows active; starting internal ticker")
			runner.StartWindowsTicker(time.Now())

		default:
			u.Log.Errorf("unexpected scheduler_mode %q", scheduler_mode)
			return
		}

		if err := finalizeRunnerMode(mqtt_enabled, runner, runDone, stop); err != nil {
			u.Log.Error(err)
		}
	},
}

// registerScdCmd registers the `schedule` command and its CLI flags.
// This is called at program startup to hook the command into Cobra's root.
// It intentionally keeps flag wiring localized here for easier testing.
//
// Note: This function is internal to the package and not exported.
//
// See: `scdCmd` defined above.
func registerScdCmd() {
	scdCmd.Flags().StringVarP(&s_url, "url", "u", "", "Set the Forecast URL. For multiple URLs, use a comma (,) to separate them")
	scdCmd.Flags().StringVarP(&s_apiKey, "apikey", "k", "", "APIKEY")
	scdCmd.Flags().StringVarP(&fronius_ip, "fronius_ip", "H", "", "FRONIUS_IP")
	scdCmd.Flags().StringVarP(&start_hr, "start_hr", "s", const_sh, "START_HR")
	scdCmd.Flags().StringVarP(&end_hr, "end_hr", "e", const_eh, "END_HR")
	scdCmd.Flags().StringVarP(&crontab, "crontab", "t", const_ct, "CRONTAB")
	scdCmd.Flags().Float64VarP(&pw_consumption, "pw_consumption", "c", const_pc, "PW_CONSUMPTION")
	scdCmd.Flags().Float64VarP(&max_charge, "max_charge", "m", const_mc, "MAX_CHARGE")
	scdCmd.Flags().Float64VarP(&pw_lwt, "pw_lwt", "L", const_plwt, "PW_LWT")
	scdCmd.Flags().Float64VarP(&pw_upt, "pw_upt", "U", const_pupt, "PW_UPT")
	scdCmd.Flags().Float64VarP(&pw_batt_reserve, "pw_batt_reserve", "r", const_pbr, "PW_BATT_RESERVE")
	scdCmd.Flags().StringVarP(&batt_reserve_start_hr, "batt_reserve_start_hr", "S", const_br_sh, "BATT_RESERVE_START_HR (default START_HR)")
	scdCmd.Flags().StringVarP(&batt_reserve_end_hr, "batt_reserve_end_hr", "E", const_br_eh, "BATT_RESERVE_END_HR (default END_HR)")
	scdCmd.Flags().BoolVarP(&s_defaults, "defaults", "d", true, "DEFAULTS")
	scdCmd.Flags().BoolVarP(&s_cache_forecast, "cache_forecast", "n", false, "CACHE_FORECAST (default false)")
	scdCmd.Flags().StringVarP(&s_cache_file_prefix, "cache_file_prefix", "f", "cached_forecast", "CACHE_FILE_PREFIX (default 'cached_forecast')")
	scdCmd.Flags().Int32VarP(&s_cache_time, "cache_time", "l", 7200, "CACHE_TIME (default 7200)")
	scdCmd.Flags().BoolVar(&mqtt_enabled, "mqtt_enabled", false, "Enable MQTT integration")
	scdCmd.Flags().StringVar(&mqtt_tls_ca_file, "mqtt_tls_ca_file", "", "MQTT TLS CA certificate file")
	scdCmd.Flags().StringVar(&mqtt_tls_client_cert, "mqtt_tls_client_cert", "", "MQTT TLS client certificate file")
	scdCmd.Flags().StringVar(&mqtt_tls_client_cert_key, "mqtt_tls_client_cert_key", "", "MQTT TLS client key file")
	scdCmd.Flags().BoolVar(&mqtt_tls_insecure_skip, "mqtt_tls_insecure_skip", false, "Skip MQTT TLS certificate verification")
	scdCmd.Flags().String("mqtt_optional_config", "", "MQTT optional config in YAML format (broker, client_id, username, password, topic_prefix, ha_discovery, ha_discovery_prefix)")
	scdCmd.Flags().StringVar(&forecast_horizon, "forecast_horizon", const_forecast_horizon, "Forecast horizon mode (default, next_solar_day, remaining_today, today, tomorrow, off)")
	scdCmd.Flags().StringVar(&consumption_horizon, "consumption_horizon", const_consumption_horizon, "Consumption horizon mode (full_day, remaining_today)")
	scdCmd.Flags().StringVar(&scheduler_mode, "scheduler_mode", const_scheduler_mode, "Scheduler mode (crontab, windows)")
	scdCmd.Flags().String("windows", "", "Charge windows in YAML format (same as config.yaml windows: key)")

	rootCmd.AddCommand(scdCmd)
}

// parseScheduleClock parses a clock string in the `scheduleClockLayout`
// ("15:04") and returns a `time.Time` representing that wall-clock
// time. The returned value has the date portion set by Go's parser and
// is intended only for clock comparisons (hours/minutes), not absolute
// timestamps.
func parseScheduleClock(value, field string) (time.Time, error) {
	parsed, err := time.Parse(scheduleClockLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s %q: %w", field, value, err)
	}

	return parsed, nil
}

// validateScheduleWindow parses and validates a start/end clock window
// provided as strings in `scheduleClockLayout` ("15:04"). It returns the
// parsed start and end times (date portion zeroed) or an error when
// parsing fails or when the two times are equal.
func validateScheduleWindow(startField, startValue, endField, endValue string) (time.Time, time.Time, error) {
	startTime, err := parseScheduleClock(startValue, startField)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	endTime, err := parseScheduleClock(endValue, endField)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if startTime.Equal(endTime) {
		return time.Time{}, time.Time{}, fmt.Errorf("%s %q must not be equal to %s %q", startField, startValue, endField, endValue)
	}

	return startTime, endTime, nil
}

// clockMinute returns the minute-of-day for the provided time (0..1439).
// This helper is used when converting parsed clock times into integer
// minute positions for window expansion and containment checks.
func clockMinute(t time.Time) int {
	return t.Hour()*60 + t.Minute()
}

// expandClockWindow converts a start and end minute into one or two
// `clockSegment` entries. When `startMinute < endMinute` the window is
// continuous and a single segment is returned. When the window crosses
// midnight the function returns two segments: one from `startMinute`
// through 23:59 and another from 00:00 through `endMinute`.
// This is used to reason about containment for cross-midnight windows.
func expandClockWindow(startMinute, endMinute int) []clockSegment {
	if startMinute < endMinute {
		return []clockSegment{{startMinute: startMinute, endMinute: endMinute}}
	}

	return []clockSegment{
		{startMinute: startMinute, endMinute: 1439},
		{startMinute: 0, endMinute: endMinute},
	}
}

// segmentContains returns true when the `inner` segment lies entirely within
// the `outer` segment (inclusive bounds). It's used by window containment
// checks after windows have been expanded into one or two segments.
func segmentContains(outer, inner clockSegment) bool {
	return outer.startMinute <= inner.startMinute && inner.endMinute <= outer.endMinute
}

// isWindowContainedIn reports whether the inner time window (innerStart..innerEnd)
// is fully covered by the outer time window (outerStart..outerEnd). Both inputs
// are clock strings in "15:04" layout. The function expands windows that span
// midnight into segments and verifies each inner segment is contained in at
// least one outer segment.
func isWindowContainedIn(innerStart, innerEnd, outerStart, outerEnd string) (bool, error) {
	innerStartTime, innerEndTime, err := validateScheduleWindow("inner_start", innerStart, "inner_end", innerEnd)
	if err != nil {
		return false, err
	}

	outerStartTime, outerEndTime, err := validateScheduleWindow("outer_start", outerStart, "outer_end", outerEnd)
	if err != nil {
		return false, err
	}

	innerSegments := expandClockWindow(clockMinute(innerStartTime), clockMinute(innerEndTime))
	outerSegments := expandClockWindow(clockMinute(outerStartTime), clockMinute(outerEndTime))

	for _, innerSeg := range innerSegments {
		covered := false
		for _, outerSeg := range outerSegments {
			if segmentContains(outerSeg, innerSeg) {
				covered = true
				break
			}
		}

		if !covered {
			return false, nil
		}
	}

	return true, nil
}

// checkScheduleschedule validates the provided scheduling and runtime
// configuration values. It returns a non-nil error when validation fails.
//
// The function intentionally keeps validation local to the command so the
// runtime can exit early with user-friendly messages when flags are invalid.
func checkScheduleschedule(crontab, apiKey, url, fronius_ip string, pw_consumption, max_charge,
	pw_batt_reserve float64, start_hr, end_hr string, windows []pw.Window, scheduler_mode string) error {

	if len(windows) > 0 {
		u.Log.Info("windows configuration is provided; start_hr/end_hr will be ignored")
		if err := pw.ValidateWindows(windows); err != nil {
			return fmt.Errorf("invalid windows configuration: %w", err)
		}
	}

	// Validate scheduler_mode.
	switch scheduler_mode {
	case "crontab":
		// Valid; crontab + windows is compatible (cron drives ticks, windows parameterize them).
	case "windows":
		if len(windows) == 0 {
			return errors.New("scheduler_mode is 'windows' but no windows are configured; add at least one entry to windows:")
		}
	case "auto":
		return fmt.Errorf("scheduler_mode 'auto' is not yet available — see issue #149")
	default:
		return fmt.Errorf("unknown scheduler_mode %q: must be crontab, windows, or auto", scheduler_mode)
	}

	if len(strings.TrimSpace(fronius_ip)) == 0 {
		err := errors.New("the --fronius_ip flag must be set")
		return err
	} else if len(strings.TrimSpace(apiKey)) == 0 {
		err := errors.New("the --apikey flag must be set")
		return err
	} else if len(strings.TrimSpace(url)) == 0 {
		err := errors.New("the --url flag must be set")
		return err
	} else if len(windows) == 0 {
		// Legacy window validation — skipped when windows: is configured.
		if _, _, err := validateScheduleWindow("start_hr", start_hr, "end_hr", end_hr); err != nil {
			return err
		}
		if max_charge < 0 {
			err := errors.New("max_charge must to be float > 0")
			return err
		}
	}
	if len(crontab) == 0 {
		fmt.Printf("the --crontab must be set")
		err := errors.New("crontab must to be integer > 0")
		return err
	} else if pw_consumption < 0 {
		err := errors.New("pw_consumption must to be float > 0")
		return err
	} else if pw_lwt < 0 {
		err := errors.New("pw_lwt must to be float > 0")
		return err
	} else if pw_upt < 0 {
		err := errors.New("pw_upt must to be float > 0")
		return err
	} else if pw_batt_reserve < 0 {
		err := errors.New("pw_batt_reserve must to be float > 0")
		return err
	} else if _, _, err := validateScheduleWindow("batt_reserve_start_hr", batt_reserve_start_hr, "batt_reserve_end_hr", batt_reserve_end_hr); err != nil {
		return err
	} else if contained, err := isWindowContainedIn(batt_reserve_start_hr, batt_reserve_end_hr, start_hr, end_hr); err != nil {
		return err
	} else if !contained {
		err := errors.New("batt_reserve_start_hr/batt_reserve_end_hr must be contained within start_hr/end_hr")
		return err
	} else if (s_cache_time < 0) || (s_cache_time > 86400) {
		err := errors.New("The cache_time must be between 0 and 86400 seconds")
		return err
	} else if _, err := pw.ValidateForecastHorizon(forecast_horizon); err != nil {
		return err
	} else if _, err := pw.ValidateConsumptionHorizon(consumption_horizon); err != nil {
		return err
	}

	return nil
}

// publishStateSnapshot publishes the provided `payload` using the MQTT client.
func publishStateSnapshot(mqttClient mqtt.Client, mqttCfg mqtt.Config, payload mqtt.StatePayload) {
	// Use a short-lived context so the publish operation cannot block
	// indefinitely. This matches other MQTT operations which use
	// `const_mqtt_op_timeout`.
	ctx, cancel := context.WithTimeout(context.Background(), const_mqtt_op_timeout)
	defer cancel()
	mqtt.PublishState(ctx, mqttClient, mqttCfg.TopicPrefix, payload)
}

// subscribeScheduleCommands subscribes to the scheduler command topic and
// forwards incoming MQTT commands to the provided `runner` via its
// `HandleCommand` method. It is invoked from the OnConnect callback so the
// client is guaranteed to be connected.
func subscribeScheduleCommands(ctx context.Context, mqttClient mqtt.Client, mqttCfg mqtt.Config, runner *Runner) error {
	if !mqttCfg.Enabled || mqttClient == nil {
		return nil
	}
	if runner == nil {
		return errors.New("runner must not be nil")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	commandFilter := mqtt.CommandTopicFilter(mqttCfg.TopicPrefix)
	subCtx, cancel := context.WithTimeout(ctx, const_mqtt_op_timeout)
	defer cancel()

	if err := mqttClient.Subscribe(subCtx, commandFilter, byte(1), func(topic string, payload []byte) {
		payloadCopy := append([]byte(nil), payload...)
		opCtx, opCancel := context.WithTimeout(context.Background(), const_mqtt_op_timeout)
		defer opCancel()
		runner.HandleCommand(opCtx, topic, payloadCopy)
	}); err != nil {
		return fmt.Errorf("mqtt subscribe %q failed: %w", commandFilter, err)
	}

	return nil
}

// makeBasePayload builds a StatePayload with fields common to all
// scheduler publish points. It returns a payload with the provided
// decision/reason and pointerized window flags; callers may set extra
// telemetry fields before publishing.
func makeBasePayload(lastDecision, lastReason string, inChargeWindow, reserveWindowActive bool, schedulerMode string) mqtt.StatePayload {
	ic := inChargeWindow
	rw := reserveWindowActive
	sm := schedulerMode
	return mqtt.StatePayload{
		LastDecision:        lastDecision,
		LastDecisionReason:  lastReason,
		ChargeWindowActive:  &ic,
		ReserveWindowActive: &rw,
		SchedulerMode:       &sm,
		Paused:              false,
		Timestamp:           time.Now().UTC(),
	}
}

// finalizeRunnerMode stops the runner immediately when MQTT is disabled
// (non-interactive mode) or blocks until the runner completes when MQTT
// is enabled (interactive mode). When stopping the runner it submits a
// shutdown intent and waits for the runner goroutine to finish.
func finalizeRunnerMode(mqttEnabled bool, runner *Runner, runDone <-chan error, stop context.CancelFunc) error {
	if !mqttEnabled {
		u.Log.Info("MQTT integration disabled, stopping runner")
		_ = runner.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown})
		if stop != nil {
			stop()
		}
		return waitForRunnerDone(runDone)
	}

	u.Log.Info("MQTT integration enabled, waiting for commands... press ctrl+c to exit...")
	return waitForRunnerDone(runDone)
}

// waitForRunnerDone waits for the runner goroutine to finish and returns
// any non-cancellation error observed. It validates the channel is not
// nil and unwraps context cancellation from the returned error path.
func waitForRunnerDone(runDone <-chan error) error {
	if runDone == nil {
		return errors.New("runner completion channel is required")
	}

	runErr := <-runDone
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}

	return nil
}

// crontabSchedule installs the `schedule` function into a cron scheduler
func strPtr(s string) *string { return &s }

// crontabSchedule installs the `schedule` function into a cron scheduler
// using the provided cron expression. Callbacks submit intents and return
// immediately; the runner serializes all schedule and Modbus operations.
func crontabSchedule(ctx context.Context, runner *Runner, crontab string, defaults bool, end_hr string) error {
	if runner == nil {
		return errors.New("runner must not be nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	layout := "15:04"
	endTime, err := time.Parse(layout, end_hr)
	if err != nil {
		return fmt.Errorf("invalid end_hr %q: %w", end_hr, err)
	}
	endTime = endTime.Add(-5 * time.Minute)
	end_crontab := fmt.Sprintf("%d %d * * *", endTime.Minute(), endTime.Hour())

	c := cron.New()
	_, err = c.AddFunc(crontab, func() {
		if !runner.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
			u.Log.Warn("schedule tick dropped because runner queue is full")
		}
	})
	if err != nil {
		return err
	}
	if defaults {
		_, err = c.AddFunc(end_crontab, func() {
			if !runner.Submit(mqtt.Intent{Kind: mqtt.IntentSetDefaults}) {
				u.Log.Warn("set_defaults dropped because runner queue is full")
			}
		})
		if err != nil {
			return err
		}
		u.Log.Infof("Battery will be reset to defaults daily at %s (end_hr - 5 min)", endTime.Format(layout))
	}

	c.Start()
	defer func() {
		stopCtx := c.Stop()
		<-stopCtx.Done()
	}()
	u.Log.Info("Scheduler started with crontab: " + crontab + " press ctrl+c to exit...")
	<-ctx.Done()
	return nil
}
