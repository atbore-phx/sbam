# PLAN: Remove numeric value from DecisionReserveCharge reason string

> Issue: [#134](https://github.com/atbore-phx/sbam/issues/134) · TASK: [134-issue-remove-numeric-value-from-decision-reserve-charge-reason-TASK.md](134-issue-remove-numeric-value-from-decision-reserve-charge-reason-TASK.md) · Created: 2026-05-24

## Task Analysis

**Goal:** Make the `DecisionReserveCharge` reason string static (no embedded battery/reserve Wh values) so Home Assistant can group it as a single history item. Mirrors the #131 approach already applied to `DecisionForecastCharge` and `DecisionIdle`.

**Non-goals:** Do NOT change the `DecisionReserveCharge` decision logic, do NOT change other decision types, do NOT change MQTT discovery, do NOT change `pw_batt` / `pw_batt_reserve` MQTT fields.

**Acceptance Criteria:** Clean reason string for `DecisionReserveCharge`, separate log line for battery and reserve Wh values, all tests updated, `make test` and `make build` pass.

## Current State

The `DecisionReserveCharge` reason string in `ClassifyDecision` (`pkg/fronius/classify.go:50`) embeds numeric values via `fmt.Sprintf`:

```go
return DecisionReserveCharge, fmt.Sprintf("battery %2f Wh < reserve %2f Wh", pw.Batt, pwBattReserve), pw, nil
```

The other two numeric reason strings (`DecisionForecastCharge` and `DecisionIdle`) were already cleaned in #131 and are now static.

`SetFroniusChargeBatteryMode` (`pkg/fronius/schedule.go:18`) already logs the Net Power value (added in #131):
```go
u.Log.Infof("Net Power: %.2f Wh", pw.Net)
```

Tests in `pkg/fronius/classify_test.go:61` assert the old numeric-containing string:
```go
expectedReason: "battery 1000.000000 Wh < reserve 3000.000000 Wh",
```

The battery and reserve values are already available separately as `pw_batt` and `pw_batt_reserve` in the MQTT state payload (`pkg/mqtt/types.go`).

## Target Architecture

No architectural changes. Only string formatting in `classify.go`, one added log line in `schedule.go`, and test updates in `classify_test.go`.

```
ClassifyDecision (classify.go)
  DecisionReserveCharge  → static string (no %2f Wh)  ← THIS CHANGE
  DecisionForecastCharge → static string (already done in #131)
  DecisionIdle           → static string (already done in #131)
  DecisionBatteryFull    → static string (unchanged)
  DecisionSkip           → dynamic debug string (unchanged)

SetFroniusChargeBatteryMode (schedule.go)
  Existing log: "Decision: %s - %s" (already clean for Net Power decisions)
  Existing log: "Net Power: %.2f Wh" (added in #131)
  New log:      "Battery: %.2f Wh, Reserve: %.2f Wh" (pw.Batt, pwBattReserve)
```

## Dependency Choices

No new dependencies. Uses existing `fmt` (classify.go — still needed for `DecisionSkip`) and `zap` via `src/utils` (schedule.go).

## Configuration Changes

None. No new CLI flags, config keys, env vars, or HA add-on schema changes.

## Implementation Blueprint

### Step 1 — Clean up `DecisionReserveCharge` reason string in `pkg/fronius/classify.go`

**File:** `pkg/fronius/classify.go`

**Line 50:** Change the `DecisionReserveCharge` reason from a `fmt.Sprintf` with numeric values to a static string:
```go
// Before:
return DecisionReserveCharge, fmt.Sprintf("battery %2f Wh < reserve %2f Wh", pw.Batt, pwBattReserve), pw, nil
// After:
return DecisionReserveCharge, "Battery charge is below reserve threshold", pw, nil
```

**Rationale:** The numeric values are redundant with `pw_batt` and `pw_batt_reserve` in the MQTT payload. Removing them makes the reason string static per decision type, fixing HA history grouping.

### Step 2 — Verify `fmt` import is still needed in `pkg/fronius/classify.go`

After Step 1, `DecisionSkip` (line 54) still uses `fmt.Sprintf` for its debug struct dump. The `fmt` package import remains needed — no import change.

### Step 3 — Add battery/reserve log line in `pkg/fronius/schedule.go`

**File:** `pkg/fronius/schedule.go`

**After line 18** (the existing `u.Log.Infof("Net Power: %.2f Wh", pw.Net)` line), add a new log line:
```go
u.Log.Infof("Battery: %.2f Wh, Reserve: %.2f Wh", pw.Batt, pwBattReserve)
```

This preserves the battery and reserve Wh information in sbam application logs for debugging, using `Infof` (not `Errorf`) to avoid spurious alerts.

The `pw` variable is already available in scope from the `ClassifyDecision` call at lines 12-16.

### Step 4 — Update test expectations in `pkg/fronius/classify_test.go`

**File:** `pkg/fronius/classify_test.go`

**Line 61:** Update expected reason string:
```go
// Before:
expectedReason: "battery 1000.000000 Wh < reserve 3000.000000 Wh",
// After:
expectedReason: "Battery charge is below reserve threshold",
```

All other test cases remain unchanged — `DecisionBatteryFull`, `DecisionForecastCharge`, `DecisionIdle`, and `DecisionSkip` expectations are unaffected.

## Test Plan

### `pkg/fronius` package

**Expected case (updated existing test):**
- `TestClassifyDecision` table-driven case for `DecisionReserveCharge` asserts the new static reason string `"Battery charge is below reserve threshold"`

**Edge cases (already covered by existing tests):**
- `DecisionBatteryFull` reason string `"Battery is full charged"` unchanged (line 35)
- `DecisionForecastCharge` reason string unchanged (line 48)
- `DecisionIdle` reason string unchanged (line 74)

**Failure case:**
- `DecisionSkip` with `forecastChargeEnabled=false, battReserveChargeEnabled=false` still returns error and `"unexpected power state"` prefix (line 93)

### Log output verification

The new log line `"Battery: %.2f Wh, Reserve: %.2f Wh"` will appear in zap logs alongside the existing Net Power log. No dedicated log-capture test is required — the existing test structure doesn't capture logs, and the values (`pw.Batt`, `pwBattReserve`) are already validated by the `"returns power state snapshot"` test case.

## Validation Gates

```bash
make test        # all unit tests pass
make build       # binary compiles
make all         # full validation suite
```

## Rollout / Backward Compatibility

- **Breaking change:** Any HA automation or template that parses `last_decision_reason` expecting the `"battery X Wh < reserve Y Wh"` format will break. The numeric data is available via `pw_batt` and `pw_batt_reserve` state attributes.
- **No migration needed:** No config changes, no schema changes.
- **No HA add-on config update needed.**

## Security Considerations

No security impact. Log output uses `Infof` and only emits already-computed internal state.

## Gotchas

- The `fmt` import in `classify.go` must remain — `DecisionSkip` (line 54) still uses `fmt.Sprintf`.
- `pw` in `schedule.go` is the full `PowerState` struct returned by `ClassifyDecision`; `pw.Batt` and the `pwBattReserve` parameter are already in scope before the log line.
- This is the last numeric reason string in the decision classification — after this change, `DecisionSkip` is the only one using `fmt.Sprintf`.

## Open Questions / Risks

- **RESOLVED:** #131 (Net Power decisions) is already complete — this mirrors its approach.
- **ACCEPTED RISK:** Downstream consumers parsing `last_decision_reason` with regex for the `"battery X Wh < reserve Y Wh"` pattern will break. Acceptable because data is available via `pw_batt` and `pw_batt_reserve` MQTT fields.

## Confidence Score

**10/10** — Single-line string change in `classify.go`, one log line in `schedule.go`, one test expectation update in `classify_test.go`. The approach is validated by the already-merged #131 implementation.
