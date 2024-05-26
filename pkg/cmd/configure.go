package cmd

import (
	"errors"
	"os"
	"sbam/pkg/fronius"
	u "sbam/src/utils"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)


var c_defaults bool
var force_charge bool
var power int

const const_pw = 0

var cfgCmd = &cobra.Command{
	Use:   "configure",
	Short: "Configure Battery Storage Charge",
	Long:  `connect via modbus to the fronius inverter and set charging`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(fronius_ip) == 0 { fronius_ip = viper.GetString("fronius_ip") }
		if !c_defaults {
			if _, exists := os.LookupEnv("DEFAULTS"); exists {
				c_defaults = viper.GetBool("defaults")
			}
		}
		if !force_charge {
			if _, exists := os.LookupEnv("FORCE_CHARGE"); exists {
				force_charge = viper.GetBool("force_charge")
			}
		}
		if power == const_pw {
			if _, exists := os.LookupEnv("POWER"); exists {
				power = viper.GetInt("power")
			}
		}

		err := checkConfigure(fronius_ip)
		if err != nil {
			u.Log.Error(err)
			return
		}

		configure(fronius_ip, power, cmd)

	},
}

func init() {
	cfgCmd.Flags().StringVarP(&fronius_ip,"fronius_ip", "H", "", "set FRONIUS_IP")
	cfgCmd.Flags().BoolVarP(&c_defaults,"defaults", "d", false, "set DEFAULTS")
	cfgCmd.Flags().BoolVarP(&force_charge,"force_charge", "f", false, "set FORCE_CHARGE")
	cfgCmd.Flags().IntVarP(&power,"power", "p", const_pw, "set percent of nominal POWER")
	viper.BindPFlag("fronius_ip", cfgCmd.Flags().Lookup("fronius_ip"))
	viper.BindPFlag("defaults", scdCmd.Flags().Lookup("defaults"))
	viper.BindPFlag("force_charge", cfgCmd.Flags().Lookup("force_charge"))
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
	if c_defaults {
		err := fronius.Setdefaults(fronius_ip)
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	} else if force_charge {
		if power == 0 {
			u.Log.Error("The --power flag must be set when using --force_charge")
			return
		}
		err := fronius.ForceCharge(fronius_ip, int16(power))
		if err != nil {
			u.Log.Error(err)
			panic(err)
		}
	}

}
