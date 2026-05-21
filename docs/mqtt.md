# MQTT Feed and Home Assistant Discovery

This document contains the detailed MQTT integration guide for SBAM.

## Quick Start

MQTT is opt-in.\
Set `mqtt_enabled=true`.\
If the broker is installed as [Home Assistant MQTT integration](https://www.home-assistant.io/integrations/mqtt/), the other MQTT options can be left empty/default to auto-fill broker URL and credentials automatically from Home Assistant service data when available.\
For external broker or custom configuration, they can be set manually as needed.

Environment variables example:

```bash
export MQTT_ENABLED=true
export MQTT_BROKER=tcp://127.0.0.1:1883
bin/sbam schedule
```

CLI flags example:

```bash
bin/sbam schedule \
  --mqtt_enabled \
  --mqtt_broker tcp://127.0.0.1:1883
```

`config.yaml` example:

```yaml
mqtt_enabled: true
mqtt_broker: "tcp://127.0.0.1:1883"
mqtt_topic_prefix: "sbam"
mqtt_ha_discovery: true
mqtt_ha_discovery_prefix: "homeassistant"
```

## Home Assistant Discovery Behavior

When MQTT is enabled and `mqtt_ha_discovery=true`, SBAM publishes retained Home Assistant discovery payloads under:

- `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config`

SBAM subscribes to `homeassistant/status` and republishes discovery when Home Assistant publishes `online`.

Current discovery set includes:

- 3 Main sensors (`Battery Capacity`, `State of Charge`, and `Forecast Today`)
- 10 Diagnostic sensors (`Charge Percent`, `Charge Window`, `Decision`, `Last Decision`, `Last Update`, `Net Energy`, `Next Run`, `Paused`, `Paused State`, `Reserve Window`)
- 2 Configuration selector (`force_charge_target_pct`, `pause_duration_s`)
- 5 Control buttons (`trigger_now`, `pause`, `resume`, `force_charge`, `set_defaults`)

### Main sensors

These three primary sensors are intended for quick at-a-glance state and are used by the scheduler and UI. All discovery sensors read their value from the state topic (`<prefix>/state`) using JSON templates (for example `{{ value_json.battery_soc_pct }}`).

- **Battery Capacity**: `battery_capacity_wh` — unit: Wh. Measured total battery capacity (watt‑hours).
- **State of Charge**: `battery_soc_pct` — unit: %; device_class: `battery`. Current battery state of charge as a percentage.
- **Forecast Today**: `forecast_today_wh` — unit: Wh. Estimated solar production for the remainder of the day used to plan grid charging.

### Diagnostic sensors

Diagnostics expose extra scheduler and operating details. Most are published on the same state topic and extracted with `value_json.<field>`. Several diagnostics are exposed as binary sensors (payload `"true"` / `"false"`) for easy automation rules.

- **Net Energy**: `pw_net_wh` — unit: Wh. Day net energy (solar − consumption). Positive = export, negative = import.
- **Charge Percent**: `charge_pct` — unit: %. Current charge target percentage (scheduler or force-charge target).
- **Last Decision**: `last_decision` — string. Last high-level scheduler decision (e.g., `force_charge`, `set_defaults`, `pause`).
- **Decision Reason**: `last_decision_reason` — string. Human-readable reason or context for the last decision.
- **Next Run**: `next_run` — timestamp. Show countdown to `pause` expiration or `null`.
- **Paused State**: `paused_state` — mirrors `paused` as a sensor value and can be used in templates.
- **Paused**: `paused` — binary_sensor (`true`/`false`). Whether automatic scheduling is currently paused.
- **Charge Window**: `charge_window_active` — binary_sensor (`true`/`false`). Whether the configured charge window is currently active.
- **Reserve Window**: `batt_reserve_window_active` — binary_sensor (`true`/`false`). Whether the battery reserve window is active.
- **Last Update**: `last_update` — timestamp (`ts`). Time of the last published state update.

Home Assistant discovery uses `value_template` like `{{ value_json.pw_net_wh }}` for numeric sensors and lowercased string templates for binary sensors (for example `{{ value_json.paused | string | lower }}`).

### Configuration selectors

These selector topics store user selection state only so no command is sent until the corresponding button is pressed.\
SBAM command execution still happens only through the control buttons.

When discovery is enabled, Home Assistant receives two numeric selector MQTT `number` entities (mode: box):

- **Force Charge Target**: retained selector on `<prefix>/control/force_charge_target_pct` with range `0..101`, mode `box`, step `1`, unit `%`.\
Force charge selector value `0..100` sends `{"target_pct":<value>,"ignore_max_charge":false}` so the scheduler respects the configured maximum charge limits capped by the configured `maximum_charge`.\
Force-charge selector value `101` is treated as an explicit full-charge override and sends `{"target_pct":100,"ignore_max_charge":true}`. This allows an uncapped full charge that ignores `max_charge` limits using the existing force charge path without needing a separate explicit override command. The scheduler still respects battery capacity and will not overcharge beyond 100% SoC or the physical battery capacity.

- **Pause Duration**: retained selector on `<prefix>/control/pause_duration_s` with range `0..86400` seconds, mode `box`, step `60`.\
Pause selector value sends `{"duration_s":<value>}` to pause for the selected duration in seconds, or indefinitely if set to `0`.

### Control buttons

Each Home Assistant button publishes to `<prefix>/cmd/<name>`, and SBAM publishes
an acknowledgement on `<prefix>/cmd/<name>/ack`.

- `trigger_now`: requests one immediate schedule evaluation run.
- `pause`: reads the `pause_duration_s` selector and sends:
  - `{}` when selector value is `0` (existing indefinite pause behavior)
  - `{"until":"<seconds>s"}` when selector value is `1..86400`
- `resume`: clears pause state and resumes automatic schedule processing.
- `force_charge`: reads the `force_charge_target_pct` selector and sends:
  - `{"target_pct":0}` when selector value is `0` (stop force charge / restore defaults)
  - `{"target_pct":<n>}` when selector value is `1..100` (still capped by `max_charge` and battery capacity)
  - `{"target_pct":100,"ignore_max_charge":true}` when selector value is `101` (explicit uncapped full-charge override)
- `set_defaults`: restores inverter defaults via the configured Modbus write path.

### Command sequencing note (schedule crontab active)

> [!IMPORTANT]
> SBAM serializes incoming commands and scheduled ticks in one runner queue, so
> write operations are not executed concurrently. If the schedule remains active via the configured crontab it can still apply new decisions after a manual command.

For eg. if you want a manual force-charge workflow with minimal crontab scheduler interference you can use pause to suppress scheduled runs between the manual `force_charge` and the subsequent `resume`:

1. Configure `Force Charge Target` selector to the desired percentage (for example `80` or `101` for uncapped full charge).
2. Configure `Pause Duration` selector to a non-zero value (for example `3600` for one hour or `0` for indefinite).
3. Send `force_charge`.
4. Send `pause` to suppress subsequent automatic crontab runs so force charge can run without interference.
5. Send `resume` when the indefinite pause ends or wait for pause expiration.
6. Send `set_defaults` to stop force charge, restore defaults, or re-apply a new manual override as needed.
7. Send `trigger_now` (or wait for the next scheduled tick) to let scheduler logic.

#### Alternatives

- Disable the crontab so SBAM only responds to MQTT commands by setting the `crontab` configuration to `0 0 0 0 0`. This makes MQTT the sole trigger for scheduler actions.
- Execute your manual command in a different time window than the crontab, run `force_charge` outside scheduled charge windows to avoid overlap and reduce the need for `pause`/`resume`.

Do not pause before `force_charge`: `force_charge` is rejected while paused.

## Migration from v1.x

> [!NOTE]
> v2.0.0 remains backward compatible while `mqtt_enabled=false` (default). Existing v1.x users do not need to change configuration unless opting into MQTT.

MQTT configuration keys in standalone mode (CLI flags, environment variables, and `config.yaml`):

- `mqtt_enabled` (`MQTT_ENABLED`, `--mqtt_enabled`)
- `mqtt_broker` (`MQTT_BROKER`, `--mqtt_broker`)
- `mqtt_client_id` (`MQTT_CLIENT_ID`, `--mqtt_client_id`)
- `mqtt_username` (`MQTT_USERNAME`, `--mqtt_username`)
- `mqtt_password` (`MQTT_PASSWORD`, `--mqtt_password`)
- `mqtt_tls_ca_file` (`MQTT_TLS_CA_FILE`, `--mqtt_tls_ca_file`)
- `mqtt_tls_client_cert` (`MQTT_TLS_CLIENT_CERT`, `--mqtt_tls_client_cert`)
- `mqtt_tls_client_cert_key` (`MQTT_TLS_CLIENT_CERT_KEY`, `--mqtt_tls_client_cert_key`)
- `mqtt_tls_insecure_skip` (`MQTT_TLS_INSECURE_SKIP`, `--mqtt_tls_insecure_skip`)
- `mqtt_topic_prefix` (`MQTT_TOPIC_PREFIX`, `--mqtt_topic_prefix`)
- `mqtt_ha_discovery` (`MQTT_HA_DISCOVERY`, `--mqtt_ha_discovery`)
- `mqtt_ha_discovery_prefix` (`MQTT_HA_DISCOVERY_PREFIX`, `--mqtt_ha_discovery_prefix`)

For Home Assistant users, entities are auto-discovered when MQTT and discovery are enabled; no manual YAML is required for those entities.

## Topic Map

Use `mqtt_topic_prefix` to change the default `sbam` prefix.

| Topic | Direction | Retained / QoS | Payload |
| --- | --- | --- | --- |
| `<prefix>/availability` (default `sbam/availability`) | SBAM publishes | retained, qos 1 | `online` or `offline` |
| `<prefix>/state` (default `sbam/state`) | SBAM publishes | retained, qos 1 | `StatePayload` JSON |
| `<prefix>/error` (default `sbam/error`) | SBAM publishes | not retained, qos 1 | `ErrorPayload` JSON |
| `<prefix>/cmd/+` (default `sbam/cmd/+`) | SBAM subscribes | qos 1 | command payload JSON |
| `<prefix>/cmd/<name>/ack` | SBAM publishes | not retained, qos 1 | `AckPayload` JSON |
| `<prefix>/control/+` | Home Assistant publishes selector values | retained, qos 1 | slider state used by discovery button templates |
| `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config` | SBAM publishes | retained, qos 1 | Home Assistant discovery config |

Defaults:

- `mqtt_topic_prefix=sbam`
- `mqtt_ha_discovery_prefix=homeassistant`

## Payload Examples

State payload (`<prefix>/state`):

```json
{
  "battery_soc_pct": 42.5,
  "battery_capacity_wh": 10000,
  "forecast_today_wh": 5100,
  "pw_net_wh": -5900,
  "charge_pct": 80,
  "last_decision": "force_charge",
  "last_decision_reason": "force charge command executed",
  "charge_window_active": true,
  "batt_reserve_window_active": false,
  "paused": false,
  "next_run": null,
  "ts": "2026-05-18T21:00:00Z"
}
```

Error payload (`<prefix>/error`):

```json
{
  "error": "invalid payload: target_pct must be between 0 and 100",
  "source": "force_charge",
  "ts": "2026-05-18T21:01:00Z"
}
```

Accepted ack payload (`<prefix>/cmd/trigger_now/ack`):

```json
{
  "ts": "2026-05-18T21:01:05Z",
  "command": "trigger_now",
  "accepted": true
}
```

Rejected ack payload (`<prefix>/cmd/force_charge/ack`):

```json
{
  "ts": "2026-05-18T21:01:10Z",
  "command": "force_charge",
  "accepted": false,
  "error": "invalid payload: target_pct must be between 0 and 100"
}
```

## Command Examples (`mosquitto_pub`)

```bash
BROKER_HOST=127.0.0.1
BROKER_PORT=1883
PREFIX=sbam

# trigger_now
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/trigger_now" -m '{}'

# pause indefinitely
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/pause" -m '{}'

# pause for one hour (duration format)
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/pause" -m '{"until":"1h"}'

# resume
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/resume" -m '{}'

# force_charge
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/force_charge" -m '{"target_pct":80}'

# force_charge explicit uncapped full-charge override
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/force_charge" -m '{"target_pct":100,"ignore_max_charge":true}'

# force_charge stop/reset (restores defaults)
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/force_charge" -m '{"target_pct":0}'

# set_defaults
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/set_defaults" -m '{}'
```

Command payload constraints:

- Maximum payload size is 4096 bytes.
- `force_charge.target_pct` is required and must be between 0 and 100.
- `force_charge.target_pct=0` stops forced charge and restores defaults through the existing Fronius defaults path.
- `force_charge.target_pct=1..100` remains capped by `max_charge` using live battery max capacity.
- `force_charge.ignore_max_charge=true` is accepted only when `target_pct=100`; it requests an explicit uncapped full-charge write.
- Raw `target_pct=101` is invalid for direct command payloads.
- `pause.until` is optional; when set, it must be a future RFC3339 timestamp or a positive Go duration.