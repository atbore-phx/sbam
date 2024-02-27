package main

import (
	"fmt"
	"ha-fronius-bm/pkg/forecast"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	version = "dev"
	commit  = "HEAD"
	date    = "today"
)

var rootCmd = &cobra.Command{
	Use:     "ha-fronius-bm",
	Short:   "ha-fronius-bm handles battery charge using weather forecast",
	Long:    `initiate URL and APIKEY from command line, env variables or config.yaml file.`,
	Version: fmt.Sprintf("Version: %s\nCommit: %s\nDate: %s\n", version, commit, date),
	Run: func(cmd *cobra.Command, args []string) {
		url := viper.GetString("url")
		apiKey := viper.GetString("apikey")

		pwr := forecast.New()
		solarPowerProduction, err := pwr.Handler(apiKey, url)
		if err != nil {
			panic(err)
		}

		fmt.Print(solarPowerProduction)
	},
}

func init() {
	viper.AutomaticEnv()

	rootCmd.PersistentFlags().String("url", "", "URL")
	rootCmd.PersistentFlags().String("apikey", "", "APIKEY")
	viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))
	viper.BindPFlag("apikey", rootCmd.PersistentFlags().Lookup("apikey"))

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Printf("Error reading config, %s", err)
	}
}

func main() {
	if _, err := os.Stat("config.yaml"); os.IsNotExist(err) {
		if len(os.Args) == 1 {
			// No command or arguments were provided, execute help command
			rootCmd.Help()
			os.Exit(0)
		}
	}
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

}
