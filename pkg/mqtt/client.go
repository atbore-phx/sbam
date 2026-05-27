package mqtt

import (
	"context"
	"strings"
)

type MessageHandler func(topic string, payload []byte)

type Client interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error
	Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error
	IsConnected() bool
	OnConnect(cb func())
}

const (
	defaultTopicPrefix       = "sbam"
	defaultHADiscoveryPrefix = "homeassistant"
	defaultHAStatusTopic     = "homeassistant/status"
	qosAtLeastOnce           = byte(1)
)

func New(cfg Config, version ...string) (Client, error) {
	if !cfg.Enabled {
		return NewNoop(), nil
	}

	client, err := NewPaho(cfg)
	if err != nil {
		return nil, err
	}

	v := "dev"
	if len(version) > 0 && strings.TrimSpace(version[0]) != "" {
		v = version[0]
	}
	client.discoveryVersion = v

	return client, nil
}

func normalizePrefix(prefix string) string {
	trimmed := strings.Trim(strings.TrimSpace(prefix), "/")
	if trimmed == "" {
		return defaultTopicPrefix
	}
	return trimmed
}

func stateTopic(prefix string) string {
	return normalizePrefix(prefix) + "/state"
}

func errorTopic(prefix string) string {
	return normalizePrefix(prefix) + "/error"
}

func availabilityTopic(prefix string) string {
	return normalizePrefix(prefix) + "/availability"
}

func CommandTopicFilter(prefix string) string {
	return normalizePrefix(prefix) + "/cmd/+"
}

func normalizeDiscoveryPrefix(prefix string) string {
	trimmed := strings.Trim(strings.TrimSpace(prefix), "/")
	if trimmed == "" {
		return defaultHADiscoveryPrefix
	}
	return trimmed
}

func discoveryConfigTopic(discoveryPrefix, component, objectID string) string {
	component = strings.Trim(strings.TrimSpace(component), "/")
	if component == "" {
		component = "sensor"
	}

	objectID = strings.Trim(strings.TrimSpace(objectID), "/")
	if objectID == "" {
		objectID = "entity"
	}

	return normalizeDiscoveryPrefix(discoveryPrefix) + "/" + component + "/sbam/" + objectID + "/config"
}

func haStatusTopic() string {
	return defaultHAStatusTopic
}
