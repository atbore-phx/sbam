package cmd

import (
	"os"
	"testing"

	"sbam/pkg/fronius"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tbrandon/mbserver"
	yaml "gopkg.in/yaml.v3"
)

type verifyCall struct {
	sunspecType fronius.SunSpecType
	froniusIP   string
}

func stubVerifySunSpecType(t *testing.T) *[]verifyCall {
	t.Helper()
	calls := []verifyCall{}
	old := verifySunSpecType
	verifySunSpecType = func(st fronius.SunSpecType, ip string, _ ...string) {
		calls = append(calls, verifyCall{st, ip})
	}
	t.Cleanup(func() { verifySunSpecType = old })
	return &calls
}

func TestSetupSunSpecType(t *testing.T) {
	calls := stubVerifySunSpecType(t)

	st, err := setupSunSpecType("float", "127.0.0.1", true)
	assert.NoError(t, err)
	assert.Equal(t, fronius.SunSpecFloat, st)
	assert.Equal(t, []verifyCall{{fronius.SunSpecFloat, "127.0.0.1"}}, *calls, "verify runs with the configured type")

	st, err = setupSunSpecType("", "127.0.0.1", false)
	assert.NoError(t, err)
	assert.Equal(t, fronius.SunSpecIntSF, st, "empty selects the default")
	assert.Len(t, *calls, 1, "verify skipped when not requested")
}

func TestSetupSunSpecTypeInvalid(t *testing.T) {
	calls := stubVerifySunSpecType(t)

	_, err := setupSunSpecType("bogus", "127.0.0.1", true)

	assert.ErrorContains(t, err, "invalid sunspec_type")
	assert.Empty(t, *calls, "no inverter access on invalid config")
}

func TestSunSpecTypeFlagDefaults(t *testing.T) {
	for _, name := range []string{"schedule", "configure"} {
		cmd, _, err := rootCmd.Find([]string{name})
		require.NoError(t, err)
		flag := cmd.Flags().Lookup("sunspec_type")
		if assert.NotNil(t, flag, name) {
			assert.Equal(t, "int_sf", flag.DefValue, name)
		}
	}
}

func newSunSpecFlagCmd() *cobra.Command {
	c := &cobra.Command{Use: "probe", Run: func(cmd *cobra.Command, args []string) {}}
	c.Flags().String("sunspec_type", const_sunspec_type, "SunSpec model type")
	return c
}

func TestSunSpecTypePrecedence(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		env  string
		flag string
		want string
	}{
		{"default", "", "", "", "int_sf"},
		{"yaml beats default", "sunspec_type: float\n", "", "", "float"},
		{"env beats yaml", "sunspec_type: float\n", "auto", "", "auto"},
		{"flag beats env and yaml", "sunspec_type: float\n", "auto", "int_sf", "int_sf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetViper(t)
			if tc.yaml != "" {
				writeConfig(t, tc.yaml)
			}
			if tc.env != "" {
				t.Setenv("SUNSPEC_TYPE", tc.env)
			}
			c := newSunSpecFlagCmd()
			if tc.flag != "" {
				require.NoError(t, c.Flags().Set("sunspec_type", tc.flag))
			}
			require.NoError(t, bindFlags(c))

			assert.Equal(t, tc.want, viper.GetString("sunspec_type"))
		})
	}
}

// The schedule command verifies the configured type (verify=true) before
// validating the rest of the config, and stops on an invalid value.
func TestScheduleRunWiresSunSpecType(t *testing.T) {
	t.Run("valid value is verified", func(t *testing.T) {
		resetViper(t)
		calls := stubVerifySunSpecType(t)
		viper.Set("fronius_ip", "192.0.2.10")
		viper.Set("sunspec_type", "auto")

		// No apikey/url: Run stops at config validation, after the verify.
		scdCmd.Run(scdCmd, nil)

		assert.Equal(t, []verifyCall{{fronius.SunSpecAuto, "192.0.2.10"}}, *calls)
	})
	t.Run("invalid value stops before the inverter is contacted", func(t *testing.T) {
		resetViper(t)
		calls := stubVerifySunSpecType(t)
		viper.Set("fronius_ip", "192.0.2.10")
		viper.Set("sunspec_type", "bogus")

		scdCmd.Run(scdCmd, nil)

		assert.Empty(t, *calls)
	})
	t.Run("no fronius_ip skips the verify", func(t *testing.T) {
		resetViper(t)
		calls := stubVerifySunSpecType(t)
		viper.Set("sunspec_type", "float")

		scdCmd.Run(scdCmd, nil)

		assert.Empty(t, *calls)
	})
}

// The configure command applies the type without the startup verify, and an
// invalid value stops before any battery write.
func TestConfigureRunWiresSunSpecType(t *testing.T) {
	t.Run("valid value is not verified", func(t *testing.T) {
		resetViper(t)
		calls := stubVerifySunSpecType(t)
		viper.Set("fronius_ip", "192.0.2.10")
		viper.Set("sunspec_type", "float")

		cfgCmd.Run(cfgCmd, nil) // neither --defaults nor --force_charge: no write

		assert.Empty(t, *calls)
	})
	t.Run("invalid value stops before writing", func(t *testing.T) {
		resetViper(t)
		calls := stubVerifySunSpecType(t)
		viper.Set("fronius_ip", "192.0.2.10")
		viper.Set("defaults", true)
		viper.Set("sunspec_type", "bogus")

		// configure() panics when a write fails, so reaching it would panic
		// on the unreachable test address.
		assert.NotPanics(t, func() { cfgCmd.Run(cfgCmd, nil) })
		assert.Empty(t, *calls)
	})
}

func TestAddonSchemaHasSunSpecType(t *testing.T) {
	data, err := os.ReadFile("../../home-assistant/addons/sbam/config.yaml")
	require.NoError(t, err)
	var config struct {
		Options map[string]interface{} `yaml:"options"`
		Schema  map[string]interface{} `yaml:"schema"`
	}
	require.NoError(t, yaml.Unmarshal(data, &config))

	assert.Equal(t, "int_sf", config.Options["sunspec_type"])
	assert.Equal(t, "list(int_sf|float|auto)?", config.Schema["sunspec_type"])
}

const writerTestAddr = "127.0.0.1"
const writerTestPort = "6504"

// startFloatInverter starts a mock Modbus server exposing the SunSpec model
// chain of a Fronius Gen24 set to "float" (model 124 at 40354).
func startFloatInverter(t *testing.T) *mbserver.Server {
	t.Helper()
	srv := mbserver.NewServer()
	require.NoError(t, srv.ListenTCP(writerTestAddr+":"+writerTestPort))
	t.Cleanup(srv.Close)

	set := func(addr, v uint16) { srv.HoldingRegisters[addr-1] = v }
	set(40001, 0x5375) // "Su"
	set(40002, 0x6e53) // "nS"
	addr := uint16(40003)
	for _, m := range [][2]uint16{{1, 65}, {113, 60}, {120, 26}, {121, 30}, {122, 44}, {123, 24}, {160, 88}, {124, 24}} {
		set(addr, m[0])
		set(addr+1, m[1])
		if m[0] == 124 {
			set(addr+25, 0xFFFE) // InOutWRte_SF = -2
		}
		addr += 2 + m[1]
	}
	set(addr, 0xFFFF)
	return srv
}

func holding(srv *mbserver.Server, addr uint16) int16 {
	return int16(srv.HoldingRegisters[addr-1])
}

// legacyStorageAddrs are the int+SF storage addresses, which fall inside the
// MPPT model when the inverter is set to "float".
var legacyStorageAddrs = []uint16{fronius.StorCtl_Mod, fronius.MinRsvPct, fronius.OutWRte, fronius.InWRte}

func TestBatteryWriterUsesSelectedType(t *testing.T) {
	for _, st := range []fronius.SunSpecType{fronius.SunSpecFloat, fronius.SunSpecAuto} {
		t.Run(string(st), func(t *testing.T) {
			srv := startFloatInverter(t)
			w := froniusBatteryWriter{sunspecType: st, port: writerTestPort}

			require.NoError(t, w.ForceCharge(writerTestAddr, 50))
			assert.Equal(t, int16(2), holding(srv, 40359), "StorCtl_Mod at the discovered address")
			assert.Equal(t, int16(-5000), holding(srv, 40366), "OutWRte at the discovered address")
			for _, a := range legacyStorageAddrs {
				assert.Equal(t, int16(0), holding(srv, a), "legacy address %d untouched", a)
			}

			require.NoError(t, w.SetDefaults(writerTestAddr))
			assert.Equal(t, int16(0), holding(srv, 40359), "StorCtl_Mod restored")
			assert.Equal(t, int16(10000), holding(srv, 40366), "OutWRte restored")
			for _, a := range legacyStorageAddrs {
				assert.Equal(t, int16(0), holding(srv, a), "legacy address %d untouched", a)
			}
		})
	}
}

func TestBatteryWriterIntSFUsesLegacyAddresses(t *testing.T) {
	srv := startFloatInverter(t)
	w := froniusBatteryWriter{sunspecType: fronius.SunSpecIntSF, port: writerTestPort}

	require.NoError(t, w.ForceCharge(writerTestAddr, 50))

	assert.Equal(t, int16(2), holding(srv, fronius.StorCtl_Mod), "int_sf writes the legacy StorCtl_Mod")
	assert.Equal(t, int16(-5000), holding(srv, fronius.OutWRte), "int_sf writes the legacy OutWRte")
	assert.Equal(t, int16(0), holding(srv, 40359), "discovered address untouched")
}

// The runner and the production factories carry the configured type.
func TestRunnerFactoriesCarrySunSpecType(t *testing.T) {
	r := NewRunner(RunnerConfig{SunSpecType: fronius.SunSpecFloat}, nil)
	w, ok := r.writer.(froniusBatteryWriter)
	require.True(t, ok)
	assert.Equal(t, fronius.SunSpecFloat, w.sunspecType)

	f, ok := newFronius(fronius.SunSpecAuto).(*fronius.Fronius)
	require.True(t, ok)
	assert.Equal(t, fronius.SunSpecAuto, f.SunSpecType)
}
