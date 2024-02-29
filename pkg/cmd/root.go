package cmd

import (
	"fmt"
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
	Long:    `initiate parameterss from command line, env variables or config.yaml file.`,
	Version: fmt.Sprintf("Version: %s\nCommit: %s\nDate: %s\n", version, commit, date),
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	viper.AutomaticEnv()
	rootCmd.PersistentFlags().String("url", "", "URL")
	rootCmd.PersistentFlags().String("apikey", "", "APIKEY")
	rootCmd.PersistentFlags().String("fronius_ip", "", "FRONIUS_IP")
	viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))
	viper.BindPFlag("apikey", rootCmd.PersistentFlags().Lookup("apikey"))
	viper.BindPFlag("apikey", rootCmd.PersistentFlags().Lookup("fronius_ip"))

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Printf("Error reading config, %s", err)
	}

	if _, err := os.Stat("config.yaml"); os.IsNotExist(err) {
		if len(os.Args) == 1 {
			// No command or arguments were provided, execute help command
			rootCmd.Help()
			os.Exit(0)
		}
	}
}
