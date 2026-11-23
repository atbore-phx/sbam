package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pw "sbam/pkg/power"
	u "sbam/src/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var e_url string
var e_apiKey string
var e_cache_forecast bool
var e_cache_file_prefix string
var e_cache_time int32
var e_forecast_horizon string

// estimateNow is a seam for tests.
var estimateNow = time.Now

var estCmd = &cobra.Command{
	Use:   "estimate",
	Short: "Estimate Forecast Solar Power",
	Long: `Print the solar forecast and the battery storage power.
With pv_source=ha_solcast it reads the Home Assistant Solcast integration and
exits 1 when the forecast cannot be used, so it can check the setup.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlags(cmd)
	},
	RunE: runEstimateCmd,
}

// runEstimateCmd validates the configuration and runs one estimate. Every
// failure is logged and returned (exit 1); the command never panics.
func runEstimateCmd(cmd *cobra.Command, _ []string) error {
	e_url = viper.GetString("url")
	e_apiKey = viper.GetString("apikey")
	fronius_ip = viper.GetString("fronius_ip")
	e_cache_forecast = viper.GetBool("cache_forecast")
	e_cache_file_prefix = viper.GetString("cache_file_prefix")
	e_cache_time = viper.GetInt32("cache_time")
	e_forecast_horizon = viper.GetString("forecast_horizon")

	u.LogStartupParams(cmd, nil)

	fail := func(err error) error {
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true // logged here
		return err
	}

	pvCfg, pvErr := loadPVSourceConfigEnv()
	if err := errors.Join(CheckEstimate(e_apiKey, e_url, fronius_ip, pvCfg.Source), pvErr); err != nil {
		u.Log.Error(err)
		return fail(err)
	}
	logPVSourceStartup(pvCfg)

	src, err := newPVDaySource(pvCfg, solcastDaySource{
		apiKey:        e_apiKey,
		url:           e_url,
		cacheForecast: e_cache_forecast,
		cachePrefix:   e_cache_file_prefix,
		cacheTime:     e_cache_time,
	}, false)
	if err != nil {
		u.Log.Error(err)
		return fail(err)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := estimate(ctx, src, pvCfg.isHA(), fronius_ip, e_forecast_horizon, estimateNow()); err != nil {
		return fail(err)
	}
	return nil
}

func registerEstCmd() {
	estCmd.Flags().StringVarP(&e_url, "url", "u", "", helpSolcastURL)
	estCmd.Flags().StringVarP(&e_apiKey, "apikey", "k", "", helpSolcastAPIKey)
	estCmd.Flags().StringVarP(&fronius_ip, "fronius_ip", "H", "", "set FRONIUS_IP")
	estCmd.Flags().BoolVarP(&e_cache_forecast, "cache_forecast", "n", false, "CACHE_FORECAST (default false)")
	estCmd.Flags().StringVarP(&e_cache_file_prefix, "cache_file_prefix", "f", "cached_forecast", "CACHE_FILE_NAME (default 'cached_forecast')")
	estCmd.Flags().Int32VarP(&e_cache_time, "cache_time", "l", 7200, "CACHE_TIME (default 7200)")
	estCmd.Flags().StringVar(&e_forecast_horizon, "forecast_horizon", "default", "Forecast horizon mode (default, next_solar_day, remaining_today, today, tomorrow, off)")
	registerHAAccessFlags(estCmd)
	registerPVSourceFlags(estCmd)

	rootCmd.AddCommand(estCmd)
}

// CheckEstimate validates the estimate flags. apikey and url are required
// unless pvSource is ha_solcast (SPEC-0003 AC-5).
func CheckEstimate(apiKey string, url string, fronius_ip string, pvSource string) error {
	solcastAPI := pvSource != pw.PVSourceHASolcast
	if len(strings.TrimSpace(fronius_ip)) == 0 {
		err := errors.New("the --fronius_ip flag must be set")
		return err
	} else if solcastAPI && len(strings.TrimSpace(apiKey)) == 0 {
		err := errors.New("the --apikey flag must be set")
		return err
	} else if solcastAPI && len(strings.TrimSpace(url)) == 0 {
		err := errors.New("the --url flag must be set")
		return err
	} else if (e_cache_time < 0) || (e_cache_time > 86400) {
		err := errors.New("The cache_time must be between 0 and 86400 seconds")
		return err
	}
	return nil
}

// estimate reads the day's PV forecast once (no retries) and the battery
// state, logging both. With the HA source a missing, stale or uncovered
// forecast is an error (AC-7, AC-8); with solcast_api a failed download
// keeps today's behaviour (logged by the handler, not an error).
func estimate(ctx context.Context, src pvDaySource, haSource bool, fronius_ip string, forecastHorizon string, now time.Time) error {
	_, _, err := src.DayProduction(ctx, forecastHorizon, now)
	if err != nil {
		if haSource {
			u.Log.Errorf("PV forecast from Home Assistant failed: %v", err)
			return fmt.Errorf("PV forecast from Home Assistant failed: %w", err)
		}
		u.Log.Error(err)
		return err
	}

	str := newStorage()
	_, _, _, err = str.Handler(fronius_ip)
	if err != nil {
		u.Log.Error(err)
		return err
	}
	return nil
}
