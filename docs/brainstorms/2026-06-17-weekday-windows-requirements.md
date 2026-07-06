---
date: 2026-06-17
topic: weekday-windows
---

## Summary

Add optional `weekdays` filtering to charge windows so users can configure
different charging behavior on different days of the week. The feature is
gated behind `weekday_feature` (default `true`) as a kill switch.

## Requirements

### Configuration

- R1. Each window accepts an optional `weekdays` field. When absent or
  empty, the window is active every day (backward compatible).
- R2. `weekdays` accepts one or more weekdays in any combination of:
  - `mon` — single day
  - `mon,fri` — comma-separated individual days
  - `mon-fri` — inclusive range
  - `mon-fri,sun` — range and individual day combined
- R3. Day names use lowercase 3-letter English abbreviations:
  `mon`, `tue`, `wed`, `thu`, `fri`, `sat`, `sun`.

### Resolution (Start-Day Model)

- R4. A window's weekday applies to the **day the window starts**. For a
  same-day window this is the current calendar day. For a cross-midnight
  window, the post-midnight portion inherits the start day — `23:00–03:00`
  on `fri` means Friday 23:00 through Saturday 03:00.

### Validation

- R5. `ValidateWindows` rejects unknown weekday tokens (e.g., `mon-fri,xyz`)
  with a message naming the invalid token.
- R6. `ValidateWindows` rejects empty elements (`mon,,fri`) with a message
  naming the offending entry.
- R7. `ValidateWindows` does not flag two windows as overlapping when their
  weekday sets are disjoint, even if their clock ranges overlap.

### Scheduling

- R8. `ResolveActiveWindow` returns `nil` for a window whose weekday set
  does not include the resolved start day.
- R9. `scheduleBoundaryTick` computes the next future time a window starts on
  a matching weekday, across multiple days if needed, and picks the earliest.

### Feature Flag

- R10. `weekday_feature` is a top-level boolean (default `true`). When
  `false`, the `weekdays` field is ignored at validation and resolution
  time — all windows are active every day.
- R11. When `weekday_feature` is `false`, `ValidateWindows` skips weekday
  token validation and does not perform weekday-set overlap skipping.

## Key Decisions

- **Start-day model.** A window is active on the weekday its `start` time
  falls on, regardless of whether `end` spills into the next day. This avoids
  splitting cross-midnight windows into two unintuitive entries.
- **Format: single, comma, range.** Parsing accepts `mon`, `mon,fri`,
  `mon-fri`, and `mon-fri,sun`. Ranges are always start–end (mon–fri, not
  fri–mon). Overlapping ranges are flattened so `mon-wed,tue-thu` produces
  `[mon, tue, wed, thu]`.
- **Feature flag default true.** The flag ships enabled. It exists as a
  maintainer kill switch — set to `false` in `config.yaml` to disable all
  weekday logic without a code change.
- **Omitted = all days.** An empty or absent `weekdays` field means the
  window is active every day. Existing configs work without modification.

## Acceptance Examples

### AE1. Beta-tester use case — weekday cross-midnight

```yaml
weekday_feature: true
scheduler_mode: windows
windows:
  - name: "weekday-offpeak"
    start: "21:00"
    end: "07:00"
    max_charge: 3500
    weekdays: "mon-fri"
```

| Time | Active? | Why |
|---|---|---|
| Monday 22:00 | Yes | Start day is Monday, in range |
| Tuesday 03:00 | Yes | Start-day Monday's post-midnight tail |
| Friday 23:00 | Yes | Start day is Friday |
| Saturday 01:00 | No | Friday's window ended at 07:00 Saturday — Sat not in mon-fri |
| Saturday 22:00 | No | Saturday not in mon-fri |
| Sunday 22:00 | No | Sunday not in mon-fri |

### AE2. Weekend-only daytime (no cross-midnight)

```yaml
windows:
  - name: "weekend-solar"
    start: "06:00"
    end: "22:00"
    max_charge: 0
    weekdays: "sat,sun"
```

Saturday 12:00 → active. Monday 12:00 → not active (Mon not in sat,sun).

### AE3. Same clock range, different weekdays — no overlap error

```yaml
windows:
  - name: "weekday-night"
    start: "22:00"
    end: "04:00"
    max_charge: 3500
    weekdays: "mon-fri"
  - name: "weekend-night"
    start: "22:00"
    end: "04:00"
    max_charge: 5000
    weekdays: "sat,sun"
```

Validation passes. The clock ranges overlap but the weekday sets are
disjoint, so the windows never collide on the same calendar day.

### AE4. Single-day special rate

```yaml
windows:
  - name: "wed-free-power"
    start: "12:00"
    end: "15:00"
    max_charge: 5000
    weekdays: "wed"
```

Active only on Wednesday 12:00–15:00. All other days this window is
invisible to the scheduler.

### AE5. Mixed weekday/weekend with day-only windows

```yaml
windows:
  - name: "weekday-morning"
    start: "02:00"
    end: "05:00"
    max_charge: 3500
    weekdays: "mon-fri"
  - name: "weekend-morning"
    start: "02:00"
    end: "05:00"
    max_charge: 5000
    weekdays: "sat,sun"
  - name: "sunday-extended"
    start: "06:00"
    end: "10:00"
    max_charge: 4000
    weekdays: "sun"
```

Three windows at the same clock times but different day assignments. All
validate without overlap errors. The scheduler picks the one matching the
current day.

## Scope Boundaries

- Weekday filtering applies to window activation only. It does not affect
  forecast horizon resolution, MQTT discovery, or Modbus operations.
- Holiday calendars, timezone-aware day-boundary logic, and UI-based
  scheduling are out of scope.

## Dependencies / Assumptions

- The existing `Window` struct, `ResolveActiveWindow`, `ValidateWindows`,
  and `scheduleBoundaryTick` in `pkg/power/window.go` and
  `pkg/cmd/schedule_runner.go` provide the integration points.
- `time.Weekday()` provides the day-of-week source. Cross-midnight
  resolution uses `now.Add(-24h).Weekday()` for the post-midnight check.
- The `scheduler_mode: windows` path is the primary beneficiary. Crontab
  mode with legacy `start_hr`/`end_hr` is unaffected.
