package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sbam/pkg/fronius"
	"sbam/pkg/power"
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

var scdCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule Battery Storage Charge",
	Long:  `Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range`,
	Run: func(cmd *cobra.Command, args []string) {
		url := viper.GetString("url")
		apiKey := viper.GetString("apikey")
		fronius_ip := viper.GetString("fronius_ip")
		pw_consumption := viper.GetFloat64("pw_consumption")
		start_hr := viper.GetString("start_hr")
		end_hr := viper.GetString("end_hr")
		max_charge := viper.GetFloat64("max_charge")
		pw_batt_reserve := viper.GetFloat64("pw_batt_reserve")
		crontab := viper.GetString("crontab")
		defaults := viper.GetBool("defaults")

		err := checkScheduleschedule(crontab, apiKey, url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr)
		if err != nil {
			u.Log.Error(err)
			return
		}

		if crontab != "0 0 0 0 0" {
			crontabSchedule(apiKey, url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, crontab, defaults)

		} else {
			schedule(apiKey, url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr)

		}
	},
}

func init() {
	scdCmd.Flags().StringP("url", "u", "", "URL")
	scdCmd.Flags().StringP("apikey", "k", "", "APIKEY")
	scdCmd.Flags().StringP("fronius_ip", "H", "", "FRONIUS_IP")
	scdCmd.Flags().StringP("start_hr", "s", "00:00", "START_HR")
	scdCmd.Flags().StringP("end_hr", "e", "05:55", "END_HR")
	scdCmd.Flags().StringP("crontab", "t", "0 0 0 0 0", "crontab")
	scdCmd.Flags().Float64P("pw_consumption", "c", 0.0, "PW_CONSUMPTION")
	scdCmd.Flags().Float64P("max_charge", "m", 3500, "MAX_CHARGE")
	scdCmd.Flags().Float64P("pw_batt_reserve", "r", 0, "PW_BATT_RESERVE")
	scdCmd.Flags().BoolP("defaults", "d", true, "DEFAULTS")

	viper.BindPFlag("url", scdCmd.Flags().Lookup("url"))
	viper.BindPFlag("apikey", scdCmd.Flags().Lookup("apikey"))
	viper.BindPFlag("fronius_ip", scdCmd.Flags().Lookup("fronius_ip"))
	viper.BindPFlag("pw_consumption", scdCmd.Flags().Lookup("pw_consumption"))
	viper.BindPFlag("start_hr", scdCmd.Flags().Lookup("start_hr"))
	viper.BindPFlag("end_hr", scdCmd.Flags().Lookup("end_hr"))
	viper.BindPFlag("crontab", scdCmd.Flags().Lookup("crontab"))
	viper.BindPFlag("max_charge", scdCmd.Flags().Lookup("max_charge"))
	viper.BindPFlag("pw_batt_reserve", scdCmd.Flags().Lookup("pw_batt_reserve"))
	viper.BindPFlag("defaults", scdCmd.Flags().Lookup("defaults"))

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
	} else if pw_batt_reserve < 0 {
		err := errors.New("pw_batt_reserve must to be float > 0")
		return err
	}

	return nil
}

func schedule(apiKey string, url string, fronius_ip string, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string) {
	if !CheckTimeRange(start_hr, end_hr) {
		u.Log.Info("not in time range: " + start_hr + " <= t <= " + end_hr)
	} else {
		pwr := power.New()
		solarPowerProduction, err := pwr.Handler(apiKey, url)
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
		u.Log.Infof("your Daily consumption is:%d W", int(pw_consumption))

		scd := fronius.New()
		_, err = scd.Handler(solarPowerProduction, capacity2charge, capacity_max, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, fronius_ip)
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	}
}

func crontabSchedule(apiKey string, url string, fronius_ip string, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, crontab string, defaults bool) {
	layout := "15:04"
	endTime, _ := time.Parse(layout, end_hr)
	endTime = endTime.Add(-5 * time.Minute)
	end_crontab := strconv.Itoa(endTime.Minute()) + " " + strconv.Itoa(endTime.Hour()) + " * * *"

	c := cron.New()
	_, err := c.AddFunc(crontab, func() {
		schedule(apiKey, url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr)
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
