package mqtt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sbam/src/utils"
	"strings"
	"time"
)

const MaxPayloadBytes = 4096

var (
	ErrUnknownCommand  = errors.New("unknown command")
	ErrPayloadTooLarge = errors.New("payload too large")
	ErrInvalidPayload  = errors.New("invalid payload")
)

type commandTopicInfo struct {
	RawName      string
	Canonical    IntentKind
	AckTopic     string
	KnownCommand bool
}

type forceChargePayload struct {
	TargetPct *int `json:"target_pct"`
	DurationS *int `json:"duration_s,omitempty"`
}

type pausePayload struct {
	Until string `json:"until"`
}

func ParseIntent(topic string, payload []byte) (Intent, error) {
	return parseIntentAt(topic, payload, time.Now().UTC())
}

func parseIntentAt(topic string, payload []byte, now time.Time) (Intent, error) {
	if len(payload) > MaxPayloadBytes {
		return Intent{}, fmt.Errorf("%w: payload is %d bytes (max %d)", ErrPayloadTooLarge, len(payload), MaxPayloadBytes)
	}

	info := parseCommandTopic(topic)
	if !info.KnownCommand {
		return Intent{}, fmt.Errorf("%w", ErrUnknownCommand)
	}

	switch info.Canonical {
	case IntentTriggerNow, IntentSetDefaults, IntentResume:
		if err := validateEmptyOrObjectPayload(payload); err != nil {
			return Intent{}, err
		}
		return Intent{Kind: info.Canonical}, nil
	case IntentForceCharge:
		forcePayload, err := parseForceChargePayload(payload)
		if err != nil {
			return Intent{}, err
		}

		intent := Intent{
			Kind:      IntentForceCharge,
			TargetPct: int16(*forcePayload.TargetPct),
		}
		if forcePayload.DurationS != nil {
			intent.DurationS = *forcePayload.DurationS
		}

		return intent, nil
	case IntentPause:
		pauseData, err := parsePausePayload(payload)
		if err != nil {
			return Intent{}, err
		}

		pauseUntil, err := parsePauseUntil(pauseData.Until, now)
		if err != nil {
			return Intent{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
		}

		return Intent{
			Kind:       IntentPause,
			PauseUntil: &pauseUntil,
		}, nil
	default:
		return Intent{}, fmt.Errorf("%w", ErrUnknownCommand)
	}
}

func parseCommandTopic(topic string) commandTopicInfo {
	trimmedTopic := strings.Trim(strings.TrimSpace(topic), "/")
	if trimmedTopic == "" {
		return commandTopicInfo{}
	}

	parts := strings.Split(trimmedTopic, "/")
	cmdIndex := -1
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "cmd" {
			cmdIndex = i
			break
		}
	}

	if cmdIndex < 0 || cmdIndex+1 >= len(parts) {
		return commandTopicInfo{}
	}

	rawName := strings.TrimSpace(parts[cmdIndex+1])
	if rawName == "" {
		return commandTopicInfo{}
	}

	info := commandTopicInfo{RawName: strings.ToLower(rawName)}
	if cmdIndex+2 != len(parts) {
		return info
	}

	info.AckTopic = strings.Join(parts, "/") + "/ack"

	switch info.RawName {
	case string(IntentTriggerNow):
		info.Canonical = IntentTriggerNow
		info.KnownCommand = true
	case string(IntentForceCharge):
		info.Canonical = IntentForceCharge
		info.KnownCommand = true
	case string(IntentSetDefaults):
		info.Canonical = IntentSetDefaults
		info.KnownCommand = true
	case string(IntentPause):
		info.Canonical = IntentPause
		info.KnownCommand = true
	case string(IntentResume):
		info.Canonical = IntentResume
		info.KnownCommand = true
	}

	return info
}

func validateEmptyOrObjectPayload(payload []byte) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("%w: expected empty payload or {}", ErrInvalidPayload)
	}

	parsed := map[string]json.RawMessage{}
	if err := decodeStrictJSON(trimmed, &parsed); err != nil {
		return fmt.Errorf("%w: expected empty payload or {}", ErrInvalidPayload)
	}
	if len(parsed) > 0 {
		return fmt.Errorf("%w: expected empty payload or {}", ErrInvalidPayload)
	}

	return nil
}

func parseForceChargePayload(payload []byte) (forceChargePayload, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return forceChargePayload{}, fmt.Errorf("%w: target_pct is required", ErrInvalidPayload)
	}

	parsed := forceChargePayload{}
	if err := decodeStrictJSON(trimmed, &parsed); err != nil {
		return forceChargePayload{}, fmt.Errorf("%w: invalid force_charge payload", ErrInvalidPayload)
	}

	if parsed.TargetPct == nil {
		return forceChargePayload{}, fmt.Errorf("%w: target_pct is required", ErrInvalidPayload)
	}
	if *parsed.TargetPct < 1 || *parsed.TargetPct > 100 {
		return forceChargePayload{}, fmt.Errorf("%w: target_pct must be between 1 and 100", ErrInvalidPayload)
	}

	if parsed.DurationS != nil {
		if *parsed.DurationS < 0 || *parsed.DurationS > 86400 {
			return forceChargePayload{}, fmt.Errorf("%w: duration_s must be between 0 and 86400", ErrInvalidPayload)
		}
	}

	return parsed, nil
}

func parsePausePayload(payload []byte) (pausePayload, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return pausePayload{}, fmt.Errorf("%w: until is required", ErrInvalidPayload)
	}

	parsed := pausePayload{}
	if err := decodeStrictJSON(trimmed, &parsed); err != nil {
		return pausePayload{}, fmt.Errorf("%w: invalid pause payload", ErrInvalidPayload)
	}

	if strings.TrimSpace(parsed.Until) == "" {
		return pausePayload{}, fmt.Errorf("%w: until is required", ErrInvalidPayload)
	}

	return parsed, nil
}

func parsePauseUntil(until string, now time.Time) (time.Time, error) {
	if parsedTime, err := time.Parse(time.RFC3339, until); err == nil {
		if !parsedTime.After(now) {
			return time.Time{}, errors.New("until must be in the future")
		}
		return parsedTime.UTC(), nil
	}

	duration, err := time.ParseDuration(until)
	if err != nil {
		return time.Time{}, errors.New("until must be RFC3339 or Go duration")
	}
	if duration <= 0 {
		return time.Time{}, errors.New("until duration must be > 0")
	}

	parsedTime := now.Add(duration)
	if !parsedTime.After(now) {
		return time.Time{}, errors.New("until must be in the future")
	}

	return parsedTime.UTC(), nil
}

func buildAck(topic string, intent Intent, parseErr error, now time.Time) (string, AckPayload, error) {
	info := parseCommandTopic(topic)
	if strings.TrimSpace(info.AckTopic) == "" {
		return "", AckPayload{}, errors.New("invalid command topic for ack")
	}

	ack := AckPayload{
		Timestamp: now.UTC(),
		Accepted:  parseErr == nil,
	}

	if ack.Accepted {
		if intent.Kind == "" {
			return "", AckPayload{}, errors.New("accepted ack requires a command intent")
		}
		ack.Command = string(intent.Kind)
		return info.AckTopic, ack, nil
	}

	if info.KnownCommand {
		ack.Command = string(info.Canonical)
	} else if info.RawName != "" {
		ack.Command = info.RawName
	} else if intent.Kind != "" {
		ack.Command = string(intent.Kind)
	} else {
		ack.Command = "unknown"
	}

	if errors.Is(parseErr, ErrUnknownCommand) {
		ack.Error = ErrUnknownCommand.Error()
	} else if parseErr != nil {
		ack.Error = parseErr.Error()
	} else {
		ack.Error = "unknown error"
	}

	return info.AckTopic, ack, nil
}

func PublishAck(ctx context.Context, client Client, topic string, intent Intent, parseErr error) error {
	if client == nil {
		return errors.New("nil mqtt client")
	}

	ackTopic, ackPayload, err := buildAck(topic, intent, parseErr, time.Now().UTC())
	if err != nil {
		return err
	}

	payload, err := json.Marshal(ackPayload)
	if err != nil {
		return err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	utils.Log.Debugw("mqtt publish ack requested", "topic", ackTopic, "qos", qosAtLeastOnce, "retained", false, "accepted", ackPayload.Accepted, "size", len(payload))
	if err := client.Publish(ctx, ackTopic, qosAtLeastOnce, false, payload); err != nil {
		utils.Log.Warnw("mqtt publish ack failed", "topic", ackTopic, "qos", qosAtLeastOnce, "retained", false, "error", err)
		return err
	}
	utils.Log.Debugw("mqtt publish ack succeeded", "topic", ackTopic, "qos", qosAtLeastOnce, "retained", false, "accepted", ackPayload.Accepted, "size", len(payload))

	return nil
}

func decodeStrictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return err
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing json content")
	}

	return nil
}
