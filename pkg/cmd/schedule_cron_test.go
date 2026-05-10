package cmd

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"sbam/pkg/mqtt"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const crontabHelperModeEnv = "SBAM_CRONTAB_HELPER_MODE"

func runCrontabHelper(t *testing.T, mode string) error {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestCrontabSchedule_SubprocessHelper$")
	cmd.Env = append(os.Environ(), crontabHelperModeEnv+"="+mode)
	cmd.Dir = t.TempDir()

	return cmd.Run()
}

func TestCrontabSchedule_SubprocessHelper(t *testing.T) {
	mode := os.Getenv(crontabHelperModeEnv)
	if mode == "" {
		t.Skip("helper subprocess test")
	}

	defaults := false
	switch mode {
	case "defaults-off":
		defaults = false
	case "defaults-on":
		defaults = true
	default:
		t.Fatalf("unexpected helper mode %q", mode)
	}

	go func() {
		time.Sleep(250 * time.Millisecond)
		proc, err := os.FindProcess(os.Getpid())
		if err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}()

	crontabSchedule(
		"api-key",
		"https://a.test,https://b.test,https://c.test",
		"127.0.0.1",
		1000,
		3500,
		100,
		"00:00",
		"00:55",
		"0 0 1 1 *",
		defaults,
		"00:00",
		"00:55",
		0,
		0,
		false,
		"cached_forecast",
		7200,
		nil,
		mqtt.Config{},
	)
}

func TestCrontabSchedule_PanicsOnInvalidCronExpression(t *testing.T) {
	assert.Panics(t, func() {
		crontabSchedule(
			"api-key",
			"https://example.test/forecast",
			"127.0.0.1",
			1000,
			3500,
			100,
			"00:00",
			"00:55",
			"not a cron expression",
			false,
			"00:00",
			"00:55",
			0,
			0,
			false,
			"cached_forecast",
			7200,
			nil,
			mqtt.Config{},
		)
	})
}

func TestCrontabSchedule_ValidSpecReturnsAfterSignal(t *testing.T) {
	err := runCrontabHelper(t, "defaults-off")
	require.NoError(t, err)
}

func TestCrontabSchedule_DefaultsBranchReturnsAfterSignal(t *testing.T) {
	err := runCrontabHelper(t, "defaults-on")
	require.NoError(t, err)
}
