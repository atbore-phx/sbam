package homeassistant

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// T-0080 (AC-21): Supervisor token vs ha_url/ha_token vs neither.
func TestResolveAccess_T0080(t *testing.T) {
	tests := []struct {
		name    string
		haURL   string
		haToken string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{
			name: "supervisor token only",
			env:  map[string]string{"SUPERVISOR_TOKEN": "test-sup-token"},
			want: Config{BaseURL: "http://supervisor/core/api", Token: "test-sup-token", Source: SourceSupervisor},
		},
		{
			name:    "ha_url wins over supervisor",
			haURL:   "http://homeassistant.local:8123",
			haToken: "test-ha-token",
			env:     map[string]string{"SUPERVISOR_TOKEN": "test-sup-token"},
			want:    Config{BaseURL: "http://homeassistant.local:8123/api", Token: "test-ha-token", Source: SourceURL},
		},
		{
			name:    "ha_url with trailing slash",
			haURL:   "https://ha.example.test/",
			haToken: "test-ha-token",
			want:    Config{BaseURL: "https://ha.example.test/api", Token: "test-ha-token", Source: SourceURL},
		},
		{
			name:    "ha_url already ends in /api/",
			haURL:   "https://ha.example.test/api/",
			haToken: "test-ha-token",
			want:    Config{BaseURL: "https://ha.example.test/api", Token: "test-ha-token", Source: SourceURL},
		},
		{
			name:    "ha_url with sub path",
			haURL:   "https://proxy.example.test/hass",
			haToken: "test-ha-token",
			want:    Config{BaseURL: "https://proxy.example.test/hass/api", Token: "test-ha-token", Source: SourceURL},
		},
		{
			name:    "ha_url without token",
			haURL:   "http://192.0.2.10:8123",
			env:     map[string]string{"SUPERVISOR_TOKEN": "test-sup-token"},
			wantErr: "ha_token is required",
		},
		{
			name:    "neither configured",
			haToken: "test-ha-token",
			env:     map[string]string{},
			wantErr: "no access configured",
		},
		{
			name:    "whitespace supervisor token",
			env:     map[string]string{"SUPERVISOR_TOKEN": "   "},
			wantErr: "no access configured",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAccess(tt.haURL, tt.haToken, envOf(tt.env))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveAccess_NilGetenv(t *testing.T) {
	_, err := ResolveAccess("", "", nil)
	assert.Error(t, err)
}

// T-0087 (TC-014a): invalid ha_url values are rejected and never echoed.
func TestResolveAccess_InvalidURL_T0087(t *testing.T) {
	tests := []struct {
		name    string
		haURL   string
		wantErr string
	}{
		{"ftp scheme", "ftp://x", "scheme"},
		{"userinfo", "http://u:p@h", "userinfo"},
		{"userinfo user only", "https://user@ha.example.test", "userinfo"},
		{"query", "http://ha.example.test:8123/?x=1", "query"},
		{"empty query", "http://ha.example.test:8123/?", "query"},
		{"fragment", "http://ha.example.test:8123/#frag", "fragment"},
		{"no host", "http:///api", "host"},
		{"no scheme", "ha.example.test:8123", ""},
		{"garbage", "http://[::1", "valid URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveAccess(tt.haURL, "test-ha-token", envOf(nil))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.NotContains(t, err.Error(), "u:p")
			assert.NotContains(t, err.Error(), "test-ha-token")
		})
	}
}

// T-0087 (TC-014c): entity IDs that could inject into URLs are rejected.
func TestValidateEntityID_T0087(t *testing.T) {
	valid := []string{"sensor.house_load_power", "sensor.nordpool_kwh_dk1_dkk_3_10_025", "sensor.a", "input_number.x1"}
	for _, id := range valid {
		assert.NoError(t, ValidateEntityID(id), id)
	}
	invalid := []string{
		"",
		"sensor.a&end_time=2000",
		"sensor",
		"Sensor.House",
		"sensor.a/../../config",
		"sensor.a?x=1",
		"sensor.a b",
		".a",
		"sensor.",
		"sensor.a.b",
		"sensor.a%26",
	}
	for _, id := range invalid {
		assert.Error(t, ValidateEntityID(id), id)
	}
}

func TestConfig_StringRedactsToken(t *testing.T) {
	cfg := Config{BaseURL: "http://supervisor/core/api", Token: "test-sup-token", Source: SourceSupervisor}
	for _, s := range []string{fmt.Sprint(cfg), fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), fmt.Sprintf("%s", cfg)} {
		assert.NotContains(t, s, "test-sup-token")
		assert.Contains(t, s, "***")
	}
	assert.NotContains(t, fmt.Sprint(Config{}), "***")
}

func TestConfig_PlainHTTPToPublicHost(t *testing.T) {
	tests := []struct {
		base string
		src  string
		want bool
	}{
		{"http://supervisor/core/api", SourceSupervisor, false},
		{"http://127.0.0.1:8123/api", SourceURL, false},
		{"http://localhost:8123/api", SourceURL, false},
		{"http://192.168.1.10:8123/api", SourceURL, false},
		{"http://10.0.0.5:8123/api", SourceURL, false},
		{"http://homeassistant:8123/api", SourceURL, false},
		{"http://homeassistant.local:8123/api", SourceURL, false},
		{"http://[::1]:8123/api", SourceURL, false},
		{"https://ha.example.test/api", SourceURL, false},
		{"http://203.0.113.7:8123/api", SourceURL, true},
		{"http://ha.example.test/api", SourceURL, true},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			assert.Equal(t, tt.want, Config{BaseURL: tt.base, Source: tt.src}.PlainHTTPToPublicHost())
		})
	}
}
