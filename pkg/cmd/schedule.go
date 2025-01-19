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
var pw_batt_reserve float64
var batt_reserve_start_hr string
var batt_reserve_end_hr string
var crontab string
var s_defaults bool

const (
	const_pc    = 0.0
	const_sh    = "00:00"
	const_eh    = "00:55"
	const_mc    = 3500
	const_pbr   = 0
	const_br_sh = const_sh
	const_br_eh = const_eh
	const_ct    = "0 0 0 0 0"
)

var scdCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule Battery Storage Charge",
	Long:  `Workflow to Check Forecast and Battery residual Capacity and decide if it has to be charged in a definited time range`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(s_url) == 0 {
			s_url = viper.GetString("url")
		}
		if len(s_apiKey) == 0 {
			s_apiKey = viper.GetString("apikey")
		}
		if len(fronius_ip) == 0 {
			fronius_ip = viper.GetString("fronius_ip")
		}
		if pw_consumption == const_pc {
			if _, exists := os.LookupEnv("PW_CONSUMPTION"); exists {
				pw_consumption = viper.GetFloat64("pw_consumption")
			}
		}
		if start_hr == const_sh {
			if _, exists := os.LookupEnv("START_HR"); exists {
				start_hr = viper.GetString("start_hr")
			}
		}
		if end_hr == const_eh {
			if _, exists := os.LookupEnv("END_HR"); exists {
				end_hr = viper.GetString("end_hr")
			}
		}
		if max_charge == const_mc {
			if _, exists := os.LookupEnv("MAX_CHARGE"); exists {
				max_charge = viper.GetFloat64("max_charge")
			}
		}
		if pw_batt_reserve == const_pbr {
			if _, exists := os.LookupEnv("PW_BATT_RESERVE"); exists {
				pw_batt_reserve = viper.GetFloat64("pw_batt_reserve")
			}
		}
		if batt_reserve_start_hr == const_br_sh {
			if _, exists := os.LookupEnv("BATT_RESERVE_START_HR"); exists {
				if len(viper.GetString("batt_reserve_start_hr")) == 0 {
					batt_reserve_start_hr = viper.GetString("start_hr")
				} else {
					batt_reserve_start_hr = viper.GetString("batt_reserve_start_hr")
				}
			}
		}
		if batt_reserve_end_hr == const_br_sh {
			if _, exists := os.LookupEnv("BATT_RESERVE_END_HR"); exists {
				if len(viper.GetString("batt_reserve_end_hr")) == 0 {
					batt_reserve_end_hr = viper.GetString("end_hr")
				} else {
					batt_reserve_end_hr = viper.GetString("batt_reserve_end_hr")
				}
			}
		}
		if crontab == const_ct {
			if _, exists := os.LookupEnv("CRONTAB"); exists {
				crontab = viper.GetString("crontab")
			}
		}
		if s_defaults {
			if _, exists := os.LookupEnv("DEFAULTS"); exists {
				s_defaults = viper.GetBool("defaults")
			}
		}

		err := checkScheduleschedule(crontab, s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr)
		if err != nil {
			u.Log.Error(err)
			return
		}

		if crontab != "0 0 0 0 0" {
			crontabSchedule(s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, crontab, s_defaults, batt_reserve_start_hr, batt_reserve_end_hr)

		} else {
			schedule(s_apiKey, s_url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, batt_reserve_start_hr, batt_reserve_end_hr)

		}
	},
}

func init() {
	scdCmd.Flags().StringVarP(&s_url, "url", "u", "", "Set the URL. For multiple URLs, use a comma (,) to separate them")
	scdCmd.Flags().StringVarP(&s_apiKey, "apikey", "k", "", "APIKEY")
	scdCmd.Flags().StringVarP(&fronius_ip, "fronius_ip", "H", "", "FRONIUS_IP")
	scdCmd.Flags().StringVarP(&start_hr, "start_hr", "s", const_sh, "START_HR")
	scdCmd.Flags().StringVarP(&end_hr, "end_hr", "e", const_eh, "END_HR")
	scdCmd.Flags().StringVarP(&crontab, "crontab", "t", const_ct, "CRONTAB")
	scdCmd.Flags().Float64VarP(&pw_consumption, "pw_consumption", "c", const_pc, "PW_CONSUMPTION")
	scdCmd.Flags().Float64VarP(&max_charge, "max_charge", "m", const_mc, "MAX_CHARGE")
	scdCmd.Flags().Float64VarP(&pw_batt_reserve, "pw_batt_reserve", "r", const_pbr, "PW_BATT_RESERVE")
	scdCmd.Flags().StringVarP(&batt_reserve_start_hr, "batt_reserve_start_hr", "S", const_br_sh, "BATT_RESERVE_START_HR")
	scdCmd.Flags().StringVarP(&batt_reserve_end_hr, "batt_reserve_end_hr", "E", const_br_eh, "BATT_RESERVE_END_HR")
	scdCmd.Flags().BoolVarP(&s_defaults, "defaults", "d", true, "DEFAULTS")

	viper.BindPFlag("url", scdCmd.Flags().Lookup("url"))
	viper.BindPFlag("apikey", scdCmd.Flags().Lookup("apikey"))
	viper.BindPFlag("fronius_ip", scdCmd.Flags().Lookup("fronius_ip"))
	viper.BindPFlag("pw_consumption", scdCmd.Flags().Lookup("pw_consumption"))
	viper.BindPFlag("start_hr", scdCmd.Flags().Lookup("start_hr"))
	viper.BindPFlag("end_hr", scdCmd.Flags().Lookup("end_hr"))
	viper.BindPFlag("crontab", scdCmd.Flags().Lookup("crontab"))
	viper.BindPFlag("max_charge", scdCmd.Flags().Lookup("max_charge"))
	viper.BindPFlag("pw_batt_reserve", scdCmd.Flags().Lookup("pw_batt_reserve"))
	viper.BindPFlag("batt_reserve_start_hr", scdCmd.Flags().Lookup("batt_reserve_start_hr"))
	viper.BindPFlag("batt_reserve_end_hr", scdCmd.Flags().Lookup("batt_reserve_end_hr"))
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

func schedule(apiKey string, url string, fronius_ip string, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, batt_reserve_start_hr string, batt_reserve_end_hr string) {
	if !CheckTimeRange(start_hr, end_hr) {
		u.Log.Info("not in time range: " + start_hr + " <= t <= " + end_hr)
	} else {
		pwr := pw.New()
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
		u.Log.Infof("your Daily consumption is:%d Wh", int(pw_consumption))

		scd := fronius.New()
		_, err = scd.Handler(solarPowerProduction, capacity2charge, capacity_max, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, fronius_ip, CheckTimeRange(batt_reserve_start_hr, batt_reserve_end_hr))
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	}
}

func crontabSchedule(apiKey string, url string, fronius_ip string, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, crontab string, defaults bool, batt_reserve_start_hr string, batt_reserve_end_hr string) {
	layout := "15:04"
	endTime, _ := time.Parse(layout, end_hr)
	endTime = endTime.Add(-5 * time.Minute)
	end_crontab := strconv.Itoa(endTime.Minute()) + " " + strconv.Itoa(endTime.Hour()) + " * * *"

	c := cron.New()
	_, err := c.AddFunc(crontab, func() {
		schedule(apiKey, url, fronius_ip, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, batt_reserve_start_hr, batt_reserve_end_hr)
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
