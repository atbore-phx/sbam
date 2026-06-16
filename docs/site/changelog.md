# Changelog

## v2.1.0

### Multi-window charging schedule

New `windows` configuration option replaces the single `start_hr`/`end_hr`/`max_charge` triple with an ordered list of charge windows, each with its own `max_charge` and optional `forecast_horizon`/`consumption_horizon` overrides.

- Define multiple charge windows per day (e.g., night 02:00–06:00 and midday 12:00–15:00) with independent power limits and horizons.
- Cross-midnight windows supported (e.g., 22:00–04:00).
- Overlap detection rejects misconfigured windows at startup.
- Legacy `start_hr`/`end_hr`/`max_charge` keys remain fully supported — synthesized into a single window when `windows` is absent.
- Mixing `windows` with legacy keys is rejected with a clear error.
- MQTT state payload and HA discovery expose the active window name, max charge, and forecast horizon as diagnostic sensors.

### Configurable forecast and consumption horizons

New `forecast_horizon` and `consumption_horizon` options replace the hardcoded noon-threshold forecast selection with explicit, named modes:

- `forecast_horizon`: `default`, `next_solar_day`, `remaining_today`, `today`, `tomorrow`, `off`.
- `consumption_horizon`: `full_day`, `remaining_today`.

Existing installations keep current behavior under `forecast_horizon=default` and `consumption_horizon=full_day`.

### Scheduler mode selector

New `scheduler_mode` option (`crontab` | `windows`) with `crontab` deprecation path. The `crontab` key remains functional but is deprecated and will be removed in v3.0.0. A one-shot warning is logged when `mode: crontab` is configured explicitly. Mixing `mode: crontab` with `windows:` is rejected at startup.

### HA add-on YAML config

The Home Assistant add-on configuration is now defined in YAML format with nested MQTT configuration, matching the standalone `config.yaml` structure.

### Crontab default validation fix

The Home Assistant add-on configuration schema now accepts `0 0 0 0 0` as a valid crontab value, allowing users to disable scheduled execution through the HA UI.

---

## v2.0.2

### Battery reset at end of charge window

Fixed a race condition where the battery could remain in force-charge mode after the charging window ended. The periodic schedule tick and the end-of-window reset could fire at the same minute. The fix adds a 5-minute cooldown at the tail of the charging window: no new charge decisions are made in the last 5 minutes before `end_hr`, ensuring the reset cannot be overridden.

---

## v2.0.1

### ARM64/aarch64 build fix

The v2.0.0 release introduced an ARM64 regression where the Docker image was mislabeled as `linux/amd64`, preventing installation on ARM64 hardware (Raspberry Pi, ODROID, etc.). The fix uses a native ARM64 GitHub Actions runner for aarch64 builds, ensuring the correct `linux/arm64` platform manifest. 32-bit architecture support (i386, armv7) has been dropped.

---

## v2.0.0

### MQTT integration

MQTT is opt-in. When enabled, sbam publishes state, availability, and error topics and subscribes to command topics. Home Assistant MQTT discovery publishes entities automatically when `mqtt_ha_discovery=true`.

Discovery set includes:

- 3 Main sensors: Battery Capacity, State of Charge, Forecast Today
- 10 Diagnostic sensors: Charge Percent, Charge Window, Decision, Last Decision, Last Update, Net Energy, Next Run, Paused, Paused State, Reserve Window
- 2 Configuration selectors: force_charge_target_pct, pause_duration_s
- 5 Control buttons: trigger_now, pause, resume, force_charge, set_defaults

### Cross-midnight time windows

Charge and reserve windows can now span midnight (e.g., `start_hr: 22:00`, `end_hr: 06:00`). Equal start and end values are invalid.

---

[Full release history on GitHub](https://github.com/atbore-phx/sbam/releases)
