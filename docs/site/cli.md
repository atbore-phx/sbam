# CLI Reference

sbam is a command-line application built with Cobra. Commands and flags follow standard CLI conventions. Configuration is resolved with the precedence: **CLI flag > environment variable > config.yaml > built-in default**.

## Global Flags

| Flag | Description |
|------|-------------|
| `-h, --help` | Help for sbam |
| `-v, --version` | Version information |

## Commands

### `schedule`

The main intelligent charging workflow. Checks the forecast and battery residual capacity and decides whether to charge during the configured time windows.

```bash
sbam schedule [flags]
```

| Flag | Env var | Description | Default |
|------|---------|-------------|---------|
| `-k, --apikey` | `APIKEY` | Solcast API key | — |
| `-u, --url` | `URL` | Forecast URL(s), comma-separated | — |
| `-f, --cache_file_prefix` | `CACHE_FILE_PREFIX` | Cache file name prefix | `cached_forecast` |
| `-n, --cache_forecast` | `CACHE_FORECAST` | Enable forecast caching | `false` |
| `-l, --cache_time` | `CACHE_TIME` | Cache TTL in seconds | `7200` |
| `-t, --crontab` | `CRONTAB` | Cron expression (deprecated) | `0 0 0 0 0` (crontab disabled)|
| `--scheduler_mode` | `SCHEDULER_MODE` | Scheduling mode (`crontab` or `windows`) | `crontab` |
| `-H, --fronius_ip` | `FRONIUS_IP` | Fronius inverter LAN IP | — |
| `-s, --start_hr` | `START_HR` | Charge window start (HH:MM) | `00:00` |
| `-e, --end_hr` | `END_HR` | Charge window end (HH:MM) | `06:00` |
| `-m, --max_charge` | `MAX_CHARGE` | Max grid charging power (W) | `3500` |
| `-S, --batt_reserve_start_hr` | `BATT_RESERVE_START_HR` | Reserve window start (HH:MM) | `start_hr` value |
| `-E, --batt_reserve_end_hr` | `BATT_RESERVE_END_HR` | Reserve window end (HH:MM) | `end_hr` value |
| `-c, --pw_consumption` | `PW_CONSUMPTION` | Daily consumption (Wh) | `11000` |
| `-r, --pw_batt_reserve` | `PW_BATT_RESERVE` | Battery reserve (Wh) | `4000` |
| `-L, --pw_lwt` | `PW_LWT` | Lower threshold offset (Wh) | `0` |
| `-U, --pw_upt` | `PW_UPT` | Upper threshold offset (Wh) | `0` |
| `-d, --defaults` | `DEFAULTS` | Reconfigure inverter to defaults at cycle end | `true` |
| `--forecast_horizon` | `FORECAST_HORIZON` | Forecast window mode | `default` |
| `--consumption_horizon` | `CONSUMPTION_HORIZON` | Consumption model | `full_day` |
| `--windows` | `WINDOWS` | Charge windows in YAML format | — |
| `--mqtt_enabled` | `MQTT_ENABLED` | Enable MQTT | `false` |
| `--mqtt_broker` | `MQTT_BROKER` | MQTT broker URL | — |
| `--mqtt_client_id` | `MQTT_CLIENT_ID` | MQTT client ID | random |
| `--mqtt_username` | `MQTT_USERNAME` | MQTT username | — |
| `--mqtt_password` | `MQTT_PASSWORD` | MQTT password | — |
| `--mqtt_topic_prefix` | `MQTT_TOPIC_PREFIX` | MQTT topic prefix | `sbam` |
| `--mqtt_ha_discovery` | `MQTT_HA_DISCOVERY` | Enable HA MQTT discovery | `true` |
| `--mqtt_ha_discovery_prefix` | `MQTT_HA_DISCOVERY_PREFIX` | HA discovery prefix | `homeassistant` |
| `--mqtt_tls_ca_file` | `MQTT_TLS_CA_FILE` | MQTT TLS CA cert file | — |
| `--mqtt_tls_client_cert` | `MQTT_TLS_CLIENT_CERT` | MQTT TLS client cert file | — |
| `--mqtt_tls_client_cert_key` | `MQTT_TLS_CLIENT_CERT_KEY` | MQTT TLS client key file | — |
| `--mqtt_tls_insecure_skip` | `MQTT_TLS_INSECURE_SKIP` | Skip TLS verification | `false` |

### `estimate`

Print the solar forecast and battery storage power.

```bash
sbam estimate [flags]
```

| Flag | Env var | Description | Default |
|------|---------|-------------|---------|
| `-k, --apikey` | `APIKEY` | Solcast API key | — |
| `-u, --url` | `URL` | Forecast URL(s), comma-separated | — |
| `-f, --cache_file_prefix` | `CACHE_FILE_PREFIX` | Cache file name prefix | `cached_forecast` |
| `-n, --cache_forecast` | `CACHE_FORECAST` | Enable forecast caching | `false` |
| `-l, --cache_time` | `CACHE_TIME` | Cache TTL in seconds | `7200` |
| `--forecast_horizon` | `FORECAST_HORIZON` | Forecast horizon mode | `default` |
| `-H, --fronius_ip` | `FRONIUS_IP` | Fronius inverter LAN IP | — |

### `configure`

Connect to the Fronius inverter via Modbus and set charging parameters.

```bash
sbam configure [flags]
```

| Flag | Description |
|------|-------------|
| `-d, --defaults` | Set DEFAULTS |
| `-f, --force_charge` | Set FORCE_CHARGE |
| `-H, --fronius_ip` | Fronius inverter LAN IP |
| `-p, --power` | Percent of nominal power |

## Debug Logs

Set the `DEBUG` environment variable to `true` to increase log level:

```bash
export DEBUG=true
sbam schedule
# 2026-04-29T11:15:19+02:00  DEBUG  Debug Logs activated: true
```

## Log Format

Set `LOG_TYPE` to `console` (human-friendly) or `json` (machine-readable):

```bash
export LOG_TYPE=json
export DEBUG=true
sbam schedule
# {"level":"debug","ts":"2026-04-29T11:16:24+02:00","msg":"Debug Logs activated: true"}
```

## Charge Windows

Charge windows can be defined as a single legacy window (`--start_hr`, `--end_hr`, `--max_charge`) or as a multi-window list (`--windows` or the `windows:` key in `config.yaml`). The two forms are mutually exclusive.

**Legacy single window:**

Time windows can span midnight:

```bash
--start_hr 22:00 --end_hr 06:00
```

With optional reserve window:

```bash
--batt_reserve_start_hr 23:00 --batt_reserve_end_hr 05:00
```

**Multi-window list (YAML):**

```bash
--windows '[{name: night, start: "02:00", end: "06:00", max_charge: 3500, forecast_horizon: tomorrow}, {name: midday, start: "12:00", end: "15:00", max_charge: 2000}]'
```

Equal start/end values are invalid. Overlapping windows are rejected at startup.

Each window can also include a `weekdays` field for day-of-week filtering and
the `weekday_feature` flag controls the feature globally. See the
[Weekday Filtering](weekdays.md) guide for the format, start-day model, and
worked examples.
