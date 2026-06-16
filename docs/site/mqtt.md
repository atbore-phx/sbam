# MQTT Guide

This document covers MQTT integration for sbam: setup, topic mapping, payload schemas, Home Assistant discovery, command examples, and migration from v1.x.

## Quick Start

MQTT is opt-in. Set `mqtt_enabled=true`.

If the broker is installed as a [Home Assistant MQTT integration](https://www.home-assistant.io/integrations/mqtt/), the other MQTT options can be left empty/default to auto-fill broker URL and credentials from Home Assistant service data.

**Environment variables:**

```bash
export MQTT_ENABLED=true
export MQTT_BROKER=tcp://127.0.0.1:1883
bin/sbam schedule
```

**CLI flags:**

```bash
bin/sbam schedule \
  --mqtt_enabled \
  --mqtt_broker tcp://127.0.0.1:1883
```

**config.yaml:**

```yaml
mqtt_enabled: true
mqtt_broker: "tcp://127.0.0.1:1883"
mqtt_topic_prefix: "sbam"
mqtt_ha_discovery: true
mqtt_ha_discovery_prefix: "homeassistant"
```

## Topic Map

Use `mqtt_topic_prefix` to change the default `sbam` prefix.

| Topic | Direction | Retained / QoS | Payload |
|-------|-----------|----------------|---------|
| `<prefix>/availability` | SBAM publishes | retained, QoS 1 | `online` or `offline` |
| `<prefix>/state` | SBAM publishes | retained, QoS 1 | `StatePayload` JSON |
| `<prefix>/error` | SBAM publishes | not retained, QoS 1 | `ErrorPayload` JSON |
| `<prefix>/cmd/+` | SBAM subscribes | QoS 1 | command payload JSON |
| `<prefix>/cmd/<name>/ack` | SBAM publishes | not retained, QoS 1 | `AckPayload` JSON |
| `<prefix>/control/+` | HA publishes selector values | retained, QoS 1 | selector state used by discovery button templates |
| `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config` | SBAM publishes | retained, QoS 1 | HA discovery config |

Defaults:

- `mqtt_topic_prefix=sbam`
- `mqtt_ha_discovery_prefix=homeassistant`

## Home Assistant Discovery Behavior

When MQTT is enabled and `mqtt_ha_discovery=true`, SBAM publishes retained Home Assistant discovery payloads under:

- `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config`

SBAM subscribes to `homeassistant/status` and republishes discovery when Home Assistant publishes `online`.

### Main Sensors

These three primary sensors are intended for quick at-a-glance state and are used by the scheduler and UI. All discovery sensors read their value from the state topic (`<prefix>/state`) using JSON templates (e.g., `{{ value_json.battery_soc_pct }}`).

- **Battery Capacity**: `battery_capacity_wh` — unit: Wh. Measured total battery capacity.
- **State of Charge**: `battery_soc_pct` — unit: %; device_class: `battery`. Current battery state of charge.
- **Forecast Today**: `forecast_today_wh` — unit: Wh. Estimated solar production used to plan grid charging.

### Diagnostic Sensors

Diagnostics expose extra scheduler and operating details. Several diagnostics use binary sensors (payload `true` / `false`) for easy automation rules.

- **Net Energy**: `pw_net_wh` — unit: Wh. Day net energy (solar − consumption). Positive = export, negative = import.
- **Charge Percent**: `charge_pct` — unit: %. Current charge target percentage.
- **Last Decision**: `last_decision` — string. Last high-level scheduler decision.
- **Decision Reason**: `last_decision_reason` — string. Human-readable context for the last decision.
- **Next Run**: `next_run` — timestamp. Countdown to `pause` expiration or `null`.
- **Paused State**: `paused_state` — mirrors `paused` as a sensor value.
- **Paused**: `paused` — binary_sensor (`true`/`false`). Whether automatic scheduling is paused.
- **Charge Window**: `charge_window_active` — binary_sensor (`true`/`false`). Whether the charge window is active.
- **Reserve Window**: `batt_reserve_window_active` — binary_sensor (`true`/`false`). Whether the battery reserve window is active.
- **Last Update**: `last_update` — timestamp (`ts`). Time of the last published state update.

### Configuration Selectors

When discovery is enabled, Home Assistant receives two numeric selector MQTT `number` entities (mode: box):

- **Force Charge Target**: retained selector on `<prefix>/control/force_charge_target_pct` with range `0..101`, step `1`, unit `%`.
    - Values `0..100`: `{"target_pct":<value>,"ignore_max_charge":false}` — scheduler respects configured maximum charge limits.
    - Value `101`: treated as an explicit full-charge override: `{"target_pct":100,"ignore_max_charge":true}` — ignores `max_charge` limits. Scheduler still respects battery capacity and won't overcharge.

- **Pause Duration**: retained selector on `<prefix>/control/pause_duration_s` with range `0..86400` seconds, step `60`.
    - Value `0`: pause indefinitely.
    - Values `1..86400`: pause for the selected duration in seconds.

### Control Buttons

Each Home Assistant button publishes to `<prefix>/cmd/<name>`, and SBAM publishes an acknowledgement on `<prefix>/cmd/<name>/ack`.

- `trigger_now`: requests one immediate schedule evaluation run.
- `pause`: reads the `pause_duration_s` selector and sends:
    - `{}` when selector value is `0` (indefinite pause).
    - `{"until":"<seconds>s"}` when selector value is `1..86400`.
- `resume`: clears pause state and resumes automatic schedule processing.
- `force_charge`: reads the `force_charge_target_pct` selector and sends:
    - `{"target_pct":0}` when selector value is `0` (stop force charge / restore defaults).
    - `{"target_pct":<n>}` when selector value is `1..100` (capped by `max_charge`).
    - `{"target_pct":100,"ignore_max_charge":true}` when selector value is `101` (uncapped full-charge override).
- `set_defaults`: restores inverter defaults via the configured Modbus write path.

### Command Sequencing

!!! important
    SBAM serializes incoming commands and scheduled ticks in one runner queue, so write operations are not executed concurrently. If the schedule remains active via the configured crontab, it can still apply new decisions after a manual command.

For a manual force-charge workflow with minimal crontab interference, use `pause` to suppress scheduled runs between the manual `force_charge` and the subsequent `resume`:

1. Configure **Force Charge Target** selector to the desired percentage (e.g., `80` or `101` for uncapped full charge).
2. Configure **Pause Duration** selector to a non-zero value (e.g., `3600` for one hour or `0` for indefinite).
3. Send `force_charge`.
4. Send `pause` to suppress subsequent automatic crontab runs.
5. Send `resume` when the indefinite pause ends or wait for pause expiration.
6. Send `set_defaults` to stop force charge and restore defaults.
7. Send `trigger_now` (or wait for the next scheduled tick) to let scheduler logic resume.

Do not pause before `force_charge`: `force_charge` is rejected while paused.

#### Alternatives

- Set `crontab` to `0 0 0 0 0` so SBAM only responds to MQTT commands.
- Execute your manual command in a different time window than the crontab.

## Payload Examples

**State payload** (`<prefix>/state`):

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

**Error payload** (`<prefix>/error`):

```json
{
  "error": "invalid payload: target_pct must be between 0 and 100",
  "source": "force_charge",
  "ts": "2026-05-18T21:01:00Z"
}
```

**Accepted ack** (`<prefix>/cmd/trigger_now/ack`):

```json
{
  "ts": "2026-05-18T21:01:05Z",
  "command": "trigger_now",
  "accepted": true
}
```

**Rejected ack** (`<prefix>/cmd/force_charge/ack`):

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

# pause for one hour
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/pause" -m '{"until":"1h"}'

# resume
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/resume" -m '{}'

# force_charge
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/force_charge" -m '{"target_pct":80}'

# force_charge uncapped full-charge override
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/force_charge" -m '{"target_pct":100,"ignore_max_charge":true}'

# force_charge stop/reset
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/force_charge" -m '{"target_pct":0}'

# set_defaults
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/set_defaults" -m '{}'
```

## Command Payload Constraints

- Maximum payload size is 4096 bytes.
- `force_charge.target_pct` is required and must be between 0 and 100.
- `force_charge.target_pct=0` stops forced charge and restores defaults.
- `force_charge.target_pct=1..100` remains capped by `max_charge` using live battery max capacity.
- `force_charge.ignore_max_charge=true` is accepted only when `target_pct=100`.
- Raw `target_pct=101` is invalid for direct command payloads.
- `pause.until` is optional; when set, it must be a future RFC3339 timestamp or a positive Go duration.

## Migration from v1.x

!!! note
    v2.0.0 remains backward compatible while `mqtt_enabled=false` (default). Existing v1.x users do not need to change configuration unless opting into MQTT.

For Home Assistant users, entities are auto-discovered when MQTT and discovery are enabled; no manual YAML is required.
