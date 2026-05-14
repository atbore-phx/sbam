package cmd

import (
	"context"
	"errors"
	"fmt"
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
)

var s_apiKey, s_url, start_hr, end_hr, batt_reserve_start_hr, batt_reserve_end_hr,
	crontab, s_cache_file_prefix, mqtt_broker, mqtt_client_id, mqtt_username,
	mqtt_password, mqtt_tls_ca_file, mqtt_tls_client_cert, mqtt_tls_client_cert_key,
	mqtt_topic_prefix, mqtt_ha_discovery_prefix string
var pw_consumption, max_charge, pw_lwt, pw_upt, pw_batt_reserve float64
var s_cache_time int32
var s_defaults, s_cache_forecast, mqtt_enabled, mqtt_ha_discovery, mqtt_tls_insecure_skip bool

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
)

// froniusClient abstracts the subset of Fronius behavior used by the
// scheduler. It's defined here so tests can inject a fake implementation
// without changing production logic.
type froniusClient interface {
	Handler(pw_forecast, pw_batt2charge, pw_batt_max, pw_consumption, max_charge, pw_batt_reserve float64,
		start_hr, end_hr, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt, pw_upt float64,
		forecast_charge_enabled bool, fronius_port ...string) (int16, fronius.Decision, string, fronius.PowerState, error)
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
	Handler(apiKey string, url string, cache_forecast bool, cache_file_prefix string, cache_time int32) (float64, bool, error)
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
		mqtt_broker = viper.GetString("mqtt_broker")
		mqtt_client_id = viper.GetString("mqtt_client_id")
		mqtt_username = viper.GetString("mqtt_username")
		mqtt_password = viper.GetString("mqtt_password")
		mqtt_tls_ca_file = viper.GetString("mqtt_tls_ca_file")
		mqtt_tls_client_cert = viper.GetString("mqtt_tls_client_cert")
		mqtt_tls_client_cert_key = viper.GetString("mqtt_tls_client_cert_key")
		mqtt_tls_insecure_skip = viper.GetBool("mqtt_tls_insecure_skip")
		mqtt_topic_prefix = viper.GetString("mqtt_topic_prefix")
		mqtt_ha_discovery = viper.GetBool("mqtt_ha_discovery")
		mqtt_ha_discovery_prefix = viper.GetString("mqtt_ha_discovery_prefix")

		if len(viper.GetString("batt_reserve_start_hr")) == 0 {
			batt_reserve_start_hr = viper.GetString("start_hr")
		} else {
			batt_reserve_start_hr = viper.GetString("batt_reserve_start_hr")
		}
		if len(viper.GetString("batt_reserve_end_hr")) == 0 {
			batt_reserve_end_hr = viper.GetString("end_hr")
		} else {
			batt_reserve_end_hr = viper.GetString("batt_reserve_end_hr")
		}
		crontab = viper.GetString("crontab")
		s_defaults = viper.GetBool("defaults")

		mqttCfg := mqtt.Config{
			Enabled:           mqtt_enabled,
			Broker:            mqtt_broker,
			ClientID:          mqtt_client_id,
			Username:          mqtt_username,
			Password:          mqtt_password,
			TLSCAFile:         mqtt_tls_ca_file,
			TLSClientCert:     mqtt_tls_client_cert,
			TLSClientCertKey:  mqtt_tls_client_cert_key,
			TLSInsecureSkip:   mqtt_tls_insecure_skip,
			TopicPrefix:       mqtt_topic_prefix,
			HADiscovery:       mqtt_ha_discovery,
			HADiscoveryPrefix: mqtt_ha_discovery_prefix,
			FroniusIP:         fronius_ip,
		}
		mqttClient, mqttCleanup, err := mqtt.InitWithCleanup(mqttCfg, appVersion, 3, 250*time.Millisecond)
		defer mqttCleanup()
		if err != nil {
			u.HandleError(err, "mqtt homeassistant/status subscription failed")
		}

		u.LogStartupParams(cmd)

		err = checkScheduleschedule(crontab, s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr)
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
			CacheFilePrefix:    s_cache_file_prefix,
			CacheTime:          s_cache_time,
			Defaults:           s_defaults,
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

		if err := subscribeScheduleCommands(runCtx, mqttClient, mqttCfg, runner); err != nil {
			u.HandleError(err, "mqtt command subscription failed")
		}

		if crontab != const_ct {
			if err := crontabSchedule(runCtx, runner, crontab, s_defaults, end_hr); err != nil {
				u.Log.Error(err)
				_ = runner.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown})
				stop()
				if waitErr := waitForRunnerDone(runDone); waitErr != nil {
					u.Log.Error(waitErr)
				}
				return
			}
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
	scdCmd.Flags().StringVar(&mqtt_broker, "mqtt_broker", "", "MQTT broker URL")
	scdCmd.Flags().StringVar(&mqtt_client_id, "mqtt_client_id", "", "MQTT client identifier")
	scdCmd.Flags().StringVar(&mqtt_username, "mqtt_username", "", "MQTT username")
	scdCmd.Flags().StringVar(&mqtt_password, "mqtt_password", "", "MQTT password")
	scdCmd.Flags().StringVar(&mqtt_tls_ca_file, "mqtt_tls_ca_file", "", "MQTT TLS CA certificate file")
	scdCmd.Flags().StringVar(&mqtt_tls_client_cert, "mqtt_tls_client_cert", "", "MQTT TLS client certificate file")
	scdCmd.Flags().StringVar(&mqtt_tls_client_cert_key, "mqtt_tls_client_cert_key", "", "MQTT TLS client key file")
	scdCmd.Flags().BoolVar(&mqtt_tls_insecure_skip, "mqtt_tls_insecure_skip", false, "Skip MQTT TLS certificate verification")
	scdCmd.Flags().StringVar(&mqtt_topic_prefix, "mqtt_topic_prefix", const_mqtt_topic_prefix, "MQTT topic prefix")
	scdCmd.Flags().BoolVar(&mqtt_ha_discovery, "mqtt_ha_discovery", true, "Enable Home Assistant MQTT discovery")
	scdCmd.Flags().StringVar(&mqtt_ha_discovery_prefix, "mqtt_ha_discovery_prefix", const_ha_discovery_prefix, "Home Assistant MQTT discovery prefix")

	rootCmd.AddCommand(scdCmd)
}

// isStartBeforeEnd parses the given `start` and `end` time strings (layout "15:04")
// and returns true when `start` is strictly before `end`.
//
// It panics on parse errors; callers should validate input beforehand.
func isStartBeforeEnd(start, end string) bool {
	// Define a layout for parsing time strings
	layout := "15:04"

	// Parse the time strings
	startTime, err := time.Parse(layout, start)
	if err != nil {
		u.Log.Error("Something goes wrong parsing start time")
		panic(err)
	}

	endTime, err := time.Parse(layout, end)
	if err != nil {
		u.Log.Error("Something goes wrong parsing end time")
		panic(err)
	}

	// Compare the times
	return startTime.Before(endTime)
}

// isStartAfterEnd parses `start` and `end` (layout "15:04") and returns true
// when `start` is strictly after `end`.
//
// It panics on parse errors similarly to isStartBeforeEnd.
func isStartAfterEnd(start, end string) bool {
	// Define a layout for parsing time strings
	layout := "15:04"

	// Parse the time strings
	startTime, err := time.Parse(layout, start)
	if err != nil {
		u.Log.Error("Something goes wrong parsing start time")
		panic(err)
	}

	endTime, err := time.Parse(layout, end)
	if err != nil {
		u.Log.Error("Something goes wrong parsing end time")
		panic(err)
	}

	// Compare the times
	return startTime.After(endTime)
}

// CheckTimeRange returns true when the current local time is within the
// inclusive interval between `start_hr` and `end_hr` (both in "15:04" format).
//
// This helper is used by the scheduler to determine whether charging window
// and reserve windows are active at runtime.
func CheckTimeRange(start_hr, end_hr string) bool {
	inRange, err := checkTimeRangeAt(time.Now(), start_hr, end_hr)
	if err != nil {
		u.Log.Error("Something goes wrong parsing time range")
		panic(err)
	}

	return inRange
}

// checkScheduleschedule validates the provided scheduling and runtime
// configuration values. It returns a non-nil error when validation fails.
//
// The function intentionally keeps validation local to the command so the
// runtime can exit early with user-friendly messages when flags are invalid.
func checkScheduleschedule(crontab, apiKey, url, fronius_ip string, pw_consumption, max_charge,
	pw_batt_reserve float64, start_hr, end_hr string) error {
	if len(strings.TrimSpace(fronius_ip)) == 0 {
		err := errors.New("the --fronius_ip flag must be set")
		return err
	} else if len(strings.TrimSpace(apiKey)) == 0 {
		err := errors.New("the --apikey flag must be set")
		return err
	} else if len(strings.TrimSpace(url)) == 0 {
		err := errors.New("the --url flag must be set")
		return err
	} else if !isStartBeforeEnd(start_hr, end_hr) {
		err := errors.New("start_hr: " + start_hr + " is not before end_hr: " + end_hr)
		return err
	} else if len(crontab) == 0 {
		fmt.Printf("the --crontab must be set")
		err := errors.New("crontab must to be integer > 0")
		return err
	} else if pw_consumption < 0 {
		err := errors.New("pw_consumption must to be float > 0")
		return err
	} else if max_charge < 0 {
		err := errors.New("max_charge must to be float > 0")
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
	} else if !isStartBeforeEnd(batt_reserve_start_hr, batt_reserve_end_hr) {
		err := errors.New("batt_reserve_start_hr: " + batt_reserve_start_hr + " is not before batt_reserve_end_hr: " + batt_reserve_end_hr)
		return err
	} else if isStartAfterEnd(start_hr, batt_reserve_start_hr) {
		err := errors.New("start_hr: " + start_hr + " is not before or equal batt_reserve_start_hr: " + batt_reserve_start_hr)
		return err
	} else if isStartAfterEnd(batt_reserve_end_hr, end_hr) {
		err := errors.New("batt_reserve_end_hr: " + batt_reserve_end_hr + " is not before or equal end_hr: " + end_hr)
		return err
	} else if (s_cache_time < 0) || (s_cache_time > 86400) {
		err := errors.New("The cache_time must be between 0 and 86400 seconds")
		return err
	}

	return nil
}

// NOTE: the single-shot `schedule` compatibility wrapper was moved to
// package tests (pkg/cmd/schedule_test.go). Tests should call the
// wrapper or directly call `NewRunner(...).Tick(...)`.

// publishStateSnapshot publishes the provided `payload` using the MQTT client.
func publishStateSnapshot(mqttClient mqtt.Client, mqttCfg mqtt.Config, payload mqtt.StatePayload) {
	// Use a short-lived context so the publish operation cannot block
	// indefinitely. This matches other MQTT operations which use
	// `const_mqtt_op_timeout`.
	ctx, cancel := context.WithTimeout(context.Background(), const_mqtt_op_timeout)
	defer cancel()
	mqtt.PublishState(ctx, mqttClient, mqttCfg.TopicPrefix, payload)
}

// publishLatestState re-publishes the cached latest state snapshot, if one exists.
// latest-state republish was removed; discovery publish remains in mqtt.InitWithCleanup

func subscribeScheduleCommands(ctx context.Context, mqttClient mqtt.Client, mqttCfg mqtt.Config, runner *Runner) error {
	if !mqttCfg.Enabled || mqttClient == nil {
		return nil
	}
	if runner == nil {
		return errors.New("runner must not be nil")
	}
	if !mqttClient.IsConnected() {
		return nil
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
func makeBasePayload(lastDecision, lastReason string, inChargeWindow, reserveWindowActive bool) mqtt.StatePayload {
	ic := inChargeWindow
	rw := reserveWindowActive
	return mqtt.StatePayload{
		LastDecision:        lastDecision,
		LastDecisionReason:  lastReason,
		ChargeWindowActive:  &ic,
		ReserveWindowActive: &rw,
		Paused:              false,
		Timestamp:           time.Now().UTC(),
	}
}

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
