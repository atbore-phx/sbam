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
| `--scheduler_mode` | `SCHEDULER_MODE` | Scheduling mode (`crontab`, `windows` or `optimize`) | `crontab` |
| `-H, --fronius_ip` | `FRONIUS_IP` | Fronius inverter LAN IP | — |
| `--sunspec_type` | `SUNSPEC_TYPE` | Inverter Modbus SunSpec model type (`int_sf`, `float`, `auto`) | `int_sf` |
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
| `--ha_url` | `HA_URL` | Home Assistant base URL (not needed in the HA add-on) | — |
| `--ha_token` | `HA_TOKEN` | HA long-lived access token (secret; prefer the env var) | — |
| `--opt_price_entity` | `OPT_PRICE_ENTITY` | Nordpool price sensor entity ID (prices incl. VAT) | — |
| `--opt_consumption_entity` | `OPT_CONSUMPTION_ENTITY` | Household load power sensor entity ID | — |
| `--opt_ev_entity` | `OPT_EV_ENTITY` | Optional EV charger power sensor entity ID | — |
| `--opt_ev_guard_w` | `OPT_EV_GUARD_W` | EV power in W above which the battery is held instead of discharging into the car in `self_consume` hours; 0 disables [0, 50000] | `1000` |
| `--opt_history_days` | `OPT_HISTORY_DAYS` | Days of consumption history for the profile (1..30) | `10` |
| `--opt_battery_efficiency` | `OPT_BATTERY_EFFICIENCY` | Battery round-trip efficiency (0, 1] | `0.83` |
| `--opt_max_charge_rate_w` | `OPT_MAX_CHARGE_RATE_W` | Maximum battery charge rate in W (0, 30000] | `4500` |
| `--opt_max_discharge_rate_w` | `OPT_MAX_DISCHARGE_RATE_W` | Maximum battery discharge rate in W (0, 30000] | `4500` |
| `--opt_min_soc_pct` | `OPT_MIN_SOC_PCT` | Minimum battery SoC in percent [0, 100) | `7` |
| `--opt_vat_rate` | `OPT_VAT_RATE` | VAT rate as a fraction (0.25 = 25 %) | `0.25` |
| `--opt_sell_margin` | `OPT_SELL_MARGIN` | Sell margin in currency/kWh incl. VAT [-5, 10] | `0` |
| `--opt_min_gain` | `OPT_MIN_GAIN` | Minimum gain per hour before a charge or discharge replaces self-consumption; hold and standby need a tenth [0, 10] | `0.5` |
| `--opt_wear_cost` | `OPT_WEAR_COST` | Battery wear cost per kWh a forced charge or discharge moves [0, 5] | `0` |
| `--opt_purchase_surcharge` | `OPT_PURCHASE_SURCHARGE` | 24 comma-separated surcharges incl. VAT, hour 00..23 | 24 × `0` |
| `--opt_dry_run` | `OPT_DRY_RUN` | Compute and publish the plan without writing to the inverter | `true` |

The `--ha_*` and `--opt_*` flags are used only with `--scheduler_mode optimize`. See [Configuration → Cost Optimizer](configuration.md#cost-optimizer) for valid ranges and the surcharge format. With `scheduler_mode: optimize`, a configuration error makes `sbam schedule` exit with code 1; in live mode it first attempts to restore inverter defaults.

### `optimize`

Compute the cost-optimal battery plan for the current price horizon once, print it and exit. See [Cost-optimized Dispatch](optimizer.md).

```bash
sbam optimize [flags]
```

`sbam optimize` is **always a dry run**. It never writes to the inverter and publishes nothing over MQTT, whatever `opt_dry_run` says. Inverter writes only happen from `sbam schedule` with `scheduler_mode: optimize`.

| Flag | Env var | Description | Default |
|------|---------|-------------|---------|
| `--dry-run` | — | Print the plan only. Always on; `--dry-run=false` is rejected with exit code 1. Command-local, not a config key. | `true` |
| `--output` | — | Output format: `table` or `json` | `table` |
| `-u, --url` | `URL` | Solcast forecast URL(s), comma-separated | — |
| `-k, --apikey` | `APIKEY` | Solcast API key | — |
| `-H, --fronius_ip` | `FRONIUS_IP` | Fronius inverter LAN IP | — |
| `-n, --cache_forecast` | `CACHE_FORECAST` | Accepted for compatibility; the optimizer always uses the forecast cache | `false` |
| `-f, --cache_file_prefix` | `CACHE_FILE_PREFIX` | Cache file name prefix | `cached_forecast` |
| `-l, --cache_time` | `CACHE_TIME` | Cache TTL in seconds | `7200` |
| `-c, --pw_consumption` | `PW_CONSUMPTION` | Daily consumption (Wh); static fallback for hours without history. Must be > 0. | `0` |
| `--ha_url` | `HA_URL` | Home Assistant base URL (not needed in the HA add-on) | — |
| `--ha_token` | `HA_TOKEN` | HA long-lived access token (secret; prefer the env var) | — |
| `--opt_price_entity` | `OPT_PRICE_ENTITY` | Nordpool price sensor entity ID (prices incl. VAT) | — |
| `--opt_consumption_entity` | `OPT_CONSUMPTION_ENTITY` | Household load power sensor entity ID | — |
| `--opt_ev_entity` | `OPT_EV_ENTITY` | Optional EV charger power sensor entity ID | — |
| `--opt_ev_guard_w` | `OPT_EV_GUARD_W` | EV power in W above which the battery is held instead of discharging into the car in `self_consume` hours; 0 disables [0, 50000] | `1000` |
| `--opt_history_days` | `OPT_HISTORY_DAYS` | Days of consumption history for the profile (1..30) | `10` |
| `--opt_battery_efficiency` | `OPT_BATTERY_EFFICIENCY` | Battery round-trip efficiency (0, 1] | `0.83` |
| `--opt_max_charge_rate_w` | `OPT_MAX_CHARGE_RATE_W` | Maximum battery charge rate in W (0, 30000] | `4500` |
| `--opt_max_discharge_rate_w` | `OPT_MAX_DISCHARGE_RATE_W` | Maximum battery discharge rate in W (0, 30000] | `4500` |
| `--opt_min_soc_pct` | `OPT_MIN_SOC_PCT` | Minimum battery SoC in percent [0, 100) | `7` |
| `--opt_vat_rate` | `OPT_VAT_RATE` | VAT rate as a fraction (0.25 = 25 %) | `0.25` |
| `--opt_sell_margin` | `OPT_SELL_MARGIN` | Sell margin in currency/kWh incl. VAT [-5, 10] | `0` |
| `--opt_min_gain` | `OPT_MIN_GAIN` | Minimum gain per hour before a charge or discharge replaces self-consumption; hold and standby need a tenth [0, 10] | `0.5` |
| `--opt_wear_cost` | `OPT_WEAR_COST` | Battery wear cost per kWh a forced charge or discharge moves [0, 5] | `0` |
| `--opt_purchase_surcharge` | `OPT_PURCHASE_SURCHARGE` | 24 comma-separated surcharges incl. VAT, hour 00..23 | 24 × `0` |
| `--opt_dry_run` | `OPT_DRY_RUN` | Accepted; the command never writes regardless of its value | `true` |

Example (the token is read from `HA_TOKEN`; do not pass it as a flag in shell history):

```bash
export HA_TOKEN=<long-lived-token>
sbam optimize -H 192.168.1.100 -k "$APIKEY" -u "$SOLCAST_URL" -c 11000 \
  --ha_url http://homeassistant.local:8123 \
  --opt_price_entity sensor.nordpool_kwh_dk2_dkk_3_10_025 \
  --opt_consumption_entity sensor.solarnet_power_load_consumed \
  --opt_purchase_surcharge "0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,2.04,2.04,2.04,2.04,0.78,0.78,0.78"
```

**Table output** (`--output table`, rows shortened here; the real output prints every hour):

```
sbam optimize - DRY RUN (nothing written to the inverter)
Run at      2026-10-05 10:20 +02:00     Horizon  36 h (until 2026-10-06 21:59)
Battery     SoC 50.0 %  capacity 10 000 Wh  min SoC 7 %  usable 4 300 Wh
Model       efficiency 0.83  max charge 4 500 W  max discharge 4 500 W
Prices      sensor.nordpool_kwh_dk2_dkk_3_10_025 (DKK/kWh, incl. VAT)  VAT 0.25  sell margin 0.10
Consumption sensor.solarnet_power_load_consumed
            history 10 days; static fallback hours: none
PV          Solcast (forecast cache)

START             CONS Wh   PV Wh    BUY    SELL  ACTION         RATE W  SOC END %
Mon 10-05 10:00       333    1333   1.50    1.10  self_consume        0       58.1
Mon 10-05 16:00       500       0   1.50    1.10  hold                0      100.0
Mon 10-05 18:00       500       0   3.00    2.30  discharge        4325       51.6
Tue 10-06 04:00       500       0   0.80    0.54  charge           4482       62.8

Expected cost   -30.74 DKK  (import 17.45 - export 48.19 - terminal value 0.00)
Terminal value    0.00 DKK  (0 Wh left above min SoC x 0.83 x mean buy 1.63)
Decision        hour 0: self_consume 0 W - optimizer_self_consume_no_gain
Reasons         optimizer_self_consume_no_gain
```

- `START` is local time. On DST days, repeated or skipped hours get the UTC offset appended, e.g. `Sun 10-26 02:00+02`.
- `static fallback hours` shows `none`, `some, see log (optimizer_consumption_static_fallback)` or `all (optimizer_consumption_entity_not_found)`.
- `PV` shows `forecast cache`, or that missing or stale forecast hours are counted as 0 Wh.

**Fallback output.** When no usable plan can be computed, no table is printed:

```
sbam optimize - DRY RUN (nothing written to the inverter)
Run at      2026-10-05 10:20 +02:00

FALLBACK: optimizer_fallback_missing_prices - inverter defaults would be restored
Input       price: no price for the current hour
Battery     SoC 50.0 %
Reasons     optimizer_fallback_missing_prices
```

**JSON output** (`--output json`) prints the same object as the `<prefix>/plan` MQTT topic; see [Optimizer Plan Topic](mqtt.md#optimizer-plan-topic). On fallback, `hours` is empty and the cost fields are `null`.

**Exit codes**

| Code | Meaning |
|------|---------|
| `0` | Plan computed and printed. |
| `1` | Configuration error (all problems are listed in one message), invalid `--output`, or `--dry-run=false`. |
| `2` | Optimizer fallback: the command ran, but no usable plan could be computed. The output names the reason. |

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
| `--sunspec_type` | Inverter Modbus SunSpec model type (`int_sf`, `float`, `auto`). Default `int_sf` |

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

In `scheduler_mode: optimize`, each run writes exactly one `optimizer run` Info line. Fallbacks and degraded inputs add one Warn line each:

```
INFO   optimizer run  {"dry_run": true, "action": "hold", "rate_w": 0, "reason": "optimizer_hold_for_later_peak", "reasons": ["optimizer_hold_for_later_peak"], "expected_cost": 41.87, "terminal_value": 1.93, "horizon_h": 34, "soc_pct": 52.0, "duration_ms": 312}
INFO   dry run: no Modbus registers written  {"action": "hold", "rate_w": 0, "registers": "..."}
WARN   optimizer fallback: inverter defaults restored  {"reason": "optimizer_fallback_ha_unreachable", ...}
WARN   optimizer degraded input  {"reason": "optimizer_consumption_static_fallback", "hours": [3, 4]}
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
