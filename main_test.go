package main

import (
	"os"
	"path/filepath"
	"sbam/pkg/cmd"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(t *testing.T) {
	err := cmd.SetVersionInfo("1.0", "abc123", "2022-01-01")
	assert.NoError(t, err)

	old := os.Args
	defer func() { os.Args = old }()

	os.Args = []string{"cmd", "--version"}

	err = cmd.Execute()
	assert.NoError(t, err)
}

// TestExecute_WithConfigYaml is a smoke test for issue #68: it ensures that
// cmd.Execute() does not regress when a config.yaml exists in the current
// working directory. This guards the init-order fix that moved viper
// bindings into PersistentPreRunE.
func TestExecute_WithConfigYaml(t *testing.T) {
	require.NoError(t, cmd.SetVersionInfo("1.0", "abc123", "2022-01-01"))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "config.yaml"),
		[]byte("url: http://from-yaml/\nfronius_ip: 10.0.0.1\napikey: yaml-key\n"),
		0o600,
	))
	t.Chdir(dir)

	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"cmd", "--version"}

	assert.NoError(t, cmd.Execute())
}

func TestMain_EntrypointWithVersionArg(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()

	t.Chdir(t.TempDir())
	os.Args = []string{"cmd", "--version"}

	assert.NotPanics(t, func() {
		main()
	})
}
