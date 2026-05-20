# Feature: MQTT docs and migration note

> Source issue: [#91](https://github.com/atbore-phx/sbam/issues/91)
> Fetched: 2026-05-17
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)
> Reconciled: 2026-05-17
> Slug: `91-issue-docs-mqtt`

## Summary

Finish release documentation for the v2.0.0 MQTT feed. The README and project structure already contain partial MQTT text from earlier #64 work, but they need release-polish coverage for the topic map, implemented payload schemas, command examples, Home Assistant discovery behavior, and a v1.x migration callout.

## Motivation / User Story

As a standalone, Docker, or Home Assistant user upgrading to v2.0.0, I need to understand that MQTT is opt-in and backward compatible by default. If I choose to enable MQTT, I need copyable configuration examples, accurate topic names, payload shapes, and command examples that match the implemented runtime behavior.

## Scope

- In scope: Expand the README MQTT section with quick-start enablement examples, topic map, state/error/ack payload examples, command publishing examples, Home Assistant discovery notes, and migration guidance.
- In scope: Document all runtime topics: `availability`, `state`, `error`, `cmd/<name>`, `cmd/<name>/ack`, and Home Assistant discovery config topics.
- In scope: Document `mqtt.StatePayload`, `mqtt.ErrorPayload`, and `mqtt.AckPayload` JSON fields as implemented.
- In scope: Add `mosquitto_pub` examples for `trigger_now`, `pause`, `resume`, `force_charge`, and `set_defaults`.
- In scope: State that `set_reserve` is deferred beyond v2.0.0 even though an internal intent constant exists.
- In scope: Add a migration note explaining that v1.x users need no changes while `mqtt_enabled=false`.
- In scope: Verify `.github/copilot-instructions.md` Project Structure includes the MQTT package and runner files created by #87/#88; update it only if the current list is stale.
- Out of scope: Changing MQTT source behavior, command parsing, payload structs, or Home Assistant discovery generation.
- Out of scope: Home Assistant add-on DOCS cleanup tracked separately by #89.

## Functional Requirements

- README must show how to enable MQTT with at least one CLI/env/YAML-oriented quick-start example, including `mqtt_enabled=true` and `MQTT_BROKER=tcp://127.0.0.1:1883`.
- README must include a topic map for the default `sbam` prefix and note that `mqtt_topic_prefix` changes that prefix.
- README must describe Home Assistant discovery topics as `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config` and note the default prefix `homeassistant`.
- README must include representative JSON examples for state, error, and ack payloads using the implemented field names.
- README command examples must use only implemented v2.0.0 command names: `trigger_now`, `pause`, `resume`, `force_charge`, and `set_defaults`.
- README must document that command ack payloads include `ts`, `command`, `accepted`, and optional `error`.
- README migration note must list the twelve standalone MQTT keys available through config, env, and CLI flags: `mqtt_enabled`, `mqtt_broker`, `mqtt_client_id`, `mqtt_username`, `mqtt_password`, `mqtt_tls_ca_file`, `mqtt_tls_client_cert`, `mqtt_tls_client_cert_key`, `mqtt_tls_insecure_skip`, `mqtt_topic_prefix`, `mqtt_ha_discovery`, and `mqtt_ha_discovery_prefix`.
- The implementation must verify the Project Structure list against current files and update it only if required.

## Non-functional Requirements

- Backward compatibility: Default behavior remains unchanged for v1.x users because `mqtt_enabled=false` disables MQTT.
- Safety / defaults: Documentation must avoid implying that MQTT commands are active unless MQTT is explicitly enabled and connected.
- Accuracy: Examples must be cross-checked against existing `pkg/mqtt` structs and topic helpers rather than invented from the issue text.
- Maintainability: Documentation should be concise enough to maintain alongside the MQTT implementation and should avoid duplicating large implementation details.

## Configuration Impact

- New CLI flags: None introduced by this documentation task; document the existing MQTT flags already wired for `schedule`.
- New config keys (`config.yaml`): None introduced by this documentation task; document the existing twelve `mqtt_*` keys.
- New env vars: None introduced by this documentation task; document the uppercase forms of the existing `mqtt_*` keys.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): None in this task; HA add-on documentation cleanup is tracked by #89.

## External Integrations Touched

- Solcast: Not touched.
- Fronius Solar API: Not touched.
- Fronius Modbus registers: Not touched.
- MQTT broker: Documentation only; describe published topics, subscribed command topics, acknowledgements, retained availability/discovery behavior, and Home Assistant discovery integration.

## Acceptance Criteria

- [ ] README topic map matches `pkg/mqtt` helper topics.
- [ ] README state payload example matches `mqtt.StatePayload` JSON fields.
- [ ] README error payload example matches `mqtt.ErrorPayload` JSON fields.
- [ ] README ack example matches `mqtt.AckPayload` JSON fields.
- [ ] Command examples use implemented command names only and include `mosquitto_pub` examples for all implemented commands.
- [ ] README notes that `set_reserve` is deferred beyond v2.0.0.
- [ ] Migration note explains the opt-in default and twelve standalone MQTT keys.
- [ ] `.github/copilot-instructions.md` Project Structure includes `pkg/mqtt` and current `pkg/cmd` runner source/test files, or the implementation notes that no update was needed.
- [ ] README markdown tables and fenced code blocks render correctly.

## Test Strategy

- Documentation review: Preview or inspect README markdown for table and code block formatting.
- Cross-check expected case: Compare documented topic names against `pkg/mqtt/client.go`, command parsing against `pkg/mqtt/commands.go`, and payload fields against `pkg/mqtt/types.go`.
- Edge case: Verify examples mention prefix customization and do not hard-code `sbam` without explaining `mqtt_topic_prefix`.
- Failure case: Verify rejected command acknowledgements show `accepted=false` with an `error` field.
- Automated tests: No source tests are required for documentation-only changes; run focused tests only if implementation touches source files.

## Risks / Open Questions

- RESOLVED: Use `91-issue-docs-mqtt` as the slug, matching the existing local directory and owner reconciliation comment.
- RESOLVED: Treat the 2026-05-10 owner comment as final scope for release polish.
- RESOLVED: Use docs cross-check plus markdown review as the required validation level.
- Risk: README examples can drift if MQTT structs or helper topics change after this plan is implemented.
- Risk: `.github/copilot-instructions.md` may already be current; the implementer should verify before editing.

## References

- Issue: [#91](https://github.com/atbore-phx/sbam/issues/91)
- Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)
- Existing MQTT implementation: `pkg/mqtt`
- Existing schedule runner wiring: `pkg/cmd/schedule_runner.go`

## Clarifications

- 2026-05-17: User confirmed the existing slug `91-issue-docs-mqtt`, the owner comment as final release-polish scope, and docs cross-check plus markdown review as the required validation path.
