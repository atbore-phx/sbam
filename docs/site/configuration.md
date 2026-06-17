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

These options define a single charge window. When `windows` is configured, `start_hr`, `end_hr`, and `max_charge` will be ignored.

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `start_hr` | Start time of the advantageous grid rate (HH:MM). Cross-midnight ranges supported. | `--start_hr` | `START_HR` | `start_hr` | `00:00` |
| `end_hr` | End time of the advantageous grid rate (HH:MM). Cross-midnight ranges supported. | `--end_hr` | `END_HR` | `end_hr` | `06:00` |
| `max_charge` | Maximum grid charging power in watts. | `--max_charge` | `MAX_CHARGE` | `max_charge` | `3500` |

### Multi-window

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
| `forecast_horizon` | No | Per-window forecast horizon override. See [Forecast horizon modes](#forecast-horizon-modes). |
| `consumption_horizon` | No | Per-window consumption horizon override. See [Consumption horizon modes](#consumption-horizon-modes). |
| `tick_minutes` | No | Evaluation interval in minutes within this window. |
| `set_defaults` | No | Whether to reset inverter to defaults when this window ends. |
| `before_end_defaults_minutes` | No | Minutes before window end to trigger defaults. |
| `weekdays` | No | Day-of-week filter for the window. See [Weekday Filtering](weekdays.md). |

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

`crontab + windows`: cron drives ticks, but each tick uses the window parameters from the windows: list. This is a transitional mode: cron cadence, multi-window decisions.

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `scheduler_mode` | Scheduling mode. `crontab` — the legacy cron engine. Uses the global `crontab` field and the top-level `start_hr`/`end_hr` single window. Fully backward-compatible with existing configs. `windows` — the new internal ticker. No cron needed. Ticks fire at per-window intervals (tick_minutes), window transitions are detected at exact boundary times, and a tick fires immediately on startup. This is the recommended mode.. | `--scheduler_mode` | `SCHEDULER_MODE` | `scheduler_mode` | `crontab` |
| `crontab` | Cron expression for recurring execution. Deprecated use `scheduler_mode: windows` instead.| `--crontab` | `CRONTAB` | `crontab` | `0 0 0 0 0` (CLI) / `00 00-05 * * *` (HA) |
| `weekday_feature` | Enable weekday filtering on charge windows. Set to `false` to disable all weekday logic. See [Weekday Filtering](weekdays.md). CLI/standalone only. | `--weekday_feature` | `WEEKDAY_FEATURE` | — | `true` |

!!! warning "crontab deprecation"
    `crontab` is deprecated and will be removed in v3.0.0. Migrate to `scheduler_mode: windows` before then.

!!! info "crontab vs. windows"
    - if `scheduler_mode: windows` is used, the `crontab` field is ignored.
    - If `scheduler_mode: crontab` and `windows` is empty, the legacy single window (`start_hr`, `end_hr`, `max_charge`) is used.
    - If `scheduler_mode: crontab` and `windows` is non-empty, the windows list is used for decisions, but ticks are still driven by the cron schedule.

!!! note "HA add-on crontab default"
    The HA add-on default is `00 00-05 * * *` (runs every minute from midnight to 5 AM). The CLI binary default is `0 0 0 0 0` (crontab disabled).

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

!!! info "HA MQTT auto-fill"
    When running as a Home Assistant add-on, the MQTT broker, username, and password are automatically filled from the Home Assistant service data if left empty. This allows sbam to connect to the same broker as Home Assistant without requiring manual configuration.

## Operational

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `defaults` | At end of crontab cycle, reconfigure Fronius inverter to defaults. | `--defaults` | `DEFAULTS` | `defaults` | `true` |
| `reset` | At add-on startup, reconfigure Fronius inverter to defaults. | — | — | `reset` | `false` |
| `debug` | Increase log level to debug. | — | `DEBUG` | `debug` | `false` |
| `log_type` | Logger output mode: `console` (human-friendly) or `json` (machine-readable). | — | `LOG_TYPE` | `log_type` | `console` |
