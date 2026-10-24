package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

// PublishPlan publishes the optimizer plan as a retained QoS 1 message on
// <prefix>/plan. Values are rounded (prices 4 decimals, Wh and W integers,
// SoC 1 decimal) and nil Hours/Reasons are sent as empty arrays.
func PublishPlan(ctx context.Context, client Client, prefix string, payload PlanPayload) {
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now().UTC()
	}
	publishJSON(ctx, client, planTopic(prefix), qosAtLeastOnce, true, normalizePlan(payload))
}

// ClearPlan removes the retained <prefix>/plan message by publishing an empty
// retained payload.
func ClearPlan(ctx context.Context, client Client, prefix string) {
	publishEmptyRetained(ctx, client, planTopic(prefix))
}

func normalizePlan(payload PlanPayload) PlanPayload {
	payload.StartSocPct = roundPtr(payload.StartSocPct, 1)
	payload.ExpectedCost = roundPtr(payload.ExpectedCost, 4)
	payload.TerminalValue = roundPtr(payload.TerminalValue, 4)
	if payload.Reasons == nil {
		payload.Reasons = []string{}
	}

	hours := make([]PlanEntry, 0, len(payload.Hours))
	for _, hour := range payload.Hours {
		hour.RateW = roundTo(hour.RateW, 0)
		hour.SocEndPct = roundTo(hour.SocEndPct, 1)
		hour.Buy = roundTo(hour.Buy, 4)
		hour.Sell = roundTo(hour.Sell, 4)
		hour.ConsumptionWh = roundTo(hour.ConsumptionWh, 0)
		hour.PvWh = roundTo(hour.PvWh, 0)
		hours = append(hours, hour)
	}
	payload.Hours = hours
	return payload
}

func roundTo(value float64, decimals int) float64 {
	factor := math.Pow(10, float64(decimals))
	rounded := math.Round(value*factor) / factor
	if rounded == 0 {
		return 0 // avoid "-0" in JSON
	}
	return rounded
}

func roundPtr(value *float64, decimals int) *float64 {
	if value == nil {
		return nil
	}
	rounded := roundTo(*value, decimals)
	return &rounded
}

// PublishDiscovery publishes Home Assistant discovery. With cfg.Optimizer the
// optimizer entities are included; otherwise their config topics receive
// empty retained payloads (removing them in HA) and <prefix>/plan is cleared.
// Both paths are idempotent.
func PublishDiscovery(ctx context.Context, client Client, cfg Config, version string) {
	if !cfg.Enabled || !cfg.HADiscovery {
		return
	}

	if client == nil {
		logPublishWarning(discoveryConfigTopic(cfg.HADiscoveryPrefix, "sensor", "sbam"), qosAtLeastOnce, true, errors.New("nil mqtt client"))
		return
	}

	entities := BuildDiscoveryWithDeviceID(cfg, version, EnsureStableDeviceID(cfg))
	for _, entity := range entities {
		utils.Log.Debugw("mqtt publish discovery requested", "topic", entity.Topic, "size", len(entity.Payload))
		if err := client.Publish(ctx, entity.Topic, qosAtLeastOnce, true, entity.Payload); err != nil {
			logPublishWarning(entity.Topic, qosAtLeastOnce, true, err)
			continue
		}
		utils.Log.Debugw("mqtt publish discovery succeeded", "topic", entity.Topic, "size", len(entity.Payload))
	}

	if cfg.Optimizer {
		return
	}
	for _, entity := range BuildOptimizerDiscoveryRemoval(cfg) {
		publishEmptyRetained(ctx, client, entity.Topic)
	}
	ClearPlan(ctx, client, cfg.TopicPrefix)
}

func publishEmptyRetained(ctx context.Context, client Client, topic string) {
	if client == nil {
		logPublishWarning(topic, qosAtLeastOnce, true, errors.New("nil mqtt client"))
		return
	}
	if err := client.Publish(ctx, topic, qosAtLeastOnce, true, []byte{}); err != nil {
		logPublishWarning(topic, qosAtLeastOnce, true, err)
		return
	}
	utils.Log.Debugw("mqtt publish empty retained succeeded", "topic", topic)
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
