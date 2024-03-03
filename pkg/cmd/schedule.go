package cmd

import (
	"fmt"
	"sbam/pkg/fronius"
	"sbam/pkg/power"
	"sbam/pkg/storage"
	u "sbam/src/utils"
	"strings"

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
		max_charge := viper.GetInt("max_charge")

		if len(strings.TrimSpace(fronius_ip)) == 0 {
			fmt.Println("The --fronius_ip flag must be set")
			return
		} else if len(strings.TrimSpace(apiKey)) == 0 {
			fmt.Println("The --apiKey flag must be set")
			return
		} else if len(strings.TrimSpace(url)) == 0 {
			fmt.Println("The --url flag must be set")
			return
		}

		pwr := power.New()
		solarPowerProduction, err := pwr.Handler(apiKey, url)
		if err != nil {
			panic(err)
		}

		str := storage.New()
		capacity2charge, err := str.Handler(fronius_ip)
		if err != nil {
			panic(err)
		}
		u.Log.Infof("your Daily consumption is:%d W", int(pw_consumption))

		scd := fronius.New()
		_, err = scd.Handler(solarPowerProduction, capacity2charge, pw_consumption, max_charge, start_hr, end_hr, fronius_ip)
		if err != nil {
			panic(err)
		}
	},
}

func init() {
	scdCmd.Flags().StringP("url", "u", "", "URL")
	scdCmd.Flags().StringP("apikey", "k", "", "APIKEY")
	scdCmd.Flags().StringP("fronius_ip", "H", "", "FRONIUS_IP")
	scdCmd.Flags().StringP("start_hr", "s", "00:00", "START_HR")
	scdCmd.Flags().StringP("end_hr", "e", "06:00", "END_HR")
	scdCmd.Flags().Float64P("pw_consumption", "c", 0.0, "PW_CONSUMPTION")
	scdCmd.Flags().IntP("max_charge", "m", 3500, "MAX_CHARGE")

	viper.BindPFlag("url", scdCmd.Flags().Lookup("url"))
	viper.BindPFlag("apikey", scdCmd.Flags().Lookup("apikey"))
	viper.BindPFlag("fronius_ip", scdCmd.Flags().Lookup("fronius_ip"))
	viper.BindPFlag("pw_consumption", scdCmd.Flags().Lookup("pw_consumption"))
	viper.BindPFlag("start_hr", scdCmd.Flags().Lookup("start_hr"))
	viper.BindPFlag("end_hr", scdCmd.Flags().Lookup("end_hr"))
	viper.BindPFlag("max_charge", scdCmd.Flags().Lookup("max_charge"))
	rootCmd.AddCommand(scdCmd)
}
