package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFinalizeRunnerModeMQTTEnabledWaitsForSignal(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	runner := NewRunner(RunnerConfig{Now: time.Now}, nil)
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	finalized := make(chan error, 1)
	go func() {
		finalized <- finalizeRunnerMode(true, "crontab", runner, runDone, stop)
	}()

	select {
	case err := <-finalized:
		t.Fatalf("finalizeRunnerMode returned before signal: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	stop()

	select {
	case err := <-finalized:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("finalizeRunnerMode did not return after stop")
	}
}

func TestFinalizeRunnerModeMQTTDisabledStopsRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := NewRunner(RunnerConfig{Now: time.Now}, nil)
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	err := finalizeRunnerMode(false, "crontab", runner, runDone, nil)
	require.NoError(t, err)
}

func TestFinalizeRunnerModeWindowsMQTTDisabledKeepsRunning(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	runner := NewRunner(RunnerConfig{Now: time.Now}, nil)
	runDone := make(chan error, 1)
	go func() {
		runDone <- runner.Run(ctx)
	}()

	finalized := make(chan error, 1)
	go func() {
		finalized <- finalizeRunnerMode(false, "windows", runner, runDone, stop)
	}()

	// Windows mode with MQTT disabled should NOT return immediately —
	// the runner should stay alive, driven by its internal ticker.
	select {
	case err := <-finalized:
		t.Fatalf("finalizeRunnerMode returned before signal in windows mode: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	stop()

	select {
	case err := <-finalized:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("finalizeRunnerMode did not return after stop in windows mode")
	}
}

func TestWaitForRunnerDoneRejectsNilChannel(t *testing.T) {
	err := waitForRunnerDone(nil)
	require.ErrorContains(t, err, "runner completion channel is required")
}

func TestWaitForRunnerDoneReturnsUnexpectedError(t *testing.T) {
	expected := errors.New("boom")
	runDone := make(chan error, 1)
	runDone <- expected

	err := waitForRunnerDone(runDone)
	require.ErrorIs(t, err, expected)
}
