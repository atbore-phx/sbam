package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetViper clears viper's global state between tests and re-enables
// AutomaticEnv. Tests in this file mutate the global viper, so they must not
// run with t.Parallel().
func resetViper(t *testing.T) {
	t.Helper()
	viper.Reset()
	viper.AutomaticEnv()
	t.Cleanup(viper.Reset)
}

// writeConfig writes a config.yaml in a temp dir and points viper at it.
func writeConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	viper.SetConfigFile(path)
	require.NoError(t, viper.ReadInConfig())
}

// bindFlags is a local copy of pkg/cmd.bindFlags so this test file can
// exercise the helper without importing pkg/cmd (which would create an
// import cycle).
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

func TestDumpStartupParams_Expected(t *testing.T) {
	resetViper(t)
	writeConfig(t, "url: from-yaml\n")
	t.Setenv("FRONIUS_IP", "1.2.3.4")

	cmd := &cobra.Command{Use: "schedule", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("url", "", "URL")
	cmd.Flags().String("apikey", "", "APIKEY")
	cmd.Flags().String("fronius_ip", "", "FRONIUS_IP")
	cmd.Flags().Bool("defaults", true, "DEFAULTS")
	require.NoError(t, cmd.Flags().Set("apikey", "super-secret-value"))
	require.NoError(t, bindFlags(cmd))

	out := DumpStartupParams(cmd)

	assert.Contains(t, out, "effective startup parameters (subcommand: schedule)")
	assert.Contains(t, out, "apikey")
	assert.Contains(t, out, "***")
	assert.NotContains(t, out, "super-secret-value", "raw secret must never appear")
	assert.Regexp(t, `apikey\s+=\s+\*\*\*\s+source=flag`, out)
	assert.Regexp(t, `fronius_ip\s+=\s+"1\.2\.3\.4"\s+source=env`, out)
	assert.Regexp(t, `url\s+=\s+"from-yaml"\s+source=yaml`, out)
	assert.Regexp(t, `defaults\s+=\s+true\s+source=default`, out)
}

func TestDumpStartupParams_NumericAndBoolDefaults(t *testing.T) {
	resetViper(t)

	cmd := &cobra.Command{Use: "configure", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().Int("power", 0, "POWER")
	cmd.Flags().Bool("force_charge", false, "FORCE_CHARGE")
	require.NoError(t, bindFlags(cmd))

	out := DumpStartupParams(cmd)

	assert.Contains(t, out, "effective startup parameters (subcommand: configure)")
	assert.Regexp(t, `power\s+=\s+0\s+source=default`, out)
	assert.Regexp(t, `force_charge\s+=\s+false\s+source=default`, out)
}

func TestDumpStartupParams_NoFlagsNoConfig(t *testing.T) {
	resetViper(t)

	assert.NotPanics(t, func() {
		out := DumpStartupParams(nil)
		assert.Equal(t, "effective startup parameters", out)
	})
}

func TestDumpStartupParams_AutoDiscoversNewFlag(t *testing.T) {
	resetViper(t)

	cmd := &cobra.Command{Use: "probe", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("brand_new_flag", "", "a flag the helper has never seen")
	require.NoError(t, bindFlags(cmd))

	out := DumpStartupParams(cmd)

	assert.Contains(t, out, "brand_new_flag",
		"any newly registered flag must appear without editing the helper")
	assert.Regexp(t, `brand_new_flag\s+=\s+""\s+source=default`, out)
}

func TestDumpStartupParams_RedactsRegisteredSecret(t *testing.T) {
	resetViper(t)

	const secretKey = "extra_secret"
	SecretKeys[secretKey] = struct{}{}
	t.Cleanup(func() { delete(SecretKeys, secretKey) })

	cmd := &cobra.Command{Use: "probe", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String(secretKey, "", "extra secret")
	require.NoError(t, cmd.Flags().Set(secretKey, "should-not-leak"))
	require.NoError(t, bindFlags(cmd))

	out := DumpStartupParams(cmd)

	assert.NotContains(t, out, "should-not-leak")
	assert.True(t, strings.Contains(out, "***"),
		"value of a registered secret key must be redacted")
}

func TestDumpStartupParams_RedactsMQTTSecrets(t *testing.T) {
	resetViper(t)

	cmd := &cobra.Command{Use: "schedule", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("mqtt_password", "", "MQTT password")
	cmd.Flags().String("mqtt_tls_client_cert_key", "", "MQTT client cert key")
	require.NoError(t, cmd.Flags().Set("mqtt_password", "top-secret-password"))
	require.NoError(t, cmd.Flags().Set("mqtt_tls_client_cert_key", "top-secret-key"))
	require.NoError(t, bindFlags(cmd))

	out := DumpStartupParams(cmd)

	assert.NotContains(t, out, "top-secret-password")
	assert.NotContains(t, out, "top-secret-key")
	assert.Regexp(t, `mqtt_password\s+=\s+\*\*\*\s+source=flag`, out)
	assert.Regexp(t, `mqtt_tls_client_cert_key\s+=\s+\*\*\*\s+source=flag`, out)
}

func TestSourceOf_Precedence(t *testing.T) {
	resetViper(t)
	writeConfig(t, "url: from-yaml\n")
	t.Setenv("URL", "from-env")

	cmd := &cobra.Command{Use: "probe", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("url", "", "URL")
	require.NoError(t, bindFlags(cmd))

	// env > yaml when no flag is set
	assert.Equal(t, "env", SourceOf(cmd, "url"))

	// flag wins when set
	require.NoError(t, cmd.Flags().Set("url", "from-flag"))
	assert.Equal(t, "flag", SourceOf(cmd, "url"))

	// nil cmd skips flag check; env still wins
	assert.Equal(t, "env", SourceOf(nil, "url"))

	// unknown key with no env / yaml / default falls back to "default"
	assert.Equal(t, "default", SourceOf(cmd, "totally_unknown_key"))
}

func TestLogStartupParams_DoesNotPanic(t *testing.T) {
	resetViper(t)

	cmd := &cobra.Command{Use: "probe", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().String("foo", "bar", "foo")
	require.NoError(t, bindFlags(cmd))

	assert.NotPanics(t, func() { LogStartupParams(cmd) })
}
