package mqtt

import (
	paho "github.com/eclipse/paho.mqtt.golang"

	"sbam/src/utils"
)

type pahoReconnectManager struct{}

func (m *pahoReconnectManager) configure(opts *paho.ClientOptions, client *Paho) {
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(false)
	opts.SetMaxReconnectInterval(reconnectMaxDelay)
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		if client.closed.Load() {
			return
		}

		utils.Log.Warnw("mqtt connection lost", "broker", sanitizeBroker(client.cfg.Broker), "strategy", ReconnectStrategyPaho, "error", err)
	})
	opts.SetReconnectingHandler(func(_ paho.Client, _ *paho.ClientOptions) {
		if client.closed.Load() {
			return
		}

		utils.Log.Warnw("mqtt reconnecting", "broker", sanitizeBroker(client.cfg.Broker), "strategy", ReconnectStrategyPaho)
	})
}

func (m *pahoReconnectManager) stop() {}

func (m *pahoReconnectManager) strategy() ReconnectStrategy {
	return ReconnectStrategyPaho
}
