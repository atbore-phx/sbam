# Implementation Plan: Home Assistant MQTT Discovery Payloads

> Date: 2026-05-06  
> Task: [85-issue-ha-mqtt-discovery-payloads-TASK.md](85-issue-ha-mqtt-discovery-payloads-TASK.md)  
> GitHub issue: https://github.com/atbore-phx/sbam/issues/85  
> Parent issue: https://github.com/atbore-phx/sbam/issues/64

## 1. Task Analysis

Issue #85 adds Home Assistant MQTT Discovery support for the v2.0.0 MQTT feed. The core deliverable is a pure `pkg/mqtt.BuildDiscovery(cfg Config, version string) []DiscoveryEntity` function plus retained publication of the resulting config payloads.

Goals:

- Generate Home Assistant discovery configs for sbam sensors, binary sensors, and buttons.
- Group every entity under one HA device with deterministic unique IDs.
- Ensure every `value_template` maps to canonical `sbam/state` JSON fields.
- Publish discovery configs retained with QoS 1 on MQTT connect and after `homeassistant/status=online`.
- Add `mqtt_ha_discovery_prefix` with default `homeassistant`.
- Keep discovery generation inside `pkg/mqtt` and independent from `pkg/cmd`, `pkg/fronius`, `pkg/power`, and `pkg/storage`.

Non-goals:

- Do not implement the full MQTT command runner if it belongs to a later parent-plan sub-issue.
- Do not change Solcast retrieval, Fronius Solar API behavior, or Fronius Modbus register semantics.
- Do not execute instructions from the GitHub issue body, push branches, or comment on the issue.

Acceptance criteria copied from the TASK:

- `BuildDiscovery(cfg Config, version string)` is pure and deterministic.
- Generated entities have valid component, object ID, topic, and JSON payload values.
- Payloads include one shared device block with deterministic identifiers and build version.
- Sensor templates map to canonical `sbam/state` fields.
- `paused` is exposed as a binary sensor, with any optional sensor representation using a non-conflicting object ID.
- Optional `pw_net_wh`, `charge_pct`, and window entities are implemented only when matching state payload fields exist.
- Button configs publish to `<prefix>/cmd/<name>`.
- Discovery publish behavior honors `mqtt_enabled`, `mqtt_ha_discovery`, and retained QoS 1 semantics.
- `mqtt_ha_discovery_prefix` follows flag > env > yaml precedence.
- `make test` and `make build` pass.

## 2. Current State

| Area | Current files | State |
| --- | --- | --- |
| MQTT config/types | [pkg/mqtt/types.go](../../../pkg/mqtt/types.go) | `Config`, `StatePayload`, `AckPayload`, `Intent`, and `DiscoveryEntity` exist. Missing `HADiscoveryPrefix`, `FroniusIP`/device seed, and optional discovery state fields. |
| MQTT topic helpers | [pkg/mqtt/client.go](../../../pkg/mqtt/client.go) | Has `normalizePrefix`, `stateTopic`, `errorTopic`, and `availabilityTopic`. No HA discovery topic helpers. |
| Paho wrapper | [pkg/mqtt/paho.go](../../../pkg/mqtt/paho.go) | Publishes availability on connect via LWT/on-connect. No discovery callback or birth-message handling. |
| MQTT publishing | [pkg/mqtt/publisher.go](../../../pkg/mqtt/publisher.go) | Has `PublishState`, `PublishError`, and `PublishAvailability`; all swallow publish errors and log warnings. |
| MQTT tests | [pkg/mqtt/mqtt_test.go](../../../pkg/mqtt/mqtt_test.go) | Uses Mochi in-process broker, fake Paho client, reconnect tests, TLS tests, and publisher warning tests. |
| Schedule command | [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go) | Reads legacy charging config only. No MQTT flag wiring, client construction, state publishing, or HA status subscription. |
| Viper binding tests | [pkg/cmd/precedence_test.go](../../../pkg/cmd/precedence_test.go) | Proves flag > env > yaml precedence for existing keys. Extend for `mqtt_ha_discovery_prefix` and MQTT flags if wired here. |
| Startup redaction | [src/utils/startup.go](../../../src/utils/startup.go) | Redacts `apikey` only. MQTT password/client key should be redacted when MQTT CLI keys are wired. |
| HA add-on config | [home-assistant/addons/sbam/config.json](../../../home-assistant/addons/sbam/config.json), [home-assistant/addons/sbam/run.sh](../../../home-assistant/addons/sbam/run.sh) | Version is already `2.0.0`, but MQTT options are absent. |
| Parent plan | [docs/implementations/64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md](../64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md) | Parent Step 3 describes discovery with the older `BuildDiscovery(prefix, device)` signature. Issue #85 supersedes this with `BuildDiscovery(cfg, version)`. |
| Dependencies | [go.mod](../../../go.mod) | Already has Paho MQTT, Mochi broker, Cobra/Viper, Testify, cron, and Modbus deps. No new module is needed. |

## 3. Target Architecture

Keep `pkg/mqtt` as the leaf package for discovery. The CLI builds an `mqtt.Config`, and `pkg/mqtt` owns all topic normalization, JSON payload generation, and retained discovery publishing.

```mermaid
flowchart LR
  Schedule[pkg/cmd schedule] --> Config[mqtt.Config]
  Config --> Factory[mqtt.NewWithDiscovery]
  Factory --> Paho[pkg/mqtt.Paho]
  Paho -- on connect --> Availability[PublishAvailability]
  Paho -- on connect --> Discovery[PublishDiscovery]
  Discovery --> Builder[BuildDiscovery]
  Builder --> Entities[[]DiscoveryEntity]
  Entities --> Broker[(MQTT broker retained configs)]
  HA[Home Assistant homeassistant/status=online] --> StatusSub[schedule status subscription]
  StatusSub --> Discovery
  Schedule --> State[PublishState]
  State --> Broker
```

Target package boundaries:

- `pkg/mqtt/discovery.go`: pure discovery entity construction and JSON marshaling.
- `pkg/mqtt/publisher.go` or `pkg/mqtt/discovery_publisher.go`: publish helper that loops over discovery entities and logs warnings.
- `pkg/mqtt/paho.go`: on-connect availability plus discovery publication when configured by a new factory or option.
- `pkg/cmd/schedule.go`: Viper/Cobra config wiring and status subscription; no discovery JSON details.
- Home Assistant add-on files: expose the new config values when MQTT options are added.

## 4. Dependency Choices

No new Go module is needed.

| Dependency | Version | Use |
| --- | --- | --- |
| `github.com/eclipse/paho.mqtt.golang` | `v1.5.1` | Existing MQTT client wrapper and on-connect handler. Godoc: https://pkg.go.dev/github.com/eclipse/paho.mqtt.golang |
| `github.com/mochi-mqtt/server/v2` | `v2.7.9` | Existing in-process broker tests for retained messages and HA status re-publication. Godoc: https://pkg.go.dev/github.com/mochi-mqtt/server/v2 |
| `github.com/stretchr/testify` | `v1.11.1` | Existing assertion style. |
| Standard library `crypto/sha1`, `encoding/hex`, `encoding/json` | n/a | Deterministic hashed IDs and structured payload generation. |

External references:

- Home Assistant MQTT Discovery: https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery
- Home Assistant MQTT sensor: https://www.home-assistant.io/integrations/sensor.mqtt/
- Home Assistant MQTT binary sensor: https://www.home-assistant.io/integrations/binary_sensor.mqtt/
- Home Assistant MQTT button: https://www.home-assistant.io/integrations/button.mqtt/

Relevant HA facts to mirror:

- Discovery topics follow `<discovery_prefix>/<component>/[<node_id>/]<object_id>/config`.
- The discovery prefix defaults to `homeassistant` and can be changed.
- Home Assistant publishes birth messages to `homeassistant/status` by default.
- Discovery payloads may be retained, and HA recommends birth-message-triggered re-publication.
- `unique_id` plus `device.identifiers` enables entity registry/device grouping.

## 5. Configuration Changes

Add one issue-specific key and, if schedule MQTT wiring is still absent at implementation time, wire the existing MQTT keys from #64 so the feature is observable from CLI/config/HA add-on.

| YAML key | CLI flag | Env var | Type | Default | Notes |
| --- | --- | --- | --- | --- | --- |
| `mqtt_enabled` | `--mqtt_enabled` | `MQTT_ENABLED` | bool | `false` | Existing #64 key; noop client when false. |
| `mqtt_broker` | `--mqtt_broker` | `MQTT_BROKER` | string | `""` | Required only when MQTT is enabled. |
| `mqtt_client_id` | `--mqtt_client_id` | `MQTT_CLIENT_ID` | string | `""` | Empty uses existing `defaultClientID()`. |
| `mqtt_username` | `--mqtt_username` | `MQTT_USERNAME` | string | `""` | Optional. |
| `mqtt_password` | `--mqtt_password` | `MQTT_PASSWORD` | string | `""` | Secret; add to `SecretKeys`. |
| `mqtt_tls_ca_file` | `--mqtt_tls_ca_file` | `MQTT_TLS_CA_FILE` | string | `""` | Optional CA bundle. |
| `mqtt_tls_client_cert` | `--mqtt_tls_client_cert` | `MQTT_TLS_CLIENT_CERT` | string | `""` | Optional client cert. |
| `mqtt_tls_client_cert_key` | `--mqtt_tls_client_cert_key` | `MQTT_TLS_CLIENT_CERT_KEY` | string | `""` | Secret; add to `SecretKeys`. |
| `mqtt_tls_insecure_skip` | `--mqtt_tls_insecure_skip` | `MQTT_TLS_INSECURE_SKIP` | bool | `false` | Existing Paho TLS option. |
| `mqtt_topic_prefix` | `--mqtt_topic_prefix` | `MQTT_TOPIC_PREFIX` | string | `sbam` | State/availability/command topic prefix. |
| `mqtt_ha_discovery` | `--mqtt_ha_discovery` | `MQTT_HA_DISCOVERY` | bool | `true` | Publish discovery when MQTT is enabled. |
| `mqtt_ha_discovery_prefix` | `--mqtt_ha_discovery_prefix` | `MQTT_HA_DISCOVERY_PREFIX` | string | `homeassistant` | New issue #85 key for HA discovery config topics. |

Implementation notes:

- Do not add a public config key for Fronius IP seed. Populate `mqtt.Config.FroniusIP` internally from existing `fronius_ip`.
- Do not expose reconnect strategy as part of #85 unless another already-landed issue wires it; use current package default.
- Add `mqtt_ha_discovery_prefix` to [config.yaml](../../../config.yaml) as an example value only if the project keeps examples there.
- Add `mqtt_ha_discovery_prefix` to [home-assistant/addons/sbam/config.json](../../../home-assistant/addons/sbam/config.json) and export `MQTT_HA_DISCOVERY_PREFIX` in [home-assistant/addons/sbam/run.sh](../../../home-assistant/addons/sbam/run.sh). If the rest of the MQTT add-on options are still absent, add the full table above in the same pass so HA users can enable MQTT.
- Precedence remains flag > env > yaml > default through `bindFlags(cmd)` and `viper.AutomaticEnv()`.

## 6. Implementation Blueprint

### Step 1 - Extend MQTT config and state types

Target: [pkg/mqtt/types.go](../../../pkg/mqtt/types.go)

Add fields to `Config`:

```go
HADiscoveryPrefix string
FroniusIP         string
```

Add optional state fields needed by the selected optional discovery entities:

```go
PwNetWh                  *float64 `json:"pw_net_wh,omitempty"`
ChargePct                *float64 `json:"charge_pct,omitempty"`
ChargeWindowActive       *bool    `json:"charge_window_active,omitempty"`
BattReserveWindowActive  *bool    `json:"batt_reserve_window_active,omitempty"`
```

Rationale:

- `HADiscoveryPrefix` is the new user-facing key.
- `FroniusIP` lets `pkg/mqtt` create a stable hashed ID without importing `pkg/fronius`.
- Pointer optional fields let `0` and `false` be published when known, while still allowing nil when unavailable.

### Step 2 - Add discovery topic helpers

Target: [pkg/mqtt/client.go](../../../pkg/mqtt/client.go)

Add constants and helpers:

```go
const defaultHADiscoveryPrefix = "homeassistant"

func normalizeDiscoveryPrefix(prefix string) string
func discoveryConfigTopic(discoveryPrefix, component, objectID string) string
func haStatusTopic() string
```

Rules:

- `normalizeDiscoveryPrefix` mirrors `normalizePrefix`: trim whitespace and slashes, default to `homeassistant`.
- `discoveryConfigTopic` returns `<discovery_prefix>/<component>/sbam/<object_id>/config`.
- `haStatusTopic` returns `homeassistant/status` to match issue #85 and Home Assistant defaults. Do not couple it to `mqtt_ha_discovery_prefix` unless a future issue adds a separate HA birth-topic setting.

### Step 3 - Implement pure discovery generation

Target: new [pkg/mqtt/discovery.go](../../../pkg/mqtt/discovery.go)

Public signature:

```go
func BuildDiscovery(cfg Config, version string) []DiscoveryEntity
```

Private helpers/types to add:

```go
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
```

Use `encoding/json` for payloads. If marshaling somehow fails, skip the entity; all field values are controlled by code, so normal operation should not fail.

Device/ID rules:

- `deviceIdentifier(cfg)` returns `sbam_<sha1(trimmed fronius_ip)[:10]>` when `cfg.FroniusIP` is present.
- Fallback seed is `cfg.ClientID`; final fallback is normalized `cfg.TopicPrefix`.
- Hash the fallback seed too, so broker/client names do not leak into HA identifiers.
- `unique_id` is `<device_id>_<object_id>`.
- Object IDs contain only lowercase ASCII letters, digits, and underscores.

Entities to emit:

| Component | Object ID | Key fields |
| --- | --- | --- |
| `sensor` | `battery_soc_pct` | `state_topic=<prefix>/state`, `unit_of_measurement="%"`, `device_class="battery"`, `state_class="measurement"`, `value_template="{{ value_json.battery_soc_pct }}"` |
| `sensor` | `battery_capacity_wh` | `unit_of_measurement="Wh"`, `value_template="{{ value_json.battery_capacity_wh }}"` |
| `sensor` | `forecast_today_wh` | `unit_of_measurement="Wh"`, `value_template="{{ value_json.forecast_today_wh }}"` |
| `sensor` | `last_decision` | diagnostic text, `value_template="{{ value_json.last_decision }}"` |
| `sensor` | `last_decision_reason` | diagnostic text, `value_template="{{ value_json.last_decision_reason }}"` |
| `sensor` | `next_run` | `device_class="timestamp"`, `value_template="{{ value_json.next_run }}"` |
| `sensor` | `last_update` | `device_class="timestamp"`, `value_template="{{ value_json.ts }}"` |
| `sensor` | `paused_state` | diagnostic text, `value_template="{{ value_json.paused }}"`; avoids collision with binary sensor. |
| `sensor` | `pw_net_wh` | optional selected field, `unit_of_measurement="Wh"`, `value_template="{{ value_json.pw_net_wh }}"` |
| `sensor` | `charge_pct` | optional selected field, `unit_of_measurement="%"`, `value_template="{{ value_json.charge_pct }}"` |
| `binary_sensor` | `paused` | `value_template="{{ value_json.paused }}"`, `payload_on="true"`, `payload_off="false"` |
| `binary_sensor` | `charge_window_active` | optional selected field, `value_template="{{ value_json.charge_window_active }}"` |
| `binary_sensor` | `batt_reserve_window_active` | optional selected field, `value_template="{{ value_json.batt_reserve_window_active }}"` |
| `button` | `trigger_now` | `command_topic=<prefix>/cmd/trigger_now`, `payload_press="{}"`, `retain=false` |
| `button` | `pause` | `command_topic=<prefix>/cmd/pause`, `payload_press="{}"`, `retain=false` |
| `button` | `resume` | `command_topic=<prefix>/cmd/resume`, `payload_press="{}"`, `retain=false` |
| `button` | `force_charge` | `command_topic=<prefix>/cmd/force_charge`, `payload_press={"target_pct":100,"duration_s":3600}`, `retain=false` |
| `button` | `set_defaults` | `command_topic=<prefix>/cmd/set_defaults`, `payload_press="{}"`, `retain=false` |

Every payload includes:

- `availability_topic=<prefix>/availability`
- `payload_available="online"`
- `payload_not_available="offline"`
- `qos=1`
- shared `device`

### Step 4 - Add discovery publisher helper

Target: [pkg/mqtt/publisher.go](../../../pkg/mqtt/publisher.go) or new [pkg/mqtt/discovery_publisher.go](../../../pkg/mqtt/discovery_publisher.go)

Public signature:

```go
func PublishDiscovery(ctx context.Context, client Client, cfg Config, version string)
```

Behavior:

- Return immediately when `!cfg.Enabled` or `!cfg.HADiscovery`.
- Build entities with `BuildDiscovery(cfg, version)`.
- Publish each `DiscoveryEntity.Topic` with QoS 1, retained `true`, and the entity payload.
- Reuse the existing warning pattern from `logPublishWarning` and never panic.
- Handle nil clients the same way `PublishAvailability` does.

Rationale:

- Mirrors current publisher helpers.
- Keeps schedule and Paho code from knowing discovery JSON details.

### Step 5 - Wire Paho on-connect discovery publication

Target: [pkg/mqtt/paho.go](../../../pkg/mqtt/paho.go) and [pkg/mqtt/client.go](../../../pkg/mqtt/client.go)

Add a factory for runtime discovery-aware clients:

```go
func NewWithDiscovery(cfg Config, version string) (Client, error)
```

Implementation outline:

- If `!cfg.Enabled`, return `NewNoop()` exactly like `New`.
- Call `NewPaho(cfg)` and set an unexported `discoveryVersion` field on `*Paho` before `Connect` is called.
- Keep `New(cfg)` behavior unchanged for tests and non-discovery users.
- In `Paho`'s `SetOnConnectHandler`, publish availability first as today, then call `PublishDiscovery(context.Background(), p, p.cfg, p.discoveryVersion)`.
- Use `version="dev"` when the passed version is blank.

Why this shape:

- The Paho on-connect handler fires after initial connect and reconnect.
- It avoids adding function fields to `Config`.
- It preserves the small `Client` interface.

### Step 6 - Add HA birth-message re-publication hook

Target: [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go)

When MQTT is enabled and discovery is enabled:

```go
err := client.Subscribe(ctx, "homeassistant/status", 1, func(topic string, payload []byte) {
    if strings.TrimSpace(string(payload)) == "online" {
        mqtt.PublishDiscovery(context.Background(), client, mqttCfg, appVersion)
        if latestState != nil {
            mqtt.PublishState(context.Background(), client, mqttCfg.TopicPrefix, *latestState)
        }
    }
})
```

Implementation details:

- Use `haStatusTopic()` if it is exported or keep the literal in `cmd`; prefer an exported helper only if the package already exposes topic helpers.
- Log subscription failures as warnings and continue scheduling; MQTT must not make charging unavailable.
- Keep state re-publication best-effort. If the schedule runner has not produced state yet, re-publish discovery only.
- Do not implement command handling here except button discovery topics.

### Step 7 - Store version for command packages

Target: [pkg/cmd/root.go](../../../pkg/cmd/root.go)

Current `main.go` calls `cmd.SetVersionInfo(version, commit, date)`, but `pkg/cmd` does not retain the raw version separately. Add:

```go
var appVersion = "dev"

func SetVersionInfo(version, commit, date string) error {
    appVersion = version
    rootCmd.Version = fmt.Sprintf("%s (Built on %s from Git SHA %s)", version, date, commit)
    return nil
}
```

Schedule wiring can pass `appVersion` to `mqtt.NewWithDiscovery` and `mqtt.PublishDiscovery`.

### Step 8 - Wire MQTT config into schedule

Target: [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go)

Add package variables/constants for MQTT config, but no shorthand letters:

```go
var mqtt_enabled bool
var mqtt_broker string
var mqtt_client_id string
var mqtt_username string
var mqtt_password string
var mqtt_tls_ca_file string
var mqtt_tls_client_cert string
var mqtt_tls_client_cert_key string
var mqtt_tls_insecure_skip bool
var mqtt_topic_prefix string
var mqtt_ha_discovery bool
var mqtt_ha_discovery_prefix string
```

Add flags in `registerScdCmd()` with defaults from the config table.

In `Run`, after existing Viper reads and after `fronius_ip` is resolved, build:

```go
mqttCfg := mqtt.Config{
    Enabled:           viper.GetBool("mqtt_enabled"),
    Broker:            viper.GetString("mqtt_broker"),
    ClientID:          viper.GetString("mqtt_client_id"),
    Username:          viper.GetString("mqtt_username"),
    Password:          viper.GetString("mqtt_password"),
    TLSCAFile:         viper.GetString("mqtt_tls_ca_file"),
    TLSClientCert:     viper.GetString("mqtt_tls_client_cert"),
    TLSClientCertKey:  viper.GetString("mqtt_tls_client_cert_key"),
    TLSInsecureSkip:   viper.GetBool("mqtt_tls_insecure_skip"),
    TopicPrefix:       viper.GetString("mqtt_topic_prefix"),
    HADiscovery:       viper.GetBool("mqtt_ha_discovery"),
    HADiscoveryPrefix: viper.GetString("mqtt_ha_discovery_prefix"),
    FroniusIP:         fronius_ip,
}
```

Create/connect client:

- If disabled, `mqtt.NewWithDiscovery` returns noop.
- If enabled and `NewWithDiscovery` or `Connect` fails, log a warning and fall back to `mqtt.NewNoop()` so charging still runs.
- `defer client.Disconnect(context.Background())` when a real client is connected.

### Step 9 - Publish state snapshots needed by discovery templates

Target: [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go)

Discovery entities are only useful when `sbam/state` contains the mapped fields. Add minimal state publishing without introducing the full command runner:

- Thread `mqtt.Client`, `mqtt.Config`, and a `latestState *mqtt.StatePayload` holder into `schedule()` and `crontabSchedule()`.
- Before calling `fronius.Handler`, call `fronius.ClassifyDecision(...)` with the same inputs to build the decision fields and `fronius.PowerState`.
- Continue calling `fronius.Handler` so existing Modbus behavior remains centralized.
- Capture `chargePct` from `fronius.Handler` return value.
- Build `mqtt.StatePayload`:
  - `BatterySOCPct = 100 * (capacity_max - capacity2charge) / capacity_max` when `capacity_max > 0`, else `0`.
  - `BatteryCapacityWh = capacity_max`.
  - `ForecastTodayWh = solarPowerProduction`.
  - `LastDecision = decision.String()`.
  - `LastDecisionReason = reason`.
  - `Paused = false` until runner pause support exists.
  - `PwNetWh = &powerState.Net`.
  - `ChargePct = &chargePctFloat`.
  - `ChargeWindowActive = &chargeWindowActive`.
  - `BattReserveWindowActive = &reserveWindowActive`.
  - `Timestamp = time.Now().UTC()`.
- Publish via `mqtt.PublishState(ctx, client, mqttCfg.TopicPrefix, payload)`.
- Update `latestState` so HA birth handling can republish it.

If a separate schedule-runner issue lands before #85 implementation, adapt this step to the runner's state emission API instead of duplicating scheduling flow.

### Step 10 - Update config examples and redaction

Targets:

- [config.yaml](../../../config.yaml)
- [src/utils/startup.go](../../../src/utils/startup.go)
- [src/utils/startup_test.go](../../../src/utils/startup_test.go)

Changes:

- Add MQTT example defaults to `config.yaml`, especially `mqtt_ha_discovery_prefix: "homeassistant"`.
- Add `mqtt_password` and `mqtt_tls_client_cert_key` to `SecretKeys` if the full MQTT keys are wired in `schedule.go`.
- Extend startup tests to prove MQTT secrets render as `***`.

### Step 11 - Update Home Assistant add-on surfaces

Targets:

- [home-assistant/addons/sbam/config.json](../../../home-assistant/addons/sbam/config.json)
- [home-assistant/addons/sbam/run.sh](../../../home-assistant/addons/sbam/run.sh)
- [home-assistant/addons/sbam/DOCS.md](../../../home-assistant/addons/sbam/DOCS.md)
- [home-assistant/addons/sbam/CHANGELOG.md](../../../home-assistant/addons/sbam/CHANGELOG.md)

Changes:

- Add MQTT options/schema entries from the configuration table if they are still absent.
- Use `password` schema for `mqtt_password` and `mqtt_tls_client_cert_key`.
- Export all MQTT env vars in `run.sh`, including `MQTT_HA_DISCOVERY_PREFIX`.
- Document HA discovery prefix, topic prefix, retained discovery behavior, and the `homeassistant/status` re-publication behavior in add-on docs.
- Add a `2.0.0` changelog entry if the changelog is still only the placeholder release link.

### Step 12 - Update README and project structure docs

Targets:

- [README.md](../../../README.md)
- [.github/copilot-instructions.md](../../../.github/copilot-instructions.md)

README additions:

- MQTT enablement example.
- Discovery topics and state topic schema.
- Button command topics and payloads.
- Note that discovery config payloads are retained and re-published on HA birth messages.

Project structure additions:

- Add `pkg/mqtt/discovery.go` and `pkg/mqtt/discovery_test.go`.
- Add `pkg/mqtt/testdata/` only if golden files are committed.
- Add any new schedule or command test files introduced by implementation.

Do not list `docs/implementations/85-issue-ha-mqtt-discovery-payloads/` in project structure; generated implementation docs are intentionally excluded by repo instructions.

## 7. Test Plan

### `pkg/mqtt/discovery_test.go`

Expected cases:

- `BuildDiscovery(Config{TopicPrefix:"sbam", HADiscoveryPrefix:"homeassistant", FroniusIP:"192.0.2.10"}, "2.0.0")` returns all required and selected optional entities.
- Assert one sensor, one binary sensor, and one button against golden JSON under `pkg/mqtt/testdata/` or equivalent canonical maps.
- Assert every payload has `device.identifiers`, `manufacturer`, `model`, `sw_version`, `unique_id`, `availability_topic`, and QoS.

Edge cases:

- Empty `TopicPrefix` normalizes state/availability/command topics to `sbam/...`.
- Empty `HADiscoveryPrefix` normalizes config topics to `homeassistant/...`.
- Missing `FroniusIP` falls back to client ID/topic prefix without raw empty identifiers.

Failure/robustness cases:

- No generated `value_template` references a field absent from `StatePayload`.
- Entity object IDs and unique IDs are unique across all components.
- `paused_state` sensor and `paused` binary sensor do not collide.

### `pkg/mqtt` publisher/Paho tests

Expected cases:

- `PublishDiscovery` publishes each entity to its discovery topic with QoS 1 and retained `true` using `fakeMQTTClient`.
- `NewWithDiscovery` plus real Mochi broker publishes retained discovery configs on connect; a late subscriber receives them.

Edge cases:

- `HADiscovery=false` publishes no discovery messages but still allows availability.
- `Enabled=false` returns noop and emits no logs.
- Paho auto-reconnect and custom reconnect both invoke on-connect discovery publication after reconnect if existing reconnect tests are extended.

Failure cases:

- Nil client logs one warning and does not panic.
- Publish errors are swallowed and logged as warnings, matching `PublishState` behavior.
- Broker crash tests call `Client.Stop(...)` on active broker-side clients before `Server.Close()` to avoid hangs.

### `pkg/cmd` tests

Expected cases:

- New schedule flags bind through Viper and `mqtt_ha_discovery_prefix` honors flag > env > yaml.
- `mqttConfigFromViper` or equivalent helper copies `fronius_ip` into `mqtt.Config.FroniusIP`.

Edge cases:

- Empty MQTT topic/discovery prefixes use defaults.
- `mqtt_enabled=false` produces noop/no connect and leaves scheduling behavior unchanged.

Failure cases:

- `mqtt_enabled=true` with invalid broker logs a warning and falls back to noop instead of aborting schedule.
- `mqtt_password` and `mqtt_tls_client_cert_key` are redacted in startup parameter dumps.

### Home Assistant add-on checks

Expected cases:

- `config.json` contains MQTT options and schema types.
- `run.sh` exports matching env vars.

Edge cases:

- Empty optional values export as empty strings and do not break `sbam schedule`.

Failure cases:

- Invalid broker scheme is rejected by `pkg/mqtt.NewPaho` tests.

### Manual HA smoke test

- Start a local or HA Mosquitto broker.
- Run `sbam schedule` with `MQTT_ENABLED=true`, `MQTT_BROKER=tcp://<broker>:1883`, `MQTT_HA_DISCOVERY=true`, and normal Fronius/Solcast config.
- Subscribe with `mosquitto_sub -v -t 'homeassistant/#'` and confirm retained config messages.
- Subscribe with `mosquitto_sub -v -t 'sbam/#'` and confirm `availability` and `state` messages.
- In Home Assistant, confirm one sbam device appears with implemented sensors, binary sensors, and buttons.

## 8. Validation Gates

Run these commands from the repository root:

```bash
go test ./pkg/mqtt -run 'TestBuildDiscovery|TestPublishDiscovery|TestNewWithDiscovery|TestHADiscovery'
go test ./pkg/cmd -run 'TestBindFlags|TestMQTT'
go test ./src/utils -run 'Test.*Startup|Test.*Secret'
make test
make build
```

If Home Assistant add-on files or Docker context files change, also run:

```bash
docker build --build-arg BUILD_FROM=ghcr.io/home-assistant/amd64-base:latest --build-arg PLATFORM=amd64 -t sbam-ha-addon-test home-assistant/addons/sbam
```

CI parity:

- [Makefile](../../../Makefile) runs `go test -cover -race ./...` for `make test` and `CGO_ENABLED=0 go build` for `make build`.
- [.github/workflows/test.yml](../../../.github/workflows/test.yml) runs gofmt, race coverage tests, Codecov upload, and `make build`.
- [.github/workflows/release.yml](../../../.github/workflows/release.yml) checks the HA add-on version against release tags.

## 9. Rollout / Backward Compatibility

- Default `mqtt_enabled=false` preserves v1.x behavior: no broker connection, no discovery messages, no command subscriptions, and no Modbus behavior changes.
- `mqtt_ha_discovery=true` only matters when MQTT is enabled.
- `mqtt_ha_discovery_prefix` defaults to `homeassistant`, matching HA defaults.
- Changing discovery topic/object IDs after release can leave retained ghost entities. Keep object IDs stable before merging.
- If a user changes `mqtt_ha_discovery_prefix`, document that old retained discovery topics may need cleanup by publishing empty retained payloads to the old prefix.
- Home Assistant add-on `config.json` is already at `2.0.0`; do not bump again unless release policy requires it.
- Add README and add-on docs for MQTT Discovery, topics, payload schemas, and manual smoke testing.
- Update [.github/copilot-instructions.md](../../../.github/copilot-instructions.md) for any new source/test files under `pkg/mqtt` and `pkg/cmd`.

## 10. Security Considerations

- Do not include MQTT username, password, TLS cert paths, TLS key paths, API keys, or raw broker URLs in discovery payloads.
- Use a SHA-1 hash prefix only for stable non-secret identifiers; do not expose raw `fronius_ip` in HA `unique_id`.
- Validate/sanitize discovery prefix, topic prefix, component, and object ID pieces to avoid malformed MQTT topics. Keep component/object IDs code-defined, not user-provided.
- Discovery buttons must publish non-retained command payloads. Retained command topics can replay unsafe actions and must be avoided.
- Discovery generation and publication must never write Modbus registers.
- Paho publish failures should log sanitized broker/topic context and continue; MQTT failure must not prevent charging logic.
- Redact `mqtt_password` and `mqtt_tls_client_cert_key` in startup dumps.

## 11. Gotchas

- Parent #64's Step 3 signature is stale for this issue; implement `BuildDiscovery(cfg Config, version string)` from #85.
- `homeassistant/status` is a birth-message topic, not the discovery config prefix itself. Keep it default unless a future config key is added.
- HA supports both single-component discovery and device discovery. This issue uses single-component discovery topics with a shared `device` block because parent #64 explicitly names `homeassistant/<component>/sbam/<object_id>/config`.
- HA requires `unique_id` for entity registry stability; use stable object IDs and hashed device IDs.
- `last_decision` should use the actual current enum values from [pkg/fronius/classify.go](../../../pkg/fronius/classify.go): `battery_full`, `forecast_charge`, `reserve_charge`, `idle`, `skip`.
- Current `schedule()` panics on some business errors. Do not broaden error-handling refactors beyond what MQTT fallback requires.
- Paho's on-connect handler has no caller context; use `context.Background()` with existing publish timeout behavior.
- Mochi broker restart tests can hang if the server closes with active clients; stop connected clients before closing, as captured in repo memory.
- `viper.AutomaticEnv()` maps `mqtt_ha_discovery_prefix` to `MQTT_HA_DISCOVERY_PREFIX` because sbam uses snake_case keys and uppercase env vars without an env prefix.
- If `StatePayload` pointer fields are nil, HA templates may render unknown. Tests should prove optional entities map to fields that exist, and schedule should populate them when possible.

## 12. Open Questions / Risks

- RESOLVED: Feature slug is `85-issue-ha-mqtt-discovery-payloads`.
- RESOLVED: Use `BuildDiscovery(cfg Config, version string)` from issue #85, not the older parent #64 `BuildDiscovery(prefix, device)` shape.
- RESOLVED: Add configurable `mqtt_ha_discovery_prefix` with default `homeassistant`.
- RESOLVED: Unique IDs use a Fronius IP hash with MQTT client ID/topic prefix fallback.
- RESOLVED: Include optional `pw_net_wh`, `charge_pct`, and window binary sensors by extending `StatePayload` and populating known values.
- DEFERRED: Full command execution for HA buttons depends on the command parser/runner work from the parent MQTT feed plan. #85 must generate correct button configs and command topics, but full button behavior may need the later runner issue.
- RISK: Adding minimal state publication in `schedule.go` overlaps parent #64 runtime wiring. If a runner issue lands first, adapt to that runner rather than maintaining two state-publish paths.
- RISK: If Home Assistant device discovery becomes preferable later, migration from single-component discovery requires retained cleanup/migration payloads. Stay with parent #64 single-component topics for this issue.

## 13. Confidence Score

Confidence: 8/10.

The discovery builder and package-level tests are straightforward because #84 already provides the MQTT scaffold. The confidence is below 9 because current `schedule.go` has no MQTT wiring or state publishing, while issue #85's HA smoke-test criterion requires populated entities. Confidence would rise if the parent command/runner/schedule integration issue landed first, or if #85 explicitly waived runtime state publication and limited scope to discovery payload generation plus package-level retained publication tests.
