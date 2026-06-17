## What's New in v2.1.0

### Multi-window charging schedule

New `windows` configuration option replaces the single `start_hr`/`end_hr`/`max_charge` triple with an ordered list of charge windows, each with its own `max_charge` and optional `forecast_horizon`/`consumption_horizon` overrides. Key highlights:

- Define multiple charge windows per day (e.g. night 02:00–06:00 and midday 12:00–15:00) with independent power limits and horizons.
- Cross-midnight windows supported (e.g. 22:00–04:00).
- Overlap detection rejects misconfigured windows at startup.
- Legacy `start_hr`/`end_hr`/`max_charge` keys remain fully supported — they are synthesized into a single window when `windows` is absent.
- MQTT state payload and Home Assistant discovery now expose the active window name, max charge, and forecast horizon as diagnostic sensors.
- New `forecast_horizon` and `consumption_horizon` options replace the hardcoded noon-threshold forecast selection with explicit, named modes:
    - `forecast_horizon`: `default` (current behavior), `next_solar_day`, `remaining_today`, `today`, `tomorrow`, `off`
    - `consumption_horizon`: `full_day` (current behavior), `remaining_today`

Existing installations keep current behavior under `forecast_horizon=default` and `consumption_horizon=full_day`.

### Scheduler mode selector

New `scheduler_mode` option (`crontab` | `windows`) replaces the legacy crontab field. The `crontab` key remains functional but is deprecated and will be removed in v3.0.0. A one-shot warning is logged when `mode: crontab` is configured. \
If scheduler_mode: `windows` is used, the `crontab` field is ignored.\
If scheduler_mode: `crontab` and `windows` is empty, the legacy single window (`start_hr`, `end_hr`, `max_charge`) is used.\
If scheduler_mode: `crontab` and `windows` is non-empty, the `windows` list is used for decisions, but ticks are still driven by the cron schedule.

### HA add-on YAML config

The Home Assistant add-on configuration is now defined in YAML format (`config.yaml`) with nested MQTT configuration, matching the standalone `config.yaml` structure. All options are documented in the [sbam documentation site](https://atbore-phx.github.io/sbam/configuration/).

### Crontab default validation fix

The Home Assistant add-on configuration schema now accepts `0 0 0 0 0` as a valid crontab value, allowing users to disable scheduled execution through the HA UI. Previously the regex validation rejected this value even though the Go application has always treated it as the disabled sentinel.

## What's New in v2.0.2

### Battery Reset at End of Charge Window

Fixed a race condition where the battery could remain in force-charge mode after the charging window ended. The periodic schedule tick and the end-of-window reset could fire at the same minute (e.g., both at 06:50 when `end_hr: 06:55` with `*/5 * * * *`). If the tick processed after the reset, the tick's charge decision overrode the reset, leaving the battery stuck in force charge.

The fix adds a 5-minute cooldown at the tail of the charging window: no new charge decisions are made in the last 5 minutes before `end_hr`, ensuring the reset cannot be overridden.

Thanks [@travellingkiwi](https://github.com/travellingkiwi) for reporting this in [#165](https://github.com/atbore-phx/sbam/issues/165).

## What's New in v2.0.1

### ARM64/aarch64 Build Fix

The v2.0.0 release introduced an ARM64 regression where the Docker image was mislabeled as `linux/amd64`, preventing installation on ARM64 hardware (Raspberry Pi, ODROID, etc.). This was caused by the upgrade of the `home-assistant/builder` action to v2026.03.2, which no longer handles cross-platform manifest labeling.

The fix uses a native ARM64 GitHub Actions runner (`ubuntu-24.04-arm`) for aarch64 builds, ensuring the correct `linux/arm64` platform manifest.

32-bit architecture support (i386, armv7) has been dropped — GitHub provides no native 32-bit runners, and the home assistant builder action cannot cross-compile because it doesn't rely on QEMU anymore.

## What's New in v2.0.0
### MQTT in the Home Assistant App (Add-on)

MQTT is opt-in.\
Quick start MQTT configuration example:

Install first the [Home Assistant MQTT integration](https://www.home-assistant.io/integrations/mqtt/)

Set MQTT in SBAM app (add-on) configuration:
- `mqtt_enabled: true`
the other MQTT options can be left empty/default to auto-fill broker URL and credentials automatically from Home Assistant service data when available or for external broker or custom configuration, they can be set manually as needed.

App (Add-on) specific behavior:

When MQTT discovery is enabled `mqtt_ha_discovery=true`, sbam publishes automatically its entities. Current discovery set includes:

- 3 Main sensors (`Battery Capacity`, `State of Charge`, and `Forecast Today`)
- 10 Diagnostic sensors (`Charge Percent`, `Charge Window`, `Decision`, `Last Decision`, `Last Update`, `Net Energy`, `Next Run`, `Paused`, `Paused State`, `Reserve Window`)
- 2 Configuration selector (`force_charge_target_pct`, `pause_duration_s`)
- 5 Controlbuttons (`trigger_now`, `pause`, `resume`, `force_charge`, `set_defaults`)

For complete MQTT reference (topic map, payload schemas, command examples,
and migration notes), see:
[MQTT Guide](https://atbore-phx.github.io/sbam/mqtt/)

### Cross-midnight time window configuration

Example overnight configuration: `start_hr: 22:00` and `end_hr: 06:00`.
Reserve windows can also cross midnight, for example `batt_reserve_start_hr: 23:00`
and `batt_reserve_end_hr: 05:00`. Equal start and end values are invalid.

## Pre-v2.0.0 Releases

### v1.6.0

Go toolchain upgraded to 1.26. Viper config loading hardened — file read errors now surface instead of failing silently. CLI flag precedence enforced (flag > env > yaml). Startup parameter dump in debug mode with secret masking. Copilot agent workflow prompts added. Error helpers consolidated from `pkg/fronius/error.go` into `src/utils/error.go`.

### v1.5.0

Simple forecast caching added — reduce Solcast API calls by caching forecasts locally with configurable TTL. Contributed by [@travellingkiwi](https://github.com/travellingkiwi) in [#61](https://github.com/atbore-phx/sbam/issues/61).

### v1.4.0

Battery reserve charging now triggers based on time rather than charge level alone. Charging uses hysteresis logic to avoid rapid toggling. Contributed by [@Johnnexto](https://github.com/Johnnexto).

### v1.3.9

HA add-on `reset` switch to restore Fronius defaults at boot. Debug logging for Modbus read/write operations via `DEBUG` env var. Skip force charging when target is below 1%.

### v1.3.5

Fixed battery charging when under reserve and net PV power is insufficient.

### v1.2.0

Minimum battery reserve capacity feature — maintain a configurable floor charge level. Test coverage improvements.

### v1.1.0

Battery maximum capacity now retrieved from the local Fronius API instead of using a hardcoded value. Fronius and Modbus test suite added.

### v1.0.0

Initial release. Static time-of-day battery charging with Solcast weather forecast integration.

## Full Release Changelog
[link to sbam latest release](https://github.com/atbore-phx/sbam/releases/latest)
