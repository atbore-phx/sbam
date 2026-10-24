package mqtt

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// optimizerActions are the enum options of the Optimizer Action and plan-hour
// sensors (UX §3.1).
var optimizerActions = []string{"charge", "hold", "standby", "discharge", "self_consume"}

// OptimizerPlanHours is the number of per-hour plan sensors opt_plan_h00..h35.
const OptimizerPlanHours = 36

type availabilityEntry struct {
	Topic               string `json:"topic"`
	ValueTemplate       string `json:"value_template,omitempty"`
	PayloadAvailable    string `json:"payload_available,omitempty"`
	PayloadNotAvailable string `json:"payload_not_available,omitempty"`
}

type discoveryDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	SWVersion    string   `json:"sw_version"`
}

type discoveryPayload struct {
	Name              string          `json:"name,omitempty"`
	UniqueID          string          `json:"unique_id"`
	Device            discoveryDevice `json:"device"`
	AvailabilityTopic string          `json:"availability_topic,omitempty"`
	PayloadAvailable  string          `json:"payload_available,omitempty"`
	PayloadNotAvail   string          `json:"payload_not_available,omitempty"`
	StateTopic        string          `json:"state_topic,omitempty"`
	ValueTemplate     string          `json:"value_template,omitempty"`
	Unit              string          `json:"unit_of_measurement,omitempty"`
	DeviceClass       string          `json:"device_class,omitempty"`
	StateClass        string          `json:"state_class,omitempty"`
	EntityCategory    string          `json:"entity_category,omitempty"`
	Icon              string          `json:"icon,omitempty"`
	PayloadOn         string          `json:"payload_on,omitempty"`
	PayloadOff        string          `json:"payload_off,omitempty"`
	CommandTopic      string          `json:"command_topic,omitempty"`
	CommandTemplate   string          `json:"command_template,omitempty"`
	PayloadPress      string          `json:"payload_press,omitempty"`
	DefaultEntityID   string          `json:"default_entity_id,omitempty"`
	Min               *float64        `json:"min,omitempty"`
	Max               *float64        `json:"max,omitempty"`
	Step              *float64        `json:"step,omitempty"`
	Mode              string          `json:"mode,omitempty"`
	Retain            *bool           `json:"retain,omitempty"`
	QoS               int             `json:"qos,omitempty"`
	// Fields below are only set on optimizer entities; omitempty keeps the
	// existing entity payloads byte-for-byte unchanged.
	Availability           []availabilityEntry `json:"availability,omitempty"`
	AvailabilityMode       string              `json:"availability_mode,omitempty"`
	JSONAttributesTopic    string              `json:"json_attributes_topic,omitempty"`
	JSONAttributesTemplate string              `json:"json_attributes_template,omitempty"`
	Options                []string            `json:"options,omitempty"`
}

// BuildDiscovery returns Home Assistant MQTT discovery entities for sbam.
//
// BuildDiscovery publishes sensor state entities plus command controls and
// selector entities used by templated buttons. It normalizes prefixes and
// falls back to "dev" when the version string is empty.
func BuildDiscovery(cfg Config, version string) []DiscoveryEntity {
	return BuildDiscoveryWithDeviceID(cfg, version, "")
}

// BuildDiscoveryWithDeviceID is BuildDiscovery with an explicit device
// identifier; empty deviceID falls back to the config-derived value.
func BuildDiscoveryWithDeviceID(cfg Config, version string, deviceID string) []DiscoveryEntity {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}

	prefix := normalizePrefix(cfg.TopicPrefix)
	discoveryPrefix := normalizeDiscoveryPrefix(cfg.HADiscoveryPrefix)
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		deviceID = discoveryDeviceIdentifier(cfg)
	}

	device := discoveryDevice{
		Identifiers:  []string{deviceID},
		Name:         "sbam",
		Manufacturer: "atbore-phx",
		Model:        "sbam",
		SWVersion:    version,
	}

	base := discoveryPayload{
		Device:            device,
		AvailabilityTopic: availabilityTopic(prefix),
		PayloadAvailable:  "online",
		PayloadNotAvail:   "offline",
		StateTopic:        stateTopic(prefix),
		QoS:               int(qosAtLeastOnce),
	}

	forceChargeControlTopic := controlTopic(prefix, "force_charge_target_pct")
	pauseDurationControlTopic := controlTopic(prefix, "pause_duration_s")
	forceChargeCommandTemplate := `{% set target = states('number.sbam_force_charge_target_pct') | int(100) %}{% if target > 100 %}{"target_pct":100,"ignore_max_charge":true}{% elif target < 0 %}{"target_pct":0}{% else %}{"target_pct":{{ target }}}{% endif %}`
	pauseCommandTemplate := `{% set seconds = states('number.sbam_pause_duration_s') | int(0) %}{% if seconds <= 0 %}{}{% else %}{"until":"{{ seconds }}s"}{% endif %}`

	entities := make([]DiscoveryEntity, 0, 21)

	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "battery_soc_pct", sensorPayload(base, deviceID, "battery_soc_pct", "Battery SoC", "{{ value_json.battery_soc_pct }}", "%", "battery", "measurement", ""))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "battery_capacity_wh", sensorPayload(base, deviceID, "battery_capacity_wh", "Battery Capacity", "{{ value_json.battery_capacity_wh }}", "Wh", "", "measurement", ""))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "forecast_today_wh", sensorPayload(base, deviceID, "forecast_today_wh", "Forecast Today", "{{ value_json.forecast_today_wh }}", "Wh", "", "measurement", ""))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "pw_net_wh", sensorPayload(base, deviceID, "pw_net_wh", "Net Energy", "{{ value_json.pw_net_wh }}", "Wh", "", "measurement", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "charge_pct", sensorPayload(base, deviceID, "charge_pct", "Charge Percent", "{{ value_json.charge_pct }}", "%", "battery", "measurement", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "last_decision", sensorPayload(base, deviceID, "last_decision", "Last Decision", "{{ value_json.last_decision }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "forecast_horizon", sensorPayload(base, deviceID, "forecast_horizon", "Forecast Horizon", "{{ value_json.forecast_horizon }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "consumption_horizon", sensorPayload(base, deviceID, "consumption_horizon", "Consumption Horizon", "{{ value_json.consumption_horizon }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "scheduler_mode", sensorPayload(base, deviceID, "scheduler_mode", "Scheduler Mode", "{{ value_json.scheduler_mode }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "active_window", sensorPayload(base, deviceID, "active_window", "Active Window", "{{ value_json.active_window }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "active_window_max_charge", sensorPayload(base, deviceID, "active_window_max_charge", "Active Window Max Charge", "{{ value_json.active_window_max_charge }}", "W", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "active_window_forecast_horizon", sensorPayload(base, deviceID, "active_window_forecast_horizon", "Active Window Forecast Horizon", "{{ value_json.active_window_forecast_horizon }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "last_decision_reason", sensorPayload(base, deviceID, "last_decision_reason", "Decision Reason", "{{ value_json.last_decision_reason }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "next_run", sensorPayload(base, deviceID, "next_run", "Next Run", "{{ value_json.next_run }}", "", "timestamp", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "paused_state", sensorPayload(base, deviceID, "paused_state", "Paused State", "{{ value_json.paused }}", "", "", "", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "last_update", sensorPayload(base, deviceID, "last_update", "Last Update", "{{ value_json.ts }}", "", "timestamp", "", "diagnostic"))

	pausedBinary := sensorPayload(base, deviceID, "paused", "Paused", "{{ value_json.paused | string | lower }}", "", "", "", "diagnostic")
	pausedBinary.PayloadOn = "true"
	pausedBinary.PayloadOff = "false"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "binary_sensor", "paused", pausedBinary)

	chargeWindowBinary := sensorPayload(base, deviceID, "charge_window_active", "Charge Window", "{{ value_json.charge_window_active | string | lower }}", "", "", "", "diagnostic")
	chargeWindowBinary.PayloadOn = "true"
	chargeWindowBinary.PayloadOff = "false"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "binary_sensor", "charge_window_active", chargeWindowBinary)

	reserveWindowBinary := sensorPayload(base, deviceID, "batt_reserve_window_active", "Reserve Window", "{{ value_json.batt_reserve_window_active | string | lower }}", "", "", "", "diagnostic")
	reserveWindowBinary.PayloadOn = "true"
	reserveWindowBinary.PayloadOff = "false"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "binary_sensor", "batt_reserve_window_active", reserveWindowBinary)

	entities = appendDiscoveryEntity(entities, discoveryPrefix, "number", "force_charge_target_pct", numberPayload(base, deviceID, "force_charge_target_pct", "Force Charge Target", "number.sbam_force_charge_target_pct", forceChargeControlTopic, 0, 101, 1, "%", "mdi:battery-charging-60", "box"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "number", "pause_duration_s", numberPayload(base, deviceID, "pause_duration_s", "Pause Duration", "number.sbam_pause_duration_s", pauseDurationControlTopic, 0, 86400, 60, "s", "mdi:timer-outline", "box"))

	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "trigger_now", buttonPayload(base, deviceID, "trigger_now", "Trigger Now", commandTopic(prefix, "trigger_now"), "{}"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "pause", templatedButtonPayload(base, deviceID, "pause", "Pause", commandTopic(prefix, "pause"), pauseCommandTemplate))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "resume", buttonPayload(base, deviceID, "resume", "Resume", commandTopic(prefix, "resume"), "{}"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "force_charge", templatedButtonPayload(base, deviceID, "force_charge", "Force Charge", commandTopic(prefix, "force_charge"), forceChargeCommandTemplate))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "set_defaults", buttonPayload(base, deviceID, "set_defaults", "Set Defaults", commandTopic(prefix, "set_defaults"), "{}"))

	if cfg.Optimizer {
		entities = appendOptimizerEntities(entities, base, discoveryPrefix, prefix, deviceID, cfg.Currency)
	}

	return entities
}

// BuildOptimizerDiscoveryRemoval returns the 41 optimizer discovery topics
// with empty payloads. Publishing them retained removes the entities in HA
// (UX §3.5).
func BuildOptimizerDiscoveryRemoval(cfg Config) []DiscoveryEntity {
	discoveryPrefix := normalizeDiscoveryPrefix(cfg.HADiscoveryPrefix)
	ids := optimizerEntityIDs()
	entities := make([]DiscoveryEntity, 0, len(ids))
	for _, id := range ids {
		entities = append(entities, DiscoveryEntity{
			Component: id[0],
			ObjectID:  id[1],
			Topic:     discoveryConfigTopic(discoveryPrefix, id[0], id[1]),
			Payload:   []byte{},
		})
	}
	return entities
}

// optimizerEntityIDs lists (component, object_id) of every optimizer entity.
func optimizerEntityIDs() [][2]string {
	ids := [][2]string{
		{"sensor", "opt_action"},
		{"sensor", "opt_rate_w"},
		{"sensor", "opt_expected_cost"},
		{"sensor", "opt_horizon_h"},
		{"binary_sensor", "opt_dry_run"},
	}
	for hour := 0; hour < OptimizerPlanHours; hour++ {
		ids = append(ids, [2]string{"sensor", planHourObjectID(hour)})
	}
	return ids
}

func planHourObjectID(hour int) string {
	return fmt.Sprintf("opt_plan_h%02d", hour)
}

func appendOptimizerEntities(entities []DiscoveryEntity, base discoveryPayload, discoveryPrefix, prefix, deviceID, currency string) []DiscoveryEntity {
	plan := planTopic(prefix)

	action := sensorPayload(base, deviceID, "opt_action", "Optimizer Action", "{{ value_json.opt_action }}", "", "enum", "", "")
	action.DefaultEntityID = "sensor.sbam_opt_action"
	action.Options = optimizerActions
	action.Icon = "mdi:battery-sync"
	action.JSONAttributesTopic = plan
	action.JSONAttributesTemplate = "{{ {'hours': value_json.hours, 'expected_cost': value_json.expected_cost, 'terminal_value': value_json.terminal_value, 'horizon_end': value_json.horizon_end, 'reasons': value_json.reasons, 'currency': value_json.currency, 'dry_run': value_json.dry_run} | tojson }}"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "opt_action", action)

	rate := sensorPayload(base, deviceID, "opt_rate_w", "Optimizer Rate", "{{ value_json.opt_rate_w }}", "W", "power", "measurement", "")
	rate.DefaultEntityID = "sensor.sbam_opt_rate_w"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "opt_rate_w", rate)

	// monetary forbids state_class measurement; without a known currency
	// neither unit nor device class is set (UX §3.1, O-3).
	currency = strings.TrimSpace(currency)
	costDeviceClass := ""
	if currency != "" {
		costDeviceClass = "monetary"
	}
	cost := sensorPayload(base, deviceID, "opt_expected_cost", "Expected Cost", "{{ value_json.opt_expected_cost }}", currency, costDeviceClass, "", "")
	cost.DefaultEntityID = "sensor.sbam_opt_expected_cost"
	cost.Icon = "mdi:cash-clock"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "opt_expected_cost", cost)

	horizon := sensorPayload(base, deviceID, "opt_horizon_h", "Optimizer Horizon", "{{ value_json.opt_horizon_h }}", "h", "duration", "measurement", "diagnostic")
	horizon.DefaultEntityID = "sensor.sbam_opt_horizon_h"
	horizon.Icon = "mdi:timeline-clock-outline"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "opt_horizon_h", horizon)

	dryRun := sensorPayload(base, deviceID, "opt_dry_run", "Optimizer Dry Run", "{{ value_json.opt_dry_run | string | lower }}", "", "", "", "diagnostic")
	dryRun.DefaultEntityID = "binary_sensor.sbam_opt_dry_run"
	dryRun.Icon = "mdi:test-tube"
	dryRun.PayloadOn = "true"
	dryRun.PayloadOff = "false"
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "binary_sensor", "opt_dry_run", dryRun)

	for hour := 0; hour < OptimizerPlanHours; hour++ {
		objectID := planHourObjectID(hour)
		entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", objectID, planHourPayload(base, deviceID, prefix, hour))
	}

	return entities
}

// planHourPayload builds sensor opt_plan_hNN (UX §3.2). Template indices are
// plain integers because Jinja rejects zero-padded literals such as 05.
func planHourPayload(base discoveryPayload, deviceID, prefix string, hour int) discoveryPayload {
	objectID := planHourObjectID(hour)
	plan := planTopic(prefix)

	payload := sensorPayload(base, deviceID, objectID, fmt.Sprintf("Plan +%02dh", hour),
		fmt.Sprintf("{{ value_json.hours[%d].action if (value_json.hours | length) > %d else 'self_consume' }}", hour, hour),
		"", "enum", "", "diagnostic")
	payload.DefaultEntityID = "sensor.sbam_" + objectID
	payload.StateTopic = plan
	payload.Options = optimizerActions
	payload.Icon = "mdi:calendar-clock"
	payload.JSONAttributesTopic = plan
	payload.JSONAttributesTemplate = fmt.Sprintf("{{ (value_json.hours[%d] if (value_json.hours | length) > %d else {}) | tojson }}", hour, hour)

	// HA rejects availability_topic together with availability.
	payload.AvailabilityTopic = ""
	payload.PayloadAvailable = ""
	payload.PayloadNotAvail = ""
	payload.AvailabilityMode = "all"
	payload.Availability = []availabilityEntry{
		{Topic: availabilityTopic(prefix), PayloadAvailable: "online", PayloadNotAvailable: "offline"},
		{
			Topic:               plan,
			ValueTemplate:       fmt.Sprintf("{{ 'online' if (value_json.hours | length) > %d else 'offline' }}", hour),
			PayloadAvailable:    "online",
			PayloadNotAvailable: "offline",
		},
	}
	return payload
}

func sensorPayload(base discoveryPayload, deviceID, objectID, name, valueTemplate, unit, deviceClass, stateClass, entityCategory string) discoveryPayload {
	payload := base
	payload.Name = name
	payload.UniqueID = uniqueEntityID(deviceID, objectID)
	payload.ValueTemplate = valueTemplate
	payload.Unit = unit
	payload.DeviceClass = deviceClass
	payload.StateClass = stateClass
	payload.EntityCategory = entityCategory
	return payload
}

func buttonPayload(base discoveryPayload, deviceID, objectID, name, commandTopic, payloadPress string) discoveryPayload {
	payload := base
	payload.Name = name
	payload.UniqueID = uniqueEntityID(deviceID, objectID)
	payload.CommandTopic = commandTopic
	payload.CommandTemplate = ""
	payload.PayloadPress = payloadPress
	payload.DefaultEntityID = ""
	payload.Min = nil
	payload.Max = nil
	payload.Step = nil
	payload.Mode = ""
	payload.Retain = boolPtr(false)
	payload.StateTopic = ""
	payload.ValueTemplate = ""
	payload.Unit = ""
	payload.DeviceClass = ""
	payload.StateClass = ""
	payload.EntityCategory = ""
	payload.Icon = ""
	payload.PayloadOn = ""
	payload.PayloadOff = ""
	return payload
}

func templatedButtonPayload(base discoveryPayload, deviceID, objectID, name, commandTopic, commandTemplate string) discoveryPayload {
	payload := buttonPayload(base, deviceID, objectID, name, commandTopic, "")
	payload.CommandTemplate = commandTemplate
	return payload
}

func numberPayload(base discoveryPayload, deviceID, objectID, name, defaultEntityID, selectorTopic string, min, max, step float64, unit, icon, mode string) discoveryPayload {
	payload := base
	payload.Name = name
	payload.UniqueID = uniqueEntityID(deviceID, objectID)
	payload.DefaultEntityID = defaultEntityID
	payload.CommandTopic = selectorTopic
	payload.CommandTemplate = ""
	payload.PayloadPress = ""
	payload.StateTopic = selectorTopic
	payload.ValueTemplate = ""
	payload.Unit = unit
	payload.DeviceClass = ""
	payload.StateClass = ""
	payload.EntityCategory = "config"
	payload.Icon = icon
	payload.PayloadOn = ""
	payload.PayloadOff = ""
	payload.Min = floatPtr(min)
	payload.Max = floatPtr(max)
	payload.Step = floatPtr(step)
	payload.Mode = mode
	payload.Retain = boolPtr(true)
	return payload
}

func appendDiscoveryEntity(current []DiscoveryEntity, discoveryPrefix, component, objectID string, payload discoveryPayload) []DiscoveryEntity {
	body, err := json.Marshal(payload)
	if err != nil {
		return current
	}

	return append(current, DiscoveryEntity{
		Component: component,
		ObjectID:  objectID,
		Topic:     discoveryConfigTopic(discoveryPrefix, component, objectID),
		Payload:   body,
	})
}

func commandTopic(prefix, name string) string {
	return normalizePrefix(prefix) + "/cmd/" + strings.Trim(strings.TrimSpace(name), "/")
}

func controlTopic(prefix, name string) string {
	return normalizePrefix(prefix) + "/control/" + strings.Trim(strings.TrimSpace(name), "/")
}

func boolPtr(value bool) *bool {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

func uniqueEntityID(baseID, objectID string) string {
	return baseID + "_" + strings.Trim(strings.TrimSpace(objectID), "_")
}

func discoveryDeviceIdentifier(cfg Config) string {
	seed := strings.TrimSpace(cfg.FroniusIP)
	if seed == "" {
		seed = strings.TrimSpace(cfg.ClientID)
	}
	if seed == "" {
		seed = normalizePrefix(cfg.TopicPrefix)
	}
	if seed == "" {
		seed = defaultTopicPrefix
	}

	sum := sha1.Sum([]byte(seed))
	hash := hex.EncodeToString(sum[:])
	if len(hash) > 10 {
		hash = hash[:10]
	}

	return "sbam_" + hash
}
