# Feature: Remove numeric value from Decision Reason MQTT payload; log Net Power separately

> Source issue: [#131](https://github.com/atbore-phx/sbam/issues/131)
> Fetched: 2026-05-24
> Slug: `131-issue-remove-numeric-value-from-decision-reason-mqtt-payload-log-net-power-separately` · Created: 2026-05-24

## Summary

Remove the numeric Net Power value from the MQTT `last_decision_reason` payload so the string is static per decision type, and instead log the Net Power value separately in the sbam application log. This allows Home Assistant to group and graph decision reasons as a single item in the History tab.

## Motivation / User Story

As a Home Assistant user,
I want the `last_decision_reason` MQTT payload to be a static string without embedded numeric values,
So that HA treats each decision reason as a single item in the History tab and graph instead of creating a new item every time the numeric value changes.

Currently, the MQTT state payload publishes `"last_decision_reason": "Net Power (actual battery power + Net solar power) is enough: 35420.173000 Wh"`. Because the Wh value changes every tick, HA sees each unique string as a new entry. The Net Power value is already available as a separate numeric sensor via `pw_net_wh` in the same state payload and as a dedicated "Net Energy" HA discovery entity.

## Scope

**In scope:**
- Remove the numeric `%2f Wh` value from the `ClassifyDecision` reason strings in `pkg/fronius/classify.go` for the two Net Power decisions (`DecisionForecastCharge` and `DecisionIdle`)
- Add a separate log line (zap `Infof`) that logs the Net Power value (`pw.Net`) so the information is still available in sbam logs for debugging
- Update all tests referencing the old reason string format

**Out of scope:**
- Removing or changing the `pw_net_wh` MQTT state field (numeric Net Energy sensor remains unchanged)
- Changing the MQTT discovery configuration for the "Net Energy" entity
- Changing any other MQTT payload fields
- Changing the decision logic itself
- The `DecisionReserveCharge` reason string (`"battery %2f Wh < reserve %2f Wh"`) — this also contains numeric values but is explicitly out of scope for this issue (may be a follow-up)

## Functional Requirements

- [ ] `ClassifyDecision` reason strings for `DecisionForecastCharge` and `DecisionIdle` must no longer include the numeric Net Power value (e.g., `"Net Power (actual battery power + Net solar power) is enough"` instead of `"Net Power (actual battery power + Net solar power) is enough: 35420.173000 Wh"`)
- [ ] A zap log line must be added (in `SetFroniusChargeBatteryMode` in `pkg/fronius/schedule.go`) that prints the Net Power value (`pw.Net`) in Wh
- [ ] The `last_decision_reason` field in the MQTT state payload must contain the cleaned string without any numeric value
- [ ] The `pw_net_wh` numeric field in the MQTT state payload must remain unchanged and continue to be published

## Non-functional Requirements

**Backward compatibility:**
- This is a **breaking change** for any automation or template that parses the old reason string expecting the numeric value. Users who need the value should reference the `pw_net_wh` state attribute or the "Net Energy" HA sensor instead.
- No config key changes, no CLI flag changes, no env var changes, no add-on config schema impact.

**Safety / defaults:**
- Logging must use `Infof` (not `Errorf` or higher) for the Net Power value to avoid spurious alerts.
- The decision logic itself is unchanged; only the human-readable reason string and log output are affected.

**Performance / reliability:**
- No additional external calls. The Net Power value is already computed in `ClassifyDecision`; we only log it.

## Configuration Impact

- New CLI flags: none
- New config.yaml keys: none
- New env vars: none
- Home Assistant add-on config schema impact: none

## External Integrations Touched

- Solcast: none
- Fronius Solar API: none
- Fronius Modbus registers: none
- MQTT topics: `last_decision_reason` field value format change (no topic name change)

## Acceptance Criteria

- [ ] `ClassifyDecision` returns `"Net Power (actual battery power + Net solar power) is not enough"` (no numeric value) for `DecisionForecastCharge`
- [ ] `ClassifyDecision` returns `"Net Power (actual battery power + Net solar power) is enough"` (no numeric value) for `DecisionIdle`
- [ ] A log line containing the Net Power value (e.g., `"Net Power: %.2f Wh"`) is emitted on every classification
- [ ] `pw_net_wh` continues to be published in the MQTT state payload
- [ ] `go test ./...` passes with updated test expectations
- [ ] `make build` succeeds
- [ ] `make all` passes

## Test Strategy

**Unit tests (expected case):**
- Existing tests in `pkg/fronius/classify_test.go` that assert the old reason string format (containing `%2f Wh`) must be updated to match the new static strings.
- A new test or assertion must verify that the cleaned reason string matches exactly (e.g., `assert.Equal(t, "Net Power (actual battery power + Net solar power) is enough", reason)`).

**Edge case:**
- Test that the reason string for `DecisionBatteryFull` (`"Battery is full charged"`) and `DecisionReserveCharge` (`"battery %2f Wh < reserve %2f Wh"`) are **not** changed.

**Failure case:**
- The `DecisionSkip` default case reason string (debug struct dump) is unchanged.

**Integration/validation commands:**
- `make test`
- `make build`

## Risks / Open Questions

- The `DecisionReserveCharge` reason string (`"battery %2f Wh < reserve %2f Wh"`) also contains numeric values and would cause the same HA history fragmentation. This is **out of scope** for this issue but may be a follow-up.
- Downstream consumers (e.g., automations parsing `last_decision_reason` with regex) will break. This is acceptable because the numeric data was redundant with `pw_net_wh`.

## References

- `pkg/fronius/classify.go` — `ClassifyDecision` function (lines 30–57)
- `pkg/fronius/schedule.go` — `SetFroniusChargeBatteryMode` (lines 5–44), current log at line 17
- `pkg/fronius/classify_test.go` — unit tests for ClassifyDecision
- `pkg/mqtt/types.go` — `StatePayload.LastDecisionReason` field (line 38)
- `pkg/mqtt/discovery.go` — "Net Energy" HA discovery entity (line 87, reads `pw_net_wh`)
