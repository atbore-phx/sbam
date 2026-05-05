package mqtt

import (
	"context"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math"
	"math/big"
	mrand "math/rand"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	broker "github.com/mochi-mqtt/server/v2"
	brokerauth "github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"sbam/src/utils"
)

const testTimeout = 5 * time.Second

type testBroker struct {
	server  *broker.Server
	address string
}

type receivedMessage struct {
	topic   string
	payload []byte
}

func TestNewReturnsNoopWhenDisabled(t *testing.T) {
	observed := withObservedLogger(t)

	client, err := New(Config{})
	require.NoError(t, err)

	noop, ok := client.(*Noop)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	assert.NoError(t, noop.Connect(ctx))
	assert.NoError(t, noop.Publish(ctx, stateTopic(""), qosAtLeastOnce, true, []byte("payload")))
	assert.NoError(t, noop.Subscribe(ctx, stateTopic(""), qosAtLeastOnce, func(topic string, payload []byte) {}))
	assert.NoError(t, noop.Disconnect(ctx))
	assert.False(t, noop.IsConnected())
	assert.Zero(t, observed.Len())
}

func TestNormalizePrefix(t *testing.T) {
	assert.Equal(t, defaultTopicPrefix, normalizePrefix("   "))
	assert.Equal(t, "custom", normalizePrefix(" /custom/ "))
	assert.Equal(t, defaultTopicPrefix+"/state", stateTopic(" /// "))
	assert.Equal(t, "custom/error", errorTopic(" /custom/ "))
	assert.Equal(t, "custom/availability", availabilityTopic(" /custom/ "))
}

func TestPublishStateRoundTrip(t *testing.T) {
	address := reserveTCPAddress(t)
	broker := newTestBroker(t, address, nil)
	defer broker.Close()

	subscriber := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "state-subscriber"})
	defer disconnectClient(t, subscriber)

	messages := make(chan receivedMessage, 1)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	require.NoError(t, subscriber.Subscribe(ctx, stateTopic(" "), qosAtLeastOnce, func(topic string, payload []byte) {
		messages <- receivedMessage{topic: topic, payload: payload}
	}))

	publisher := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "state-publisher"})
	defer disconnectClient(t, publisher)

	PublishState(context.Background(), publisher, " / ", StatePayload{
		BatterySOCPct:      42.5,
		BatteryCapacityWh:  10000,
		ForecastTodayWh:    5100,
		LastDecision:       "charge",
		LastDecisionReason: "forecast shortfall",
		Paused:             true,
	})

	message := waitForMessage(t, messages)
	assert.Equal(t, stateTopic(""), message.topic)

	var payload StatePayload
	require.NoError(t, json.Unmarshal(message.payload, &payload))
	assert.Equal(t, 42.5, payload.BatterySOCPct)
	assert.Equal(t, 10000.0, payload.BatteryCapacityWh)
	assert.Equal(t, 5100.0, payload.ForecastTodayWh)
	assert.Equal(t, "charge", payload.LastDecision)
	assert.Equal(t, "forecast shortfall", payload.LastDecisionReason)
	assert.True(t, payload.Paused)
	assert.False(t, payload.Timestamp.IsZero())
}

func TestPublishAvailabilityRetained(t *testing.T) {
	address := reserveTCPAddress(t)
	broker := newTestBroker(t, address, nil)
	defer broker.Close()

	publisher := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "availability-publisher"})
	defer disconnectClient(t, publisher)

	PublishAvailability(context.Background(), publisher, " /custom/ ", true)

	subscriber := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "availability-subscriber"})
	defer disconnectClient(t, subscriber)

	messages := make(chan receivedMessage, 1)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	require.NoError(t, subscriber.Subscribe(ctx, availabilityTopic("custom"), qosAtLeastOnce, func(topic string, payload []byte) {
		messages <- receivedMessage{topic: topic, payload: payload}
	}))

	message := waitForMessage(t, messages)
	assert.Equal(t, availabilityTopic("custom"), message.topic)
	assert.Equal(t, "online", string(message.payload))
}

func TestReconnectAfterBrokerRestart(t *testing.T) {
	strategies := []ReconnectStrategy{ReconnectStrategyCustom, ReconnectStrategyPaho}

	for _, strategy := range strategies {
		strategy := strategy
		t.Run(string(strategy), func(t *testing.T) {
			// Keep both reconnect paths under restart coverage so removing either one later is a low-risk diff.
			address := reserveTCPAddress(t)
			broker := newTestBroker(t, address, nil)
			defer broker.Close()

			client := mustConnectClient(t, Config{
				Enabled:           true,
				Broker:            "tcp://" + address,
				ClientID:          "reconnect-client-" + string(strategy),
				ReconnectStrategy: strategy,
			})
			defer disconnectClient(t, client)

			broker.Crash()

			if strategy == ReconnectStrategyCustom {
				assert.Eventually(t, func() bool {
					return !client.IsConnected()
				}, testTimeout, 100*time.Millisecond)
			}

			broker = newTestBroker(t, address, nil)

			reconnectDeadline := 10 * time.Second
			if strategy == ReconnectStrategyPaho {
				reconnectDeadline = 25 * time.Second
			}

			assert.Eventually(t, func() bool {
				return client.IsConnected()
			}, reconnectDeadline, 100*time.Millisecond)

			subscriber := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "reconnect-subscriber-" + string(strategy)})
			defer disconnectClient(t, subscriber)

			messages := make(chan receivedMessage, 1)
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			require.NoError(t, subscriber.Subscribe(ctx, stateTopic(""), qosAtLeastOnce, func(topic string, payload []byte) {
				messages <- receivedMessage{topic: topic, payload: payload}
			}))

			PublishState(context.Background(), client, "", StatePayload{
				BatterySOCPct:     55,
				BatteryCapacityWh: 8000,
				ForecastTodayWh:   3200,
				LastDecision:      "idle",
			})

			message := waitForMessage(t, messages)
			assert.Equal(t, stateTopic(""), message.topic)
		})
	}
}

func TestConnectFailsWithBadCredentials(t *testing.T) {
	strategies := []ReconnectStrategy{ReconnectStrategyCustom, ReconnectStrategyPaho}

	for _, strategy := range strategies {
		strategy := strategy
		t.Run(string(strategy), func(t *testing.T) {
			address := reserveTCPAddress(t)
			ledger := &brokerauth.Ledger{
				Users: brokerauth.Users{
					"good": {
						Username: "good",
						Password: "secret",
						ACL: brokerauth.Filters{
							"#": brokerauth.ReadWrite,
						},
					},
				},
			}

			broker := newTestBroker(t, address, ledger)
			defer broker.Close()

			client, err := New(Config{
				Enabled:           true,
				Broker:            "tcp://" + address,
				ClientID:          "bad-credentials-" + string(strategy),
				Username:          "good",
				Password:          "wrong",
				ReconnectStrategy: strategy,
			})
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()

			err = client.Connect(ctx)
			assert.Error(t, err)
			assert.False(t, client.IsConnected())
		})
	}
}

func TestNewPahoFailsWithInvalidTLSCAFile(t *testing.T) {
	client, err := NewPaho(Config{
		Enabled:   true,
		Broker:    "tls://127.0.0.1:8883",
		TLSCAFile: "/does/not/exist.pem",
	})
	assert.Nil(t, client)
	assert.Error(t, err)
}

func TestNormalizeReconnectStrategy(t *testing.T) {
	cases := []struct {
		name     string
		in       ReconnectStrategy
		expected ReconnectStrategy
		err      string
	}{
		{name: "empty defaults to custom", in: "", expected: ReconnectStrategyCustom},
		{name: "custom exact", in: ReconnectStrategyCustom, expected: ReconnectStrategyCustom},
		{name: "custom normalized", in: "  CUSTOM ", expected: ReconnectStrategyCustom},
		{name: "paho exact", in: ReconnectStrategyPaho, expected: ReconnectStrategyPaho},
		{name: "paho normalized", in: " pAhO ", expected: ReconnectStrategyPaho},
		{name: "invalid", in: "legacy", err: "unsupported mqtt reconnect strategy \"legacy\""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeReconnectStrategy(tc.in)
			if tc.err != "" {
				assert.EqualError(t, err, tc.err)
				assert.Equal(t, ReconnectStrategy(""), got)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestNewPahoReconnectStrategyConfiguration(t *testing.T) {
	t.Run("defaults to custom reconnect", func(t *testing.T) {
		client, err := NewPaho(Config{Broker: "tcp://example.com:1883"})
		require.NoError(t, err)
		require.NotNil(t, client)

		assert.Equal(t, ReconnectStrategyCustom, client.cfg.ReconnectStrategy)
		require.NotNil(t, client.reconnecter)
		assert.Equal(t, ReconnectStrategyCustom, client.reconnecter.strategy())

		opts := client.client.OptionsReader()
		assert.False(t, opts.AutoReconnect())
		assert.False(t, opts.ConnectRetry())
	})

	t.Run("configures paho auto reconnect when selected", func(t *testing.T) {
		client, err := NewPaho(Config{Broker: "tcp://example.com:1883", ReconnectStrategy: ReconnectStrategyPaho})
		require.NoError(t, err)
		require.NotNil(t, client)

		assert.Equal(t, ReconnectStrategyPaho, client.cfg.ReconnectStrategy)
		require.NotNil(t, client.reconnecter)
		assert.Equal(t, ReconnectStrategyPaho, client.reconnecter.strategy())

		opts := client.client.OptionsReader()
		assert.True(t, opts.AutoReconnect())
		assert.False(t, opts.ConnectRetry())
		assert.Equal(t, reconnectMaxDelay, opts.MaxReconnectInterval())
	})

	t.Run("invalid reconnect strategy", func(t *testing.T) {
		client, err := NewPaho(Config{Broker: "tcp://example.com:1883", ReconnectStrategy: "legacy"})
		assert.Nil(t, client)
		assert.EqualError(t, err, "unsupported mqtt reconnect strategy \"legacy\"")
	})
}

func TestNewPahoValidationAndDefaults(t *testing.T) {
	t.Run("empty broker", func(t *testing.T) {
		client, err := NewPaho(Config{})
		assert.Nil(t, client)
		assert.EqualError(t, err, "mqtt broker required when enabled")
	})

	t.Run("parse error", func(t *testing.T) {
		client, err := NewPaho(Config{Broker: "tcp://example.com/%zz"})
		assert.Nil(t, client)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse mqtt broker")
	})

	t.Run("unsupported scheme", func(t *testing.T) {
		client, err := NewPaho(Config{Broker: "udp://example.com:1883"})
		assert.Nil(t, client)
		assert.EqualError(t, err, "unsupported mqtt broker scheme \"udp\"")
	})

	t.Run("default client id from hostname", func(t *testing.T) {
		withHostnameStub(t, func() (string, error) { return "test-host", nil })
		client, err := NewPaho(Config{Broker: "tcp://example.com:1883"})
		require.NoError(t, err)
		assert.Equal(t, "sbam-test-host", client.cfg.ClientID)
	})

	t.Run("tls scheme builds config", func(t *testing.T) {
		certPEM, keyPEM := generateTestCertificate(t)
		caFile := writeTempFile(t, certPEM)
		certFile := writeTempFile(t, certPEM)
		keyFile := writeTempFile(t, keyPEM)

		withSystemCertPoolStub(t, func() (*x509.CertPool, error) { return nil, nil })

		client, err := NewPaho(Config{
			Broker:           "tls://example.com:8883",
			TLSCAFile:        caFile,
			TLSClientCert:    certFile,
			TLSClientCertKey: keyFile,
		})
		require.NoError(t, err)
		assert.NotNil(t, client)
	})
}

func TestPahoMethodsWithFakeClient(t *testing.T) {
	t.Run("connect closed client", func(t *testing.T) {
		client := newFakePahoClient()
		wrapper := &Paho{client: client}
		wrapper.closed.Store(true)

		err := wrapper.Connect(context.Background())
		assert.EqualError(t, err, "mqtt client closed")
	})

	t.Run("connect already connected", func(t *testing.T) {
		client := newFakePahoClient()
		client.setConnected(true)
		wrapper := &Paho{client: client}

		assert.NoError(t, wrapper.Connect(context.Background()))
	})

	t.Run("connect delegates token", func(t *testing.T) {
		client := newFakePahoClient()
		client.connectToken = newCompleteToken(nil)
		wrapper := &Paho{client: client}

		assert.NoError(t, wrapper.Connect(context.Background()))
	})

	t.Run("disconnect nil context and second call", func(t *testing.T) {
		client := newFakePahoClient()
		wrapper := &Paho{client: client}

		assert.NoError(t, wrapper.Disconnect(testNilContext()))
		assert.NoError(t, wrapper.Disconnect(context.Background()))
		disconnectCalls := client.disconnectCallsSnapshot()
		assert.Len(t, disconnectCalls, 1)
		assert.Equal(t, uint(disconnectQuiesceMS), disconnectCalls[0])
	})

	t.Run("disconnect context canceled", func(t *testing.T) {
		client := newFakePahoClient()
		client.disconnectDelay = 50 * time.Millisecond
		wrapper := &Paho{client: client}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := wrapper.Disconnect(ctx)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("publish blank topic", func(t *testing.T) {
		wrapper := &Paho{client: newFakePahoClient()}
		assert.EqualError(t, wrapper.Publish(context.Background(), "   ", 1, false, nil), "mqtt topic required")
	})

	t.Run("publish delegates token", func(t *testing.T) {
		client := newFakePahoClient()
		client.publishToken = newCompleteToken(nil)
		wrapper := &Paho{client: client}

		assert.NoError(t, wrapper.Publish(context.Background(), "topic", 1, true, []byte("payload")))
		publishCalls := client.publishCallsSnapshot()
		require.Len(t, publishCalls, 1)
		assert.Equal(t, "topic", publishCalls[0].topic)
	})

	t.Run("subscribe validation", func(t *testing.T) {
		wrapper := &Paho{client: newFakePahoClient()}
		assert.EqualError(t, wrapper.Subscribe(context.Background(), " ", 1, func(string, []byte) {}), "mqtt topic required")
		assert.EqualError(t, wrapper.Subscribe(context.Background(), "topic", 1, nil), "mqtt handler required")
	})

	t.Run("subscribe callback copies payload", func(t *testing.T) {
		client := newFakePahoClient()
		client.subscribeToken = newCompleteToken(nil)
		wrapper := &Paho{client: client}

		var gotTopic string
		var gotPayload []byte
		err := wrapper.Subscribe(context.Background(), "topic", 1, func(topic string, payload []byte) {
			gotTopic = topic
			gotPayload = payload
		})
		require.NoError(t, err)

		payload := []byte("hello")
		client.subscribeHandlerFunc()(client, fakeMessage{topic: "topic", payload: payload})
		payload[0] = 'x'

		assert.Equal(t, "topic", gotTopic)
		assert.Equal(t, []byte("hello"), gotPayload)
	})
}

func TestReconnectHelpers(t *testing.T) {
	assert.Equal(t, 2*time.Second, nextReconnectDelay(1*time.Second))
	assert.Equal(t, reconnectMaxDelay, nextReconnectDelay(reconnectMaxDelay))
	assert.False(t, supportedScheme("udp"))
	assert.Equal(t, "%%%", sanitizeBroker("%%%"))

	t.Run("start reconnect loop no-op when already running", func(t *testing.T) {
		manager := newCustomReconnectManager()
		wrapper := &Paho{client: newFakePahoClient()}
		manager.reconnect.Store(true)
		manager.startReconnectLoop(wrapper)
		assert.True(t, manager.reconnect.Load())
	})

	t.Run("start reconnect loop exits when closed", func(t *testing.T) {
		manager := newCustomReconnectManager()
		manager.rand = randSourceForTest()
		wrapper := &Paho{client: newFakePahoClient()}
		wrapper.closed.Store(true)
		manager.startReconnectLoop(wrapper)
		assert.Eventually(t, func() bool {
			return !manager.reconnect.Load()
		}, time.Second, 10*time.Millisecond)
	})

	t.Run("failed reconnect retries then stops when closed", func(t *testing.T) {
		fakeClient := newFakePahoClient()
		fakeClient.connectToken = newCompleteToken(errors.New("connect failed"))
		manager := newCustomReconnectManager()
		manager.rand = randSourceForTest()
		wrapper := &Paho{client: fakeClient, cfg: Config{Broker: "tcp://broker"}}

		manager.startReconnectLoop(wrapper)

		assert.Eventually(t, func() bool {
			return fakeClient.connectCallsCount() > 0
		}, 2*time.Second, 50*time.Millisecond)

		wrapper.closed.Store(true)
		manager.stop()
		assert.Eventually(t, func() bool {
			return !manager.reconnect.Load()
		}, 4*time.Second, 50*time.Millisecond)
	})

	t.Run("wait reconnect delay interrupted by close channel", func(t *testing.T) {
		manager := newCustomReconnectManager()
		manager.stop()
		assert.False(t, manager.waitReconnectDelay(25*time.Millisecond))
	})

	t.Run("start reconnect loop exits when close channel interrupts wait", func(t *testing.T) {
		fakeClient := newFakePahoClient()
		manager := newCustomReconnectManager()
		manager.rand = randSourceForTest()
		wrapper := &Paho{client: fakeClient, cfg: Config{Broker: "tcp://broker"}}

		manager.startReconnectLoop(wrapper)
		manager.stop()

		assert.Eventually(t, func() bool {
			return !manager.reconnect.Load()
		}, 2*time.Second, 25*time.Millisecond)
		assert.Zero(t, fakeClient.connectCallsCount())
	})

	t.Run("closed after timer fires", func(t *testing.T) {
		fakeClient := newFakePahoClient()
		manager := newCustomReconnectManager()
		manager.rand = randSourceForTest()
		wrapper := &Paho{client: fakeClient, cfg: Config{Broker: "tcp://broker"}}

		manager.startReconnectLoop(wrapper)
		wrapper.closed.Store(true)
		manager.stop()

		assert.Eventually(t, func() bool {
			return !manager.reconnect.Load()
		}, 2*time.Second, 50*time.Millisecond)
		assert.Zero(t, fakeClient.connectCallsCount())
	})
}

func TestWaitTokenBranches(t *testing.T) {
	t.Run("nil context success", func(t *testing.T) {
		assert.NoError(t, waitToken(testNilContext(), newCompleteToken(nil)))
	})

	t.Run("deadline already exceeded", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		assert.ErrorIs(t, waitToken(ctx, newPendingToken(nil)), context.DeadlineExceeded)
	})

	t.Run("timeout returns paho timed out sentinel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		err := waitToken(ctx, newPendingToken(errors.New("token timeout")))
		assert.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, paho.TimedOut))
	})

	t.Run("timeout returns context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := waitToken(ctx, newPendingToken(nil))
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("timeout with no context error returns paho timed out", func(t *testing.T) {
		ctx := &steadyDeadlineContext{deadline: time.Now().Add(2 * time.Millisecond)}
		err := waitToken(ctx, newPendingToken(nil))
		assert.ErrorIs(t, err, paho.TimedOut)
	})

	t.Run("expired deadline timeout branch returns context error", func(t *testing.T) {
		ctx := &errAfterTimeoutContext{deadline: time.Now().Add(1 * time.Millisecond)}
		err := waitToken(ctx, newPendingToken(nil))
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("timeout returns deadline exceeded after wait", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		err := waitToken(ctx, &fakeToken{
			waitTimeoutFunc: func(timeout time.Duration) bool {
				time.Sleep(10 * time.Millisecond)
				return false
			},
			done: make(chan struct{}),
		})
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("timeout returns context error in deadline branch", func(t *testing.T) {
		ctx := &toggleErrContext{deadline: time.Now().Add(1 * time.Millisecond)}
		err := waitToken(ctx, &fakeToken{
			waitTimeoutFunc: func(timeout time.Duration) bool {
				time.Sleep(5 * time.Millisecond)
				return false
			},
			done: make(chan struct{}),
		})
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("completed token returns token error", func(t *testing.T) {
		tokenErr := errors.New("publish failed")
		assert.Equal(t, tokenErr, waitToken(context.Background(), newCompleteToken(tokenErr)))
	})
}

func TestConnectionLostHandlerWhenClosed(t *testing.T) {
	address := reserveTCPAddress(t)
	broker := newTestBroker(t, address, nil)
	defer broker.Close()

	client, err := NewPaho(Config{Broker: "tcp://" + address, ClientID: "closed-loss"})
	require.NoError(t, err)
	require.NoError(t, client.Connect(context.Background()))
	defer disconnectClient(t, client)

	client.closed.Store(true)
	broker.Crash()

	assert.Eventually(t, func() bool {
		return !client.IsConnected()
	}, testTimeout, 50*time.Millisecond)

	manager, ok := client.reconnecter.(*customReconnectManager)
	require.True(t, ok)
	assert.False(t, manager.reconnect.Load())
}

func TestBuildTLSConfigBranches(t *testing.T) {
	t.Run("insecure skip", func(t *testing.T) {
		cfg, err := buildTLSConfig(Config{Broker: "tls://broker", TLSInsecureSkip: true})
		require.NoError(t, err)
		assert.True(t, cfg.InsecureSkipVerify)
	})

	t.Run("mismatched client cert and key", func(t *testing.T) {
		cfg, err := buildTLSConfig(Config{TLSClientCert: "cert.pem"})
		assert.Nil(t, cfg)
		assert.EqualError(t, err, "mqtt TLS client certificate and key must both be set")
	})

	t.Run("read CA file error", func(t *testing.T) {
		withSystemCertPoolStub(t, func() (*x509.CertPool, error) { return nil, nil })
		withReadFileStub(t, func(string) ([]byte, error) { return nil, errors.New("boom") })

		cfg, err := buildTLSConfig(Config{TLSCAFile: "ca.pem"})
		assert.Nil(t, cfg)
		assert.EqualError(t, err, "read mqtt CA file: boom")
	})

	t.Run("append CA certificates error", func(t *testing.T) {
		withSystemCertPoolStub(t, func() (*x509.CertPool, error) { return nil, nil })
		withReadFileStub(t, func(string) ([]byte, error) { return []byte("not a cert"), nil })

		cfg, err := buildTLSConfig(Config{TLSCAFile: "ca.pem"})
		assert.Nil(t, cfg)
		assert.EqualError(t, err, "append mqtt CA certificates: no certificates added")
	})

	t.Run("client certificate load error", func(t *testing.T) {
		withLoadX509KeyPairStub(t, func(string, string) (tls.Certificate, error) {
			return tls.Certificate{}, errors.New("bad pair")
		})

		cfg, err := buildTLSConfig(Config{TLSClientCert: "cert.pem", TLSClientCertKey: "key.pem"})
		assert.Nil(t, cfg)
		assert.EqualError(t, err, "load mqtt client certificate: bad pair")
	})

	t.Run("successful CA and client certificate load", func(t *testing.T) {
		certPEM, keyPEM := generateTestCertificate(t)
		caFile := writeTempFile(t, certPEM)
		certFile := writeTempFile(t, certPEM)
		keyFile := writeTempFile(t, keyPEM)

		withSystemCertPoolStub(t, func() (*x509.CertPool, error) { return nil, nil })

		cfg, err := buildTLSConfig(Config{
			Broker:           "tls://broker",
			TLSCAFile:        caFile,
			TLSClientCert:    certFile,
			TLSClientCertKey: keyFile,
		})
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.NotNil(t, cfg.RootCAs)
		assert.Len(t, cfg.Certificates, 1)
	})
}

func TestDefaultClientIDFallbacks(t *testing.T) {
	withHostnameStub(t, func() (string, error) { return "", errors.New("hostname failed") })
	assert.Equal(t, defaultTopicPrefix, defaultClientID())

	withHostnameStub(t, func() (string, error) { return "   ", nil })
	assert.Equal(t, defaultTopicPrefix, defaultClientID())
}

func TestPublisherErrorBranches(t *testing.T) {
	t.Run("publish error helper uses expected topic", func(t *testing.T) {
		client := &fakeMQTTClient{}
		PublishError(context.Background(), client, " /prefix/ ", ErrorPayload{Error: "failed"})
		require.Len(t, client.publishes, 1)
		assert.Equal(t, "prefix/error", client.publishes[0].topic)
		assert.False(t, client.publishes[0].retained)
		var payload ErrorPayload
		require.NoError(t, json.Unmarshal(client.publishes[0].payload, &payload))
		assert.False(t, payload.Timestamp.IsZero())
	})

	t.Run("availability nil client", func(t *testing.T) {
		PublishAvailability(context.Background(), nil, "", false)
	})

	t.Run("availability publish error", func(t *testing.T) {
		client := &fakeMQTTClient{publishErr: errors.New("publish failed")}
		PublishAvailability(context.Background(), client, "prefix", false)
	})

	t.Run("publish json nil client", func(t *testing.T) {
		publishJSON(context.Background(), nil, "topic", 1, false, map[string]string{"ok": "yes"})
	})

	t.Run("publish json marshal error", func(t *testing.T) {
		client := &fakeMQTTClient{}
		publishJSON(context.Background(), client, "topic", 1, false, map[string]float64{"bad": math.NaN()})
		assert.Empty(t, client.publishes)
	})

	t.Run("publish json publish error", func(t *testing.T) {
		client := &fakeMQTTClient{publishErr: errors.New("publish failed")}
		publishJSON(context.Background(), client, "topic", 1, false, map[string]string{"ok": "yes"})
		require.Len(t, client.publishes, 1)
	})
}

func TestPublisherSwallowsErrorsAndLogsWarn(t *testing.T) {
	observed := withObservedLogger(t)
	client := &fakeMQTTClient{publishErr: errors.New("publish failed")}

	assert.NotPanics(t, func() {
		PublishState(context.Background(), client, "", StatePayload{})
	})
	assert.NotPanics(t, func() {
		PublishError(context.Background(), client, "", ErrorPayload{Error: "failed"})
	})
	assert.NotPanics(t, func() {
		PublishAvailability(context.Background(), client, "", false)
	})

	warnCount := 0
	for _, entry := range observed.AllUntimed() {
		if entry.Level == zap.WarnLevel {
			warnCount++
		}
	}
	assert.Equal(t, 3, warnCount)
	assert.Equal(t, 3, observed.FilterMessage("mqtt publish failed").Len())
}

func newTestBroker(t *testing.T, address string, ledger *brokerauth.Ledger) *testBroker {
	t.Helper()

	server := broker.New(nil)
	server.Options.Capabilities.Compatibilities.ObscureNotAuthorized = true
	server.Options.Capabilities.Compatibilities.PassiveClientDisconnect = true
	server.Options.Capabilities.Compatibilities.NoInheritedPropertiesOnAck = true

	var err error
	if ledger == nil {
		err = server.AddHook(new(brokerauth.AllowHook), nil)
	} else {
		err = server.AddHook(new(brokerauth.Hook), &brokerauth.Options{Ledger: ledger})
	}
	require.NoError(t, err)

	listener := listeners.NewTCP(listeners.Config{ID: "mqtt-test", Address: address})
	require.NoError(t, server.AddListener(listener))

	go func() {
		_ = server.Serve()
	}()

	assert.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, testTimeout, 50*time.Millisecond)

	return &testBroker{server: server, address: address}
}

func (b *testBroker) Close() {
	if b == nil || b.server == nil {
		return
	}
	_ = b.server.Close()
	b.server = nil
}

func (b *testBroker) Crash() {
	if b == nil || b.server == nil {
		return
	}

	for _, client := range b.server.Clients.GetAll() {
		client.Stop(errors.New("test broker crash"))
	}

	b.Close()
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	return listener.Addr().String()
}

func mustConnectClient(t *testing.T, cfg Config) Client {
	t.Helper()

	client, err := New(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	require.NoError(t, client.Connect(ctx))

	return client
}

func disconnectClient(t *testing.T, client Client) {
	t.Helper()
	if client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	assert.NoError(t, client.Disconnect(ctx))
}

func waitForMessage(t *testing.T, messages <-chan receivedMessage) receivedMessage {
	t.Helper()

	select {
	case message := <-messages:
		return message
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for MQTT message")
		return receivedMessage{}
	}
}

type fakeToken struct {
	waitTimeoutFunc func(time.Duration) bool
	done            chan struct{}
	err             error
}

func newCompleteToken(err error) *fakeToken {
	done := make(chan struct{})
	close(done)
	return &fakeToken{
		waitTimeoutFunc: func(time.Duration) bool { return true },
		done:            done,
		err:             err,
	}
}

func newPendingToken(err error) *fakeToken {
	return &fakeToken{
		waitTimeoutFunc: func(time.Duration) bool { return false },
		done:            make(chan struct{}),
		err:             err,
	}
}

func (t *fakeToken) Wait() bool {
	<-t.done
	return true
}

func (t *fakeToken) WaitTimeout(timeout time.Duration) bool {
	return t.waitTimeoutFunc(timeout)
}

func (t *fakeToken) Done() <-chan struct{} {
	return t.done
}

func (t *fakeToken) Error() error {
	return t.err
}

type publishCall struct {
	topic    string
	qos      byte
	retained bool
	payload  []byte
}

type fakePahoClient struct {
	mu               sync.Mutex
	connected        bool
	connectToken     paho.Token
	publishToken     paho.Token
	subscribeToken   paho.Token
	connectCalls     int
	subscribeHandler paho.MessageHandler
	publishCalls     []publishCall
	disconnectCalls  []uint
	disconnectDelay  time.Duration
}

func newFakePahoClient() *fakePahoClient {
	return &fakePahoClient{
		connectToken:   newCompleteToken(nil),
		publishToken:   newCompleteToken(nil),
		subscribeToken: newCompleteToken(nil),
	}
}

func (c *fakePahoClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *fakePahoClient) IsConnectionOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *fakePahoClient) Connect() paho.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectCalls++
	c.connected = true
	return c.connectToken
}

func (c *fakePahoClient) Disconnect(quiesce uint) {
	c.mu.Lock()
	c.disconnectCalls = append(c.disconnectCalls, quiesce)
	disconnectDelay := c.disconnectDelay
	c.mu.Unlock()
	if disconnectDelay > 0 {
		time.Sleep(disconnectDelay)
	}
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
}

func (c *fakePahoClient) Publish(topic string, qos byte, retained bool, payload interface{}) paho.Token {
	var body []byte
	switch value := payload.(type) {
	case []byte:
		body = append([]byte(nil), value...)
	case string:
		body = []byte(value)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publishCalls = append(c.publishCalls, publishCall{topic: topic, qos: qos, retained: retained, payload: body})
	return c.publishToken
}

func (c *fakePahoClient) Subscribe(topic string, qos byte, callback paho.MessageHandler) paho.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribeHandler = callback
	return c.subscribeToken
}

func (c *fakePahoClient) SubscribeMultiple(filters map[string]byte, callback paho.MessageHandler) paho.Token {
	return newCompleteToken(nil)
}

func (c *fakePahoClient) Unsubscribe(topics ...string) paho.Token {
	return newCompleteToken(nil)
}

func (c *fakePahoClient) AddRoute(topic string, callback paho.MessageHandler) {}

func (c *fakePahoClient) OptionsReader() paho.ClientOptionsReader { return paho.ClientOptionsReader{} }

func (c *fakePahoClient) setConnected(connected bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = connected
}

func (c *fakePahoClient) connectCallsCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectCalls
}

func (c *fakePahoClient) disconnectCallsSnapshot() []uint {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint(nil), c.disconnectCalls...)
}

func (c *fakePahoClient) publishCallsSnapshot() []publishCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]publishCall(nil), c.publishCalls...)
}

func (c *fakePahoClient) subscribeHandlerFunc() paho.MessageHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subscribeHandler
}

type fakeMessage struct {
	topic   string
	payload []byte
}

func (m fakeMessage) Duplicate() bool   { return false }
func (m fakeMessage) Qos() byte         { return 1 }
func (m fakeMessage) Retained() bool    { return false }
func (m fakeMessage) Topic() string     { return m.topic }
func (m fakeMessage) MessageID() uint16 { return 1 }
func (m fakeMessage) Payload() []byte   { return m.payload }
func (m fakeMessage) Ack()              {}

func testNilContext() context.Context {
	return nil
}

type fakeMQTTClient struct {
	publishErr error
	publishes  []publishCall
}

func (c *fakeMQTTClient) Connect(ctx context.Context) error { return nil }

func (c *fakeMQTTClient) Disconnect(ctx context.Context) error { return nil }

func (c *fakeMQTTClient) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	c.publishes = append(c.publishes, publishCall{topic: topic, qos: qos, retained: retained, payload: append([]byte(nil), payload...)})
	return c.publishErr
}

func (c *fakeMQTTClient) Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error {
	return nil
}

func (c *fakeMQTTClient) IsConnected() bool { return true }

func withObservedLogger(t *testing.T) *observer.ObservedLogs {
	t.Helper()

	previous := utils.Log
	core, observed := observer.New(zap.DebugLevel)
	utils.Log = zap.New(core).Sugar()

	t.Cleanup(func() {
		_ = utils.Log.Sync()
		utils.Log = previous
	})

	return observed
}

func withHostnameStub(t *testing.T, stub func() (string, error)) {
	t.Helper()
	previous := hostnameFunc
	hostnameFunc = stub
	t.Cleanup(func() {
		hostnameFunc = previous
	})
}

func withSystemCertPoolStub(t *testing.T, stub func() (*x509.CertPool, error)) {
	t.Helper()
	previous := systemCertPoolFunc
	systemCertPoolFunc = stub
	t.Cleanup(func() {
		systemCertPoolFunc = previous
	})
}

func withReadFileStub(t *testing.T, stub func(string) ([]byte, error)) {
	t.Helper()
	previous := readFileFunc
	readFileFunc = stub
	t.Cleanup(func() {
		readFileFunc = previous
	})
}

func withLoadX509KeyPairStub(t *testing.T, stub func(string, string) (tls.Certificate, error)) {
	t.Helper()
	previous := loadX509KeyPairFunc
	loadX509KeyPairFunc = stub
	t.Cleanup(func() {
		loadX509KeyPairFunc = previous
	})
}

func writeTempFile(t *testing.T, content []byte) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "mqtt-*")
	require.NoError(t, err)
	_, err = file.Write(content)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	return file.Name()
}

func generateTestCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(crand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mqtt-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	derBytes, err := x509.CreateCertificate(crand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	certPEM := pemEncodeCertificate(derBytes)
	keyPEM := pemEncodePrivateKey(privateKey)
	return certPEM, keyPEM
}

func pemEncodeCertificate(der []byte) []byte {
	return pemEncode("CERTIFICATE", der)
}

func pemEncodePrivateKey(key *rsa.PrivateKey) []byte {
	return pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
}

func pemEncode(blockType string, bytes []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: bytes})
}

func randSourceForTest() *mrand.Rand {
	return mrand.New(mrand.NewSource(1))
}

type toggleErrContext struct {
	deadline time.Time
	errCalls int
}

func (c *toggleErrContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func (c *toggleErrContext) Done() <-chan struct{} {
	return nil
}

func (c *toggleErrContext) Err() error {
	c.errCalls++
	if c.errCalls == 1 {
		return nil
	}
	return context.DeadlineExceeded
}

func (c *toggleErrContext) Value(key any) any {
	return nil
}

type steadyDeadlineContext struct {
	deadline time.Time
}

func (c *steadyDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func (c *steadyDeadlineContext) Done() <-chan struct{} {
	return nil
}

func (c *steadyDeadlineContext) Err() error {
	return nil
}

func (c *steadyDeadlineContext) Value(key any) any {
	return nil
}

type errAfterTimeoutContext struct {
	deadline time.Time
	mu       sync.Mutex
	calls    int
}

func (c *errAfterTimeoutContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func (c *errAfterTimeoutContext) Done() <-chan struct{} {
	return nil
}

func (c *errAfterTimeoutContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++
	if c.calls == 1 {
		time.Sleep(2 * time.Millisecond)
		return nil
	}
	return context.DeadlineExceeded
}

func (c *errAfterTimeoutContext) Value(key any) any {
	return nil
}
