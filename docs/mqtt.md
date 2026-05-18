# MQTT Feed and Home Assistant Discovery

This document contains the detailed MQTT integration guide for sbam.

## Quick Start

MQTT is opt-in. Set `mqtt_enabled=true` and provide a broker URL.

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

## Topic Map

Use `mqtt_topic_prefix` to change the default `sbam` prefix.

| Topic | Direction | Retained / QoS | Payload |
| --- | --- | --- | --- |
| `<prefix>/availability` (default `sbam/availability`) | sbam publishes | retained, qos 1 | `online` or `offline` |
| `<prefix>/state` (default `sbam/state`) | sbam publishes | retained, qos 1 | `StatePayload` JSON |
| `<prefix>/error` (default `sbam/error`) | sbam publishes | not retained, qos 1 | `ErrorPayload` JSON |
| `<prefix>/cmd/+` (default `sbam/cmd/+`) | sbam subscribes | qos 1 | command payload JSON |
| `<prefix>/cmd/<name>/ack` | sbam publishes | not retained, qos 1 | `AckPayload` JSON |
| `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config` | sbam publishes | retained, qos 1 | Home Assistant discovery config |

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
  "error": "invalid payload: target_pct must be between 1 and 100",
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
  "error": "invalid payload: target_pct must be between 1 and 100"
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

# set_defaults
mosquitto_pub -h "$BROKER_HOST" -p "$BROKER_PORT" -q 1 -t "$PREFIX/cmd/set_defaults" -m '{}'
```

Command payload constraints:

- Maximum payload size is 4096 bytes.
- `force_charge.target_pct` is required and must be between 1 and 100.
- `pause.until` is optional; when set, it must be a future RFC3339 timestamp or a positive Go duration.

## Home Assistant Discovery Behavior

When MQTT is enabled and `mqtt_ha_discovery=true`, sbam publishes retained Home Assistant discovery payloads under:

- `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config`

Current discovery set includes:

- 10 sensors
- 3 binary sensors
- 5 buttons (`trigger_now`, `pause`, `resume`, `force_charge`, `set_defaults`)

### Discovery button actions

Each Home Assistant button publishes to `<prefix>/cmd/<name>`, and sbam publishes
an acknowledgement on `<prefix>/cmd/<name>/ack`.

- `trigger_now`: requests one immediate schedule evaluation run.
- `pause`: pauses indefinitely the automatic schedule processing.
- `resume`: clears pause state and resumes automatic schedule processing.
- `force_charge`: requests a manual force charge at 100%. The button sends `{"target_pct":100}`.
- `set_defaults`: restores inverter defaults via the configured Modbus write path.

### Command sequencing note (schedule active)

> [!IMPORTANT]
> sbam serializes incoming commands and scheduled ticks in one runner queue, so
> write operations are not executed concurrently. If the schedule remains active,
> later ticks can still apply new decisions after a manual command.

For a manual force-charge workflow with minimal scheduler interference:

1. Send `force_charge` first.
2. Wait for an accepted ack on `<prefix>/cmd/force_charge/ack`.
3. Send `pause` to suppress subsequent automatic runs.
4. Send `resume` when the manual window ends.
5. Send `trigger_now` (or wait for the next scheduled tick) to let scheduler logic re-evaluate state.

Do not pause before `force_charge`: `force_charge` is rejected while paused.

Device grouping uses a stable `sbam_` identifier derived from Fronius IP, client ID, or topic prefix.

sbam subscribes to `homeassistant/status` and republishes discovery when Home Assistant publishes `online`.

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
