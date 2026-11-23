package power

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-0310: pv_source accepts exactly solcast_api and ha_solcast after
// trimming; anything else is rejected naming the allowed values (S-027).
func TestValidatePVSource(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "solcast api", in: "solcast_api", want: PVSourceSolcastAPI},
		{name: "ha solcast trimmed", in: " ha_solcast ", want: PVSourceHASolcast},
		{name: "empty", in: "", wantErr: true},
		{name: "wrong case and dash", in: "HA-Solcast", wantErr: true},
		{name: "dash", in: "ha-solcast", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidatePVSource(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "pv_source")
				assert.Contains(t, err.Error(), "solcast_api, ha_solcast")
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// S-026: a long or multi-line value is quoted and truncated in the error.
func TestValidatePVSource_BoundsValue(t *testing.T) {
	_, err := ValidatePVSource(strings.Repeat("x\n", 500))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\n")
	assert.Less(t, len(err.Error()), 200)
	assert.Contains(t, err.Error(), "(truncated)")
}
