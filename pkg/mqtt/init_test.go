package mqtt

import (
	"context"
	"errors"
	"strings"
	"testing"

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
}

func (f *fakeInitClient) Connect(ctx context.Context) error {
	_ = ctx
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

func TestConnectWithRetriesSucceedsAfterRetry(t *testing.T) {
	client := &fakeInitClient{connectErrs: []error{errors.New("first attempt failed"), nil}}

	err := connectWithRetries(client, 3, 0)

	require.NoError(t, err)
	assert.Equal(t, 2, client.connectCalls)
	assert.Equal(t, 0, client.disconnectCalls)
}

func TestConnectWithRetriesContextErrorsTriggerDisconnect(t *testing.T) {
	lastErr := errors.New("last failure")
	client := &fakeInitClient{connectErrs: []error{context.DeadlineExceeded, context.Canceled, lastErr}}

	err := connectWithRetries(client, 3, 0)

	require.Error(t, err)
	assert.ErrorIs(t, err, lastErr)
	assert.Equal(t, 3, client.connectCalls)
	assert.Equal(t, 2, client.disconnectCalls)
}

func TestInitWithCleanupSetupErrorFallsBackToNoop(t *testing.T) {
	cfg := Config{Enabled: true}

	client, cleanup, err := InitWithCleanup(cfg, "dev", 1, 0)
	defer cleanup()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mqtt client setup failed")
	_, isNoop := client.(*Noop)
	assert.True(t, isNoop)
}

func TestInitWithCleanupConnectFailureFallsBackToNoop(t *testing.T) {
	failingClient := &fakeInitClient{connectErrs: []error{errors.New("connect failed"), errors.New("still failing")}}
	fallbackClient := &fakeInitClient{}
	withInitFactories(
		t,
		func(_ Config, _ ...string) (Client, error) {
			return failingClient, nil
		},
		func() Client {
			return fallbackClient
		},
	)

	cfg := Config{Enabled: true}
	client, cleanup, err := InitWithCleanup(cfg, "dev", 2, 0)
	defer cleanup()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mqtt connect failed after retries")
	assert.Same(t, fallbackClient, client)
	assert.Equal(t, 2, failingClient.connectCalls)
}

func TestInitWithCleanupSubscribeErrorIsReturned(t *testing.T) {
	client := &fakeInitClient{
		connectErrs:  []error{nil},
		subscribeErr: errors.New("subscribe failed"),
	}
	withInitFactories(
		t,
		func(_ Config, _ ...string) (Client, error) {
			return client, nil
		},
		nil,
	)

	cfg := Config{Enabled: true, HADiscovery: true}
	_, cleanup, err := InitWithCleanup(cfg, "dev", 1, 0)
	defer cleanup()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mqtt subscribe homeassistant/status failed")
	assert.Equal(t, 1, client.subscribeCalls)
	assert.Equal(t, haStatusTopic(), client.subscribeTopic)
}

func TestInitWithCleanupHADiscoveryPublishesOnOnline(t *testing.T) {
	client := &fakeInitClient{connectErrs: []error{nil}}
	withInitFactories(
		t,
		func(_ Config, _ ...string) (Client, error) {
			return client, nil
		},
		nil,
	)

	cfg := Config{
		Enabled:           true,
		HADiscovery:       true,
		HADiscoveryPrefix: "homeassistant",
		TopicPrefix:       "sbam",
		FroniusIP:         "127.0.0.1",
	}
	_, cleanup, err := InitWithCleanup(cfg, "dev", 1, 0)
	defer cleanup()

	require.NoError(t, err)
	require.NotNil(t, client.subscribeHdlr)

	client.subscribeHdlr(haStatusTopic(), []byte("offline"))
	assert.Equal(t, 0, client.publishCalls)

	client.subscribeHdlr(haStatusTopic(), []byte("online"))
	assert.Greater(t, client.publishCalls, 0)

	foundConfigTopic := false
	for _, topic := range client.publishTopics {
		if strings.HasSuffix(topic, "/config") {
			foundConfigTopic = true
			break
		}
	}
	assert.True(t, foundConfigTopic)
}

func TestInitWithCleanupOnlineHandlerInvokedOnOnlineOnly(t *testing.T) {
	client := &fakeInitClient{connectErrs: []error{nil}}
	withInitFactories(
		t,
		func(_ Config, _ ...string) (Client, error) {
			return client, nil
		},
		nil,
	)

	// handler registration support removed; discovery publish is covered
	// by TestInitWithCleanupHADiscoveryPublishesOnOnline.
}

// handler-based registration removed; subscription behavior is covered
// by TestInitWithCleanupHADiscoveryPublishesOnOnline and
// TestInitWithCleanupSubscribeErrorIsReturned.

func TestInitWithCleanupNoHandlersNoDiscoveryDoesNotSubscribe(t *testing.T) {
	client := &fakeInitClient{connectErrs: []error{nil}}
	withInitFactories(
		t,
		func(_ Config, _ ...string) (Client, error) {
			return client, nil
		},
		nil,
	)

	cfg := Config{Enabled: true, HADiscovery: false}
	_, cleanup, err := InitWithCleanup(cfg, "dev", 1, 0)
	defer cleanup()

	require.NoError(t, err)
	assert.Equal(t, 0, client.subscribeCalls, "should not subscribe when no handlers and discovery disabled")
}
