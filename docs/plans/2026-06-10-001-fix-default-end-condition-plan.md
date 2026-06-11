---
title: "fix: Prevent tick from overriding set_defaults reset at end of charge window"
type: fix
date: 2026-06-10
origin: docs/brainstorms/2026-06-10-default-end-condition-requirements.md
---

## Summary

Add a 5-minute cooldown gate at the tail of the charging window. When the current time is within 5 minutes of `end_hr`, Tick refuses new charge decisions so the set_defaults cron (which fires at `end_hr - 5 min`) cannot be overridden by a simultaneous periodic tick.

## Problem Frame

`crontabSchedule` installs two cron jobs when `--defaults` is enabled: the periodic tick and a set_defaults reset at `end_hr - 5 min`. When the main crontab aligns with the reset minute (e.g., `*/5 * * * *` at `06:50` with `end_hr=06:55`), both submit intents to the runner simultaneously. The runner serializes them via its buffered channel, but Go scheduler ordering is non-deterministic. If Tick dequeues after SetDefaults, Tick writes a force-charge (e.g., `reserve_charge` at 1%), overriding the reset. The battery stays in force charge indefinitely because subsequent ticks outside the window only log and return. See issue 165 and `docs/brainstorms/2026-06-10-default-end-condition-requirements.md`.

## Requirements

- R1. Tick must refuse charge decisions when the current time is within the last 5 minutes before `end_hr`.
- R2. The cooldown must use the same wall clock, timezone, and cross-midnight logic as the existing `checkTimeRangeAt` window check.
- R3. Ticks outside the charging window continue to log and return without modifying the battery.

## Key Technical Decisions

- **5-minute cooldown, hardcoded constant.** Matches the fixed offset at which `crontabSchedule` schedules the set_defaults cron (`end_hr - 5 min`). Defined as a package-level `const` alongside the existing schedule constants.
- **Applied in Tick, not in cron scheduling.** The race is resolved at the decision point (the Tick handler) rather than by changing cron timing or merging the two cron entries. This preserves the separate set_defaults entry as a belt-and-suspenders guarantee.
- **Cross-midnight cooldown uses the same calendar principles as `checkTimeRangeAt`.** The cooldown check is a proximity computation (distance to `end_hr`), not a containment check, so the arithmetic differs structurally from `checkTimeRangeAt`'s OR expression — but it uses the same clock parsing, same timezone handling via `now.Location()`, and the same 24-hour adjustment when the window spans midnight and `now` falls on the start-day side.

## Implementation Units

### U1. Add cooldown check to Tick handler

- **Goal:** Prevent Tick from invoking the Fronius charge handler when the current time is within the cooldown period (last 5 minutes before `end_hr`).
- **Requirements:** R1, R2
- **Dependencies:** none
- **Files:**
  - `pkg/cmd/schedule_runner.go` — add cooldown constant and cooldown check in `Tick`
  - `pkg/cmd/schedule_runner_test.go` — add test coverage
- **Patterns to follow:**
  - `checkTimeRangeAt` in `pkg/cmd/schedule_runner.go:534-550` — the existing window containment check uses `parseScheduleClock`, `isCrossMidnightWindow`, and `time.Date` with `now.Location()`. The cooldown check mirrors this pattern.
  - `runnerIntentQueueSize` at `pkg/cmd/schedule_runner.go:15` — the existing file-level constant; add the cooldown constant alongside it.
- **Approach:**
  1. Add a constant `chargeCooldownMinutes = 5` in `schedule_runner.go`.
  2. Add a helper `isInCooldown(now time.Time, startHR, endHR string) (bool, error)` that:
     - Parses `endHR` via `parseScheduleClock`.
     - Builds `endAt` using `time.Date` with `now`'s date and location.
     - For cross-midnight windows where `now` is after `startHR` (on the start side), adds 24 hours to `endAt`.
     - Returns `true` when `now` is at or after `endAt - 5 min` and before `endAt`.
  3. In `Tick`, after the `checkTimeRangeAt` passes and before the storage/power/fronius handler chain, call `isInCooldown`. If `true`, log "cooldown active" and return without charging, publishing a state payload with reason `"cooldown active — charge decisions suppressed near window end"`.
  4. The cooldown path must still publish a state payload (via `makeBasePayload` and `publishState`) so MQTT consumers see the current state even when no charge decision was made. Include `BatterySOCPct` and `BatteryCapacityWh` from the already-retrieved storage data — matching the `!inChargeWindow` path at line 203-204.
- **Test scenarios:**
  - Tick at `end_hr - 4 min` inside a same-day window — cooldown active, Tick publishes state with cooldown reason, Fronius handler is NOT called, no Modbus write occurs.
  - Tick at `end_hr - 6 min` inside a same-day window — cooldown not active, Tick proceeds normally through the Fronius handler.
  - Tick at `end_hr - 4 min` inside a cross-midnight window, `now` after midnight — cooldown active, Tick suppressed.
  - Tick at `end_hr - 4 min` inside a cross-midnight window, `now` before midnight — cooldown active, Tick suppressed.
  - Tick at `end_hr` — `checkTimeRangeAt` returns false, falls through to existing outside-window path (unchanged behavior).
  - `isInCooldown` returns error when `endHR` is unparseable — error surfaces, Tick does not charge.
- **Verification:** `go test ./pkg/cmd/ -run TestRunner_Tick -v` passes with the new test cases. Existing Tick tests continue to pass unchanged.

## Scope Boundaries

- Not changing the cron scheduling logic in `crontabSchedule`.
- Not making the cooldown duration configurable.
- Not altering the set_defaults reset path in `handleSetDefaults`.

## Sources

- `pkg/cmd/schedule.go:505-549` — `crontabSchedule`: installs both cron entries
- `pkg/cmd/schedule_runner.go:157-257` — `Tick`: the scheduling cycle handler
- `pkg/cmd/schedule_runner.go:534-550` — `checkTimeRangeAt`: window containment, cross-midnight pattern to follow
- `pkg/cmd/schedule_runner.go:527-529` — `isCrossMidnightWindow`: helper used by the cooldown logic
- `pkg/cmd/schedule.go:255-261` — `parseScheduleClock`: parses "15:04" clock strings
- `docs/brainstorms/2026-06-10-default-end-condition-requirements.md` — origin requirements
