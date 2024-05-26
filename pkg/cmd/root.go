package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)


var fronius_ip string

var rootCmd = &cobra.Command{
	Use:   "sbam",
	Short: "sbam",
	Long: `sbam - Smart Battery Advanced Manager.
	Charge Fronius© battery using weather forecast.
	Initiate parameters from command line, env variables or config.yaml file.`,
}

func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
	if _, err := os.Stat("config.yaml"); os.IsNotExist(err) {
		if len(os.Args) == 1 {
			// No command or arguments were provided, execute help command
			rootCmd.Help()
			os.Exit(0)
		}
	}
	return nil
}

func init() {
	viper.AutomaticEnv()

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	viper.ReadInConfig()
}

func SetVersionInfo(version, commit, date string) error {
	rootCmd.Version = fmt.Sprintf("%s (Built on %s from Git SHA %s)", version, date, commit)
	return nil
}
