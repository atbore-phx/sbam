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

!!! note "`pw_consumption` in optimize mode"
    With `scheduler_mode: optimize`, `pw_consumption / 24` is the static fallback for every hour of day with fewer than 3 days of consumption history. It must be greater than 0 in that mode, otherwise startup fails.

## Scheduling

`crontab + windows`: cron drives ticks, but each tick uses the window parameters from the windows: list. This is a transitional mode: cron cadence, multi-window decisions.

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `scheduler_mode` | Scheduling mode. `crontab` — the legacy cron engine. Uses the global `crontab` field and the top-level `start_hr`/`end_hr` single window. Fully backward-compatible with existing configs. `windows` — the new internal ticker. No cron needed. Ticks fire at per-window intervals (tick_minutes), window transitions are detected at exact boundary times, and a tick fires immediately on startup. This is the recommended mode.. `optimize` — hourly cost-optimized dispatch on spot prices; see [Cost Optimizer](#cost-optimizer) and [Cost-optimized Dispatch](optimizer.md). | `--scheduler_mode` | `SCHEDULER_MODE` | `scheduler_mode` | `crontab` |
| `crontab` | Cron expression for recurring execution. Deprecated use `scheduler_mode: windows` instead.| `--crontab` | `CRONTAB` | `crontab` | `0 0 0 0 0` (CLI) / `00 00-05 * * *` (HA) |

!!! warning "crontab deprecation"
    `crontab` is deprecated and will be removed in v3.0.0. Migrate to `scheduler_mode: windows` before then.

!!! info "crontab vs. windows"
    - if `scheduler_mode: windows` is used, the `crontab` field is ignored.
    - If `scheduler_mode: crontab` and `windows` is empty, the legacy single window (`start_hr`, `end_hr`, `max_charge`) is used.
    - If `scheduler_mode: crontab` and `windows` is non-empty, the windows list is used for decisions, but ticks are still driven by the cron schedule.
    - If `scheduler_mode: optimize` is used, `crontab`, `windows`, `start_hr`/`end_hr`/`max_charge` and the reserve window are ignored. A Warn is logged when `crontab` is set.

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
| `to_next_window` | Scale `pw_consumption` by the time from now until the next charge window starts (`pw_consumption × hours / 24`). The span may cross midnight, and may exceed 24 h for weekday-gapped windows — the result is the expected consumption until the next charge opportunity. Falls back to `remaining_today` when no next window can be resolved. |

!!! info "How `to_next_window` picks the next window"
    The "next window" is resolved as the window whose **start is soonest
    after now**, scanned across *all* configured windows and honouring each
    window's [weekday filter](weekdays.md). It is **not** the next entry in
    the `windows:` list, so:

    - **Config order does not matter.** Windows may be listed in any order;
      the nearest upcoming start always wins.
    - **A later-same-day window is preferred over tomorrow.** With, say, a
      morning and an afternoon window, sitting inside the morning window
      sizes consumption only to the afternoon start — not to tomorrow.
    - **Weekday-gapped coverage lengthens the span.** After a window whose
      next occurrence is days away (e.g. a `sat`-only or `wed`-only free-power
      window), the span — and therefore the sized consumption — can be
      several times `pw_consumption`. This is intended: the battery must
      carry the load until the next chance to charge. It is bounded in
      practice by battery capacity, so the effect is simply "charge toward
      full."
    - **DST:** day advancement uses fixed 24 h steps. In timezones that
      observe daylight-saving, a span that crosses a DST boundary can be off
      by an hour. Timezones without DST (e.g. `Australia/Perth`) are
      unaffected.

## Cost Optimizer

These keys are used by `sbam schedule` with `scheduler_mode: optimize` and by `sbam optimize`. They are ignored in the other modes, so existing configurations stay valid. See [Cost-optimized Dispatch](optimizer.md) for how the optimizer works and the dry-run-first workflow.

All CLI flags are long-only. Every problem found at startup is reported in one message (`invalid configuration for scheduler_mode=optimize (N problems)`) and the process exits with code 1. The values are validated even in dry run.

### Home Assistant API

| Option | Description | CLI flag | Env var | HA add-on key | Default |
|--------|-------------|----------|---------|---------------|---------|
| `ha_url` | Home Assistant base URL, e.g. `http://homeassistant.local:8123`. Scheme `http` or `https`; no user info, query or fragment. `/api` is appended if missing. A Warn is logged for plain `http` to a non-private host. | `--ha_url` | `HA_URL` | `ha_url` (optional, hidden) | — |
| `ha_token` | Home Assistant long-lived access token. **Secret**: printed as `***` in the startup dump. Required when `ha_url` is set. Prefer the env var over `config.yaml` and never pass it as a flag in shell history. | `--ha_token` | `HA_TOKEN` | `ha_token` (optional, hidden, password) | — |

How sbam reaches Home Assistant:

| Condition | Access used |
|-----------|-------------|
| `ha_url` is set | `ha_url` with `ha_token`. This also wins inside the add-on. |
| `ha_url` is empty and the Supervisor token is present (add-on) | The Supervisor API proxy with the add-on's own token. Nothing to configure. |
| Neither | Startup error: `home assistant access: no Supervisor token found and ha_url/ha_token are not set`. |

The startup log line `optimize mode active` shows `ha_access=supervisor` or `ha_access=url`. The Supervisor token is never logged.

To create a long-lived token for Docker or the CLI: in Home Assistant open your **Profile → Security → Long-lived access tokens → Create token**. A dedicated non-admin user is recommended. sbam only reads from Home Assistant (`/api/states/` and `/api/history/period/`).

### Optimizer keys

| Option | Description | CLI flag | Env var | HA add-on key | Default | Valid range |
|--------|-------------|----------|---------|---------------|---------|-------------|
| `opt_price_entity` | Nordpool price sensor (prices incl. VAT), e.g. `sensor.nordpool_kwh_dk2_dkk_3_10_025`. **Required** in optimize mode. | `--opt_price_entity` | `OPT_PRICE_ENTITY` | `opt_price_entity` | — | `sensor.<object_id>` (`^sensor\.[a-z0-9_]+$`) |
| `opt_consumption_entity` | Household load power sensor (W or kW), e.g. `sensor.solarnet_power_load_consumed`. **Required** in optimize mode. | `--opt_consumption_entity` | `OPT_CONSUMPTION_ENTITY` | `opt_consumption_entity` | — | `sensor.<object_id>` |
| `opt_ev_entity` | Optional EV charger power sensor (W or kW). Its energy is subtracted from the household load, and it drives the EV guard. | `--opt_ev_entity` | `OPT_EV_ENTITY` | `opt_ev_entity` | — (EV skipped) | empty or `sensor.<object_id>` |
| `opt_ev_guard_w` | EV power in W above which sbam holds the battery instead of letting it discharge into the car in `self_consume` hours. Needs `opt_ev_entity`. `0` disables it. See [EV guard](optimizer.md#ev-guard-opt_ev_guard_w). | `--opt_ev_guard_w` | `OPT_EV_GUARD_W` | `opt_ev_guard_w` | `1000` | `0`–`50000` |
| `opt_history_days` | Days of consumption history used for the hourly profile. Keep the HA recorder's `purge_keep_days` at least this high. | `--opt_history_days` | `OPT_HISTORY_DAYS` | `opt_history_days` | `10` | `1`–`30` |
| `opt_battery_efficiency` | Battery round-trip efficiency η. sbam splits the loss evenly: √η on charging and √η on discharging. | `--opt_battery_efficiency` | `OPT_BATTERY_EFFICIENCY` | `opt_battery_efficiency` | `0.83` | `> 0` and `≤ 1` |
| `opt_max_charge_rate_w` | Maximum battery charge rate in W, measured at the battery (the power stored, after conversion losses), like the inverter's own charge setpoint. Also capped by the inverter's `WChaMax`. | `--opt_max_charge_rate_w` | `OPT_MAX_CHARGE_RATE_W` | `opt_max_charge_rate_w` | `4500` | `> 0` and `≤ 30000` |
| `opt_max_discharge_rate_w` | Maximum battery discharge rate in W, measured at the battery (the power taken out of it). sbam writes it to the inverter as the AC-side setpoint × √`opt_battery_efficiency`, because the Gen24 discharge setpoint is AC-side ([details](optimizer.md#the-five-actions)). Also capped by the inverter's `WChaMax`. | `--opt_max_discharge_rate_w` | `OPT_MAX_DISCHARGE_RATE_W` | `opt_max_discharge_rate_w` | `4500` | `> 0` and `≤ 30000` |
| `opt_min_soc_pct` | Minimum battery SoC in percent that the plan never goes below. Values below 5 log a Warn. | `--opt_min_soc_pct` | `OPT_MIN_SOC_PCT` | `opt_min_soc_pct` | `7` | `≥ 0` and `< 100` |
| `opt_vat_rate` | VAT rate as a fraction (`0.25` = 25 %). Used to derive the sell price. | `--opt_vat_rate` | `OPT_VAT_RATE` | `opt_vat_rate` | `0.25` | `0`–`1` (a value like `25` is rejected with a hint) |
| `opt_sell_margin` | Supplier's sell margin in currency/kWh **incl. VAT**, subtracted from the ex-VAT spot price. | `--opt_sell_margin` | `OPT_SELL_MARGIN` | `opt_sell_margin` | `0` | `-5`–`10` |
| `opt_min_gain` | Minimum saving in currency **per hour** before a `charge` or `discharge` replaces the inverter's normal self-consumption; `hold` and `standby` need a tenth of it. Smaller gains stay `self_consume`. `0` disables the threshold. See [minimum gain and wear cost](optimizer.md#minimum-gain-and-wear-cost-opt_min_gain-opt_wear_cost). | `--opt_min_gain` | `OPT_MIN_GAIN` | `opt_min_gain` | `0.5` | `0`–`10` |
| `opt_wear_cost` | Battery wear cost in currency **per kWh** a forced `charge` or `discharge` moves beyond self-consumption, added to `opt_min_gain`. Example: `0.15`. `0` disables it. | `--opt_wear_cost` | `OPT_WEAR_COST` | `opt_wear_cost` | `0` | `0`–`5` |
| `opt_purchase_surcharge` | 24 hourly surcharges (grid tariff, taxes, fees) in currency/kWh **incl. VAT**, added to the spot price. First value = 00:00–01:00. See the format below. | `--opt_purchase_surcharge` | `OPT_PURCHASE_SURCHARGE` | `opt_purchase_surcharge` | 24 × `0` | exactly 24 numbers, each `-5`–`10` |
| `opt_dry_run` | Compute and publish the plan without writing to the inverter. Set to `false` to go live. Must be `true` or `false`; a malformed value is an error and sbam stays in dry run. | `--opt_dry_run` | `OPT_DRY_RUN` | `opt_dry_run` | `true` | `true` / `false` |

!!! warning "Dry run is the default everywhere"
    `opt_dry_run` defaults to `true` on the CLI, in Docker and in the add-on. Work through [Before going live](optimizer.md#before-going-live) before setting it to `false`.

!!! info "Surcharge format (`opt_purchase_surcharge`)"
    - Exactly **24** numbers, one per hour of the day, starting with 00:00–01:00.
    - Use `.` as the decimal separator. A decimal comma (`0,52`) splits into two values and fails the count check.
    - Negative values (for example a rebate) are allowed. Empty items (`0.5,,0.5`) and non-numbers are errors.
    - CLI flag, env var and add-on: one comma-separated string, e.g. `"0.52,0.52,0.52,0.52,0.52,0.52,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,0.78,2.04,2.04,2.04,2.04,0.78,0.78,0.78"`.
    - `config.yaml`: the same string, or a YAML list of 24 numbers.
    - Empty or unset means 24 × `0`.

    Error examples:

    ```
    opt_purchase_surcharge: expected exactly 24 comma-separated numbers (hour 00..23), got 23; use '.' as decimal separator
    opt_purchase_surcharge: item 7 ("0,78x") is not a number
    ```

**Prices.** For every hour: `buy = spot incl. VAT + surcharge[hour of day]` and `sell = spot incl. VAT / (1 + opt_vat_rate) − opt_sell_margin`. Example: spot 2.00, surcharge 2.04, VAT 0.25, margin 0.05 gives `buy = 4.04` and `sell = 1.55`.

**Efficiency and terminal value.** Charging ΔE into the battery costs ΔE / η, so grid-charging only pays when a later price beats the current price ÷ η. Energy left at the end of the horizon is valued at `η × mean(buy)` per kWh, so a short horizon does not empty the battery.

**Other keys in optimize mode.** `fronius_ip`, `url` and `apikey` are required as in the other modes. The PV forecast is always read through the forecast cache (`cache_file_prefix`, `cache_time`), whatever `cache_forecast` says; a cached forecast older than `max(cache_time, 24 h)` is treated as missing.

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
