package cmd

import (
	"errors"
	pw "sbam/pkg/power"
	"sbam/pkg/storage"
	u "sbam/src/utils"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var e_url string
var e_apiKey string
var e_cache_forecast bool
var e_cache_file_name string
var e_cache_time int32

var estCmd = &cobra.Command{
	Use:   "estimate",
	Short: "Estimate Forecast Solar Power",
	Long:  `Print the solar forecast and the battery storage power`,
	Run: func(cmd *cobra.Command, args []string) {
		e_url = viper.GetString("url")
		e_apiKey = viper.GetString("apikey")
		fronius_ip = viper.GetString("fronius_ip")
                e_cache_forecast = viper.GetBool("cache_forecast")
                e_cache_file_name = viper.GetString("cache_file_name")
                e_cache_time = viper.GetInt32("cache_time")

		err := CheckEstimate(e_apiKey, e_url, fronius_ip)
		if err != nil {
			u.Log.Error(err)
			return
		}
		estimate(e_apiKey, e_url, fronius_ip)

	},
}

func init() {
	estCmd.Flags().StringVarP(&e_url, "url", "u", "", "Set the forecast URL. For multiple URLs, use a comma (,) to separate them")
	estCmd.Flags().StringVarP(&e_apiKey, "apikey", "k", "", "set APIKEY")
	estCmd.Flags().StringVarP(&fronius_ip, "fronius_ip", "H", "", "set FRONIUS_IP")
        estCmd.Flags().BoolVarP(&e_cache_forecast, "cache_forecast", "n", false, "CACHE_FORECAST (default false)")
        estCmd.Flags().StringVarP(&e_cache_file_name, "cache_file_name", "f", "cached_forecast.json", "CACHE_FILE_NAME (default 'cached_forecast.json')")
        estCmd.Flags().Int32VarP(&e_cache_time, "cache_time", "l", 7200, "CACHE_TIME (default 7200)")

	viper.BindPFlag("url", estCmd.Flags().Lookup("url"))
	viper.BindPFlag("apikey", estCmd.Flags().Lookup("apikey"))
	viper.BindPFlag("fronius_ip", estCmd.Flags().Lookup("fronius_ip"))
        viper.BindPFlag("cache_forecast", estCmd.Flags().Lookup("cache_forecast"))
        viper.BindPFlag("cache_file_name", estCmd.Flags().Lookup("cache_file_name"))
        viper.BindPFlag("cache_time", estCmd.Flags().Lookup("cache_time"))

	rootCmd.AddCommand(estCmd)
}

func CheckEstimate(apiKey string, url string, fronius_ip string) error {
	if len(strings.TrimSpace(fronius_ip)) == 0 {
		err := errors.New("the --fronius_ip flag must be set")
		return err
	} else if len(strings.TrimSpace(apiKey)) == 0 {
		err := errors.New("the --apikey flag must be set")
		return err
	} else if len(strings.TrimSpace(url)) == 0 {
		err := errors.New("the --url flag must be set")
		return err
	}
	return nil
}

func estimate(apiKey string, url string, fronius_ip string) {
	pwr := pw.New()
	_, _, err := pwr.Handler(apiKey, url)
	if err != nil {
		u.Log.Error(err)
		panic(err)
	}

	str := storage.New()
	_, _, err = str.Handler(fronius_ip)
	if err != nil {
		u.Log.Error(err)
		panic(err)
	}
}
