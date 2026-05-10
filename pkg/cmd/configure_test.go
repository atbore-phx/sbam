package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withConfigureGlobals(t *testing.T, defaults, force bool) {
	t.Helper()
	oldDefaults := c_defaults
	oldForceCharge := force_charge

	c_defaults = defaults
	force_charge = force

	t.Cleanup(func() {
		c_defaults = oldDefaults
		force_charge = oldForceCharge
	})
}

func TestCheckConfigure(t *testing.T) {
	tests := []struct {
		name      string
		froniusIP string
		wantErr   string
	}{
		{
			name:      "missing ip",
			froniusIP: "",
			wantErr:   "--fronius_ip",
		},
		{
			name:      "blank ip",
			froniusIP: "   ",
			wantErr:   "--fronius_ip",
		},
		{
			name:      "valid ip",
			froniusIP: "127.0.0.1",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := checkConfigure(tc.froniusIP)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestConfigure_ForceChargeWithoutPowerDoesNotPanic(t *testing.T) {
	withConfigureGlobals(t, false, true)

	assert.NotPanics(t, func() {
		configure("unused", 0)
	})
}

func TestConfigure_PanicsWhenDefaultsWriteFails(t *testing.T) {
	withConfigureGlobals(t, true, false)

	assert.Panics(t, func() {
		configure("bad host", 0)
	})
}

func TestConfigure_PanicsWhenForceChargeWriteFails(t *testing.T) {
	withConfigureGlobals(t, false, true)

	assert.Panics(t, func() {
		configure("bad host", 20)
	})
}
