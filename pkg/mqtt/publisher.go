package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"sbam/src/utils"
)

func PublishState(ctx context.Context, client Client, prefix string, payload StatePayload) {
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now().UTC()
	}
	publishJSON(ctx, client, stateTopic(prefix), qosAtLeastOnce, true, payload)
}

func PublishError(ctx context.Context, client Client, prefix string, payload ErrorPayload) {
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now().UTC()
	}
	publishJSON(ctx, client, errorTopic(prefix), qosAtLeastOnce, false, payload)
}

func PublishAvailability(ctx context.Context, client Client, prefix string, online bool) {
	status := "offline"
	if online {
		status = "online"
	}

	if client == nil {
		logPublishWarning(availabilityTopic(prefix), qosAtLeastOnce, true, errors.New("nil mqtt client"))
		return
	}

	utils.Log.Debugw("mqtt publish availability requested", "topic", availabilityTopic(prefix), "status", status)
	if err := client.Publish(ctx, availabilityTopic(prefix), qosAtLeastOnce, true, []byte(status)); err != nil {
		logPublishWarning(availabilityTopic(prefix), qosAtLeastOnce, true, err)
		return
	}
	utils.Log.Debugw("mqtt publish availability succeeded", "topic", availabilityTopic(prefix), "status", status)
}

func PublishDiscovery(ctx context.Context, client Client, cfg Config, version string) {
	if !cfg.Enabled || !cfg.HADiscovery {
		return
	}

	if client == nil {
		logPublishWarning(discoveryConfigTopic(cfg.HADiscoveryPrefix, "sensor", "sbam"), qosAtLeastOnce, true, errors.New("nil mqtt client"))
		return
	}

	entities := BuildDiscovery(cfg, version)
	for _, entity := range entities {
		utils.Log.Debugw("mqtt publish discovery requested", "topic", entity.Topic, "size", len(entity.Payload))
		if err := client.Publish(ctx, entity.Topic, qosAtLeastOnce, true, entity.Payload); err != nil {
			logPublishWarning(entity.Topic, qosAtLeastOnce, true, err)
			continue
		}
		utils.Log.Debugw("mqtt publish discovery succeeded", "topic", entity.Topic, "size", len(entity.Payload))
	}
}

func publishJSON(ctx context.Context, client Client, topic string, qos byte, retained bool, payload any) {
	if client == nil {
		logPublishWarning(topic, qos, retained, errors.New("nil mqtt client"))
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		logPublishWarning(topic, qos, retained, err)
		return
	}

	utils.Log.Debugw("mqtt publish json requested", "topic", topic, "qos", qos, "retained", retained, "size", len(body))
	if err := client.Publish(ctx, topic, qos, retained, body); err != nil {
		logPublishWarning(topic, qos, retained, err)
		return
	}
	utils.Log.Debugw("mqtt publish json succeeded", "topic", topic, "qos", qos, "retained", retained, "size", len(body))
}

func logPublishWarning(topic string, qos byte, retained bool, err error) {
	utils.Log.Warnw("mqtt publish failed", "topic", topic, "qos", qos, "retained", retained, "error", err)
}
