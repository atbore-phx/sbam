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
| `<prefix>/plan` | SBAM publishes after every optimizer run (`scheduler_mode: optimize` only) | retained, QoS 1 | `PlanPayload` JSON, see [Optimizer Plan Topic](#optimizer-plan-topic). Cleared (empty retained message) in the other modes. |
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

### Device Identity

Every discovery payload shares one device identifier (`sbam_<hash>`), which also prefixes each entity `unique_id`. The hash is derived from `fronius_ip` (falling back to `mqtt_client_id`, then the topic prefix), and is **persisted to a `mqtt_device_id` file in the working directory** (`/data` for the Home Assistant add-on) the first time discovery is published. From then on the persisted value always wins.

Without persistence, changing `fronius_ip` (a VLAN move, DHCP renumbering) re-identified the device: Home Assistant kept the old entities as permanently-unavailable ghosts and created a duplicate `_2` set that no automation or dashboard referenced.

- **Manual override**: edit `mqtt_device_id` to any single `sbam_...` token (letters, digits, `_`, `-`) and restart SBAM. Useful to re-adopt a previous identity after an unwanted change.
- **Re-derive**: delete the file and restart; SBAM re-computes from the current configuration and persists the result.
- If the file is unreadable or unwritable, SBAM logs a warning and uses the derived value, so discovery still publishes.

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

### Optimizer Entities (optimize mode)

With `scheduler_mode: optimize`, discovery adds 5 entities and 36 per-hour plan sensors (41 in total) to the sbam device. In every other mode their discovery topics receive an empty retained payload, which removes them from Home Assistant, and `<prefix>/plan` is cleared. Existing entities are unchanged. The new entities set `default_entity_id`, so their entity IDs are predictable.

| Entity | Entity ID | Source | Unit / class | Category |
|--------|-----------|--------|--------------|----------|
| **Optimizer Action** | `sensor.sbam_opt_action` | state `opt_action` | enum: `charge`, `hold`, `standby`, `discharge`, `self_consume` | main |
| **Optimizer Rate** | `sensor.sbam_opt_rate_w` | state `opt_rate_w` | W, `power` | main |
| **Expected Cost** | `sensor.sbam_opt_expected_cost` | state `opt_expected_cost` | currency of the price sensor (e.g. `DKK`), `monetary`; no unit until the currency is known | main |
| **Optimizer Horizon** | `sensor.sbam_opt_horizon_h` | state `opt_horizon_h` | h, `duration` | diagnostic |
| **Optimizer Dry Run** | `binary_sensor.sbam_opt_dry_run` | state `opt_dry_run` | `on` = nothing written to the inverter | diagnostic |
| **Plan +00h** … **Plan +35h** | `sensor.sbam_opt_plan_h00` … `sensor.sbam_opt_plan_h35` | `<prefix>/plan`, `hours[NN].action` | enum, same options as Optimizer Action | diagnostic |

- **Optimizer Action attributes** come from `<prefix>/plan`: `hours`, `expected_cost`, `terminal_value`, `horizon_end`, `reasons`, `currency` and `dry_run`. This is the full plan for dashboards.
- **Plan +NNh** is relative to the current hour: `+00h` is the hour being executed now. Its attributes are the hour's fields from the plan (`start`, `rate_w`, `soc_end_pct`, `buy`, `sell`, `consumption_wh`, `pv_wh`); `start` holds the absolute local time.
- **Plan +NNh availability.** A plan sensor is `unavailable` when its hour is beyond the horizon (normal before tomorrow's prices are published), on every fallback (the plan is empty), and when sbam is offline.
- The existing **Decision Reason** sensor carries the top reason code, and **Last Decision** shows the action, `fallback_defaults` or `paused`. See [reason codes](optimizer.md#reason-codes).

!!! tip "Recorder"
    The plan sensors change every hour and the full plan is already in the Optimizer Action attributes. Exclude them from the recorder:

    ```yaml
    recorder:
      exclude:
        entity_globs:
          - sensor.sbam_opt_plan_*
    ```

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

### Commands in Optimize Mode

With `scheduler_mode: optimize`, the topics and payloads are the same, but the semantics change so that a safety command always works:

| Command | Behaviour in optimize mode |
|---------|----------------------------|
| `trigger_now` | Runs the optimizer immediately (writes unless dry run, paused or overridden). Limited to one accepted run per 60 s; extra commands are acked with `accepted: false, error: "rate limited"`. |
| `pause` | **Writes inverter defaults first** (in dry run only after a `force_charge` command), then pauses. While paused, hourly runs still compute and publish the plan with reason `optimizer_paused`, but write nothing. If the defaults write fails, the pause is still set and the ack carries the error. Unlike the other modes, `pause` writes to the inverter. |
| `resume` | Clears the pause and runs the optimizer immediately. Pause expiry does the same. |
| `set_defaults` | **Always accepted**, also while paused. Writes defaults and ends a manual override. The next hourly run (or `resume`) applies the plan again. |
| `force_charge` | Rejected while paused. Otherwise it writes the force charge **plus an inverter revert timeout** of `duration_s` + 300 s (end of the current hour + 300 s without `duration_s`), and starts a manual override until `duration_s` has passed or the current hour ends. During the override, runs publish the plan with reason `optimizer_manual_override` and write nothing. `target_pct: 0` restores defaults and ends the override. |
| `set_reserve` | Rejected: `accepted: false, error: "not supported in scheduler_mode=optimize"`. The optimizer uses `opt_min_soc_pct`. |

The manual force-charge sequence above is not needed in optimize mode: `force_charge` with `duration_s` keeps the optimizer from overwriting it, for example `{"target_pct":80,"duration_s":1800}`. For an emergency stop, send `pause` and then `set_defaults`.

### Broker Security in Optimize Mode

!!! warning
    In optimize mode, MQTT publish rights on `<prefix>/cmd/#` are effectively battery control rights: a `force_charge` can charge the battery from the grid at any price, and `pause`/`resume` change what the inverter does. Protect the broker:

    - Enable broker authentication; do not allow anonymous clients.
    - Use an ACL so that only sbam and your Home Assistant user can publish to `<prefix>/cmd/#` and `<prefix>/control/#`, and only sbam can publish to `<prefix>/state`, `<prefix>/plan` and `<prefix>/availability`.
    - Restrict subscriptions to `<prefix>/#`: the plan and state reveal when the household is home.

    Example Mosquitto ACL (user names are placeholders):

    ```
    user sbam
    topic readwrite sbam/#
    topic readwrite homeassistant/#

    user homeassistant
    topic readwrite homeassistant/#
    topic read sbam/#
    topic write sbam/cmd/#
    topic readwrite sbam/control/#
    ```

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

**State payload in optimize mode** (after a live run). The `opt_*` fields are present only in optimize mode:

```json
{
  "battery_soc_pct": 52.0,
  "battery_capacity_wh": 10000,
  "forecast_today_wh": 5100,
  "charge_pct": 100,
  "last_decision": "charge",
  "last_decision_reason": "optimizer_charge_ahead_of_peak",
  "charge_window_active": false,
  "batt_reserve_window_active": false,
  "scheduler_mode": "optimize",
  "opt_action": "charge",
  "opt_rate_w": 4500,
  "opt_expected_cost": 39.4,
  "opt_horizon_h": 22,
  "opt_dry_run": false,
  "opt_reasons": ["optimizer_charge_ahead_of_peak"],
  "paused": false,
  "next_run": null,
  "ts": "2026-10-03T00:00:01Z"
}
```

| Field | Type | Meaning |
|-------|------|---------|
| `opt_action` | string | `charge`, `hold`, `standby`, `discharge` or `self_consume` for the current hour. `self_consume` on fallback. |
| `opt_rate_w` | number | Rate in W, ≥ 0; the direction comes from the action. `0` for `hold`, `standby` and `self_consume`. |
| `opt_expected_cost` | number or `null` | Expected cost over the horizon minus the terminal value, in the price sensor's currency. `null` on fallback. |
| `opt_horizon_h` | integer | Number of hours in the plan; `0` on fallback. |
| `opt_dry_run` | boolean | `true` when nothing was written to the inverter. |
| `opt_reasons` | string array | All applicable reason codes in precedence order; the first equals `last_decision_reason`. |

In optimize mode, `last_decision` is the action, `fallback_defaults` or `paused`; `charge_window_active` and `batt_reserve_window_active` are `false`; `charge_pct` is set only for `charge`.

**State payload on fallback**:

```json
{
  "last_decision": "fallback_defaults",
  "last_decision_reason": "optimizer_fallback_missing_prices",
  "scheduler_mode": "optimize",
  "opt_action": "self_consume",
  "opt_rate_w": 0,
  "opt_expected_cost": null,
  "opt_horizon_h": 0,
  "opt_dry_run": false,
  "opt_reasons": ["optimizer_fallback_missing_prices"],
  "paused": false,
  "ts": "2026-10-02T13:00:00Z"
}
```

### Optimizer Plan Topic

`<prefix>/plan` is published retained (QoS 1) after every optimizer run, including dry-run, paused, override and fallback runs. It holds hourly aggregates only, never raw history. Prices are rounded to 4 decimals, energy and rates to whole Wh/W, and SoC to 1 decimal.

```json
{
  "ts": "2026-10-02T12:00:01Z",
  "dry_run": true,
  "currency": "DKK",
  "horizon_h": 3,
  "horizon_end": "2026-10-02T17:00:00+02:00",
  "start_soc_pct": 52,
  "expected_cost": 41.8712,
  "terminal_value": 1.9301,
  "reason": "optimizer_hold_for_later_peak",
  "reasons": ["optimizer_hold_for_later_peak"],
  "hours": [
    {"start": "2026-10-02T14:00:00+02:00", "action": "hold", "rate_w": 0, "soc_end_pct": 57.2, "buy": 1.38, "sell": 0.62, "consumption_wh": 610, "pv_wh": 1240},
    {"start": "2026-10-02T15:00:00+02:00", "action": "hold", "rate_w": 0, "soc_end_pct": 58.7, "buy": 1.45, "sell": 0.66, "consumption_wh": 640, "pv_wh": 820},
    {"start": "2026-10-02T16:00:00+02:00", "action": "discharge", "rate_w": 2000, "soc_end_pct": 34.6, "buy": 3.62, "sell": 1.98, "consumption_wh": 981, "pv_wh": 60}
  ]
}
```

| Field | Meaning |
|-------|---------|
| `ts` | Time of the run (UTC). |
| `dry_run` | `true` when the run wrote nothing. |
| `currency` | Currency of the price sensor; empty if unknown. |
| `horizon_h` / `horizon_end` | Number of hours (up to 36) and the end of the last hour. `horizon_end` is `null` on fallback. |
| `start_soc_pct` | SoC at the start of the run; `null` if it could not be read. |
| `expected_cost` / `terminal_value` | Expected cost over the horizon (after subtracting the terminal value) and the value of the energy left at the end. `null` on fallback. |
| `reason` / `reasons` | Top reason code and all reason codes. |
| `hours[]` | One entry per hour: `start` (local time with offset), `action`, `rate_w`, `soc_end_pct`, `buy`, `sell`, `consumption_wh`, `pv_wh`. Empty on fallback. |

`sbam optimize --output json` prints the same object.

**Dashboard: optimizer plan.** A Markdown card that reads the plan from the Optimizer Action attributes. Check the entity ID of your Decision Reason sensor under **Settings → Entities**, because the existing sensors have no fixed entity ID.

```yaml
type: markdown
title: sbam battery plan
content: |
  {% set p = state_attr('sensor.sbam_opt_action', 'hours') or [] %}
  {% set cur = state_attr('sensor.sbam_opt_action', 'currency') or '' %}
  **Now:** {{ states('sensor.sbam_opt_action') }} {{ states('sensor.sbam_opt_rate_w') }} W -
  `{{ states('sensor.sbam_decision_reason') }}`{% if is_state('binary_sensor.sbam_opt_dry_run','on') %} - *dry run*{% endif %}
  **Expected cost:** {{ states('sensor.sbam_opt_expected_cost') }} {{ cur }} over {{ p | length }} h

  | Time | Action | Rate W | SoC % | Buy | Sell | Load Wh | PV Wh |
  |---|---|--:|--:|--:|--:|--:|--:|
  {% for h in p -%}
  | {{ as_timestamp(h.start) | timestamp_custom('%a %H:%M') }} | {{ h.action }} | {{ h.rate_w | int }} | {{ h.soc_end_pct | round(0) }} | {{ '%.2f' % h.buy }} | {{ '%.2f' % h.sell }} | {{ h.consumption_wh | int }} | {{ h.pv_wh | int }} |
  {% endfor %}
```

Next hours at a glance:

```yaml
type: entities
title: sbam next 6 hours
entities:
  - entity: sensor.sbam_opt_plan_h00
    secondary_info: attribute
    attribute: start
  - sensor.sbam_opt_plan_h01
  - sensor.sbam_opt_plan_h02
  - sensor.sbam_opt_plan_h03
  - sensor.sbam_opt_plan_h04
  - sensor.sbam_opt_plan_h05
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
- `force_charge.duration_s` is optional, `0..86400` seconds. In optimize mode it sets the manual override length and the revert timeout.
- Raw `target_pct=101` is invalid for direct command payloads.
- `pause.until` is optional; when set, it must be a future RFC3339 timestamp or a positive Go duration.

## Migration from v1.x

!!! note
    v2.0.0 remains backward compatible while `mqtt_enabled=false` (default). Existing v1.x users do not need to change configuration unless opting into MQTT.

For Home Assistant users, entities are auto-discovered when MQTT and discovery are enabled; no manual YAML is required.
