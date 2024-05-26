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

var estCmd = &cobra.Command{
	Use:   "estimate",
	Short: "Estimate Forecast Solar Power",
	Long:  `Print the solar forecast and the battery storage power`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(e_url) == 0 {e_url = viper.GetString("url") }
		if len(e_apiKey) == 0 { e_apiKey = viper.GetString("apikey") }
		if len(fronius_ip) == 0 { fronius_ip = viper.GetString("fronius_ip") }

		err := CheckEstimate(e_apiKey, e_url, fronius_ip)
		if err != nil {
			u.Log.Error(err)
			return
		}
		estimate(e_apiKey, e_url, fronius_ip)

	},
}

func init() {
	estCmd.Flags().StringVarP(&e_url,"url", "u", "", "set URL")
	estCmd.Flags().StringVarP(&e_apiKey,"apikey", "k", "", "set APIKEY")
	estCmd.Flags().StringVarP(&fronius_ip,"fronius_ip", "H", "", "set FRONIUS_IP")

	viper.BindPFlag("url", estCmd.Flags().Lookup("url"))
	viper.BindPFlag("apikey", estCmd.Flags().Lookup("apikey"))
	viper.BindPFlag("fronius_ip", estCmd.Flags().Lookup("fronius_ip"))

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
	_, err := pwr.Handler(apiKey, url)
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
