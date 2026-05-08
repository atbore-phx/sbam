package mqtt

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strings"
)

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
	PayloadPress      string          `json:"payload_press,omitempty"`
	Retain            *bool           `json:"retain,omitempty"`
	QoS               int             `json:"qos,omitempty"`
}

func BuildDiscovery(cfg Config, version string) []DiscoveryEntity {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}

	prefix := normalizePrefix(cfg.TopicPrefix)
	discoveryPrefix := normalizeDiscoveryPrefix(cfg.HADiscoveryPrefix)
	deviceID := discoveryDeviceIdentifier(cfg)

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

	entities := make([]DiscoveryEntity, 0, 18)

	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "battery_soc_pct", sensorPayload(base, deviceID, "battery_soc_pct", "Battery SoC", "{{ value_json.battery_soc_pct }}", "%", "battery", "measurement", ""))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "battery_capacity_wh", sensorPayload(base, deviceID, "battery_capacity_wh", "Battery Capacity", "{{ value_json.battery_capacity_wh }}", "Wh", "", "measurement", ""))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "forecast_today_wh", sensorPayload(base, deviceID, "forecast_today_wh", "Forecast Today", "{{ value_json.forecast_today_wh }}", "Wh", "", "measurement", ""))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "pw_net_wh", sensorPayload(base, deviceID, "pw_net_wh", "Net Energy", "{{ value_json.pw_net_wh }}", "Wh", "", "measurement", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "charge_pct", sensorPayload(base, deviceID, "charge_pct", "Charge Percent", "{{ value_json.charge_pct }}", "%", "battery", "measurement", "diagnostic"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "last_decision", sensorPayload(base, deviceID, "last_decision", "Last Decision", "{{ value_json.last_decision }}", "", "", "", "diagnostic"))
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

	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "trigger_now", buttonPayload(base, deviceID, "trigger_now", "Trigger Now", commandTopic(prefix, "trigger_now"), "{}"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "pause", buttonPayload(base, deviceID, "pause", "Pause", commandTopic(prefix, "pause"), "{}"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "resume", buttonPayload(base, deviceID, "resume", "Resume", commandTopic(prefix, "resume"), "{}"))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "force_charge", buttonPayload(base, deviceID, "force_charge", "Force Charge", commandTopic(prefix, "force_charge"), `{"target_pct":100,"duration_s":3600}`))
	entities = appendDiscoveryEntity(entities, discoveryPrefix, "button", "set_defaults", buttonPayload(base, deviceID, "set_defaults", "Set Defaults", commandTopic(prefix, "set_defaults"), "{}"))

	return entities
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
	retained := false

	payload := base
	payload.Name = name
	payload.UniqueID = uniqueEntityID(deviceID, objectID)
	payload.CommandTopic = commandTopic
	payload.PayloadPress = payloadPress
	payload.Retain = &retained
	payload.StateTopic = ""
	payload.ValueTemplate = ""
	payload.Unit = ""
	payload.DeviceClass = ""
	payload.StateClass = ""
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
