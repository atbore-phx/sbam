## What's New in v2.3.0

### Cost-optimized dispatch: `scheduler_mode: optimize`

New opt-in third scheduler mode. Every hour sbam builds a plan over the known price horizon (up to 36 h) from Nordpool spot prices, the Solcast PV forecast and the household's consumption history, then applies the action for the current hour: `self_consume`, `charge`, `hold`, `standby` or `discharge`. Prices come from a Nordpool sensor in Home Assistant; consumption is an hourly profile built from HA recorder history, with an optional EV guard that holds the battery while the car charges. Charging and discharging only replace self-consumption when they beat it by the configured minimum gain and wear cost.

Fail-safes: an inverter revert timeout on every write, register read-back, and a fallback to inverter defaults on missing or implausible input, each with a stable reason code. The optimizer honours `sunspec_type` (`int_sf`, `float`, `auto`).

`opt_dry_run` (now `optimizer.dry_run`) defaults to `true`: the plan is computed and published but nothing is written to the inverter. `sbam optimize` prints the plan once (always dry run); `sbam schedule` runs it hourly in optimize mode. New MQTT `<prefix>/plan` topic and Home Assistant discovery entities expose the action, rate, expected cost, horizon and per-hour plan.

The optimizer options are grouped under one `optimizer:` map in `config.yaml` and the add-on. The CLI keeps the flat `--opt_*`/`--ha_*` flags; environment variables are `OPTIMIZER_*` (with `HA_URL`/`HA_TOKEN` for Home Assistant access). Contributed by [@Morten-bigum](https://github.com/Morten-bigum) in [#210](https://github.com/atbore-phx/sbam/pull/210). See [Cost-optimized Dispatch](https://atbore-phx.github.io/sbam/optimizer/).

### Charge-window hold, reserve and grid import limit

`hold_battery: true` keeps the battery from discharging during a cheap charge window when no charge is needed. `reserve_horizon: until_solar` sizes the reserve from the Solcast 30-minute periods so the battery covers the house until PV does. `grid_import_limit` keeps the whole site under the contracted power while sbam charges from the grid, measured on the Fronius meter, and `grid_import_margin` keeps a safety margin. A new `hold` MQTT command, the `reserve_target_wh`/`reserve_deficit_wh`/`grid_charge_allowance_w` sensors and the `reserve_at_risk` binary sensor are available. Contributed by [@ccervigon](https://github.com/ccervigon) in [#208](https://github.com/atbore-phx/sbam/pull/208).

### SunSpec auto/float model

sbam now locates the SunSpec storage model on inverters configured for the `float` model type instead of relying on the fixed int+SF addresses. `auto` locates the storage model the same way, works with either inverter setting and falls back to the fixed int+SF addresses on devices without a SunSpec map. `int_sf` keeps the previous behavior and uses the fixed addresses. Contributed by [@ccervigon](https://github.com/ccervigon) in [#201](https://github.com/atbore-phx/sbam/pull/201).

### Window-end reset fix

The tick that leaves a charge window could cancel the window-end reset timer (for example with `before_end_defaults_minutes: 0`), and a dropped intent, a failed Modbus write or a restart at the wrong moment left the inverter held or force-charging with nothing to restore it. The tick leaving a window now resets as well, a failed reset retries after five minutes, and in `windows` mode a tick outside every window reads `StorCtl_Mod` and restores normal mode if the inverter is still held or force-charging. `force_charge` and `hold` commands given outside a window are left alone until `set_defaults`; that exemption lives in memory, so a restart ends it. The new behavior only runs for windows with `set_defaults` or `hold_battery`; windows with neither leave the inverter alone, and `crontab` and `optimize` modes are unchanged. Contributed by [@ccervigon](https://github.com/ccervigon) in [#220](https://github.com/atbore-phx/sbam/pull/220). See [Configuration → Scheduling](https://atbore-phx.github.io/sbam/configuration/#scheduling).

### Optimizer consumption profile rebuild fix

The interim mitigation from #210 cancelled the daily consumption profile rebuild whenever any intent was queued. With the EV guard enabled, sbam queues its own EV poll every 60 seconds, so the rebuild gave up almost every hour, the profile expired after 26 hours and the plan silently fell back to the static `pw_consumption` profile. The runner now counts queued commands separately from its own timer intents: tick, EV poll, watchdog and limit check wait for the rebuild instead of cancelling it, while MQTT commands and shutdown still make it yield. A yielded rebuild no longer logs the misleading consumption-profile failure warning. Contributed by [@Morten-bigum](https://github.com/Morten-bigum) in [#221](https://github.com/atbore-phx/sbam/pull/221). See [Cost-optimized Dispatch](https://atbore-phx.github.io/sbam/optimizer/).

### New documentation

- [Cost-optimized Dispatch](https://atbore-phx.github.io/sbam/optimizer/) — new page covering the hourly plan, dry run, go-live checklist, fail-safes and reason codes.
- [Configuration → Cost Optimizer](https://atbore-phx.github.io/sbam/configuration/#cost-optimizer) — the `optimizer:` options and Home Assistant API access.
- [Configuration → Inverter Connection](https://atbore-phx.github.io/sbam/configuration/#inverter-connection) — `sunspec_type` for `int_sf`, `float` and `auto`.
- [Configuration → Multi-window](https://atbore-phx.github.io/sbam/configuration/#multi-window) — `hold_battery` and the per-window options.
- [Configuration → Grid import limit](https://atbore-phx.github.io/sbam/configuration/#grid-import-limit) — `grid_import_limit`, `grid_import_margin` and `grid_charge_priority`.
- [Configuration → Reserve until solar](https://atbore-phx.github.io/sbam/configuration/#reserve-until-solar) — `reserve_horizon: until_solar`, `pw_base_load` and `batt_min_soc`.
- [MQTT Guide → Optimizer Entities (optimize mode)](https://atbore-phx.github.io/sbam/mqtt/#optimizer-entities-optimize-mode) — plan topic, sensors and discovery entities.
- [MQTT Guide → Commands in Optimize Mode](https://atbore-phx.github.io/sbam/mqtt/#commands-in-optimize-mode) — command semantics in optimize mode.
- [MQTT Guide → Broker Security in Optimize Mode](https://atbore-phx.github.io/sbam/mqtt/#broker-security-in-optimize-mode) — broker permissions and topic exposure.
- [MQTT Guide → Optimizer Plan Topic](https://atbore-phx.github.io/sbam/mqtt/#optimizer-plan-topic) — full plan payload reference.
- [CLI Reference → `optimize`](https://atbore-phx.github.io/sbam/cli/#optimize) — `sbam optimize`, the `--opt_*`/`--ha_*` flags and exit codes.
- [Prerequisites → Cost Optimizer Prerequisites](https://atbore-phx.github.io/sbam/prerequisites/#cost-optimizer-prerequisites) — the Home Assistant entities and API access.

## What's New in v2.2.0

### `to_next_window` consumption horizon

New third mode for the `consumption_horizon` option: `to_next_window` sizes consumption to the span from now until the next charge window starts (`pw_consumption × hours / 24`) instead of the full day or the midnight-resetting `remaining_today`. The span may cross midnight and can exceed 24 h for weekday-gapped windows, producing the expected consumption until the next charge opportunity. Next-window selection scans all windows for the nearest upcoming start, independent of list order, weekday filters, and cross-midnight windows. When no next window can be resolved it falls back to `remaining_today`, and the published MQTT state reports the horizon actually applied. Contributed by [@chodeus](https://github.com/chodeus) in [#188](https://github.com/atbore-phx/sbam/pull/188).

See: [Configuration → Consumption horizon modes](https://atbore-phx.github.io/sbam/configuration/#consumption-horizon-modes) · [Weekdays → Interaction with the `to_next_window` consumption horizon](https://atbore-phx.github.io/sbam/weekdays/#interaction-with-the-to_next_window-consumption-horizon)

### Stable Home Assistant device identity across inverter IP changes

The MQTT discovery device identifier used to be derived from the inverter IP, so moving the inverter (VLAN change, DHCP renumbering) re-identified every entity in Home Assistant: the previously discovered entities were stranded as permanently unavailable ghosts and a duplicate `_2` set appeared that no automation or dashboard referenced. The identifier is now persisted to a `mqtt_device_id` file in the working directory (`/data` for the add-on) on first discovery publish and reused on every later run, so entities keep their identity when `fronius_ip` changes. Existing installs keep their current identity on upgrade; editing the file is a supported manual override and deleting it re-derives from config. Read/write failures fall back to the derived value so discovery still publishes. Contributed by [@chodeus](https://github.com/chodeus) in [#195](https://github.com/atbore-phx/sbam/pull/195).

See: [MQTT Guide → Home Assistant Discovery Behavior → Device Identity](https://atbore-phx.github.io/sbam/mqtt/#device-identity)

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

See: [Configuration → Multi-window](https://atbore-phx.github.io/sbam/configuration/#multi-window) · [Forecast horizon modes](https://atbore-phx.github.io/sbam/configuration/#forecast-horizon-modes) · [Consumption horizon modes](https://atbore-phx.github.io/sbam/configuration/#consumption-horizon-modes)

### Weekday filtering on charge windows

Each window in the `windows:` list now accepts an optional `weekdays` field
for day-of-week filtering:

```yaml
weekdays: "mon-fri"        # Monday through Friday
weekdays: "mon,fri"        # Monday and Friday only
weekdays: "mon-fri,sun"    # weekdays plus Sunday
```

A cross-midnight window uses the start-day model: `start: "22:00", end:
"04:00", weekdays: "fri"` is active Friday night/Saturday morning but not
Saturday night. Two windows with the same clock range but different weekday
sets (e.g., `mon-fri` vs `sat,sun`) validate without overlap errors.

See the [Weekday Filtering guide](https://atbore-phx.github.io/sbam/weekdays/)
for the full format reference, start-day model explanation, and worked
configuration examples.

### Scheduler mode selector

New `scheduler_mode` option (`crontab` | `windows`) replaces the legacy crontab field. The `crontab` key remains functional but is deprecated and will be removed in v3.0.0. A one-shot warning is logged when `mode: crontab` is configured.

- `scheduler_mode: crontab` — legacy cron-driven scheduling. If `windows` is empty, the legacy single window (`start_hr`, `end_hr`, `max_charge`) is used. If `windows` is non-empty, the windows list drives charge decisions but ticks are still driven by the cron schedule.
- `scheduler_mode: windows` — the new internal ticker drives charge cycles, the `crontab` field is ignored, and a `windows` list must be provided.

Windows mode adds per-window scheduling controls:

- `tick_minutes` — per-window tick interval override (default 60 min).
- `defaults` (`true`/`false`) — enable per-window Fronius defaults reset at window end.
- `before_end_defaults_minutes` — how many minutes before the window end to fire the reset (default 5, minimum 0 for exact end).

The windows-mode ticker fires an immediate tick on startup (no idle gap if sbam starts mid-window) and uses exact boundary timers for window transitions. `ValidateWindows` rejects configurations where one window's end equals another's start to eliminate ambiguity at transitions.

See: [Configuration → Scheduling](https://atbore-phx.github.io/sbam/configuration/#scheduling)

### HA add-on YAML config

The Home Assistant add-on configuration is now defined in YAML format (`config.yaml`) with nested MQTT configuration, matching the standalone `config.yaml` structure. All options are documented in the [sbam documentation site](https://atbore-phx.github.io/sbam/configuration/).

See: [Configuration](https://atbore-phx.github.io/sbam/configuration/) · [Installation](https://atbore-phx.github.io/sbam/installation/)

### Crontab default validation fix

The Home Assistant add-on configuration schema now accepts `0 0 0 0 0` as a valid crontab value, allowing users to disable scheduled execution through the HA UI. Previously the regex validation rejected this value even though the Go application has always treated it as the disabled sentinel.

See: [Configuration → Scheduling](https://atbore-phx.github.io/sbam/configuration/#scheduling)

### Inverter status check before Modbus defaults write

On classifier error (DecisionSkip) the system now reads the StorCtl_Mod register (40349) before writing Fronius defaults. If the inverter is already in normal operating mode (value 0), the write is skipped, avoiding an unnecessary Modbus cycle. A read failure falls through to the existing defaults write so safety is preserved.

See: [Configuration → Operational](https://atbore-phx.github.io/sbam/configuration/#operational)

### Runner lifecycle fix for windows mode without MQTT

Fixed an issue where the runner would shut down immediately when `scheduler_mode=windows` was configured without MQTT enabled. The runner is now correctly kept alive by its internal ticker in windows mode regardless of MQTT state. Only crontab mode without MQTT triggers an immediate shutdown (no internal driver to keep the process alive).

See: [Configuration → Scheduling](https://atbore-phx.github.io/sbam/configuration/#scheduling)

### Error stack trace suppression

Error log output is now cleaner by default: stack traces are suppressed unless `DEBUG=true` is set in the environment. When `DEBUG=true`, full stack traces are included as before. Contributed by [@mhornsby](https://github.com/mhornsby) in [#185](https://github.com/atbore-phx/sbam/pull/185).

See: [CLI → Debug Logs](https://atbore-phx.github.io/sbam/cli/#debug-logs)

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
