# PLAN: Remove numeric value from Decision Reason MQTT payload; log Net Power separately

> Issue: [#131](https://github.com/atbore-phx/sbam/issues/131) · TASK: [131-issue-remove-numeric-value-from-decision-reason-mqtt-payload-log-net-power-separately-TASK.md](131-issue-remove-numeric-value-from-decision-reason-mqtt-payload-log-net-power-separately-TASK.md) · Created: 2026-05-24

## Task Analysis

**Goal:** Make `last_decision_reason` MQTT payload strings static (no embedded numeric values) so Home Assistant can group them as single history items.

**Non-goals:** Do NOT change `DecisionReserveCharge` reason string, do NOT change MQTT discovery, do NOT change decision logic, do NOT change `pw_net_wh`.

**Acceptance Criteria:** Clean reason strings for `DecisionForecastCharge` and `DecisionIdle`, separate log line for `pw.Net`, all tests updated, `make test` and `make build` pass.

## Current State

Three reason strings in `ClassifyDecision` (`pkg/fronius/classify.go:48,50,52`) embed numeric values via `fmt.Sprintf`:

| Line | Decision | Current String |
|------|----------|---------------|
| 48 | `DecisionForecastCharge` | `"Net Power (actual battery power + Net solar power) is not enough: %2f Wh"` |
| 50 | `DecisionReserveCharge` | `"battery %2f Wh < reserve %2f Wh"` (out of scope) |
| 52 | `DecisionIdle` | `"Net Power (actual battery power + Net solar power) is enough: %2f Wh"` |

All three are returned as the `reason` string, eventually published as `last_decision_reason` in the MQTT state payload (`pkg/mqtt/types.go:38`).

Tests in `pkg/fronius/classify_test.go:48,61,74` assert the old numeric-containing strings exactly.

The Net Power value is already available separately as `pw_net_wh` in the MQTT state payload (`pkg/mqtt/types.go:35`) and as a "Net Energy" HA discovery entity (`pkg/mqtt/discovery.go:87`).

## Target Architecture

No architectural changes. Only string formatting in `classify.go`, one added log line in `schedule.go`, and test updates in `classify_test.go`.

```
ClassifyDecision (classify.go)
  DecisionForecastCharge → static string (no %2f Wh)
  DecisionIdle           → static string (no %2f Wh)
  DecisionReserveCharge  → unchanged (still has %2f Wh)
  DecisionBatteryFull    → unchanged
  DecisionSkip           → unchanged

SetFroniusChargeBatteryMode (schedule.go)
  Existing log: "Decision: %s - %s" (now cleaner reason strings)
  New log:      "Net Power: %.2f Wh" (pw.Net value)
```

## Dependency Choices

No new dependencies. Uses existing `fmt` (classify.go) and `zap` via `src/utils` (schedule.go).

## Configuration Changes

None. No new CLI flags, config keys, env vars, or HA add-on schema changes.

## Implementation Blueprint

### Step 1 — Clean up reason strings in `pkg/fronius/classify.go`

**File:** `pkg/fronius/classify.go`

**Line 48:** Change the `DecisionForecastCharge` reason from a `fmt.Sprintf` with numeric value to a static string:
```go
// Before:
return DecisionForecastCharge, fmt.Sprintf("Net Power (actual battery power + Net solar power) is not enough: %2f Wh", pw.Net), pw, nil
// After:
return DecisionForecastCharge, "Net Power (actual battery power + Net solar power) is not enough", pw, nil
```

**Line 52:** Change the `DecisionIdle` reason from a `fmt.Sprintf` with numeric value to a static string:
```go
// Before:
return DecisionIdle, fmt.Sprintf("Net Power (actual battery power + Net solar power) is enough: %2f Wh", pw.Net), pw, nil
// After:
return DecisionIdle, "Net Power (actual battery power + Net solar power) is enough", pw, nil
```

**Line 50:** Leave `DecisionReserveCharge` reason string **unchanged** (out of scope).

**Rationale:** The numeric value is redundant with `pw_netWh` in the MQTT payload. Removing it makes the reason string static per decision type, fixing HA history grouping.

### Step 2 — Clean up unused import in `pkg/fronius/classify.go`

After Step 1, check if `fmt` is still used in `classify.go`. `DecisionReserveCharge` (line 50) still uses `fmt.Sprintf`, and `DecisionSkip` (line 54) also uses `fmt.Sprintf`. The `fmt` package is still needed — no import change required.

### Step 3 — Add Net Power log line in `pkg/fronius/schedule.go`

**File:** `pkg/fronius/schedule.go`

**After line 17** (the existing `u.Log.Infof("Decision: %s - %s", ...)` line), add a new log line:
```go
u.Log.Infof("Net Power: %.2f Wh", pw.Net)
```

This preserves the Net Power information in sbam application logs for debugging, using `Infof` (not `Errorf`) to avoid spurious alerts.

The `pw` variable is already available in scope from the `ClassifyDecision` call at line 12-16.

### Step 4 — Update test expectations in `pkg/fronius/classify_test.go`

**File:** `pkg/fronius/classify_test.go`

**Line 48:** Update expected reason string:
```go
// Before:
expectedReason: "Net Power (actual battery power + Net solar power) is not enough: -1900.000000 Wh",
// After:
expectedReason: "Net Power (actual battery power + Net solar power) is not enough",
```

**Line 74:** Update expected reason string:
```go
// Before:
expectedReason: "Net Power (actual battery power + Net solar power) is enough: 9400.000000 Wh",
// After:
expectedReason: "Net Power (actual battery power + Net solar power) is enough",
```

**Line 61:** Leave `DecisionReserveCharge` expectation **unchanged** (still `"battery 1000.000000 Wh < reserve 3000.000000 Wh"`).

## Test Plan

### `pkg/fronius` package

**Expected case (updated existing tests):**
- `TestClassifyDecision` table-driven cases for `DecisionForecastCharge` and `DecisionIdle` assert cleaned static reason strings
- `TestClassifyDecision` case for `DecisionBatteryFull` continues to assert `"Battery is full charged"`

**Edge case (already covered by existing tests):**
- `DecisionReserveCharge` reason string still contains numeric values (unchanged, verified by existing test at line 61)
- `DecisionSkip` default case unchanged (verified by existing test at line 93)

**Failure case:**
- `DecisionSkip` with `forecastChargeEnabled=false, battReserveChargeEnabled=false` still returns error and `"unexpected power state"` prefix

### Log output verification

The new log line `"Net Power: %.2f Wh"` will appear in zap logs. No dedicated log-capture test is required for this simple change — the existing	test structure doesn't capture logs, and the value (`pw.Net`) is already validated by the `"returns power state snapshot"` test case.

## Validation Gates

```bash
make test        # all unit tests pass
make build       # binary compiles
make all         # full validation suite
```

## Rollout / Backward Compatibility

- **Breaking change:** Any HA automation or template that parses `last_decision_reason` expecting the embedded numeric value will break. The numeric data is available via `pw_net_wh` or the "Net Energy" sensor.
- **No migration needed:** No config changes, no schema changes.
- **No HA add-on config update needed.**

## Security Considerations

No security impact. Log output uses `Infof` and only emits already-computed internal state.

## Gotchas

- The `fmt` import in `classify.go` must remain — `DecisionReserveCharge` and `DecisionSkip` still use `fmt.Sprintf`.
- `pw` in `schedule.go` is the full `PowerState` struct returned by `ClassifyDecision`; `pw.Net` is already computed before the log line.

## Open Questions / Risks

- **RESOLVED:** `DecisionReserveCharge` reason string left unchanged (out of scope per issue).
- **ACCEPTED RISK:** Downstream consumers parsing `last_decision_reason` with regex will break. Acceptable because data is redundant.

## Confidence Score

**10/10** — Minimal, surgical changes to string formatting in 3 files with no architectural impact. The issue is exceptionally well-specified with clear acceptance criteria and scope boundaries.
