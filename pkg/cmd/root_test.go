package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const executeHelperModeEnv = "SBAM_EXECUTE_HELPER_MODE"

func withRootState(t *testing.T) {
	t.Helper()

	oldArgs := os.Args
	oldVersion := rootCmd.Version
	oldAppVersion := appVersion

	t.Cleanup(func() {
		os.Args = oldArgs
		rootCmd.Version = oldVersion
		appVersion = oldAppVersion
		rootCmd.SetArgs(nil)
	})
}

func runExecuteHelper(t *testing.T, mode string, dir string) error {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestExecute_SubprocessHelper$")
	cmd.Env = append(os.Environ(), executeHelperModeEnv+"="+mode)
	cmd.Dir = dir

	return cmd.Run()
}

func TestExecute_SubprocessHelper(t *testing.T) {
	mode := os.Getenv(executeHelperModeEnv)
	if mode == "" {
		t.Skip("helper subprocess test")
	}

	switch mode {
	case "help-exit":
		os.Args = []string{"sbam"}
		rootCmd.SetArgs([]string{})
	case "error-exit":
		os.Args = []string{"sbam", "--definitely-unknown-flag"}
		rootCmd.SetArgs([]string{"--definitely-unknown-flag"})
	default:
		t.Fatalf("unexpected helper mode %q", mode)
	}

	_ = Execute()
	t.Fatalf("expected Execute to exit for mode %q", mode)
}

func TestSetVersionInfo_UpdatesAppVersionAndRootVersion(t *testing.T) {
	withRootState(t)

	err := SetVersionInfo("1.2.3", "abc123", "2026-05-10")
	require.NoError(t, err)

	assert.Equal(t, "1.2.3", appVersion)
	assert.Contains(t, rootCmd.Version, "1.2.3")
	assert.Contains(t, rootCmd.Version, "2026-05-10")
	assert.Contains(t, rootCmd.Version, "abc123")
}

func TestSetVersionInfo_EmptyVersionKeepsExistingAppVersion(t *testing.T) {
	withRootState(t)
	appVersion = "keep-existing"

	err := SetVersionInfo("", "deadbeef", "2026-05-10")
	require.NoError(t, err)

	assert.Equal(t, "keep-existing", appVersion)
	assert.Contains(t, rootCmd.Version, "deadbeef")
}

func TestExecute_NoArgsWithConfigDoesNotExit(t *testing.T) {
	withRootState(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("url: https://example.test/forecast\n"), 0o600))
	t.Chdir(dir)

	os.Args = []string{"sbam"}
	rootCmd.SetArgs([]string{})

	require.NoError(t, Execute())
}

func TestExecute_VersionFlagWithoutConfigReturnsNil(t *testing.T) {
	withRootState(t)

	require.NoError(t, SetVersionInfo("1.0.0", "abc123", "2026-05-10"))

	t.Chdir(t.TempDir())
	os.Args = []string{"sbam", "--version"}
	rootCmd.SetArgs([]string{"--version"})

	require.NoError(t, Execute())
}

func TestExecute_NoArgsNoConfigExitsZero(t *testing.T) {
	err := runExecuteHelper(t, "help-exit", t.TempDir())
	require.NoError(t, err)
}

func TestExecute_CommandErrorExitsOne(t *testing.T) {
	err := runExecuteHelper(t, "error-exit", t.TempDir())
	require.Error(t, err)

	exitErr := &exec.ExitError{}
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
}
