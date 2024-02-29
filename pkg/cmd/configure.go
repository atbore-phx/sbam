package cmd

import (
	"ha-fronius-bm/pkg/fronius"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgCmd = &cobra.Command{
	Use:   "configure",
	Short: "Configure Battery Storage Charge",
	Long:  `connect via modbus to the fronius inverter and set charging`,
	Run: func(cmd *cobra.Command, args []string) {
		fronius_ip := viper.GetString("fronius_ip")
		fronius.Setdefaults(fronius_ip, "502")

	},
}

func init() {
	rootCmd.AddCommand(cfgCmd)
}
