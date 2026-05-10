package mqtt

import (
	"testing"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/stretchr/testify/assert"
)

func TestPahoReconnectManagerConfigureSetsOptions(t *testing.T) {
	opts := paho.NewClientOptions()
	manager := &pahoReconnectManager{}
	wrapper := &Paho{cfg: Config{Broker: "tcp://example.com:1883"}}

	manager.configure(opts, wrapper)

	reader := paho.NewOptionsReader(opts)
	assert.True(t, reader.AutoReconnect())
	assert.False(t, reader.ConnectRetry())
	assert.Equal(t, reconnectMaxDelay, reader.MaxReconnectInterval())
	assert.Equal(t, ReconnectStrategyPaho, manager.strategy())
}

func TestPahoReconnectManagerStopNoop(t *testing.T) {
	manager := &pahoReconnectManager{}
	assert.NotPanics(t, func() {
		manager.stop()
	})
}
