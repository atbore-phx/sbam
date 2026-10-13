package cmd

import (
	"testing"

	"sbam/pkg/fronius"

	"github.com/stretchr/testify/assert"
)

func stubVerifySunSpecType(t *testing.T) *[]fronius.SunSpecType {
	t.Helper()
	calls := []fronius.SunSpecType{}
	old := verifySunSpecType
	verifySunSpecType = func(st fronius.SunSpecType, _ string, _ ...string) { calls = append(calls, st) }
	t.Cleanup(func() {
		verifySunSpecType = old
		fronius.SetSunSpecType(fronius.SunSpecIntSF)
	})
	return &calls
}

func TestSetupSunSpecType(t *testing.T) {
	calls := stubVerifySunSpecType(t)

	assert.NoError(t, setupSunSpecType("float", "127.0.0.1", true))
	assert.Equal(t, fronius.SunSpecFloat, fronius.ActiveSunSpecType())
	assert.Equal(t, []fronius.SunSpecType{fronius.SunSpecFloat}, *calls, "verify runs with the configured type")

	assert.NoError(t, setupSunSpecType("", "127.0.0.1", false))
	assert.Equal(t, fronius.SunSpecIntSF, fronius.ActiveSunSpecType(), "empty selects the default")
	assert.Len(t, *calls, 1, "verify skipped when not requested")
}

func TestSetupSunSpecTypeInvalid(t *testing.T) {
	calls := stubVerifySunSpecType(t)

	err := setupSunSpecType("bogus", "127.0.0.1", true)

	assert.ErrorContains(t, err, "invalid sunspec_type")
	assert.Equal(t, fronius.SunSpecIntSF, fronius.ActiveSunSpecType(), "invalid value keeps the current type")
	assert.Empty(t, *calls, "no inverter access on invalid config")
}

func TestSunSpecTypeFlagDefaults(t *testing.T) {
	for _, name := range []string{"schedule", "configure"} {
		cmd, _, err := rootCmd.Find([]string{name})
		assert.NoError(t, err)
		flag := cmd.Flags().Lookup("sunspec_type")
		if assert.NotNil(t, flag, name) {
			assert.Equal(t, "int_sf", flag.DefValue, name)
		}
	}
}

func TestFroniusBatteryWriterUsesSelectedType(t *testing.T) {
	stubVerifySunSpecType(t)
	assert.NoError(t, setupSunSpecType("float", "", false))
	w := froniusBatteryWriter{}

	// Nothing listens on this address: both calls must fail cleanly.
	assert.Error(t, w.ForceCharge("127.0.0.1:1", 10))
	assert.Error(t, w.SetDefaults("127.0.0.1:1"))
}
