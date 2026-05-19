# Feature: Home Assistant Slider Controls for Force Charge and Pause

> Source issue: [#128](https://github.com/atbore-phx/sbam/issues/128)
> Fetched: 2026-05-19

> Slug: `128-issue-feature-home-assistant-slider-controls-for-force-charge-and-pause` · Created: 2026-05-19

## Summary
Home Assistant MQTT discovery should expose slider-style controls for `force_charge` and `pause`, paired with explicit send buttons so users can choose a target charge percent or pause duration before publishing the command. The feature replaces the awkward fixed 100% force-charge button behavior with precise values that still respect server-side safety limits unless the user intentionally requests an uncapped full charge.

## Motivation / User Story
As a Home Assistant user, I want to pick a charge target and pause duration with sliders and then press a button to send the command, so I can set precise values quickly without repeatedly pressing buttons or accidentally sending incorrect 100% force-charge commands that ignore `max_charge`.

## Scope
- In scope: add or update Home Assistant MQTT discovery payloads for slider controls related to `force_charge` target percent and `pause` duration.
- In scope: keep an explicit send action so changing a slider does not itself execute the command when the Home Assistant MQTT platform can support that behavior.
- In scope: publish or cause publication of payloads compatible with the existing `PREFIX/cmd/force_charge` and `PREFIX/cmd/pause` command behavior.
- In scope: preserve existing command topics and existing button-based integrations for backward compatibility.
- In scope: enforce existing runtime `max_charge` caps for ordinary `force_charge` requests.
- In scope: support an intentional uncapped full-charge request when the selected force-charge control value is greater than `100`.
- Out of scope: a full Home Assistant UI redesign outside MQTT discovery payloads and documented UX behavior.
- Out of scope: changing unrelated Fronius Modbus safety limits or charge scheduling logic.
- Out of scope: introducing new configuration keys unless implementation research shows opt-in/opt-out behavior is required for backward compatibility.

## Functional Requirements
- Home Assistant discovery must include a slider-like control for selecting a force-charge target percent.
- The force-charge control must represent values from `0` through at least `100`, where values `1..100` map to `target_pct` percentages.
- When a selected force-charge value is between `1` and `100`, the command payload must request `{"target_pct": <n>}` and the server must continue applying the configured `max_charge` cap as it does today.
- When a selected force-charge value is greater than `100`, the value must be treated as a generic `max`/override request meaning `charge_pct=100` without applying the `max_charge` cap.
- The override request must be explicit in the payload contract, using either a special field or a clearly documented convention chosen during implementation.
- Home Assistant discovery must include a slider-like control for selecting pause duration in seconds.
- The pause duration control must use `0..86400` seconds by default.
- A pause duration value of `0` must publish `{}` to request the existing indefinite pause behavior.
- Pause duration values from `1` through `86400` must publish a payload compatible with `parsePauseUntil`, such as `{"until":"3600s"}`.
- The implementation must prefer a slider plus explicit send-button UX using Home Assistant MQTT `command_template` if viable.
- If Home Assistant MQTT discovery cannot safely implement a separate send button with slider state using `command_template`, the PLAN must identify the limitation and propose the smallest compatible fallback.
- Existing consumers that publish directly to `PREFIX/cmd/force_charge` and `PREFIX/cmd/pause` must continue to work.

## Non-functional Requirements
- Backward compatibility: existing MQTT command topics and existing button-based integrations must remain functional.
- Safety / defaults: server-side `max_charge` caps remain enforced for ordinary force-charge values `1..100`.
- Safety / defaults: uncapped full charge requires an explicit value greater than `100` and must not be triggered by the default send button payload.
- Usability: slider changes should not execute commands when the selected Home Assistant MQTT approach can provide a separate explicit send action.
- Performance: discovery payload generation must remain local and deterministic with no additional runtime network calls.
- Maintainability: payload builders and command parsing changes must follow existing `pkg/mqtt` patterns and be covered by focused unit tests.

## Configuration Impact
- New CLI flags: none expected.
- New config keys (`config.yaml`): none expected unless the PLAN justifies an opt-in/out switch for the new discovery controls.
- New env vars: none expected.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): none expected unless a new config key is introduced.
- MQTT discovery: add or change Home Assistant discovery entities in `pkg/mqtt/discovery.go`, likely including `number` entities and send buttons for force charge and pause.

## External Integrations Touched
- MQTT discovery: Home Assistant MQTT discovery payloads under the configured discovery prefix.
- MQTT command topics: `PREFIX/cmd/force_charge` and `PREFIX/cmd/pause`.
- MQTT command parsing: `pkg/mqtt/commands.go` if an explicit override field or additional payload convention is introduced.
- Schedule command handling: `pkg/cmd/schedule_runner.go` if force-charge intent needs an uncapped/full-charge flag propagated to runner logic.
- Fronius charge handling: `pkg/fronius` only if uncapped full charge requires a new target-resolution path separate from existing capped behavior.
- Solcast: not touched.
- Fronius Solar API: not touched directly.
- Fronius Modbus registers: no new registers expected; existing force-charge write behavior may be reused.

## Acceptance Criteria
- [ ] Home Assistant discovers slider-style controls for force-charge target percent and pause duration.
- [ ] Home Assistant discovers explicit send actions for force charge and pause, or the PLAN documents why pure MQTT discovery cannot provide that UX and selects the smallest compatible fallback.
- [ ] Moving a slider alone does not execute the command when the selected Home Assistant MQTT approach supports a separate send action.
- [ ] Pressing the force-charge send action publishes or causes handling of a valid payload compatible with existing `force_charge` command behavior.
- [ ] Force-charge values `1..100` remain subject to the configured `max_charge` cap.
- [ ] Force-charge values greater than `100` request full charge at `100%` without applying the `max_charge` cap.
- [ ] Pressing the pause send action with slider value `0` requests the existing indefinite pause behavior using `{}`.
- [ ] Pressing the pause send action with values `1..86400` publishes an `until` duration string accepted by `parsePauseUntil`, for example `{"until":"3600s"}`.
- [ ] Existing button-based clients and direct MQTT publishers for `cmd/force_charge` and `cmd/pause` continue to function.
- [ ] Unit tests cover discovery payload JSON, command parsing, capped force-charge handling, uncapped full-charge handling, pause duration handling, and invalid payloads.

## Test Strategy
- Unit tests in `pkg/mqtt` should verify discovery payloads for new `number` entities, send buttons, command topics, templates, min/max/step values, and retained/QoS behavior.
- Unit tests in `pkg/mqtt` should verify parsing for ordinary `force_charge` payloads, the explicit full-charge override convention, ordinary pause durations, indefinite pause `{}`, and invalid inputs.
- Unit tests in `pkg/cmd` should verify runner command handling preserves capped behavior for `1..100` and allows explicit uncapped full charge only through the agreed override convention.
- Unit tests in `pkg/fronius` should cover target resolution if force-charge capping logic changes there.
- Edge cases: force-charge `0`, `1`, `100`, `101`, malformed values, pause `0`, `1`, `86400`, negative pause values, non-duration strings, and RFC3339 values if still supported by existing parsing.
- Failure cases: invalid JSON payloads, invalid number values, and command payloads that request override without the explicit convention.
- Validation should include `go test ./...`, `make test`, and focused package tests for any modified packages.

## Risks / Open Questions
- Home Assistant MQTT discovery may not support the desired slider plus separate send-button UX entirely through static discovery payloads. The preferred path is to use `command_template` if viable; if not viable, the PLAN must document the limitation and propose the smallest compatible fallback.
- The exact payload convention for uncapped full charge is not yet chosen. It should be explicit, testable, and backward-compatible with existing `target_pct` payloads.
- The issue references slider values greater than `100`; Home Assistant `number` entity metadata must make that intentional without making ordinary users likely to select it accidentally.
- Manual Home Assistant validation is still required after implementation because discovery payloads can be syntactically valid but awkward in the UI.

## References
- [Issue #128](https://github.com/atbore-phx/sbam/issues/128)
- [Related PR #127](https://github.com/atbore-phx/sbam/pull/127)
- [pkg/mqtt/discovery.go](../../../pkg/mqtt/discovery.go)
- [pkg/mqtt/commands.go](../../../pkg/mqtt/commands.go)
- [pkg/cmd/schedule_runner.go](../../../pkg/cmd/schedule_runner.go)

## Clarifications
- 2026-05-19: Use slug `128-issue-feature-home-assistant-slider-controls-for-force-charge-and-pause`.
- 2026-05-19: Pause slider value `0` should publish `{}` for the existing indefinite pause behavior.
- 2026-05-19: Pause slider maximum should be `86400` seconds.
- 2026-05-19: Force-charge values greater than `100` should be treated as a generic `max` value meaning `charge_pct=100` without applying the configured cap.
- 2026-05-19: Prefer Home Assistant MQTT `command_template` for the slider plus explicit send-button UX if viable.