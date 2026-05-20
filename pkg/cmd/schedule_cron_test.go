package cmd

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

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

	runner := NewRunner(RunnerConfig{
		StartHR:            "00:00",
		EndHR:              "00:55",
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "00:55",
		Now:                time.Now,
	}, nil)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	require.NoError(t, crontabSchedule(ctx, runner, "0 0 1 1 *", defaults, "00:55"))
}

func TestCrontabSchedule_InvalidCronExpressionReturnsError(t *testing.T) {
	runner := NewRunner(RunnerConfig{
		StartHR:            "00:00",
		EndHR:              "00:55",
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "00:55",
		Now:                time.Now,
	}, nil)

	err := crontabSchedule(context.Background(), runner, "not a cron expression", false, "00:55")
	require.Error(t, err)
}

func TestCrontabSchedule_ValidSpecReturnsAfterSignal(t *testing.T) {
	err := runCrontabHelper(t, "defaults-off")
	require.NoError(t, err)
}

func TestCrontabSchedule_DefaultsBranchReturnsAfterSignal(t *testing.T) {
	err := runCrontabHelper(t, "defaults-on")
	require.NoError(t, err)
}
