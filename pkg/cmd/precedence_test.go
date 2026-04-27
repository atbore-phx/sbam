package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetViper clears viper's global state between tests and re-enables
// AutomaticEnv. Tests in this file mutate the global viper, so they
// must not run with t.Parallel().
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

// newFlagCmd returns a fresh cobra.Command with a single string flag named
// "url" and the given default. Used to isolate bindFlags semantics from the
// real subcommands.
func newFlagCmd(defaultURL string) *cobra.Command {
	c := &cobra.Command{Use: "probe", Run: func(cmd *cobra.Command, args []string) {}}
	c.Flags().String("url", defaultURL, "URL")
	return c
}

func TestBindFlags_FlagBeatsEnvAndYaml(t *testing.T) {
	resetViper(t)
	writeConfig(t, "url: from-yaml\n")
	t.Setenv("URL", "from-env")

	c := newFlagCmd("from-default")
	require.NoError(t, c.Flags().Set("url", "from-flag"))
	require.NoError(t, bindFlags(c))

	assert.Equal(t, "from-flag", viper.GetString("url"))
}

func TestBindFlags_EnvBeatsYaml(t *testing.T) {
	resetViper(t)
	writeConfig(t, "url: from-yaml\n")
	t.Setenv("URL", "from-env")

	c := newFlagCmd("from-default")
	require.NoError(t, bindFlags(c))

	assert.Equal(t, "from-env", viper.GetString("url"))
}

func TestBindFlags_YamlBeatsDefault(t *testing.T) {
	resetViper(t)
	writeConfig(t, "url: from-yaml\n")

	c := newFlagCmd("from-default")
	require.NoError(t, bindFlags(c))

	assert.Equal(t, "from-yaml", viper.GetString("url"))
}

func TestBindFlags_DefaultWhenNothingSet(t *testing.T) {
	resetViper(t)

	c := newFlagCmd("from-default")
	require.NoError(t, bindFlags(c))

	assert.Equal(t, "from-default", viper.GetString("url"))
}

// TestBindFlags_PerSubcommand verifies that the binding follows the
// most-recently-executed subcommand. This is the regression guard for
// issue #68 where a shared key was permanently bound to the last init()'d
// subcommand's pflag set.
func TestBindFlags_PerSubcommand(t *testing.T) {
	resetViper(t)

	a := &cobra.Command{Use: "a", Run: func(cmd *cobra.Command, args []string) {}}
	a.Flags().String("url", "", "URL")
	b := &cobra.Command{Use: "b", Run: func(cmd *cobra.Command, args []string) {}}
	b.Flags().String("url", "", "URL")

	require.NoError(t, a.Flags().Set("url", "from-a"))
	require.NoError(t, bindFlags(a))
	assert.Equal(t, "from-a", viper.GetString("url"))

	require.NoError(t, b.Flags().Set("url", "from-b"))
	require.NoError(t, bindFlags(b))
	assert.Equal(t, "from-b", viper.GetString("url"),
		"executing subcommand must own the viper binding")
}

// TestBindFlags_RealEstimateCmd exercises bindFlags against the actual
// estCmd to ensure its full key set (including url, apikey, fronius_ip,
// cache_*) honors flag precedence.
func TestBindFlags_RealEstimateCmd(t *testing.T) {
	resetViper(t)
	writeConfig(t, "url: from-yaml\napikey: yaml-key\nfronius_ip: 10.0.0.1\n")

	require.NoError(t, estCmd.Flags().Set("url", "flag-url"))
	require.NoError(t, estCmd.Flags().Set("apikey", "flag-key"))
	require.NoError(t, estCmd.Flags().Set("fronius_ip", "192.168.1.1"))
	t.Cleanup(func() {
		// reset Changed state on the shared estCmd flags so other tests
		// see a clean slate.
		_ = estCmd.Flags().Set("url", "")
		_ = estCmd.Flags().Set("apikey", "")
		_ = estCmd.Flags().Set("fronius_ip", "")
		estCmd.Flags().Lookup("url").Changed = false
		estCmd.Flags().Lookup("apikey").Changed = false
		estCmd.Flags().Lookup("fronius_ip").Changed = false
	})

	require.NoError(t, bindFlags(estCmd))

	assert.Equal(t, "flag-url", viper.GetString("url"))
	assert.Equal(t, "flag-key", viper.GetString("apikey"))
	assert.Equal(t, "192.168.1.1", viper.GetString("fronius_ip"))
}

// TestBindFlags_RealConfigureCmdDefaults guards against the latent bug in
// configure.go where `defaults` was bound to scdCmd's flag instead of
// cfgCmd's. After bindFlags(cfgCmd), the resolved value must come from
// cfgCmd's own flag.
func TestBindFlags_RealConfigureCmdDefaults(t *testing.T) {
	resetViper(t)

	require.NoError(t, cfgCmd.Flags().Set("defaults", "true"))
	t.Cleanup(func() {
		_ = cfgCmd.Flags().Set("defaults", "false")
		cfgCmd.Flags().Lookup("defaults").Changed = false
	})

	require.NoError(t, bindFlags(cfgCmd))

	assert.True(t, viper.GetBool("defaults"),
		"cfgCmd's --defaults flag must drive viper, not scdCmd's")
}

// TestBindFlags_RealScheduleCmd covers the subcommand-specific keys that
// only exist on scdCmd (start_hr, end_hr, crontab, pw_consumption, ...).
func TestBindFlags_RealScheduleCmd(t *testing.T) {
	resetViper(t)
	writeConfig(t, "start_hr: \"01:00\"\nend_hr: \"02:00\"\ncrontab: 0 0 * * *\n")

	require.NoError(t, scdCmd.Flags().Set("start_hr", "03:00"))
	t.Cleanup(func() {
		_ = scdCmd.Flags().Set("start_hr", const_sh)
		scdCmd.Flags().Lookup("start_hr").Changed = false
	})

	require.NoError(t, bindFlags(scdCmd))

	assert.Equal(t, "03:00", viper.GetString("start_hr"),
		"flag must beat yaml for scdCmd-only keys")
	assert.Equal(t, "02:00", viper.GetString("end_hr"),
		"yaml must beat default when no flag is set")
	assert.Equal(t, "0 0 * * *", viper.GetString("crontab"))
}
