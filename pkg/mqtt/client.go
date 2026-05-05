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
}

const (
	defaultTopicPrefix = "sbam"
	qosAtLeastOnce     = byte(1)
)

func New(cfg Config) (Client, error) {
	if !cfg.Enabled {
		return NewNoop(), nil
	}

	return NewPaho(cfg)
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
