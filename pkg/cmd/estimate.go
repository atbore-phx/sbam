package cmd

import (
	"fmt"
	"sbam/pkg/power"
	"sbam/pkg/storage"
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
		_, err := pwr.Handler(apiKey, url)
		if err != nil {
			panic(err)
		}

		str := storage.New()
		_, _, err = str.Handler(fronius_ip)
		if err != nil {
			panic(err)
		}
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
