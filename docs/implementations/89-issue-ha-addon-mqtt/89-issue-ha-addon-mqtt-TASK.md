# Feature: Home Assistant add-on MQTT finish

> Source issue: [#89](https://github.com/atbore-phx/sbam/issues/89)  
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)  
> Reconciled: 2026-05-10  
> Slug: `89-issue-ha-addon-mqtt`

## Summary

Finish the Home Assistant add-on MQTT surface for v2.0.0. The current codebase already has `version: 2.0.0`, non-TLS MQTT options, env exports, a changelog entry, and initial DOCS text. The remaining work is to declare the MQTT service dependency, auto-fill Mosquitto connection settings when available, and clean up/document the add-on behavior.

## Reconciled Decisions

- The add-on exposes non-TLS MQTT options only for v2.0.0: broker, client ID, username, password, topic prefix, discovery toggle, and discovery prefix.
- Standalone binary TLS options remain supported through CLI/env/YAML but are not exposed in the add-on UI.
- Manual broker/credential values always win over Home Assistant Mosquitto auto-discovery.

## Scope

- Add `services: ["mqtt:need"]` to `home-assistant/addons/sbam/config.json`.
- Update `run.sh` to use `bashio::services 'mqtt'` when MQTT is enabled and `mqtt_broker` is empty.
- Fill `MQTT_BROKER`, `MQTT_USERNAME`, and `MQTT_PASSWORD` from the HA service only when the user did not provide manual values.
- Clean duplicated MQTT option bullets in `DOCS.md`.
- Document broker auto-fill, topic prefix, HA discovery prefix, and command/state topics.
- Keep `CHANGELOG.md` `2.0.0` entry accurate.

Out of scope:

- Adding TLS options to the add-on UI.
- Runner or command wiring (#87/#88).
- Full README release documentation (#91).

## Acceptance Criteria

- [ ] Add-on config declares the MQTT service dependency.
- [ ] Add-on starts with MQTT disabled and all defaults unchanged.
- [ ] When MQTT is enabled and `mqtt_broker` is set, run.sh preserves manual broker/credentials.
- [ ] When MQTT is enabled and `mqtt_broker` is empty, run.sh fills broker/credentials from `bashio::services 'mqtt'` when available.
- [ ] Password fields remain masked in the add-on schema.
- [ ] DOCS.md has one non-duplicated MQTT option section.
- [ ] Local add-on build script succeeds when available.
