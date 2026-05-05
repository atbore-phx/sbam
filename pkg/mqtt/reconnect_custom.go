package mqtt

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"sbam/src/utils"
)

type customReconnectManager struct {
	closeOnce sync.Once
	closeCh   chan struct{}
	reconnect atomic.Bool
	randMu    sync.Mutex
	rand      *rand.Rand
}

func newCustomReconnectManager() *customReconnectManager {
	return &customReconnectManager{
		closeCh: make(chan struct{}),
		rand:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (m *customReconnectManager) configure(opts *paho.ClientOptions, client *Paho) {
	opts.SetAutoReconnect(false)
	opts.SetConnectRetry(false)
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		if client.closed.Load() {
			return
		}

		utils.Log.Warnw("mqtt connection lost", "broker", sanitizeBroker(client.cfg.Broker), "strategy", ReconnectStrategyCustom, "error", err)
		m.startReconnectLoop(client)
	})
}

func (m *customReconnectManager) stop() {
	m.reconnect.Store(false)
	if m.closeCh == nil {
		return
	}

	m.closeOnce.Do(func() {
		close(m.closeCh)
	})
}

func (m *customReconnectManager) strategy() ReconnectStrategy {
	return ReconnectStrategyCustom
}

func (m *customReconnectManager) startReconnectLoop(client *Paho) {
	if !m.reconnect.CompareAndSwap(false, true) {
		return
	}

	go func() {
		delay := reconnectBaseDelay
		for {
			if client.closed.Load() {
				m.reconnect.Store(false)
				return
			}

			waitFor := m.jitterDelay(delay)
			if !m.waitReconnectDelay(waitFor) {
				m.reconnect.Store(false)
				return
			}

			if client.closed.Load() || client.client == nil {
				m.reconnect.Store(false)
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), defaultOperationTimeout)
			err := waitToken(ctx, client.client.Connect())
			cancel()
			if err == nil {
				m.reconnect.Store(false)
				return
			}

			utils.Log.Warnw("mqtt reconnect failed", "broker", sanitizeBroker(client.cfg.Broker), "strategy", ReconnectStrategyCustom, "backoff", waitFor, "error", err)
			delay = nextReconnectDelay(delay)
		}
	}()
}

func (m *customReconnectManager) waitReconnectDelay(waitFor time.Duration) bool {
	timer := time.NewTimer(waitFor)
	defer timer.Stop()

	if m.closeCh == nil {
		<-timer.C
		return true
	}

	select {
	case <-timer.C:
		return true
	case <-m.closeCh:
		return false
	}
}

func (m *customReconnectManager) jitterDelay(base time.Duration) time.Duration {
	m.randMu.Lock()
	defer m.randMu.Unlock()

	if m.rand == nil {
		m.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	multiplier := 1 - reconnectJitterFactor + (m.rand.Float64() * (2 * reconnectJitterFactor))
	return time.Duration(float64(base) * multiplier)
}
