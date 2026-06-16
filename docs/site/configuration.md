# Configuration

All configuration options for sbam. Options are resolved with the following precedence: **CLI flag > environment variable > config.yaml > built-in default**.

Where CLI and HA add-on forms differ (different defaults, different validation, or different availability), the difference is noted inline.

---

## Core API

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `url` | Solcast forecast site URL. Multiple URLs supported (max 2), comma-separated. | `--url` | `URL` | `url` | — |
| `apikey` | Solcast API key. | `--apikey` | `APIKEY` | `apikey` | — |

## Inverter Connection

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `fronius_ip` | Fronius inverter LAN IP address. | `--fronius_ip` | `FRONIUS_IP` | `fronius_ip` | — |

## Charge Windows

### Legacy single window

These options define a single charge window. When `windows` is configured, `start_hr`, `end_hr`, and `max_charge` must not be specified.

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `start_hr` | Start time of the advantageous grid rate (HH:MM). Cross-midnight ranges supported. | `--start_hr` | `START_HR` | `start_hr` | `00:00` |
| `end_hr` | End time of the advantageous grid rate (HH:MM). Cross-midnight ranges supported. | `--end_hr` | `END_HR` | `end_hr` | `00:55` (CLI) / `""` (HA — empty treated as 06:00 by run.sh) |
| `max_charge` | Maximum grid charging power in watts. | `--max_charge` | `MAX_CHARGE` | `max_charge` | `3500` |

!!! note "HA add-on end_hr default"
    In the HA add-on, `end_hr` defaults to empty string. The `run.sh` launcher interprets an empty `end_hr` as `06:00`. The CLI binary default is `00:55`.

### Multi-window (v2.1.0)

The `windows` option replaces the single `start_hr`/`end_hr`/`max_charge` triple with an ordered list of charge windows. List order defines the daily sequence: the first entry starts first, the last entry ends last. Cross-midnight windows are supported.

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `windows` | Ordered list of charge windows in YAML format. | `--windows` | `WINDOWS` | `windows` | `[]` |

Each window accepts these fields:

| Field | Required | Description |
|-------|----------|-------------|
| `name` | No | Human-readable label shown in MQTT and logs. Auto-generated if empty. |
| `start` | Yes | Window start time (HH:MM). |
| `end` | Yes | Window end time (HH:MM). Cross-midnight supported. |
| `max_charge` | Yes | Maximum grid charging power in watts for this window. |
| `forecast_horizon` | No | Per-window forecast horizon override. Defaults to top-level `forecast_horizon`. |
| `consumption_horizon` | No | Per-window consumption horizon override. Defaults to top-level `consumption_horizon`. |
| `tick_minutes` | No | Evaluation interval in minutes within this window. |
| `set_defaults` | No | Whether to reset inverter to defaults when this window ends. |
| `before_end_defaults_minutes` | No | Minutes before window end to trigger defaults. |

**CLI example:**

```bash
sbam schedule --windows '[{name: night, start: "02:00", end: "06:00", max_charge: 3500, forecast_horizon: tomorrow}, {name: midday, start: "12:00", end: "15:00", max_charge: 2000}]'
```

**YAML example (config.yaml or HA add-on config):**

```yaml
windows:
  - name: "night"
    start: "02:00"
    end: "06:00"
    max_charge: 3500
    forecast_horizon: "tomorrow"
  - name: "midday"
    start: "12:00"
    end: "15:00"
    max_charge: 2000
```

Mixing `windows` with legacy `start_hr`/`end_hr`/`max_charge` keys is rejected.

## Battery Reserve

The battery reserve defines a minimum charge level to maintain during the reserve window. The scheduler will not discharge below this threshold.

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `pw_batt_reserve` | Minimum battery capacity to maintain in watt-hours. | `--pw_batt_reserve` | `PW_BATT_RESERVE` | `pw_batt_reserve` | `4000` |
| `batt_reserve_start_hr` | Start time for reserve window (HH:MM). Empty uses `start_hr`. | `--batt_reserve_start_hr` | `BATT_RESERVE_START_HR` | `batt_reserve_start_hr` | `start_hr` value |
| `batt_reserve_end_hr` | End time for reserve window (HH:MM). Empty uses `end_hr`. | `--batt_reserve_end_hr` | `BATT_RESERVE_END_HR` | `batt_reserve_end_hr` | `end_hr` value |

## Power and Thresholds

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `pw_consumption` | Daily electrical consumption in watt-hours. | `--pw_consumption` | `PW_CONSUMPTION` | `pw_consumption` | `11000` |
| `pw_lwt` | Hysteresis lower threshold offset in watt-hours to stop charging. | `--pw_lwt` | `PW_LWT` | `pw_lwt` | `0` |
| `pw_upt` | Hysteresis upper threshold offset in watt-hours to start charging. | `--pw_upt` | `PW_UPT` | `pw_upt` | `0` |

## Scheduling

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `scheduler_mode` | Scheduling mode. `crontab` — legacy cron-based scheduling. `windows` — window-based scheduling (recommended). | `--scheduler_mode` | `SCHEDULER_MODE` | `scheduler_mode` | `crontab` |
| `crontab` | Cron expression for recurring execution. Deprecated — use `scheduler_mode: windows` instead. Set to `0 0 0 0 0` to disable scheduled execution (run once at startup, wait for MQTT commands if MQTT is enabled). | `--crontab` | `CRONTAB` | `crontab` | `0 0 0 0 0` (CLI) / `00 00-05 * * *` (HA) |

!!! warning "crontab deprecation"
    `crontab` is deprecated and will be removed in v3.0.0. Migrate to `scheduler_mode: windows` before then.

!!! note "HA add-on crontab default"
    The HA add-on default is `00 00-05 * * *` (runs every minute from midnight to 5 AM). The CLI binary default is `0 0 0 0 0` (runs once, no repetition).

## Forecast and Consumption Horizons (v2.1.0)

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `forecast_horizon` | Which forecast window to use. See [Forecast horizon modes](#forecast-horizon-modes). | `--forecast_horizon` | `FORECAST_HORIZON` | `forecast_horizon` | `default` |
| `consumption_horizon` | How daily consumption is applied. See [Consumption horizon modes](#consumption-horizon-modes). | `--consumption_horizon` | `CONSUMPTION_HORIZON` | `consumption_horizon` | `full_day` |

### Forecast horizon modes

| Mode | Behavior |
|------|----------|
| `default` | Current behavior: today before 12:00, tomorrow from 12:00 onward. |
| `next_solar_day` | Same as `default`. Targets the upcoming solar day. |
| `remaining_today` | Only forecast intervals from now through end of local day. |
| `today` | Full local calendar day. |
| `tomorrow` | Full next local calendar day. |
| `off` | Skip forecast retrieval entirely. Only reserve charging is active (no Solcast API calls). |

### Consumption horizon modes

| Mode | Behavior |
|------|----------|
| `full_day` | Use `pw_consumption` as-is. |
| `remaining_today` | Scale `pw_consumption` proportionally to the fraction of the local day remaining. |

## Forecast Caching

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `cache_forecast` | Enable forecast caching to reduce API calls. | `--cache_forecast` | `CACHE_FORECAST` | `cache_forecast` | `false` |
| `cache_time` | Forecast cache TTL in seconds. | `--cache_time` | `CACHE_TIME` | `cache_time` | `7200` |
| `cache_file_prefix` | Forecast cache file name prefix. | `--cache_file_prefix` | `CACHE_FILE_PREFIX` | `cache_file_prefix` | `cached_forecast` |

## MQTT

See the [MQTT Guide](mqtt.md) for detailed setup, topic mapping, payload schemas, and command examples.

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `mqtt_enabled` | Enable MQTT publishing and command subscription. | `--mqtt_enabled` | `MQTT_ENABLED` | `mqtt_enabled` | `false` |
| `mqtt_broker` | MQTT broker URL (e.g., `tcp://broker:1883`). In HA add-on, auto-fills from Home Assistant service data if empty. | `--mqtt_broker` | `MQTT_BROKER` | `mqtt_broker` | — |
| `mqtt_client_id` | MQTT client identifier. Auto-generated if empty. | `--mqtt_client_id` | `MQTT_CLIENT_ID` | `mqtt_client_id` | random |
| `mqtt_username` | MQTT username. In HA add-on, auto-fills from Home Assistant service data if empty. | `--mqtt_username` | `MQTT_USERNAME` | `mqtt_username` | — |
| `mqtt_password` | MQTT password. In HA add-on, auto-fills from Home Assistant service data if empty. | `--mqtt_password` | `MQTT_PASSWORD` | `mqtt_password` | — |
| `mqtt_topic_prefix` | Prefix for sbam state, availability, and command topics. | `--mqtt_topic_prefix` | `MQTT_TOPIC_PREFIX` | `mqtt_topic_prefix` | `sbam` |
| `mqtt_ha_discovery` | Enable Home Assistant MQTT discovery (publishes retained discovery configs). | `--mqtt_ha_discovery` | `MQTT_HA_DISCOVERY` | `mqtt_ha_discovery` | `true` |
| `mqtt_ha_discovery_prefix` | Home Assistant discovery root topic prefix. | `--mqtt_ha_discovery_prefix` | `MQTT_HA_DISCOVERY_PREFIX` | `mqtt_ha_discovery_prefix` | `homeassistant` |
| `mqtt_tls_ca_file` | MQTT TLS CA certificate file path. CLI/standalone only. | `--mqtt_tls_ca_file` | `MQTT_TLS_CA_FILE` | — | — |
| `mqtt_tls_client_cert` | MQTT TLS client certificate file path. CLI/standalone only. | `--mqtt_tls_client_cert` | `MQTT_TLS_CLIENT_CERT` | — | — |
| `mqtt_tls_client_cert_key` | MQTT TLS client key file path. CLI/standalone only. | `--mqtt_tls_client_cert_key` | `MQTT_TLS_CLIENT_CERT_KEY` | — | — |
| `mqtt_tls_insecure_skip` | Skip MQTT TLS certificate verification (insecure). CLI/standalone only. | `--mqtt_tls_insecure_skip` | `MQTT_TLS_INSECURE_SKIP` | — | `false` |

## Operational

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `defaults` | At end of crontab cycle, reconfigure Fronius inverter to defaults. | `--defaults` | `DEFAULTS` | `defaults` | `true` |
| `reset` | At add-on startup, reconfigure Fronius inverter to defaults. | — | — | `reset` | `false` |
| `debug` | Increase log level to debug. | — | `DEBUG` | `debug` | `false` |
| `log_type` | Logger output mode: `console` (human-friendly) or `json` (machine-readable). | — | `LOG_TYPE` | `log_type` | `console` |
