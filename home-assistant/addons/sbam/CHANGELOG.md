## Unreleased

### Crontab default validation fix

The Home Assistant add-on configuration schema now accepts `0 0 0 0 0` as a valid crontab value, allowing users to disable scheduled execution through the HA UI. Previously the regex validation rejected this value even though the Go application has always treated it as the disabled sentinel.

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
[MQTT Feed and Home Assistant Discovery](https://github.com/atbore-phx/sbam/blob/main/docs/mqtt.md)

### Cross-midnight time window configuration

Example overnight configuration: `start_hr: 22:00` and `end_hr: 06:00`.
Reserve windows can also cross midnight, for example `batt_reserve_start_hr: 23:00`
and `batt_reserve_end_hr: 05:00`. Equal start and end values are invalid.

## Full Release Changelog
[link to sbam latest release](https://github.com/atbore-phx/sbam/releases/latest)
