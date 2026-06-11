---
date: 2026-06-10
topic: default-end-condition
---

## Summary

Add a 5-minute cooldown at the tail of the charging window: the Tick handler refuses new charge decisions when the current time is within 5 minutes of `end_hr`, preventing the periodic tick from overriding the set_defaults reset that fires at the same boundary.

## Problem Frame

When the `schedule` command runs with `--defaults` (enabled by default), `crontabSchedule` installs two cron jobs: the main periodic tick and a set_defaults reset that fires at `end_hr - 5 min`. When the main crontab aligns with the reset (e.g., `0/5 * * * *` at 06:50 with `end_hr=06:55`), both submit intents to the runner simultaneously. The runner serializes them, but Go scheduler ordering is non-deterministic. If Tick processes after SetDefaults, Tick's charge decision (e.g., `reserve_charge`) overrides the reset, leaving the battery in force-charge mode. Subsequent ticks outside the window log and return without resetting, so the battery stays in force charge indefinitely (issue 165).

## Requirements

- R1. Tick must refuse new charge decisions when the current time is within 5 minutes before `end_hr`.
- R2. The cooldown check must use the same wall clock and timezone as the existing window containment check (`checkTimeRangeAt`).
- R3. Ticks outside the charging window continue to log and return without action (existing behavior unchanged).

## Key Decisions

- **5-minute cooldown, hardcoded.** Matches the fixed offset at which `crontabSchedule` schedules the set_defaults cron (`end_hr - 5 min`). If that offset ever becomes configurable, the cooldown must follow it.
- **Applied in Tick, not in cron scheduling.** The race is resolved at the decision point rather than by changing cron timing or merging cron entries. This keeps the separate reset entry intact as a belt-and-suspenders guarantee.

## Acceptance Examples

- AE1. Tick at end_hr minus 5 minutes, inside the window — Tick runs, `inChargeWindow` is true, but cooldown is active: no Fronius handler is invoked, no charge decision is made, and the battery is not modified.
- AE2. Tick at end_hr minus 6 minutes, inside the window — Tick runs, cooldown is not active, charge decision proceeds normally.
- AE3. Tick at end_hr, outside the window — existing `!inChargeWindow` path fires: log "outside the range" and return without modifying the battery.

## Scope Boundaries

- Not changing the cron scheduling logic in `crontabSchedule`.
- Not making the cooldown duration configurable.
- Not altering the set_defaults reset path in `handleSetDefaults`.
- Not touching the Modbus write layer.

## Sources

- `pkg/cmd/schedule.go:505-549` — `crontabSchedule`: installs both cron entries.
- `pkg/cmd/schedule_runner.go:198-207` — `Tick`: the `!inChargeWindow` path.
- `pkg/cmd/schedule_runner.go:262-268` — `handleIntent`: dispatches `IntentTick`.
- `pkg/cmd/schedule_runner.go:534-550` — `checkTimeRangeAt`: window containment check.
- GitHub issue 165 — [Bug]: sbam doesn't always reset the battery to normal operation at the end of the non-peak time.
