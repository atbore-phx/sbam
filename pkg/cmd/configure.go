package cmd

import (
	"fmt"
	"sbam/pkg/fronius"
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
		if len(strings.TrimSpace(fronius_ip)) == 0 {
			fmt.Println("The --fronius_ip flag must be set")
			return
		}
		if cmd.Flags().Changed("defaults") {
			fronius.Setdefaults(fronius_ip)
		} else if cmd.Flags().Changed("force-charge") {
			power := viper.GetViper().GetInt("power")
			if power == 0 {
				fmt.Println("The --power flag must be set when using --force-charge")
				return
			}
			fronius.ForceCharge(fronius_ip, int16(power))
		}

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
