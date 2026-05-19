## Release Changelog
[sbam](https://github.com/atbore-phx/sbam/releases/latest)

### Doc v2.0.0
[docs](https://github.com/atbore-phx/sbam/blob/main/home-assistant/addons/sbam/DOCS.md#new-features-in-v200)

### Changes 2.0.0

- Added opt-in MQTT support to the add-on with configuration keys for broker, credentials, topic prefix, and Home Assistant discovery controls.
- Added MQTT runtime topics for availability, state snapshots, error reports, command intake, and per-command acknowledgement payloads.
- Added Home Assistant MQTT Discovery publication so sbam entities are created automatically when MQTT discovery is enabled.
- Added Home Assistant MQTT slider selectors for force-charge target and pause duration with explicit send-button actions, including an explicit uncapped full-charge override payload (`{"target_pct":100,"ignore_max_charge":true}`).
- Added serialized schedule execution through a single runner queue so cron ticks and MQTT command intents do not race each other.
- Added MQTT command handling for trigger_now, pause, resume, force_charge, and set_defaults.
- Added extensive tests across MQTT client behavior, command parsing, discovery payloads, schedule runner lifecycle, cron wiring, and config precedence.
- Changed add-on manifest version to 2.0.0.
- Changed add-on manifest to declare optional Home Assistant MQTT service availability.
- Changed add-on runtime script to export MQTT environment variables used by the schedule command.
- Changed add-on startup behavior to auto-fill MQTT broker host/port and optional credentials from Home Assistant service data only when MQTT is enabled and broker is not manually set.
- Changed precedence behavior so manually configured broker, username, and password are preserved over auto-filled Home Assistant service values.
- Changed documentation coverage with expanded add-on setup guidance, MQTT usage notes, topic and payload references, command examples, and migration notes.
- Improved Fronius decision/classification error handling and related test coverage for safer failure behavior.
- Clarified release scope: set_reserve remains intentionally unsupported in v2.0.0.
- Added support for cross-midnight reserve windows, for example 23:00-05:00.
- Fixed schedule charge and reserve window evaluation so local-time cron ticks are not compared as UTC.
- Fixed early-morning tick handling in non-UTC timezones so configured local windows are honored.
- Kept malformed time strings and equal start/end values invalid with explicit validation errors.
