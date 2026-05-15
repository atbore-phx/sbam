# Feature: Home Assistant add-on MQTT finish

> Source issue: [#89](https://github.com/atbore-phx/sbam/issues/89)  
> Fetched: 2026-05-15  
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)  
> Reconciled: 2026-05-10  
> Slug: `89-issue-ha-addon-mqtt`  
> Updated: 2026-05-15

## Summary

Finish the Home Assistant add-on MQTT surface for v2.0.0. Issue #89 originally requested the complete add-on MQTT option, schema, docs, and changelog work. The May 10 reconciliation comment narrows the remaining scope because the current codebase already includes version `2.0.0`, non-TLS MQTT options, env exports, a changelog entry, and initial DOCS text. The remaining work is to declare the MQTT service dependency, auto-fill Mosquitto connection settings when available, clean up/document the add-on behavior, and evaluate whether DOCS can include a Home Assistant one-click repository/add-on install button.

## Motivation / User Story

Home Assistant users should be able to install and configure the sbam add-on with the MQTT feed enabled without manually copying broker details when the Mosquitto add-on is available. Documentation should make the recommended path obvious, including any supported Home Assistant button/deep-link flow that can reduce manual repository setup.

## Reconciled Decisions

- Treat the May 10 issue comment as the authoritative reduced scope for #89.
- The add-on exposes non-TLS MQTT options only for v2.0.0: broker, client ID, username, password, topic prefix, discovery toggle, and discovery prefix.
- Standalone binary TLS options remain supported through CLI/env/YAML but are not exposed in the add-on UI.
- Manual broker, username, and password values always win over Home Assistant Mosquitto auto-discovery.
- If local Home Assistant add-on build tooling or Docker is unavailable, the implementation should run focused JSON/schema, shell, and docs checks and document the build limitation.

## Scope

- In scope: add `services: ["mqtt:need"]` to `home-assistant/addons/sbam/config.json`.
- In scope: update `run.sh` to use `bashio::services 'mqtt'` when MQTT is enabled and `mqtt_broker` is empty.
- In scope: fill `MQTT_BROKER`, `MQTT_USERNAME`, and `MQTT_PASSWORD` from the Home Assistant MQTT service only when the user did not provide manual values.
- In scope: verify password fields remain masked in the add-on schema and startup parameter redaction covers MQTT secrets.
- In scope: clean duplicated MQTT option bullets in `DOCS.md`.
- In scope: document broker auto-fill, topic prefix, HA discovery prefix, command/state topics, and short `mosquitto_pub` examples.
- In scope: evaluate and, if supported by Home Assistant documentation, add a one-click Home Assistant repository/add-on install button to `DOCS.md`; otherwise document why it is not included.
- In scope: evaluate whether the Home Assistant add-on can default the Info tab `Show in sidebar` and `Auto update` toggles to unchecked, and add supported manifest fields only if Home Assistant documents them for repository add-ons.
- In scope: keep `CHANGELOG.md` `2.0.0` entry accurate if the current entry does not mention service auto-discovery or the docs install button.
- Out of scope: adding TLS options to the add-on UI.
- Out of scope: runner or command wiring (#87/#88).
- Out of scope: full README release documentation (#91).
- Out of scope: GitHub issue updates, branch creation, or pull request creation.

## Functional Requirements

- `config.json` declares the Home Assistant MQTT service dependency using `services: ["mqtt:need"]`.
- `config.json` keeps existing MQTT option defaults unchanged, with MQTT disabled by default.
- `config.json` keeps `mqtt_password` masked as `password`; TLS add-on UI fields are not added for v2.0.0.
- `run.sh` exports the existing MQTT options as uppercase environment variables for the sbam binary.
- `run.sh` preserves manual `mqtt_broker`, `mqtt_username`, and `mqtt_password` values whenever they are set.
- `run.sh` auto-fills `MQTT_BROKER`, `MQTT_USERNAME`, and `MQTT_PASSWORD` from `bashio::services 'mqtt'` only when MQTT is enabled and `MQTT_BROKER` is empty.
- `DOCS.md` contains one non-duplicated MQTT options section.
- `DOCS.md` explains that Mosquitto auto-fill occurs only when MQTT is enabled and the user leaves `mqtt_broker` empty.
- `DOCS.md` includes the MQTT topic map, HA discovery entities, and example commands for state and `cmd/force_charge`.
- `DOCS.md` includes a Home Assistant install/add-repository button if there is a supported Home Assistant deep link for this repository.
- Add-on metadata defaults `Show in sidebar` and `Auto update` to unchecked only if Home Assistant exposes supported manifest fields for those toggles; otherwise the PLAN documents that they are Supervisor/user-managed controls and should not be faked.
- `CHANGELOG.md` has an accurate `2.0.0` entry for the MQTT add-on support.

## Non-functional Requirements

- Backward compatibility: MQTT remains opt-in; existing default add-on behavior with MQTT disabled is unchanged.
- Safety / defaults: manual broker credentials must not be overwritten by service auto-discovery.
- Safety / defaults: MQTT passwords and TLS key material must be redacted from startup parameter dumps.
- Security: user-facing docs and examples must not encourage storing real secrets in command history or logs.
- Maintainability: keep add-on shell changes small, readable, and aligned with existing bashio usage.
- Performance: no new long-running startup probes; service lookup should happen once during add-on startup.

## Configuration Impact

- New CLI flags: none.
- New config keys (`config.yaml`): none for this add-on-only change.
- New env vars: no new sbam env var names are expected; `run.sh` may auto-fill existing `MQTT_BROKER`, `MQTT_USERNAME`, and `MQTT_PASSWORD` exports from Home Assistant service data.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): add top-level `services: ["mqtt:need"]`; verify existing non-TLS MQTT schema entries and masked `mqtt_password` remain correct; do not expose TLS options in the add-on UI.
- Home Assistant add-on metadata changes: only add documented fields for Info tab defaults such as sidebar visibility or auto-update if supported by the Home Assistant add-on specification.

## External Integrations Touched

- Home Assistant Supervisor add-on config: declare the MQTT service dependency.
- Home Assistant bashio: read the `mqtt` service object for Mosquitto host, port, username, and password when available.
- Home Assistant documentation/deep links: evaluate the official My Home Assistant repository/add-on redirect button for DOCS.
- Home Assistant Supervisor Info tab controls: evaluate whether `Show in sidebar` and `Auto update` defaults can be controlled by add-on metadata.
- Mosquitto add-on: recommended broker integration path.
- Solcast: not touched.
- Fronius Solar API: not touched.
- Fronius Modbus registers: not touched.

## Acceptance Criteria

- [ ] Add-on config declares the MQTT service dependency.
- [ ] Add-on starts with MQTT disabled and all defaults unchanged.
- [ ] When MQTT is enabled and `mqtt_broker` is set, `run.sh` preserves manual broker/credentials.
- [ ] When MQTT is enabled and `mqtt_broker` is empty, `run.sh` fills broker/credentials from `bashio::services 'mqtt'` when available.
- [ ] If the Home Assistant MQTT service is unavailable, `run.sh` handles that path without crashing and leaves manual/default values intact.
- [ ] Password fields remain masked in the add-on schema and startup parameter dumps redact MQTT secrets.
- [ ] `DOCS.md` has one non-duplicated MQTT option section.
- [ ] `DOCS.md` documents Mosquitto auto-fill behavior and manual broker override behavior.
- [ ] `DOCS.md` includes a supported Home Assistant install/add-repository button or records why one is not feasible.
- [ ] `Show in sidebar` and `Auto update` default behavior is implemented if supported by official add-on metadata fields, or documented as not controllable by the add-on repository if unsupported.
- [ ] Local add-on build via `home-assistant/addons/test_local.sh` succeeds when the environment supports it; otherwise focused JSON/schema, shell, and docs checks pass and the limitation is recorded.

## Test Strategy

- Unit/static tests: validate `config.json` parses and contains `services: ["mqtt:need"]`, unchanged MQTT defaults, and password schema for `mqtt_password`.
- Shell-focused tests: validate `run.sh` preserves manual broker credentials, fills empty values from a mock/stubbed `bashio::services 'mqtt'`, and tolerates an unavailable service.
- Docs checks: verify `DOCS.md` has one MQTT options section, documents auto-fill behavior, and uses a supported Home Assistant install button URL if included.
- Metadata checks: verify any add-on config fields used for sidebar visibility or auto-update defaults are official Home Assistant add-on fields.
- Expected case: MQTT enabled, `mqtt_broker` empty, Home Assistant MQTT service available.
- Edge case: MQTT enabled with manual broker/username/password values set; manual values win.
- Failure case: MQTT enabled, broker empty, but Home Assistant MQTT service unavailable or missing expected fields.
- Build validation: run `home-assistant/addons/test_local.sh` when Docker/Home Assistant build tooling is available.

## Risks / Open Questions

- Confirm the exact `bashio::services 'mqtt'` field names for host, port, username, password, and protocol in the current Home Assistant base image.
- Confirm the official Home Assistant deep link for adding a custom add-on repository and whether it can be used safely from `DOCS.md`.
- Confirm whether `Show in sidebar` and `Auto update` can be defaulted by repository metadata or whether they are only per-installation Supervisor/user preferences.
- Full HA UI verification may require a real Home Assistant environment; local checks can validate schema/static behavior but not the rendered options screen.

## References

- Issue: https://github.com/atbore-phx/sbam/issues/89
- Parent issue: https://github.com/atbore-phx/sbam/issues/64
- Parent MQTT implementation plan: https://github.com/atbore-phx/sbam/blob/release/v2.0.0/docs/implementations/64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md#6-implementation-blueprint

## Clarifications

- 2026-05-15: Use the May 10 issue comment as the authoritative reduced scope for #89.
- 2026-05-15: If Home Assistant add-on build tooling or Docker is unavailable, allow focused JSON/schema, shell, and docs checks and document the limitation.
- 2026-05-15: No extra non-functional constraints beyond the issue/comment constraints, backward-compatible defaults, masked secrets, manual values winning, and no TLS add-on UI.
- 2026-05-15: Evaluate whether DOCS can include a Home Assistant install/add-repository button so users do not have to manually add the repository URL.
- 2026-05-15: Evaluate whether Home Assistant add-on Info tab toggles `Show in sidebar` and `Auto update` can be defaulted to unchecked; add only documented metadata fields if supported.
