package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
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
		"active_window",
		"active_window_max_charge",
		"active_window_forecast_horizon",
		"charge_window_active",
		"batt_reserve_window_active",
		"scheduler_mode",
		"active_window",
		"active_window_max_charge",
		"active_window_forecast_horizon",
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
	// Optimizer disabled: existing entities, then 41 optimizer removals and the plan clear (T-0114).
	require.Len(t, client.publishes, len(entities)+41+1)
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

func TestHAStatusTopic(t *testing.T) {
	assert.Equal(t, "homeassistant/status", haStatusTopic())
}

func TestDiscoveryDeviceIdentifierFallbackToClientID(t *testing.T) {
	// With empty FroniusIP, should use ClientID
	cfg := Config{FroniusIP: "", ClientID: "my-client"}
	id := discoveryDeviceIdentifier(cfg)
	assert.True(t, strings.HasPrefix(id, "sbam_"))
}

func TestDiscoveryDeviceIdentifierFallbackToPrefix(t *testing.T) {
	// With empty FroniusIP and ClientID, should use normalized prefix
	cfg := Config{FroniusIP: "", ClientID: "", TopicPrefix: "custom/prefix"}
	id := discoveryDeviceIdentifier(cfg)
	assert.True(t, strings.HasPrefix(id, "sbam_"))
}

func TestDiscoveryDeviceIdentifierAllEmpty(t *testing.T) {
	cfg := Config{FroniusIP: "", ClientID: "", TopicPrefix: "   "}
	id := discoveryDeviceIdentifier(cfg)
	assert.True(t, strings.HasPrefix(id, "sbam_"))
}

func TestNormalizeDiscoveryPrefix(t *testing.T) {
	assert.Equal(t, "homeassistant", normalizeDiscoveryPrefix("  "))
	assert.Equal(t, "custom", normalizeDiscoveryPrefix(" /custom/ "))
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

var updateGolden = flag.Bool("update", false, "rewrite golden files under testdata/")

type goldenDiscoveryEntity struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
}

func goldenDiscoveryConfig(optimizer bool, currency string) Config {
	return Config{
		Enabled:           true,
		TopicPrefix:       "sbam",
		HADiscovery:       true,
		HADiscoveryPrefix: "homeassistant",
		Optimizer:         optimizer,
		Currency:          currency,
	}
}

func toGoldenEntities(entities []DiscoveryEntity) []goldenDiscoveryEntity {
	out := make([]goldenDiscoveryEntity, 0, len(entities))
	for _, entity := range entities {
		out = append(out, goldenDiscoveryEntity{Topic: entity.Topic, Payload: string(entity.Payload)})
	}
	return out
}

// assertGolden compares got (marshalled with indentation) against testdata/name.
// Run `go test ./pkg/mqtt/ -run <Test> -update` to regenerate.
func assertGolden(t *testing.T, name string, got any) {
	t.Helper()
	body, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	body = append(body, '\n')

	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, body, 0o644))
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), string(body))
}

// T-0112: existing entity IDs and payloads are byte-for-byte unchanged, both
// without and with the optimizer enabled.
func TestBuildDiscoveryLegacyEntitiesGolden(t *testing.T) {
	legacy := BuildDiscoveryWithDeviceID(goldenDiscoveryConfig(false, ""), "1.0.0", "sbam_golden")
	assertGolden(t, "discovery_legacy.golden.json", toGoldenEntities(legacy))
}

func optimizerEntities(entities []DiscoveryEntity) []DiscoveryEntity {
	out := make([]DiscoveryEntity, 0, 41)
	for _, entity := range entities {
		if strings.HasPrefix(entity.ObjectID, "opt_") {
			out = append(out, entity)
		}
	}
	return out
}

func decodeRawDiscovery(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	return decoded
}

func stringField(raw map[string]any, key string) string {
	value, _ := raw[key].(string)
	return value
}

// T-0112: with Optimizer enabled discovery adds exactly 41 optimizer entities
// (5 main + 36 plan hours) after the unchanged existing entities.
func TestBuildDiscoveryOptimizerEntities(t *testing.T) {
	legacy := BuildDiscoveryWithDeviceID(goldenDiscoveryConfig(false, ""), "1.0.0", "sbam_golden")
	withOpt := BuildDiscoveryWithDeviceID(goldenDiscoveryConfig(true, "DKK"), "1.0.0", "sbam_golden")

	require.Len(t, withOpt, len(legacy)+41)
	assert.Equal(t, legacy, withOpt[:len(legacy)], "existing entities must be byte-for-byte unchanged")
	assertGolden(t, "discovery_legacy.golden.json", toGoldenEntities(withOpt[:len(legacy)]))

	opt := optimizerEntities(withOpt)
	require.Len(t, opt, 41)
	assertGolden(t, "discovery_optimizer.golden.json", toGoldenEntities(opt))

	cases := []struct {
		component, objectID, name, defaultEntityID, unit, deviceClass, stateClass, category, icon string
	}{
		{"sensor", "opt_action", "Optimizer Action", "sensor.sbam_opt_action", "", "enum", "", "", "mdi:battery-sync"},
		{"sensor", "opt_rate_w", "Optimizer Rate", "sensor.sbam_opt_rate_w", "W", "power", "measurement", "", ""},
		{"sensor", "opt_expected_cost", "Expected Cost", "sensor.sbam_opt_expected_cost", "DKK", "monetary", "", "", "mdi:cash-clock"},
		{"sensor", "opt_horizon_h", "Optimizer Horizon", "sensor.sbam_opt_horizon_h", "h", "duration", "measurement", "diagnostic", "mdi:timeline-clock-outline"},
		{"binary_sensor", "opt_dry_run", "Optimizer Dry Run", "binary_sensor.sbam_opt_dry_run", "", "", "", "diagnostic", "mdi:test-tube"},
		{"sensor", "opt_plan_h00", "Plan +00h", "sensor.sbam_opt_plan_h00", "", "enum", "", "diagnostic", "mdi:calendar-clock"},
		{"sensor", "opt_plan_h17", "Plan +17h", "sensor.sbam_opt_plan_h17", "", "enum", "", "diagnostic", "mdi:calendar-clock"},
		{"sensor", "opt_plan_h35", "Plan +35h", "sensor.sbam_opt_plan_h35", "", "enum", "", "diagnostic", "mdi:calendar-clock"},
	}
	for _, tc := range cases {
		t.Run(tc.objectID, func(t *testing.T) {
			entity := requireEntity(t, withOpt, tc.component, tc.objectID)
			assert.Equal(t, "homeassistant/"+tc.component+"/sbam/"+tc.objectID+"/config", entity.Topic)
			raw := decodeRawDiscovery(t, entity.Payload)
			assert.Equal(t, tc.name, raw["name"])
			assert.Equal(t, "sbam_golden_"+tc.objectID, raw["unique_id"])
			assert.Equal(t, tc.defaultEntityID, raw["default_entity_id"])
			assert.Equal(t, tc.unit, stringField(raw, "unit_of_measurement"))
			assert.Equal(t, tc.deviceClass, stringField(raw, "device_class"))
			assert.Equal(t, tc.stateClass, stringField(raw, "state_class"))
			assert.Equal(t, tc.category, stringField(raw, "entity_category"))
			assert.Equal(t, tc.icon, stringField(raw, "icon"))
			if tc.deviceClass == "enum" {
				assert.Equal(t, []any{"charge", "hold", "standby", "discharge", "self_consume"}, raw["options"])
			}
		})
	}

	action := decodeRawDiscovery(t, requireEntity(t, withOpt, "sensor", "opt_action").Payload)
	assert.Equal(t, "sbam/state", action["state_topic"])
	assert.Equal(t, "sbam/plan", action["json_attributes_topic"])
	assert.Contains(t, action["json_attributes_template"], "'hours': value_json.hours")
	assert.Equal(t, "sbam/availability", action["availability_topic"])
}

// T-0112 edge: optimizer unique IDs are distinct from each other and from the
// existing entities.
func TestBuildDiscoveryOptimizerUniqueIDs(t *testing.T) {
	entities := BuildDiscoveryWithDeviceID(goldenDiscoveryConfig(true, "EUR"), "1.0.0", "sbam_golden")
	seen := map[string]bool{}
	for _, entity := range entities {
		id := decodeDiscoveryPayload(t, entity.Payload).UniqueID
		assert.False(t, seen[id], "duplicate unique_id %s", id)
		seen[id] = true
	}
}

// T-0113: opt_plan_h05 uses availability_mode all with the hours-length
// template and no availability_topic.
func TestBuildDiscoveryPlanHourAvailability(t *testing.T) {
	entities := BuildDiscoveryWithDeviceID(goldenDiscoveryConfig(true, "DKK"), "1.0.0", "sbam_golden")
	raw := decodeRawDiscovery(t, requireEntity(t, entities, "sensor", "opt_plan_h05").Payload)

	_, hasTopic := raw["availability_topic"]
	assert.False(t, hasTopic, "availability_topic must be omitted when availability is set")
	_, hasPayloadAvailable := raw["payload_available"]
	assert.False(t, hasPayloadAvailable)
	assert.Equal(t, "all", raw["availability_mode"])
	assert.Equal(t, "sbam/plan", raw["state_topic"])
	assert.Equal(t, "{{ value_json.hours[5].action if (value_json.hours | length) > 5 else 'self_consume' }}", raw["value_template"])
	assert.Equal(t, "{{ (value_json.hours[5] if (value_json.hours | length) > 5 else {}) | tojson }}", raw["json_attributes_template"])

	availability, ok := raw["availability"].([]any)
	require.True(t, ok)
	require.Len(t, availability, 2)
	first := availability[0].(map[string]any)
	assert.Equal(t, "sbam/availability", first["topic"])
	assert.Equal(t, "online", first["payload_available"])
	assert.Equal(t, "offline", first["payload_not_available"])
	second := availability[1].(map[string]any)
	assert.Equal(t, "sbam/plan", second["topic"])
	assert.Equal(t, "{{ 'online' if (value_json.hours | length) > 5 else 'offline' }}", second["value_template"])

	// Edge: indices are unpadded integers (Jinja rejects literals such as 05).
	h00 := decodeRawDiscovery(t, requireEntity(t, entities, "sensor", "opt_plan_h00").Payload)
	assert.Contains(t, h00["value_template"], "> 0 else")
	h35 := decodeRawDiscovery(t, requireEntity(t, entities, "sensor", "opt_plan_h35").Payload)
	assert.Contains(t, h35["value_template"], "hours[35]")
}

// T-0114: outside optimize mode PublishDiscovery removes the 41 optimizer
// entities with empty retained payloads and clears the plan topic.
func TestPublishDiscoveryRemovesOptimizerEntities(t *testing.T) {
	cfg := goldenDiscoveryConfig(false, "")
	client := &fakeMQTTClient{}
	PublishDiscovery(context.Background(), client, cfg, "1.0.0")

	removals := BuildOptimizerDiscoveryRemoval(cfg)
	require.Len(t, removals, 41)

	empty := map[string]publishCall{}
	for _, publish := range client.publishes {
		if len(publish.payload) == 0 {
			empty[publish.topic] = publish
		}
	}
	require.Len(t, empty, 42)
	for _, removal := range removals {
		publish, ok := empty[removal.Topic]
		require.True(t, ok, "missing removal for %s", removal.Topic)
		assert.True(t, publish.retained)
		assert.Equal(t, qosAtLeastOnce, publish.qos)
	}
	assert.Contains(t, empty, "homeassistant/binary_sensor/sbam/opt_dry_run/config")
	assert.Contains(t, empty, "homeassistant/sensor/sbam/opt_plan_h35/config")
	plan, ok := empty["sbam/plan"]
	require.True(t, ok, "plan topic must be cleared")
	assert.True(t, plan.retained)
}

// T-0114 edge: in optimize mode nothing is removed and the plan is not cleared.
func TestPublishDiscoveryOptimizerPublishesEntities(t *testing.T) {
	client := &fakeMQTTClient{}
	PublishDiscovery(context.Background(), client, goldenDiscoveryConfig(true, "DKK"), "1.0.0")

	optCount := 0
	for _, publish := range client.publishes {
		assert.NotEmpty(t, publish.payload, "unexpected empty publish to %s", publish.topic)
		assert.NotEqual(t, "sbam/plan", publish.topic)
		if strings.Contains(publish.topic, "/opt_") {
			optCount++
		}
	}
	assert.Equal(t, 41, optCount)
}

// T-0114 failure: publish errors during removal are logged, not fatal, and
// every removal is still attempted.
func TestPublishDiscoveryRemovalPublishError(t *testing.T) {
	observed := withObservedLogger(t)
	client := &fakeMQTTClient{publishErr: errors.New("publish failed")}
	cfg := goldenDiscoveryConfig(false, "")
	assert.NotPanics(t, func() { PublishDiscovery(context.Background(), client, cfg, "1.0.0") })

	legacy := BuildDiscovery(cfg, "1.0.0")
	assert.Len(t, client.publishes, len(legacy)+42)
	assert.Equal(t, len(legacy)+42, observed.FilterMessage("mqtt publish failed").Len())

	assert.NotPanics(t, func() { publishEmptyRetained(context.Background(), nil, "sbam/plan") })
}

// T-0115: the opt_expected_cost unit follows Config.Currency. A currency change
// alters only that entity, so one discovery republish updates HA. Deciding
// when to republish (once per change) belongs to the caller in pkg/cmd.
func TestBuildDiscoveryExpectedCostCurrency(t *testing.T) {
	unknown := BuildDiscoveryWithDeviceID(goldenDiscoveryConfig(true, ""), "1.0.0", "sbam_golden")
	dkk := BuildDiscoveryWithDeviceID(goldenDiscoveryConfig(true, " DKK "), "1.0.0", "sbam_golden")
	require.Len(t, dkk, len(unknown))

	before := decodeRawDiscovery(t, requireEntity(t, unknown, "sensor", "opt_expected_cost").Payload)
	_, hasUnit := before["unit_of_measurement"]
	assert.False(t, hasUnit, "no unit without a known currency")
	_, hasClass := before["device_class"]
	assert.False(t, hasClass, "no device class without a known currency")

	after := decodeRawDiscovery(t, requireEntity(t, dkk, "sensor", "opt_expected_cost").Payload)
	assert.Equal(t, "DKK", after["unit_of_measurement"])
	assert.Equal(t, "monetary", after["device_class"])
	_, hasStateClass := after["state_class"]
	assert.False(t, hasStateClass, "monetary forbids state_class measurement")

	changed := []string{}
	for i := range unknown {
		if string(unknown[i].Payload) != string(dkk[i].Payload) {
			changed = append(changed, unknown[i].ObjectID)
		}
	}
	assert.Equal(t, []string{"opt_expected_cost"}, changed)

	client := &fakeMQTTClient{}
	PublishDiscovery(context.Background(), client, goldenDiscoveryConfig(true, "DKK"), "1.0.0")
	found := 0
	for _, publish := range client.publishes {
		if publish.topic == "homeassistant/sensor/sbam/opt_expected_cost/config" {
			found++
			assert.Contains(t, string(publish.payload), `"unit_of_measurement":"DKK"`)
		}
	}
	assert.Equal(t, 1, found)
}
