[sbam](https://github.com/atbore-phx/sbam/releases/latest)

## Unreleased

- Fixed schedule charge and reserve window evaluation so local-time cron ticks are not compared as UTC.
- Added support for cross-midnight charge and reserve windows, for example 22:00-06:00.

## 2.0.0 - 2026-05-06

- Added MQTT and Home Assistant discovery options to add-on config.
- Exported MQTT environment variables in add-on runtime script.
- Added Home Assistant MQTT service dependency declaration in add-on manifest.
- Added MQTT broker and credential auto-fill from Home Assistant service data when broker is not manually configured.
- Updated add-on documentation with My Home Assistant add-repository button, MQTT topic and command map, Mosquitto guidance, and Info tab toggle limitations.