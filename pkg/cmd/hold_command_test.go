package cmd

import (
	"context"
	"errors"
	"testing"

	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withFakeWriter(t *testing.T, w *fakeBatteryWriter) {
	t.Helper()
	old := newBatteryWriter
	newBatteryWriter = func(fronius.SunSpecType) batteryWriter { return w }
	t.Cleanup(func() { newBatteryWriter = old })
}

func TestRunner_HoldCommandExecutesWriterAndPublishesAck(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	withFakeWriter(t, fakeWriter)

	runner := newRunnerForTests(client)
	require.True(t, runner.HandleCommand(context.Background(), "sbam/cmd/hold", []byte("{}")))
	runner.handleIntent(context.Background(), <-runner.intents)

	assert.Equal(t, 1, fakeWriter.holdCalls)
	assert.Equal(t, 0, fakeWriter.setDefaultsCalls)
	assert.Equal(t, "127.0.0.1", fakeWriter.lastFroniusIP)

	msgs := drainPublishes(client)
	ackMsg, ok := findPublishedBySuffix(msgs, "/ack")
	require.True(t, ok, "expected ack publish")
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "hold", ack.Command)
}

func TestRunner_HoldCommandPaused(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{}
	withFakeWriter(t, fakeWriter)

	runner := newRunnerForTests(client)
	runner.setPause(nil)
	runner.handleIntent(context.Background(), mqtt.Intent{Kind: mqtt.IntentHold, CommandTopic: "sbam/cmd/hold"})

	assert.Equal(t, 0, fakeWriter.holdCalls, "no write while paused")
	ackMsg, ok := findPublishedBySuffix(drainPublishes(client), "/ack")
	require.True(t, ok)
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Contains(t, ack.Error, errRunnerPaused.Error())
}

func TestRunner_HoldCommandWriterError(t *testing.T) {
	client := newFakeClient()
	fakeWriter := &fakeBatteryWriter{holdErr: errors.New("modbus write failed")}
	withFakeWriter(t, fakeWriter)

	runner := newRunnerForTests(client)
	runner.handleIntent(context.Background(), mqtt.Intent{Kind: mqtt.IntentHold, CommandTopic: "sbam/cmd/hold"})

	assert.Equal(t, 1, fakeWriter.holdCalls)
	ackMsg, ok := findPublishedBySuffix(drainPublishes(client), "/ack")
	require.True(t, ok)
	ack := decodeAckPayload(t, ackMsg.payload)
	assert.False(t, ack.Accepted)
	assert.Contains(t, ack.Error, "modbus write failed")
}

func TestBatteryWriterHoldUsesSelectedType(t *testing.T) {
	srv := startFloatInverter(t)
	w := froniusBatteryWriter{sunspecType: fronius.SunSpecAuto, port: writerTestPort}

	require.NoError(t, w.Hold(writerTestAddr))

	assert.Equal(t, int16(2), holding(srv, 40359), "StorCtl_Mod")
	assert.Equal(t, int16(0), holding(srv, 40366), "OutWRte 0 %")
	assert.Equal(t, int16(10000), holding(srv, 40367), "InWRte")
	for _, a := range legacyStorageAddrs {
		assert.Equal(t, int16(0), holding(srv, a), "legacy address %d untouched", a)
	}
}
