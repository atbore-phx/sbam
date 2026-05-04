package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"sbam/src/utils"
)

var (
	hostnameFunc        = os.Hostname
	systemCertPoolFunc  = x509.SystemCertPool
	readFileFunc        = os.ReadFile
	loadX509KeyPairFunc = tls.LoadX509KeyPair
)

const (
	defaultOperationTimeout = 5 * time.Second
	reconnectBaseDelay      = 1 * time.Second
	reconnectMaxDelay       = 60 * time.Second
	reconnectJitterFactor   = 0.2
	reconnectProbeInterval  = 200 * time.Millisecond
	keepAliveInterval       = 30 * time.Second
	pingTimeout             = 10 * time.Second
	disconnectQuiesceMS     = 250
)

type Paho struct {
	cfg       Config
	client    paho.Client
	closeOnce sync.Once
	closed    atomic.Bool
	reconnect atomic.Bool
	randMu    sync.Mutex
	rand      *rand.Rand
}

func NewPaho(cfg Config) (*Paho, error) {
	if strings.TrimSpace(cfg.Broker) == "" {
		return nil, errors.New("mqtt broker required when enabled")
	}

	parsed, err := url.Parse(cfg.Broker)
	if err != nil {
		return nil, fmt.Errorf("parse mqtt broker: %w", err)
	}

	if !supportedScheme(parsed.Scheme) {
		return nil, fmt.Errorf("unsupported mqtt broker scheme %q", parsed.Scheme)
	}

	if strings.TrimSpace(cfg.ClientID) == "" {
		cfg.ClientID = defaultClientID()
	}

	p := &Paho{
		cfg:  cfg,
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	opts := paho.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	opts.SetClientID(cfg.ClientID)
	opts.SetOrderMatters(false)
	opts.SetConnectTimeout(defaultOperationTimeout)
	opts.SetWriteTimeout(defaultOperationTimeout)
	opts.SetKeepAlive(keepAliveInterval)
	opts.SetPingTimeout(pingTimeout)
	opts.SetAutoReconnect(false)
	opts.SetWill(availabilityTopic(cfg.TopicPrefix), "offline", qosAtLeastOnce, true)

	if strings.TrimSpace(cfg.Username) != "" {
		opts.SetUsername(cfg.Username)
	}
	if strings.TrimSpace(cfg.Password) != "" {
		opts.SetPassword(cfg.Password)
	}

	if requiresTLS(parsed.Scheme) {
		tlsConfig, tlsErr := buildTLSConfig(cfg)
		if tlsErr != nil {
			return nil, tlsErr
		}
		opts.SetTLSConfig(tlsConfig)
	}

	opts.SetOnConnectHandler(func(client paho.Client) {
		p.reconnect.Store(false)
		if err := waitToken(context.Background(), client.Publish(availabilityTopic(p.cfg.TopicPrefix), qosAtLeastOnce, true, []byte("online"))); err != nil {
			utils.Log.Warnw("mqtt availability publish failed", "topic", availabilityTopic(p.cfg.TopicPrefix), "retained", true, "qos", qosAtLeastOnce, "error", err)
		}
	})

	opts.SetConnectionLostHandler(func(client paho.Client, err error) {
		if p.closed.Load() {
			return
		}
		utils.Log.Warnw("mqtt connection lost", "broker", sanitizeBroker(p.cfg.Broker), "error", err)
		p.startReconnectLoop()
	})

	p.client = paho.NewClient(opts)

	return p, nil
}

func (p *Paho) Connect(ctx context.Context) error {
	if p.closed.Load() {
		return errors.New("mqtt client closed")
	}
	if p.client.IsConnected() {
		return nil
	}
	return waitToken(ctx, p.client.Connect())
}

func (p *Paho) Disconnect(ctx context.Context) error {
	called := false
	done := make(chan struct{})

	p.closeOnce.Do(func() {
		called = true
		p.closed.Store(true)
		p.reconnect.Store(false)
		go func() {
			if p.client != nil {
				p.client.Disconnect(disconnectQuiesceMS)
			}
			close(done)
		}()
	})

	if !called {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (p *Paho) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	if strings.TrimSpace(topic) == "" {
		return errors.New("mqtt topic required")
	}
	return waitToken(ctx, p.client.Publish(topic, qos, retained, payload))
}

func (p *Paho) Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error {
	if strings.TrimSpace(topic) == "" {
		return errors.New("mqtt topic required")
	}
	if handler == nil {
		return errors.New("mqtt handler required")
	}

	callback := func(client paho.Client, message paho.Message) {
		payload := append([]byte(nil), message.Payload()...)
		handler(message.Topic(), payload)
	}

	return waitToken(ctx, p.client.Subscribe(topic, qos, callback))
}

func (p *Paho) IsConnected() bool {
	return p.client != nil && p.client.IsConnected()
}

func (p *Paho) startReconnectLoop() {
	if !p.reconnect.CompareAndSwap(false, true) {
		return
	}

	go func() {
		delay := reconnectBaseDelay
		for {
			if p.closed.Load() {
				p.reconnect.Store(false)
				return
			}

			waitFor := p.jitterDelay(delay)
			timer := time.NewTimer(waitFor)
			<-timer.C

			if p.closed.Load() {
				p.reconnect.Store(false)
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), defaultOperationTimeout)
			err := waitToken(ctx, p.client.Connect())
			cancel()
			if err == nil {
				p.reconnect.Store(false)
				return
			}

			utils.Log.Warnw("mqtt reconnect failed", "broker", sanitizeBroker(p.cfg.Broker), "backoff", waitFor, "error", err)
			delay = nextReconnectDelay(delay)
		}
	}()
}

func (p *Paho) jitterDelay(base time.Duration) time.Duration {
	p.randMu.Lock()
	defer p.randMu.Unlock()

	multiplier := 1 - reconnectJitterFactor + (p.rand.Float64() * (2 * reconnectJitterFactor))
	return time.Duration(float64(base) * multiplier)
}

func waitToken(ctx context.Context, token paho.Token) error {
	if ctx == nil {
		ctx = context.Background()
	}

	timeout := defaultOperationTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		timeout = remaining
	}

	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if err := token.Error(); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("mqtt operation timed out after %s", timeout)
		}

		waitFor := remaining
		if waitFor > reconnectProbeInterval {
			waitFor = reconnectProbeInterval
		}

		if token.WaitTimeout(waitFor) {
			return token.Error()
		}

		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.TLSInsecureSkip,
	}

	if cfg.TLSInsecureSkip {
		utils.Log.Warnw("mqtt tls verification disabled", "broker", sanitizeBroker(cfg.Broker))
	}

	if strings.TrimSpace(cfg.TLSCAFile) != "" {
		roots, err := systemCertPoolFunc()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}

		pemBytes, err := readFileFunc(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read mqtt CA file: %w", err)
		}
		if !roots.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("append mqtt CA certificates: no certificates added")
		}
		tlsConfig.RootCAs = roots
	}

	hasClientCert := strings.TrimSpace(cfg.TLSClientCert) != ""
	hasClientKey := strings.TrimSpace(cfg.TLSClientCertKey) != ""
	if hasClientCert != hasClientKey {
		return nil, errors.New("mqtt TLS client certificate and key must both be set")
	}
	if hasClientCert {
		certificate, err := loadX509KeyPairFunc(cfg.TLSClientCert, cfg.TLSClientCertKey)
		if err != nil {
			return nil, fmt.Errorf("load mqtt client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}

	return tlsConfig, nil
}

func defaultClientID() string {
	hostname, err := hostnameFunc()
	if err != nil {
		return defaultTopicPrefix
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return defaultTopicPrefix
	}
	return defaultTopicPrefix + "-" + hostname
}

func nextReconnectDelay(current time.Duration) time.Duration {
	next := current * 2
	if next > reconnectMaxDelay {
		return reconnectMaxDelay
	}
	return next
}

func requiresTLS(scheme string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "tls", "ssl", "wss":
		return true
	default:
		return false
	}
}

func supportedScheme(scheme string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "tcp", "tls", "ssl", "ws", "wss":
		return true
	default:
		return false
	}
}

func sanitizeBroker(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}
