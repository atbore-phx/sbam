package cmd

import (
	"errors"
	"sbam/pkg/fronius"
	u "sbam/src/utils"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgCmd = &cobra.Command{
	Use:   "configure",
	Short: "Configure Battery Storage Charge",
	Long:  `connect via modbus to the fronius inverter and set charging`,
	Run: func(cmd *cobra.Command, args []string) {
		fronius_ip := viper.GetString("fronius_ip")
		power := viper.GetViper().GetInt("power")

		err := checkConfigure(fronius_ip)
		if err != nil {
			u.Log.Error(err)
			return
		}

		configure(fronius_ip, power, cmd)

	},
}

func init() {
	cfgCmd.Flags().StringP("fronius_ip", "H", "", "FRONIUS_IP")
	cfgCmd.Flags().BoolP("defaults", "d", false, "Set defaults")
	cfgCmd.Flags().BoolP("force-charge", "f", false, "Force charge")
	cfgCmd.Flags().Int16P("power", "p", 0, "Power (percent of nominal power)")
	viper.BindPFlag("fronius_ip", cfgCmd.Flags().Lookup("fronius_ip"))
	viper.BindPFlag("power", cfgCmd.Flags().Lookup("power"))
	rootCmd.AddCommand(cfgCmd)
}

func checkConfigure(fronius_ip string) error {
	if len(strings.TrimSpace(fronius_ip)) == 0 {
		err := errors.New("the --fronius_ip flag must be set")
		return err
	}
	return nil
}

func configure(fronius_ip string, power int, cmd *cobra.Command) {
	if cmd.Flags().Changed("defaults") {
		err := fronius.Setdefaults(fronius_ip)
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	} else if cmd.Flags().Changed("force-charge") {
		if power == 0 {
			u.Log.Error("The --power flag must be set when using --force-charge")
			return
		}
		err := fronius.ForceCharge(fronius_ip, int16(power))
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	}

}
