package cmd

import (
	"errors"
	"fmt"
	"os"
	u "sbam/src/utils"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var fronius_ip string
var appVersion = "dev"

var rootCmd = &cobra.Command{
	Use:   "sbam",
	Short: "sbam",
	Long: `sbam - Smart Battery Advanced Manager.
	Charge Fronius© battery using weather forecast.
	Initiate parameters from command line, env variables or config.yaml file.`,
}

func Execute() error {
	u.Log.Debug("Debug Logs activated: true")
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

	err := viper.ReadInConfig()
	if err != nil {
		// If the config file is not present, log at debug level and continue.
		if errors.As(err, &viper.ConfigFileNotFoundError{}) {
			u.Log.Debug("Config file not found; proceeding without config.yaml")
		} else {
			u.HandleErrorPanic(err, "Error reading config file")
		}
	}

	// Register all subcommands. Per-file init() blocks were consolidated
	// here on purpose so the package has exactly one init() function and
	// the registration order is explicit (see issue #68).
	registerCfgCmd()
	registerEstCmd()
	registerScdCmd()
}

func SetVersionInfo(version, commit, date string) error {
	if len(version) > 0 {
		appVersion = version
	}
	rootCmd.Version = fmt.Sprintf("%s (Built on %s from Git SHA %s)", version, date, commit)
	return nil
}

// bindFlags binds every defined flag of cmd to viper using the flag's name as
// the viper key. It is intended to be called from each subcommand's
// PersistentPreRunE so the currently executing subcommand owns the binding.
// This restores Viper's documented precedence (flag > env > config > default)
// for keys shared across multiple subcommands.
func bindFlags(cmd *cobra.Command) error {
	var firstErr error
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if firstErr != nil {
			return
		}
		if err := viper.BindPFlag(f.Name, f); err != nil {
			firstErr = err
		}
	})
	return firstErr
}
