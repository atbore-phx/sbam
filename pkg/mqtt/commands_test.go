package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseIntentCanonicalCommands(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour).Format(time.RFC3339)

	tests := []struct {
		name             string
		topic            string
		payload          []byte
		expectedKind     IntentKind
		expectedTarget   int16
		expectedDuration int
		checkPause       bool
	}{
		{name: "trigger_now empty", topic: "sbam/cmd/trigger_now", payload: nil, expectedKind: IntentTriggerNow},
		{name: "trigger_now empty object", topic: "sbam/cmd/trigger_now", payload: []byte("{}"), expectedKind: IntentTriggerNow},
		{name: "set_defaults empty", topic: "sbam/cmd/set_defaults", payload: nil, expectedKind: IntentSetDefaults},
		{name: "resume empty object", topic: "sbam/cmd/resume", payload: []byte("{}"), expectedKind: IntentResume},
		{name: "force_charge full payload", topic: "sbam/cmd/force_charge", payload: []byte(`{"target_pct":50,"duration_s":3600}`), expectedKind: IntentForceCharge, expectedTarget: 50, expectedDuration: 3600},
		{name: "pause rfc3339 future", topic: "sbam/cmd/pause", payload: []byte(fmt.Sprintf(`{"until":%q}`, future)), expectedKind: IntentPause, checkPause: true},
		{name: "pause duration future", topic: "sbam/cmd/pause", payload: []byte(`{"until":"1h"}`), expectedKind: IntentPause, checkPause: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			intent, err := parseIntentAt(tc.topic, tc.payload, now)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedKind, intent.Kind)

			if tc.expectedKind == IntentForceCharge {
				assert.Equal(t, tc.expectedTarget, intent.TargetPct)
				assert.Equal(t, tc.expectedDuration, intent.DurationS)
			}

			if tc.checkPause {
				require.NotNil(t, intent.PauseUntil)
				assert.True(t, intent.PauseUntil.After(now))
			}
		})
	}
}

func TestParseIntentTopicValidation(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		topic   string
		payload []byte
	}{
		{name: "blank topic", topic: "", payload: nil},
		{name: "missing cmd segment", topic: "sbam/trigger_now", payload: nil},
		{name: "missing command name", topic: "sbam/cmd", payload: nil},
		{name: "ack topic rejected", topic: "sbam/cmd/pause/ack", payload: nil},
		{name: "unknown sub-topic", topic: "sbam/cmd/nope", payload: nil},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseIntentAt(tc.topic, tc.payload, now)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrUnknownCommand)
		})
	}
}

func TestParseIntentForceChargeValidation(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	valid := []string{
		`{"target_pct":1}`,
		`{"target_pct":100}`,
		`{"target_pct":50,"duration_s":0}`,
		`{"target_pct":50,"duration_s":86400}`,
	}

	for _, payload := range valid {
		intent, err := parseIntentAt("sbam/cmd/force_charge", []byte(payload), now)
		require.NoError(t, err)
		assert.Equal(t, IntentForceCharge, intent.Kind)
	}

	invalid := []string{
		`{}`,
		`{"target_pct":0}`,
		`{"target_pct":101}`,
		`{"target_pct":50,"duration_s":-1}`,
		`{"target_pct":50,"duration_s":86401}`,
		`{"target_pct":"50"}`,
		`{"target_pct":50,"duration_s":"1"}`,
		`{"target_pct":50,"extra":true}`,
		`[]`,
		`{"target_pct":50`,
	}

	for _, payload := range invalid {
		_, err := parseIntentAt("sbam/cmd/force_charge", []byte(payload), now)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidPayload)
	}
}

func TestParseIntentPauseValidation(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	valid := []string{
		fmt.Sprintf(`{"until":%q}`, now.Add(90*time.Minute).Format(time.RFC3339)),
		`{"until":"15m"}`,
	}

	for _, payload := range valid {
		intent, err := parseIntentAt("sbam/cmd/pause", []byte(payload), now)
		require.NoError(t, err)
		require.NotNil(t, intent.PauseUntil)
		assert.True(t, intent.PauseUntil.After(now))
	}

	invalid := []string{
		fmt.Sprintf(`{"until":%q}`, now.Add(-1*time.Minute).Format(time.RFC3339)),
		`{"until":"not-a-time"}`,
		`{"until":"0s"}`,
		`{"until":"-1m"}`,
		`{"until":123}`,
		`{}`,
	}

	for _, payload := range invalid {
		_, err := parseIntentAt("sbam/cmd/pause", []byte(payload), now)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidPayload)
	}
}

func TestParseIntentEmptyObjectOnlyCommands(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	validPayloads := [][]byte{nil, []byte("{}"), []byte(" { } ")}
	for _, payload := range validPayloads {
		_, err := parseIntentAt("sbam/cmd/trigger_now", payload, now)
		require.NoError(t, err)
	}

	invalidPayloads := [][]byte{[]byte("[]"), []byte("null"), []byte(`{"extra":true}`), []byte(`{"`)}
	for _, payload := range invalidPayloads {
		_, err := parseIntentAt("sbam/cmd/trigger_now", payload, now)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidPayload)
	}
}

func TestParseIntentPayloadLimit(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	basePayload := `{"target_pct":50}`
	paddedPayload := basePayload + strings.Repeat(" ", MaxPayloadBytes-len(basePayload))
	require.Len(t, []byte(paddedPayload), MaxPayloadBytes)

	_, err := parseIntentAt("sbam/cmd/force_charge", []byte(paddedPayload), now)
	require.NoError(t, err)

	tooLarge := paddedPayload + " "
	_, err = parseIntentAt("sbam/cmd/force_charge", []byte(tooLarge), now)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPayloadTooLarge)
}

func TestBuildAckPayloads(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	t.Run("accepted canonical command", func(t *testing.T) {
		topic, payload, err := buildAck("sbam/cmd/force_charge", Intent{Kind: IntentForceCharge}, nil, now)
		require.NoError(t, err)
		assert.Equal(t, "sbam/cmd/force_charge/ack", topic)
		assert.True(t, payload.Accepted)
		assert.Equal(t, "force_charge", payload.Command)
		assert.Empty(t, payload.Error)

		body, err := json.Marshal(payload)
		require.NoError(t, err)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(body, &decoded))
		assert.Equal(t, now.Format(time.RFC3339), payload.Timestamp.Format(time.RFC3339))
		_, hasError := decoded["error"]
		assert.False(t, hasError)
	})

	t.Run("unknown command", func(t *testing.T) {
		topic, payload, err := buildAck("sbam/cmd/nope", Intent{}, fmt.Errorf("%w", ErrUnknownCommand), now)
		require.NoError(t, err)
		assert.Equal(t, "sbam/cmd/nope/ack", topic)
		assert.False(t, payload.Accepted)
		assert.Equal(t, "nope", payload.Command)
		assert.Equal(t, "unknown command", payload.Error)
	})

	t.Run("invalid command topic", func(t *testing.T) {
		_, _, err := buildAck("sbam/not-a-command", Intent{}, fmt.Errorf("%w", ErrUnknownCommand), now)
		require.Error(t, err)
	})
}

func TestPublishAck(t *testing.T) {
	t.Run("publishes expected ack payload", func(t *testing.T) {
		client := &fakeMQTTClient{}

		err := PublishAck(context.Background(), client, "sbam/cmd/force_charge", Intent{Kind: IntentForceCharge}, nil)
		require.NoError(t, err)
		require.Len(t, client.publishes, 1)

		published := client.publishes[0]
		assert.Equal(t, "sbam/cmd/force_charge/ack", published.topic)
		assert.Equal(t, qosAtLeastOnce, published.qos)
		assert.False(t, published.retained)

		var payload AckPayload
		require.NoError(t, json.Unmarshal(published.payload, &payload))
		assert.Equal(t, "force_charge", payload.Command)
		assert.True(t, payload.Accepted)
		assert.Empty(t, payload.Error)
	})

	t.Run("nil client returns error", func(t *testing.T) {
		err := PublishAck(context.Background(), nil, "sbam/cmd/force_charge", Intent{Kind: IntentForceCharge}, nil)
		require.Error(t, err)
	})

	t.Run("publish error is returned", func(t *testing.T) {
		client := &fakeMQTTClient{publishErr: errors.New("publish failed")}
		err := PublishAck(context.Background(), client, "sbam/cmd/force_charge", Intent{Kind: IntentForceCharge}, nil)
		require.Error(t, err)
		assert.EqualError(t, err, "publish failed")
	})

	t.Run("unknown command publishes rejected ack", func(t *testing.T) {
		client := &fakeMQTTClient{}
		err := PublishAck(context.Background(), client, "sbam/cmd/unknown", Intent{}, fmt.Errorf("%w", ErrUnknownCommand))
		require.NoError(t, err)
		require.Len(t, client.publishes, 1)

		var payload AckPayload
		require.NoError(t, json.Unmarshal(client.publishes[0].payload, &payload))
		assert.False(t, payload.Accepted)
		assert.Equal(t, "unknown", payload.Command)
		assert.Equal(t, "unknown command", payload.Error)
	})
}
