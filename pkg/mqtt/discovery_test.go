package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type discoveryPayloadView struct {
	UniqueID          string   `json:"unique_id"`
	AvailabilityTopic string   `json:"availability_topic"`
	StateTopic        string   `json:"state_topic"`
	ValueTemplate     string   `json:"value_template"`
	CommandTopic      string   `json:"command_topic"`
	CommandTemplate   string   `json:"command_template"`
	PayloadPress      string   `json:"payload_press"`
	DefaultEntityID   string   `json:"default_entity_id"`
	Min               *float64 `json:"min"`
	Max               *float64 `json:"max"`
	Step              *float64 `json:"step"`
	Mode              string   `json:"mode"`
	Retain            *bool    `json:"retain"`
	Unit              string   `json:"unit_of_measurement"`
	Icon              string   `json:"icon"`
	EntityCategory    string   `json:"entity_category"`
	QoS               int      `json:"qos"`
	PayloadOn         string   `json:"payload_on"`
	PayloadOff        string   `json:"payload_off"`
	SWVersion         string   `json:"sw_version"`
	Device            struct {
		Identifiers  []string `json:"identifiers"`
		Manufacturer string   `json:"manufacturer"`
		Model        string   `json:"model"`
		SWVersion    string   `json:"sw_version"`
	} `json:"device"`
}

func TestBuildDiscoveryExpectedShape(t *testing.T) {
	cfg := Config{
		Enabled:           true,
		TopicPrefix:       "sbam",
		HADiscovery:       true,
		HADiscoveryPrefix: "homeassistant",
		FroniusIP:         "192.168.1.10",
		ClientID:          "sbam-client",
	}

	entities := BuildDiscovery(cfg, "2.0.0")
	require.NotEmpty(t, entities)
	assert.GreaterOrEqual(t, len(entities), 20)

	battery := requireEntity(t, entities, "sensor", "battery_soc_pct")
	assert.Equal(t, "homeassistant/sensor/sbam/battery_soc_pct/config", battery.Topic)

	batteryPayload := decodeDiscoveryPayload(t, battery.Payload)
	assert.Equal(t, "{{ value_json.battery_soc_pct }}", batteryPayload.ValueTemplate)
	assert.Equal(t, "sbam/state", batteryPayload.StateTopic)
	assert.Equal(t, "sbam/availability", batteryPayload.AvailabilityTopic)
	assert.Equal(t, "atbore-phx", batteryPayload.Device.Manufacturer)
	assert.Equal(t, "sbam", batteryPayload.Device.Model)
	assert.Equal(t, "2.0.0", batteryPayload.Device.SWVersion)
	require.Len(t, batteryPayload.Device.Identifiers, 1)
	assert.True(t, strings.HasPrefix(batteryPayload.Device.Identifiers[0], "sbam_"))

	button := requireEntity(t, entities, "button", "force_charge")
	buttonPayload := decodeDiscoveryPayload(t, button.Payload)
	assert.Equal(t, "sbam/cmd/force_charge", buttonPayload.CommandTopic)
	assert.Empty(t, buttonPayload.PayloadPress)
	assert.Contains(t, buttonPayload.CommandTemplate, "number.sbam_force_charge_target_pct")
	assert.Contains(t, buttonPayload.CommandTemplate, "ignore_max_charge")
	require.NotNil(t, buttonPayload.Retain)
	assert.False(t, *buttonPayload.Retain)

	pauseButton := requireEntity(t, entities, "button", "pause")
	pauseButtonPayload := decodeDiscoveryPayload(t, pauseButton.Payload)
	assert.Equal(t, "sbam/cmd/pause", pauseButtonPayload.CommandTopic)
	assert.Empty(t, pauseButtonPayload.PayloadPress)
	assert.Contains(t, pauseButtonPayload.CommandTemplate, "number.sbam_pause_duration_s")
	assert.Contains(t, pauseButtonPayload.CommandTemplate, `{"until":"{{ seconds }}s"}`)
	require.NotNil(t, pauseButtonPayload.Retain)
	assert.False(t, *pauseButtonPayload.Retain)

	forceSelector := requireEntity(t, entities, "number", "force_charge_target_pct")
	forceSelectorPayload := decodeDiscoveryPayload(t, forceSelector.Payload)
	assert.Equal(t, "number.sbam_force_charge_target_pct", forceSelectorPayload.DefaultEntityID)
	assert.Equal(t, "sbam/control/force_charge_target_pct", forceSelectorPayload.CommandTopic)
	assert.Equal(t, "sbam/control/force_charge_target_pct", forceSelectorPayload.StateTopic)
	assert.Equal(t, "%", forceSelectorPayload.Unit)
	assert.Equal(t, "box", forceSelectorPayload.Mode)
	assert.Equal(t, "config", forceSelectorPayload.EntityCategory)
	assert.Equal(t, "mdi:battery-charging-60", forceSelectorPayload.Icon)
	assert.Equal(t, int(qosAtLeastOnce), forceSelectorPayload.QoS)
	require.NotNil(t, forceSelectorPayload.Min)
	require.NotNil(t, forceSelectorPayload.Max)
	require.NotNil(t, forceSelectorPayload.Step)
	require.NotNil(t, forceSelectorPayload.Retain)
	assert.Equal(t, 0.0, *forceSelectorPayload.Min)
	assert.Equal(t, 101.0, *forceSelectorPayload.Max)
	assert.Equal(t, 1.0, *forceSelectorPayload.Step)
	assert.True(t, *forceSelectorPayload.Retain)

	pauseSelector := requireEntity(t, entities, "number", "pause_duration_s")
	pauseSelectorPayload := decodeDiscoveryPayload(t, pauseSelector.Payload)
	assert.Equal(t, "number.sbam_pause_duration_s", pauseSelectorPayload.DefaultEntityID)
	assert.Equal(t, "sbam/control/pause_duration_s", pauseSelectorPayload.CommandTopic)
	assert.Equal(t, "sbam/control/pause_duration_s", pauseSelectorPayload.StateTopic)
	assert.Equal(t, "s", pauseSelectorPayload.Unit)
	assert.Equal(t, "box", pauseSelectorPayload.Mode)
	assert.Equal(t, "config", pauseSelectorPayload.EntityCategory)
	assert.Equal(t, "mdi:timer-outline", pauseSelectorPayload.Icon)
	assert.Equal(t, int(qosAtLeastOnce), pauseSelectorPayload.QoS)
	require.NotNil(t, pauseSelectorPayload.Min)
	require.NotNil(t, pauseSelectorPayload.Max)
	require.NotNil(t, pauseSelectorPayload.Step)
	require.NotNil(t, pauseSelectorPayload.Retain)
	assert.Equal(t, 0.0, *pauseSelectorPayload.Min)
	assert.Equal(t, 86400.0, *pauseSelectorPayload.Max)
	assert.Equal(t, 60.0, *pauseSelectorPayload.Step)
	assert.True(t, *pauseSelectorPayload.Retain)

	paused := requireEntity(t, entities, "binary_sensor", "paused")
	pausedPayload := decodeDiscoveryPayload(t, paused.Payload)
	assert.Equal(t, "true", pausedPayload.PayloadOn)
	assert.Equal(t, "false", pausedPayload.PayloadOff)

	uniqueIDs := make(map[string]struct{})
	baseID := ""
	for _, entity := range entities {
		payload := decodeDiscoveryPayload(t, entity.Payload)
		if baseID == "" {
			require.Len(t, payload.Device.Identifiers, 1)
			baseID = payload.Device.Identifiers[0]
		}
		require.Len(t, payload.Device.Identifiers, 1)
		assert.Equal(t, baseID, payload.Device.Identifiers[0])

		_, exists := uniqueIDs[payload.UniqueID]
		assert.False(t, exists, "duplicate unique_id %s", payload.UniqueID)
		uniqueIDs[payload.UniqueID] = struct{}{}
	}
}

func TestBuildDiscoveryDefaults(t *testing.T) {
	cfg := Config{Enabled: true, HADiscovery: true}

	entities := BuildDiscovery(cfg, "")
	battery := requireEntity(t, entities, "sensor", "battery_soc_pct")
	payload := decodeDiscoveryPayload(t, battery.Payload)

	assert.Equal(t, "homeassistant/sensor/sbam/battery_soc_pct/config", battery.Topic)
	assert.Equal(t, "sbam/availability", payload.AvailabilityTopic)
	assert.Equal(t, "sbam/state", payload.StateTopic)
	assert.Equal(t, "dev", payload.Device.SWVersion)
}

func TestBuildDiscoveryTemplatesAndPublish(t *testing.T) {
	cfg := Config{
		Enabled:           true,
		TopicPrefix:       "site/sbam",
		HADiscovery:       true,
		HADiscoveryPrefix: "ha",
		ClientID:          "client-a",
	}

	entities := BuildDiscovery(cfg, "1.0.0")
	allowedFields := []string{
		"battery_soc_pct",
		"battery_capacity_wh",
		"forecast_today_wh",
		"pw_net_wh",
		"charge_pct",
		"last_decision",
		"last_decision_reason",
		"forecast_horizon",
		"consumption_horizon",
		"next_run",
		"paused",
		"ts",
		"charge_window_active",
		"batt_reserve_window_active",
	}

	for _, entity := range entities {
		payload := decodeDiscoveryPayload(t, entity.Payload)
		if payload.ValueTemplate == "" {
			continue
		}

		matched := false
		for _, field := range allowedFields {
			if strings.Contains(payload.ValueTemplate, "value_json."+field) {
				matched = true
				break
			}
		}
		assert.True(t, matched, "unexpected template %s", payload.ValueTemplate)
	}

	client := &fakeMQTTClient{}
	PublishDiscovery(context.Background(), client, cfg, "1.0.0")
	require.Len(t, client.publishes, len(entities))
	for _, publish := range client.publishes {
		assert.True(t, publish.retained)
		assert.Equal(t, qosAtLeastOnce, publish.qos)
	}

	assert.NotPanics(t, func() {
		PublishDiscovery(context.Background(), nil, cfg, "1.0.0")
	})
}

func TestNewWithDiscovery(t *testing.T) {
	client, err := New(Config{Enabled: false})
	require.NoError(t, err)
	_, isNoop := client.(*Noop)
	assert.True(t, isNoop)

	enabled, err := New(Config{Enabled: true, Broker: "tcp://example.com:1883"}, "v1.2.3")
	require.NoError(t, err)
	pahoClient, ok := enabled.(*Paho)
	require.True(t, ok)
	assert.Equal(t, "v1.2.3", pahoClient.discoveryVersion)

	defaultVersion, err := New(Config{Enabled: true, Broker: "tcp://example.com:1883"})
	require.NoError(t, err)
	pahoDefault, ok := defaultVersion.(*Paho)
	require.True(t, ok)
	assert.Equal(t, "dev", pahoDefault.discoveryVersion)
}

func requireEntity(t *testing.T, entities []DiscoveryEntity, component, objectID string) DiscoveryEntity {
	t.Helper()
	for _, entity := range entities {
		if entity.Component == component && entity.ObjectID == objectID {
			return entity
		}
	}
	t.Fatalf("entity %s/%s not found", component, objectID)
	return DiscoveryEntity{}
}

func decodeDiscoveryPayload(t *testing.T, payload []byte) discoveryPayloadView {
	t.Helper()
	var decoded discoveryPayloadView
	require.NoError(t, json.Unmarshal(payload, &decoded))
	return decoded
}

func TestDiscoveryConfigTopicDefaults(t *testing.T) {
	assert.Equal(
		t,
		"homeassistant/sensor/sbam/entity/config",
		discoveryConfigTopic(" ", " / ", " / "),
	)

	assert.Equal(
		t,
		"ha/button/sbam/trigger_now/config",
		discoveryConfigTopic(" /ha/ ", " /button/ ", " /trigger_now/ "),
	)

	assert.Equal(
		t,
		"sbam/control/force_charge_target_pct",
		controlTopic(" /sbam/ ", " /force_charge_target_pct/ "),
	)

	assert.Equal(
		t,
		"sbam/control/pause_duration_s",
		controlTopic(" ", "pause_duration_s"),
	)
}
