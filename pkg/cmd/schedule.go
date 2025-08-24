package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sbam/pkg/fronius"
	pw "sbam/pkg/power"
	"sbam/pkg/storage"
	u "sbam/src/utils"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var s_apiKey string
var s_url string
var pw_consumption float64
var start_hr string
var end_hr string
var max_charge float64
var pw_lwt float64
var pw_upt float64
var pw_batt_reserve float64
var batt_reserve_start_hr string
var batt_reserve_end_hr string
var crontab string
var s_defaults bool
var s_cache_forecast bool
var s_cache_file_prefix string
var s_cache_time int32

const (
	const_pc    = 0.0
	const_sh    = "00:00"
	const_eh    = "00:55"
	const_mc    = 3500
	const_plwt  = 0
	const_pupt  = 0
	const_pbr   = 0
	const_br_sh = ""
	const_br_eh = ""
	const_ct    = "0 0 0 0 0"
)

var scdCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule Battery Storage Charge",
	Long:  `Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range`,
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

		err := checkScheduleschedule(crontab, s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr)
		if err != nil {
			u.Log.Error(err)
			return
		}

		u.Log.Debugf("schedule crontab '%s'", crontab)
		if crontab != "0 0 0 0 0" {
			crontabSchedule(s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, crontab, s_defaults, batt_reserve_start_hr, batt_reserve_end_hr, pw_lwt, pw_upt, s_cache_forecast, s_cache_file_prefix, s_cache_time)

		} else {
			schedule(s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, batt_reserve_start_hr, batt_reserve_end_hr, pw_lwt, pw_upt, s_cache_forecast, s_cache_file_prefix, s_cache_time)

		}
	},
}

func init() {
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

	viper.BindPFlag("url", scdCmd.Flags().Lookup("url"))
	viper.BindPFlag("apikey", scdCmd.Flags().Lookup("apikey"))
	viper.BindPFlag("fronius_ip", scdCmd.Flags().Lookup("fronius_ip"))
	viper.BindPFlag("pw_consumption", scdCmd.Flags().Lookup("pw_consumption"))
	viper.BindPFlag("start_hr", scdCmd.Flags().Lookup("start_hr"))
	viper.BindPFlag("end_hr", scdCmd.Flags().Lookup("end_hr"))
	viper.BindPFlag("crontab", scdCmd.Flags().Lookup("crontab"))
	viper.BindPFlag("max_charge", scdCmd.Flags().Lookup("max_charge"))
	viper.BindPFlag("pw_lwt", scdCmd.Flags().Lookup("pw_lwt"))
	viper.BindPFlag("pw_upt", scdCmd.Flags().Lookup("pw_upt"))
	viper.BindPFlag("pw_batt_reserve", scdCmd.Flags().Lookup("pw_batt_reserve"))
	viper.BindPFlag("batt_reserve_start_hr", scdCmd.Flags().Lookup("batt_reserve_start_hr"))
	viper.BindPFlag("batt_reserve_end_hr", scdCmd.Flags().Lookup("batt_reserve_end_hr"))
	viper.BindPFlag("defaults", scdCmd.Flags().Lookup("defaults"))
	viper.BindPFlag("cache_forecast", scdCmd.Flags().Lookup("cache_forecast"))
	viper.BindPFlag("cache_file_prefix", scdCmd.Flags().Lookup("cache_file_prefix"))
	viper.BindPFlag("cache_time", scdCmd.Flags().Lookup("cache_time"))

	rootCmd.AddCommand(scdCmd)
}

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

func CheckTimeRange(start_hr string, end_hr string) bool {
	now := time.Now()

	layout := "15:04"
	startTime, err := time.Parse(layout, start_hr)
	if err != nil {
		u.Log.Error("Something goes wrong parsing start time")
		panic(err)
	}

	endTime, err := time.Parse(layout, end_hr)
	if err != nil {
		u.Log.Error("Something goes wrong parsing end time")
		panic(err)
	}

	// Convert the current time to a time.Time value for today's date with the hour and minute set to the parsed start and end times
	startTime = time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
	endTime = time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

	return (now.After(startTime) || now.Equal(startTime)) && (now.Before(endTime) || now.Equal(endTime))
}

func checkScheduleschedule(crontab string, apiKey string, url string, fronius_ip string, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string) error {
	if len(strings.TrimSpace(fronius_ip)) == 0 {
		err := errors.New("the --fronius_ip flag must be set")
		return err
	} else if len(strings.TrimSpace(apiKey)) == 0 {
		err := errors.New("the --apiKey flag must be set")
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
	}

	return nil
}

func schedule(apiKey string, url string, fronius_ip string, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, batt_reserve_start_hr string, batt_reserve_end_hr string, pw_lwt float64, pw_upt float64, cache_forecast bool, cache_file_prefix string, cache_time int32) {
	if !CheckTimeRange(start_hr, end_hr) {
		u.Log.Info("The current time is outside the range defined by start_hr and end_hr.: " + start_hr + " <= t <= " + end_hr)
	} else {
		pwr := pw.New()
		solarPowerProduction, forecast_retrieved, err := pwr.Handler(apiKey, url, cache_forecast, cache_file_prefix, cache_time)
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}

		str := storage.New()
		capacity2charge, capacity_max, err := str.Handler(fronius_ip)
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
		u.Log.Infof("your Daily consumption is:%d Wh", int(pw_consumption))

		scd := fronius.New()
		_, err = scd.Handler(solarPowerProduction, capacity2charge, capacity_max, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, fronius_ip, CheckTimeRange(batt_reserve_start_hr, batt_reserve_end_hr), pw_lwt, pw_upt, forecast_retrieved)
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	}
}

func crontabSchedule(apiKey string, url string, fronius_ip string, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, crontab string, defaults bool, batt_reserve_start_hr string, batt_reserve_end_hr string, pw_lwt float64, pw_upt float64, cache_forecast bool, cache_file_prefix string, cache_time int32) {
	layout := "15:04"
	endTime, _ := time.Parse(layout, end_hr)
	endTime = endTime.Add(-5 * time.Minute)
	end_crontab := strconv.Itoa(endTime.Minute()) + " " + strconv.Itoa(endTime.Hour()) + " * * *"

	c := cron.New()
	_, err := c.AddFunc(crontab, func() {
		schedule(apiKey, url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, batt_reserve_start_hr, batt_reserve_end_hr, pw_lwt, pw_upt, cache_forecast, cache_file_prefix, cache_time)
	})
	if err != nil {
		u.Log.Error(err)
		panic(err)
	}
	if defaults {
		_, err = c.AddFunc(end_crontab, func() {
			fronius.Setdefaults(fronius_ip)
		})
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	}
	c.Start()
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	fmt.Println("Running, press ctrl+c to exit...")
	<-done // Will block here until user hits ctrl+c
}
