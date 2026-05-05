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

	if err := client.Publish(ctx, availabilityTopic(prefix), qosAtLeastOnce, true, []byte(status)); err != nil {
		logPublishWarning(availabilityTopic(prefix), qosAtLeastOnce, true, err)
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

	if err := client.Publish(ctx, topic, qos, retained, body); err != nil {
		logPublishWarning(topic, qos, retained, err)
	}
}

func logPublishWarning(topic string, qos byte, retained bool, err error) {
	utils.Log.Warnw("mqtt publish failed", "topic", topic, "qos", qos, "retained", retained, "error", err)
}
