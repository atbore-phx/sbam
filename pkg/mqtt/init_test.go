package mqtt

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeInitClient struct {
	connectErrs     []error
	connectCalls    int
	disconnectCalls int
	subscribeCalls  int
	subscribeTopic  string
	subscribeErr    error
	subscribeHdlr   MessageHandler
	connected       bool
	publishCalls    int
	publishTopics   []string
	onConnectCBs    []func()
	connectDone     chan struct{}
}

func (f *fakeInitClient) Connect(ctx context.Context) error {
	_ = ctx
	defer func() {
		if f.connectDone != nil {
			close(f.connectDone)
		}
	}()
	idx := f.connectCalls
	f.connectCalls++
	if idx < len(f.connectErrs) {
		err := f.connectErrs[idx]
		f.connected = err == nil
		return err
	}
	f.connected = true
	return nil
}

func (f *fakeInitClient) Disconnect(ctx context.Context) error {
	_ = ctx
	f.disconnectCalls++
	f.connected = false
	return nil
}

func (f *fakeInitClient) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	_ = ctx
	_ = qos
	_ = retained
	_ = payload
	f.publishCalls++
	f.publishTopics = append(f.publishTopics, topic)
	return nil
}

func (f *fakeInitClient) Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error {
	_ = ctx
	_ = qos
	f.subscribeCalls++
	f.subscribeTopic = topic
	f.subscribeHdlr = handler
	return f.subscribeErr
}

func (f *fakeInitClient) IsConnected() bool {
	return f.connected
}

func (f *fakeInitClient) OnConnect(cb func()) {
	f.onConnectCBs = append(f.onConnectCBs, cb)
}

func withInitFactories(
	t *testing.T,
	clientFactory func(Config, ...string) (Client, error),
	noopFactory func() Client,
) {
	t.Helper()
	oldClientFactory := newClientFactory
	oldNoopFactory := newNoopFactory

	if clientFactory != nil {
		newClientFactory = clientFactory
	}
	if noopFactory != nil {
		newNoopFactory = noopFactory
	}

	t.Cleanup(func() {
		newClientFactory = oldClientFactory
		newNoopFactory = oldNoopFactory
	})
}

func TestInitWithCleanupSetupErrorFallsBackToNoop(t *testing.T) {
	cfg := Config{Enabled: true}

	client, cleanup, err := InitWithCleanup(cfg, "dev")
	defer cleanup()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mqtt client setup failed")
	_, isNoop := client.(*Noop)
	assert.True(t, isNoop)
}

func TestInitWithCleanupAsyncConnect(t *testing.T) {
	connectDone := make(chan struct{})
	realClient := &fakeInitClient{
		connectErrs: []error{nil},
		connectDone: connectDone,
	}
	withInitFactories(
		t,
		func(_ Config, _ ...string) (Client, error) {
			return realClient, nil
		},
		nil,
	)

	cfg := Config{Enabled: true, Broker: "tcp://example.com:1883"}
	client, cleanup, err := InitWithCleanup(cfg, "dev")

	require.NoError(t, err)
	// Client must be returned immediately — not swapped to noop.
	assert.Same(t, realClient, client)
	// Wait for the connect goroutine to finish before calling cleanup.
	select {
	case <-connectDone:
	case <-time.After(2 * time.Second):
		t.Fatal("connect was not called within timeout")
	}
	cleanup()
}

func TestInitWithCleanupDisabledReturnsNoop(t *testing.T) {
	cfg := Config{Enabled: false}

	client, cleanup, err := InitWithCleanup(cfg, "dev")
	defer cleanup()

	require.NoError(t, err)
	_, isNoop := client.(*Noop)
	assert.True(t, isNoop)
}
