package mqtt

import (
	"fmt"
	"strings"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type reconnectManager interface {
	configure(opts *paho.ClientOptions, client *Paho)
	stop()
	strategy() ReconnectStrategy
}

func normalizeReconnectStrategy(strategy ReconnectStrategy) (ReconnectStrategy, error) {
	normalized := ReconnectStrategy(strings.ToLower(strings.TrimSpace(string(strategy))))

	switch normalized {
	case "", ReconnectStrategyCustom:
		return ReconnectStrategyCustom, nil
	case ReconnectStrategyPaho:
		return ReconnectStrategyPaho, nil
	default:
		return "", fmt.Errorf("unsupported mqtt reconnect strategy %q", strategy)
	}
}

func newReconnectManager(strategy ReconnectStrategy) reconnectManager {
	if strategy == ReconnectStrategyPaho {
		return &pahoReconnectManager{}
	}

	return newCustomReconnectManager()
}
