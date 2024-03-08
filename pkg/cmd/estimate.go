package cmd

import (
	"errors"
	"sbam/pkg/power"
	"sbam/pkg/storage"
	u "sbam/src/utils"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var estCmd = &cobra.Command{
	Use:   "estimate",
	Short: "Estimate Forecast Solar Power",
	Long:  `Print the solar forecast and the battery storage power`,
	Run: func(cmd *cobra.Command, args []string) {
		url := viper.GetString("url")
		apiKey := viper.GetString("apikey")
		fronius_ip := viper.GetString("fronius_ip")

		err := CheckEstimate(apiKey, url, fronius_ip)
		if err != nil {
			u.Log.Error(err)
			return
		}
		estimate(apiKey, url, fronius_ip)

	},
}

func init() {
	estCmd.Flags().StringP("url", "u", "", "URL")
	estCmd.Flags().StringP("apikey", "k", "", "APIKEY")
	estCmd.Flags().StringP("fronius_ip", "H", "", "FRONIUS_IP")
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
		err := errors.New("the --apiKey flag must be set")
		return err
	} else if len(strings.TrimSpace(url)) == 0 {
		err := errors.New("the --url flag must be set")
		return err
	}
	return nil
}

func estimate(apiKey string, url string, fronius_ip string) {
	pwr := power.New()
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
