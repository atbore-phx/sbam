package cmd

import (
	"fmt"
	"ha-fronius-bm/pkg/power"
	"ha-fronius-bm/pkg/storage"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// filesCmd represents the files command
var estCmd = &cobra.Command{
	Use:   "estimate",
	Short: "Estimate Forecast Solar Power",
	Long:  `Print the solar forecast and the battery storage power`,
	Run: func(cmd *cobra.Command, args []string) {
		url := viper.GetString("url")
		apiKey := viper.GetString("apikey")
		fronius_ip := viper.GetString("fronius_ip")

		pwr := power.New()
		solarPowerProduction, err := pwr.Handler(apiKey, url)
		if err != nil {
			panic(err)
		}
		fmt.Println("Forecast Solar Power:", solarPowerProduction)

		str := storage.New()
		capacity2charge, err := str.Handler(fronius_ip)
		if err != nil {
			panic(err)
		}
		fmt.Println("Battery Capacity to charge:", capacity2charge)
	},
}

func init() {
	rootCmd.AddCommand(estCmd)
}
