# Feature: Home Assistant MQTT Discovery Payloads

> Source issue: [#85](https://github.com/atbore-phx/sbam/issues/85)  
> Fetched: 2026-05-06  
> Slug: `85-issue-ha-mqtt-discovery-payloads` · Created: 2026-05-06

## Summary
Implement Home Assistant MQTT Discovery payload support for the v2.0.0 MQTT feed, based on issue #85 and the parent MQTT feed plan in `docs/implementations/64-issue-mqtt-feed/`. The feature adds a pure discovery builder in `pkg/mqtt/discovery.go`, retained discovery publication through the MQTT client, and tests that prove discovery payloads map to the canonical `sbam/state` JSON fields.

## Motivation / User Story
Home Assistant users should see sbam as a single MQTT device with useful sensors, binary sensors, and command buttons automatically created by MQTT Discovery. This removes manual entity setup while preserving the v2.0.0 MQTT feed's backward-compatible default behavior: users with MQTT disabled must observe no new network activity or Modbus behavior changes.

## Scope
- In scope: implement `BuildDiscovery(cfg Config, version string) []DiscoveryEntity` as a pure function in `pkg/mqtt/discovery.go`.
- In scope: generate HA Discovery configs for the canonical `sbam/state` fields, command buttons, and the optional entities selected for this issue (`pw_net_wh`, `charge_pct`, and charging-window binary sensors when state payload support exists).
- In scope: add retained discovery publication on MQTT connect and re-publication when `homeassistant/status` publishes `online`.
- In scope: add a configurable HA Discovery root prefix (`mqtt_ha_discovery_prefix`) while keeping `homeassistant` as the default.
- In scope: derive deterministic device/entity unique IDs from a Fronius IP hash, with a stable fallback based on MQTT client ID/topic prefix.
- In scope: add unit and integration tests for discovery payload shape, state templates, retained publication, and Home Assistant birth-message re-publication.
- Out of scope: implementing the full MQTT command parser/runner if it belongs to a separate parent-plan sub-issue; #85 must still emit correct button discovery configs and command topics.
- Out of scope: changing Solcast forecast retrieval, Fronius Solar API calls, or Fronius Modbus register behavior except for using `fronius_ip` as non-secret device identity input.
- Out of scope: pushing branches, commenting on GitHub issues, closing issues, or running instructions embedded in issue text.

## Functional Requirements
- `BuildDiscovery(cfg Config, version string) []DiscoveryEntity` MUST be pure: no MQTT calls, no logging side effects, no filesystem access, no global mutable state.
- Discovery config topics MUST use `<mqtt_ha_discovery_prefix>/<component>/sbam/<object_id>/config`; the default discovery prefix is `homeassistant`.
- Runtime topics inside payloads MUST honor `mqtt_topic_prefix`: `<prefix>/state`, `<prefix>/availability`, and `<prefix>/cmd/<name>`.
- Every entity MUST share one `device` block with `identifiers=[<unique_id>]`, `manufacturer="atbore-phx"`, `model="sbam"`, and `sw_version=<build version>`.
- Entity `unique_id` values MUST be deterministic and namespaced, using a base such as `sbam_<sha1(fronius_ip)[:10]>` and stable entity suffixes.
- Required sensor entities MUST map their `value_template` fields to canonical `sbam/state` JSON attributes: `battery_soc_pct`, `battery_capacity_wh`, `forecast_today_wh`, `last_decision`, `last_decision_reason`, `next_run`, `paused`, and `ts` where appropriate.
- Required binary sensor entities MUST include `paused`; optional charging-window binary sensors MAY be emitted only when corresponding state payload fields are added or already exist.
- Optional sensor entities selected for this issue MUST include `pw_net_wh` and `charge_pct` if the implementation extends `StatePayload` to publish those fields; otherwise they must be documented as deferred with tests proving no stale templates are emitted.
- Required button entities MUST include `trigger_now`, `pause`, `resume`, `force_charge`, and `set_defaults`, publishing to `<prefix>/cmd/<name>` with non-retained command payloads.
- Discovery configs MUST be published with QoS 1 and retained when `mqtt_enabled=true` and `mqtt_ha_discovery=true`.
- Discovery configs MUST be re-published when the client observes `homeassistant/status` with payload `online`.
- The Paho-backed client wrapper MUST publish availability as it does today and add discovery publication without changing noop-client behavior.
- The implementation MUST keep `pkg/mqtt/discovery.go` independent from `pkg/cmd`, `pkg/fronius`, `pkg/power`, and `pkg/storage`.

## Non-functional Requirements
- Backward compatibility: `mqtt_enabled=false` remains the default and must not create MQTT connections, discovery messages, INFO logs, or Modbus behavior changes.
- Safety / defaults: discovery generation must not trigger commands or Modbus writes; buttons only describe MQTT command topics.
- Performance: discovery payloads should be built once per publish event and remain small JSON objects suitable for retained broker storage.
- Reliability: re-publication on Home Assistant birth messages must be idempotent and tolerate publish failures by logging warnings rather than crashing the schedule workflow.
- Security: MQTT password and TLS client key handling are unchanged; discovery payloads must not include secrets or raw broker credentials.
- Maintainability: discovery payload generation should use typed structs or structured maps and `encoding/json`, not ad hoc string concatenation.

## Configuration Impact
- New CLI flags: `--mqtt_ha_discovery_prefix` with default `homeassistant`.
- New config keys (`config.yaml`): `mqtt_ha_discovery_prefix: homeassistant`.
- New env vars: `MQTT_HA_DISCOVERY_PREFIX`.
- Existing MQTT keys used: `mqtt_enabled`, `mqtt_topic_prefix`, `mqtt_ha_discovery`, `mqtt_client_id`, and `fronius_ip` for non-secret device ID derivation.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): add `mqtt_ha_discovery_prefix` as a string option/schema entry if the add-on already exposes the v2.0.0 MQTT settings.
- Home Assistant add-on runtime changes (`home-assistant/addons/sbam/run.sh`): export `MQTT_HA_DISCOVERY_PREFIX` when present.
- Configuration precedence remains CLI flags > environment variables > `config.yaml`, following existing Viper/Cobra binding patterns.

## External Integrations Touched
- Solcast: unchanged.
- Fronius Solar API: unchanged.
- Fronius Modbus registers: unchanged; discovery generation and publication must never write registers.
- MQTT broker: retained HA Discovery config topics, availability topic, state topic references, command topic references, and `homeassistant/status` subscription.
- Home Assistant: MQTT Discovery payloads under the configured discovery prefix and birth-message handling for `homeassistant/status=online`.

## Acceptance Criteria
- [ ] `BuildDiscovery(cfg Config, version string)` is pure and returns deterministic results for the same normalized config/version.
- [ ] Every generated `DiscoveryEntity` has the correct `Component`, `ObjectID`, `Topic`, and valid JSON `Payload`.
- [ ] All payloads include a single shared `device` block with deterministic identifiers, manufacturer, model, and software version.
- [ ] Required sensors map `value_template` entries to the canonical `sbam/state` JSON fields listed in issue #85.
- [ ] `paused` is exposed as a binary sensor; if also exposed as a sensor because of the issue's literal field list, it uses a non-conflicting object ID.
- [ ] `pw_net_wh`, `charge_pct`, and charging-window entities are either implemented with matching state payload fields or explicitly deferred without emitting stale templates.
- [ ] Required buttons publish to the expected `<prefix>/cmd/<name>` command topics and document payload mappings.
- [ ] Discovery config messages are published retained with QoS 1 on MQTT connect when `mqtt_enabled=true` and `mqtt_ha_discovery=true`.
- [ ] Discovery config messages are re-published when `homeassistant/status` receives `online`.
- [ ] When `mqtt_ha_discovery=false`, no discovery config messages are published while state/error/availability behavior remains unchanged.
- [ ] `mqtt_ha_discovery_prefix` follows flag > env > yaml precedence and defaults to `homeassistant`.
- [ ] A real Home Assistant instance subscribed to the same broker shows the sbam device with all implemented entities populated; the smoke test is documented in the PR or implementation notes.
- [ ] `make test` passes.
- [ ] `make build` with `CGO_ENABLED=0` passes.

## Test Strategy
- Unit tests in `pkg/mqtt/discovery_test.go`: expected case for a representative config/version, edge case for empty topic/discovery prefixes, and failure/robustness case for missing Fronius IP falling back to client ID/topic prefix.
- Golden-file or canonical JSON assertions: at least one sensor, one binary sensor, and one button payload must be compared structurally against expected JSON.
- Template mapping tests: assert each `value_template` references the intended canonical state JSON field and does not reference fields absent from `StatePayload` unless the implementation adds them.
- Publication tests in `pkg/mqtt` using an in-process MQTT broker: discovery configs are retained on connect and re-published after `homeassistant/status=online`; ensure broker/client cleanup avoids reconnect-test hangs.
- Configuration tests in `pkg/cmd`: verify `mqtt_ha_discovery_prefix` default and flag/env/yaml precedence if the new key is wired through Cobra/Viper.
- Home Assistant add-on surface checks: validate `config.json` schema/options and `run.sh` export changes when the new key is added.

## Risks / Open Questions
- Parent #64's plan originally described `BuildDiscovery(prefix string, device DeviceInfo)`, while issue #85 requires `BuildDiscovery(cfg Config, version string)`. For #85, the issue-specific signature supersedes the parent-plan signature.
- Parent #64 hard-coded the HA discovery root as `homeassistant`; this TASK intentionally adds `mqtt_ha_discovery_prefix` per user clarification while keeping `homeassistant` as the default.
- Current `pkg/mqtt.StatePayload` does not contain `pw_net_wh`, `charge_pct`, or charging-window fields. The PLAN must either add those fields and populate them from schedule state or defer their entities to avoid broken HA templates.
- Issue #85 lists `paused` among sensors and also requires a `binary_sensor.paused`. The implementation must avoid duplicate object IDs; the binary sensor should be authoritative for boolean HA semantics.
- Current `Paho` connect handling publishes availability only. Discovery publication may require a callback/hook or helper that keeps `Client` testable without forcing source packages outside `pkg/mqtt` into discovery generation.
- Actual command execution for buttons may depend on parent-plan command/runner work. Discovery button configs can still be implemented independently, but end-to-end button behavior requires the command subscriber/runner to exist.

## References
- GitHub issue: https://github.com/atbore-phx/sbam/issues/85
- Parent MQTT feed task: `docs/implementations/64-issue-mqtt-feed/64-issue-mqtt-feed-TASK.md`
- Parent MQTT feed plan: `docs/implementations/64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md`
- Dependency issue: https://github.com/atbore-phx/sbam/issues/84
- Home Assistant MQTT Discovery docs: https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery
- Home Assistant MQTT button docs: https://www.home-assistant.io/integrations/button.mqtt/
- Home Assistant MQTT sensor docs: https://www.home-assistant.io/integrations/sensor.mqtt/

## Clarifications
- 2026-05-06: User selected slug `85-issue-ha-mqtt-discovery-payloads`.
- 2026-05-06: Include all optional entities from the prompt (`pw_net_wh`, `charge_pct`, charging-window binary sensors) when matching state fields exist; do not emit stale templates.
- 2026-05-06: Add a configurable HA discovery root prefix with default `homeassistant`.
- 2026-05-06: Base deterministic unique IDs on a Fronius IP hash with a stable MQTT client ID/topic prefix fallback.
- 2026-05-06: Cross-check parent issue #64 TASK/PLAN for compliance and call out mismatches before PLAN generation.
