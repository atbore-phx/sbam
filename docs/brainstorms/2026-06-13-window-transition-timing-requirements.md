---
date: 2026-06-13
topic: window-transition-timing
---

## Summary

The windows-mode ticker fires a tick immediately on initial startup and detects window transitions at their exact start time via a one-shot boundary timer. Adjacent windows with equal start/end boundaries are rejected at validation. The existing `set_defaults` timer and cooldown logic remain unchanged.

## Problem Frame

When `scheduler_mode: windows` is active, two timing gaps exist. First, sbam started mid-window (e.g., 06:20) waits a full `tick_minutes` interval before the first tick — the system is idle when it could evaluate charging state. Second, window transitions are only detected at the next periodic tick, so a window starting at 22:00 may not take effect until 22:20 or later, delaying time-sensitive charging decisions. A third concern is config ambiguity: when one window's end equals another's start, inclusive bounds make both windows active at the boundary minute, and the first-listed window wins — which is rarely the intent.

## Requirements

### Tick timing

- R1. When `StartWindowsTicker` is called from the initial `schedule` command path, the runner fires a tick immediately before starting the periodic ticker.
- R2. When the windows ticker is restarted from within `Tick()` (window change detection), no additional immediate tick is fired — the already-executing tick runs with the new window's parameters.
- R3. When starting or restarting the windows ticker, the runner schedules a one-shot boundary timer set to the start time of the next window in the ordered list. The boundary timer fires an `IntentTick`. For the last window in the list, the next boundary wraps to the first window's next start.
- R4. The boundary timer is cancelled in `stopWindowsTicker` alongside the existing periodic ticker and defaults timer teardown.
- R5. The boundary timer fires at a wall-clock time strictly after the current `now` — if the computed boundary is in the past (e.g., sbam started exactly at 06:00), the timer targets the next occurrence (next day).

### Window validation

- R6. `ValidateWindows` rejects any configuration where one window's end time equals another window's start time. The error message names both windows and the conflicting boundary value.

### Existing behavior preserved

- R7. The `set_defaults` timer (`scheduleDefaults`) and the cooldown check (`isInCooldown`) are not modified. They continue to behave as they do today.

## Key Decisions

- **Boundary timer separate from periodic ticker.** Keeps "regular cadence" and "precise transitions" as independent concerns. Each window's ticker setup manages one periodic ticker, one boundary timer, and optionally one set_defaults timer.
- **Inclusive bounds with validation over half-open intervals.** Preserves existing user config semantics. The equal-boundary ambiguity is caught at config load time rather than through a breaking interval-semantics change.
- **Immediate tick only on initial start, not on window-change restart.** The boundary-triggered tick already provides the first evaluation in the new window. An additional immediate tick on restart would double-fire.

## Acceptance Examples

- AE1. **Mid-window start, full day.** Covers R1, R3, R5.
  - Given sbam starts at 06:20 with day `06:00–21:59` (tick 30) and night `22:00–05:59` (tick 60)
  - Then a tick fires immediately at 06:20, the periodic ticker runs every 30 min (06:20, 06:50, ...), and a boundary timer is scheduled for 22:00.

- AE2. **Boundary transition, day to night.** Covers R3, R4.
  - Given sbam is running in the day window with a boundary timer at 22:00
  - When the boundary timer fires at 22:00
  - Then an `IntentTick` is submitted, `Tick()` detects the night window, restarts the ticker at 60 min, cancels the previous boundary timer, and sets a new boundary timer for 06:00.

- AE3. **set_defaults fires before boundary transition.** Covers R7.
  - Given sbam is running in the night window with `set_defaults: true` and `before_end_defaults_minutes: 5`
  - Then at 05:54 the set_defaults timer fires an `IntentSetDefaults`
  - And at 06:00 the boundary timer fires an `IntentTick` that switches to the day window
  - No interaction between the two timers.

- AE4. **Equal boundaries rejected.** Covers R6.
  - Given a config with day end `22:00` and night start `22:00`
  - When `ValidateWindows` runs
  - Then it returns an error naming both windows and the conflicting `22:00` boundary.

- AE5. **Start at exact boundary.** Covers R2, R5.
  - Given sbam starts at 06:00 exactly
  - Then a tick fires immediately (day window), and the boundary timer targets 22:00 (the next boundary after now), not 06:00 today.

## Scope Boundaries

- Aligning periodic ticks to the window start boundary (e.g., always 06:00, 06:30, 07:00 regardless of launch time) is out of scope. First-day cadence depends on when sbam launched; subsequent days inherit the window-start alignment from the boundary-triggered transition.
- The `crontab` scheduler mode is unaffected by these changes.
