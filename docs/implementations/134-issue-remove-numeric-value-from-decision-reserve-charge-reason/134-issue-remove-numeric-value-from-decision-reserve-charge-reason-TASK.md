# Feature: Remove numeric value from DecisionReserveCharge reason string

> Source issue: [#134](https://github.com/atbore-phx/sbam/issues/134)
> Fetched: 2026-05-24
> Slug: `134-issue-remove-numeric-value-from-decision-reserve-charge-reason` · Created: 2026-05-24

## Summary

Remove the numeric battery and reserve Wh values from the `DecisionReserveCharge` MQTT `last_decision_reason` payload so the string is static per decision type, and instead log the battery and reserve values separately in the sbam application log. This mirrors issue #131 and allows Home Assistant to group and graph the reserve charge decision as a single item in the History tab.

## Motivation / User Story

As a Home Assistant user,
I want the `last_decision_reason` MQTT payload for `DecisionReserveCharge` to be a static string without embedded numeric values,
So that HA treats the reserve charge decision as a single item in the History tab and graph instead of creating a new entry every time the battery or reserve values change.

Currently, the MQTT state payload publishes `"last_decision_reason": "battery 12345.678900 Wh < reserve 5000.000000 Wh"`. Because the Wh values change every tick, HA sees each unique string as a new entry. The battery and reserve values are already available as separate numeric sensors via the individual MQTT state fields (`pw_batt`, `pw_batt_reserve`).

## Scope

**In scope:**
- Remove the numeric `%2f Wh` values from the `DecisionReserveCharge` reason string in `pkg/fronius/classify.go`
- The new reason string for `DecisionReserveCharge` should be static (e.g., `"Battery charge is below reserve threshold"`)
- Add a separate zap `Infof` log line that logs the battery and reserve Wh values so the information is still available in sbam logs for debugging
- Update all tests referencing the old `DecisionReserveCharge` reason string format

**Out of scope:**
- Changing the `DecisionReserveCharge` decision logic itself
- Changing any other MQTT payload fields or decision types (these were handled in #131)
- Changing the MQTT discovery configuration

## Functional Requirements

- [ ] The `DecisionReserveCharge` reason string in `ClassifyDecision` must no longer include numeric battery/reserve Wh values (e.g., `"Battery charge is below reserve threshold"` instead of `"battery %2f Wh < reserve %2f Wh"`)
- [ ] A zap log line must be added (in `ClassifyDecision` or its immediate caller) that prints the battery and reserve Wh values
- [ ] The `last_decision_reason` field in the MQTT state payload must contain the cleaned string without any numeric values
- [ ] The individual `pw_batt` and `pw_batt_reserve` numeric fields in the MQTT state payload must remain unchanged

## Non-functional Requirements

**Backward compatibility:**
- This is a **breaking change** for any automation or template that parses the old reserve reason string expecting the numeric values. Users who need the values should reference the `pw_batt` and `pw_batt_reserve` state attributes instead.
- No config key changes, no CLI flag changes, no env var changes, no add-on config schema impact.

**Safety / defaults:**
- Logging must use `Infof` (not `Errorf` or higher) for the battery/reserve values to avoid spurious alerts.
- The decision logic itself is unchanged; only the human-readable reason string and log output are affected.

**Performance / reliability:**
- No additional external calls. The battery and reserve values are already computed in `ClassifyDecision`; we only change how they are represented.

## Configuration Impact

- New CLI flags: none
- New config.yaml keys: none
- New env vars: none
- Home Assistant add-on config schema impact: none

## External Integrations Touched

- Solcast: none
- Fronius Solar API: none
- Fronius Modbus registers: none
- MQTT topics: `last_decision_reason` field value format change for reserve charge decisions (no topic name change)

## Acceptance Criteria

- [ ] `ClassifyDecision` returns a static reason string (no numeric values) for `DecisionReserveCharge`
- [ ] A log line containing the battery and reserve Wh values is emitted on every reserve charge classification
- [ ] `pw_batt` and `pw_batt_reserve` continue to be published in the MQTT state payload
- [ ] `go test ./...` passes with updated test expectations
- [ ] `make build` succeeds
- [ ] `make all` passes

## Test Strategy

**Unit tests (expected case):**
- Existing tests that assert the old `DecisionReserveCharge` reason string format (containing `%2f Wh`) must be updated to match the new static string.
- A new test or assertion must verify that the cleaned reason string matches exactly (e.g., `assert.Equal(t, "Battery charge is below reserve threshold", reason)`).
- A log-capture test or assertion verifies the battery/reserve values are logged with correct formatting.

**Edge case:**
- Verify that the reason strings for `DecisionBatteryFull`, `DecisionForecastCharge`, `DecisionIdle`, and `DecisionSkip` are **not** affected by this change.

**Failure case:**
- The `DecisionSkip` default case reason string (debug struct dump) is unchanged.

**Integration/validation commands:**
- `make test`
- `make build`

## Risks / Open Questions

- #131 (Net Power decisions) is already complete. The approach here mirrors that implementation.
- Consider whether `pw_batt` and `pw_batt_reserve` should be published as dedicated HA discovery entities (similar to "Net Energy") if they aren't already — out of scope for this issue.

## References

- Issue #131: Remove numeric value from Decision Reason MQTT payload; log Net Power separately (completed)
- `pkg/fronius/classify.go` — `ClassifyDecision` function, `DecisionReserveCharge` case at line 50
- `pkg/fronius/schedule.go` — `SetFroniusChargeBatteryMode` caller
- `pkg/fronius/classify_test.go` — unit tests for ClassifyDecision
