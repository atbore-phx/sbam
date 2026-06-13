---
title: "feat: Precise window transition timing in windows-mode ticker"
type: feat
date: 2026-06-13
origin: docs/brainstorms/2026-06-13-window-transition-timing-requirements.md
---

## Summary

The windows-mode ticker fires a tick immediately on initial startup and detects window transitions at exact boundary times via a one-shot timer. Adjacent windows with equal start/end boundaries are rejected at validation. The existing `set_defaults` timer and cooldown are unchanged.

## Problem Frame

When `scheduler_mode: windows` is active, the system idles for a full `tick_minutes` interval before the first tick after startup, and window transitions are only detected at the next periodic tick — a window starting at 22:00 may not take effect until 22:20. A third issue is config ambiguity: when one window's end equals another's start, inclusive bounds make both windows active at the boundary minute and the first-listed window wins.

The origin document (`docs/brainstorms/2026-06-13-window-transition-timing-requirements.md`) defines seven requirements (R1–R7) and five acceptance examples (AE1–AE5) covering these three concerns.

## Requirements

- R1. `StartWindowsTicker` fires a tick immediately on initial call.
- R2. Window-change ticker restarts do not fire an additional immediate tick.
- R3. On ticker start/restart, a one-shot boundary timer fires an `IntentTick` at the next window's start. Last window wraps to first window's next start.
- R4. The boundary timer is cancelled in `stopWindowsTicker`.
- R5. If the computed boundary is in the past, the timer targets the next occurrence.
- R6. `ValidateWindows` rejects configurations where one window's end equals another window's start.
- R7. `scheduleDefaults` and `isInCooldown` are not modified.

## Key Technical Decisions

- **Boundary timer uses `time.AfterFunc`, replicates the `scheduleDefaults` pattern.** The existing one-shot timer (`scheduleDefaults` at `pkg/cmd/schedule_runner.go:196-233`) already handles cross-midnight computation and explicit stop. Using the same mechanism avoids introducing a new timer abstraction.

- **Immediate tick fires in the exported `StartWindowsTicker`, not in `startWindowsTicker`.** The exported method is called only from the initial schedule command path (`pkg/cmd/schedule.go:277`). The unexported `startWindowsTicker` is also called from `Tick()` when a window change is detected — R2 forbids an immediate tick in that case.

- **Equal-boundary check compares minute-of-day values from original window Start/End clock times, not expanded segments.** The overlap detector uses half-open `[startMinute, endMinute)` segments where adjacent boundaries are harmless by design. The equal-boundary check must convert the original `Window.Start` / `Window.End` strings to minute-of-day values and compare those, not the expanded segments.

- **Boundary timer reference stored explicitly.** Unlike the periodic ticker (intentionally not stored — `pkg/cmd/schedule_runner.go:181-183`), the boundary timer is stored as `*time.Timer` and explicitly stopped in `stopWindowsTicker`, matching the `defaultsTimer` lifecycle.

## Implementation Units

### U1. ValidateWindows rejects equal adjacent boundaries

- **Goal:** Reject configurations where one window's end clock time equals another window's start clock time.
- **Requirements:** R6
- **Dependencies:** None
- **Files:**
  - `pkg/power/window.go` — add equal-boundary check to `ValidateWindows`
  - `pkg/power/window_test.go` — add rejection cases, update existing adjacent-window test
- **Approach:** After the existing overlap-detection loop in `ValidateWindows`, add an O(n²) scan over all window pairs comparing `clockMinute(end)` of one window to `clockMinute(start)` of every other window. Use the original `Window.Start` / `Window.End` strings (not expanded segments) so cross-midnight windows are handled correctly. The check fires after overlap detection so the error for overlapping windows takes precedence.
- **Patterns to follow:** Existing `ValidateWindows` error format: `"window %q overlaps with window %q"`. Match this style for the equal-boundary error: `"window %q end %s equals window %q start %s"`.
- **Test scenarios:**
  - Happy path: Two windows with equal boundary (day end 22:00, night start 22:00) → error naming both windows
  - Edge case: Cross-midnight window end equals another window start (night end 06:00, day start 06:00) → error
  - Edge case: Three windows with equal boundary between first and second → error on that pair
  - Existing behavior preserved: Adjacent non-equal boundaries (day end 21:59, night start 22:00) → passes
  - Existing behavior preserved: Genuine overlap still detected → error
  - Existing behavior preserved: Non-adjacent, non-overlapping windows → passes
  - Edge case: Single window → passes (no other window to compare)
  - Edge case: Equal boundaries between non-adjacent windows (rare but valid) → still rejected
- **Verification:** `make test` passes with updated `TestValidateWindows` cases. Covers AE4.

### U2. Fire immediate tick on initial StartWindowsTicker call

- **Goal:** When sbam starts mid-window, fire a tick immediately instead of waiting for the first periodic interval.
- **Requirements:** R1, R2
- **Dependencies:** None
- **Files:**
  - `pkg/cmd/schedule_runner.go` — add immediate tick submission in exported `StartWindowsTicker`
  - `pkg/cmd/schedule_runner_test.go` — add test for immediate tick behavior
- **Approach:** In the exported `StartWindowsTicker` method (`schedule_runner.go:151-153`), call `r.Submit(mqtt.Intent{Kind: mqtt.IntentTick})` before delegating to `r.startWindowsTicker(now)`. The unexported `startWindowsTicker` (called from `Tick()` on window change) does not add this call, satisfying R2. The immediate tick runs with the current window's parameters since `resolveActiveWindow` is called inside `Tick()` at processing time.
- **Patterns to follow:** Existing `Submit` guard pattern: `if !r.Submit(...) { u.Log.Warn(...) }`.
- **Test scenarios:**
  - Happy path: Call `StartWindowsTicker(fakeNow)` → drain intent channel → an `IntentTick` is present
  - Edge case: Intent queue is full → `Submit` returns false, warning is logged, no panic
  - Non-regression: Window-change restart (calling `startWindowsTicker` directly from within a tick) does not enqueue a second `IntentTick`
  - Integration: The immediate tick executes `Tick()` which resolves the active window correctly and publishes state
- **Verification:** `make test` passes. Covers AE1 and AE5 (immediate tick portion).

### U3. Schedule one-shot boundary timer at next window start

- **Goal:** Detect window transitions at their exact start time by scheduling a one-shot timer at the next window's start boundary.
- **Requirements:** R3, R4, R5
- **Dependencies:** None (U1 and U2 are independent; all three can land in any order)
- **Files:**
  - `pkg/cmd/schedule_runner.go` — add `boundaryTimer` field to `Runner`, add `scheduleBoundaryTick` method, integrate into `startWindowsTicker` and `stopWindowsTicker`, add timer factory var for test injection
  - `pkg/cmd/schedule_runner_test.go` — add tests for boundary timer scheduling, firing, cancellation, and wrap-around
- **Approach:**
  1. Add `boundaryTimer *time.Timer` to the `Runner` struct (alongside `defaultsTimer`).
  2. Add `scheduleBoundaryTick(windows []pw.Window, now time.Time)` method:
     - Resolve the active window via `pw.ResolveActiveWindow`. If no window is active (gapped coverage), find the next window whose start is after `now` and use its start; if all windows start before `now`, use the first window's start + 24h.
     - When an active window exists, find its index in the ordered list and select the next window: `(currentIdx + 1) % len(windows)`
     - Parse the next window's `Start` clock time
     - Compute the absolute fire time: anchor to `now`'s date, add 24h if the time is before-or-equal to `now` (R5)
     - Use `time.AfterFunc` to submit `IntentTick` at the fire time
     - Store the returned `*time.Timer` in `r.boundaryTimer`
  3. Call `scheduleBoundaryTick` from `startWindowsTicker` after the periodic ticker is started.
  4. In `stopWindowsTicker`, stop and nil out `r.boundaryTimer`.
  5. Add a package-level `var newAfterFunc = time.AfterFunc` so tests can inject a fake timer.
  6. For a single-window list, the boundary wraps to the same window's next start. This is harmless — the periodic ticker already covers that interval.
- **Patterns to follow:** `scheduleDefaults` (`schedule_runner.go:196-233`) for wall-clock parsing, cross-midnight date adjustment, `time.AfterFunc` usage, timer storage, and stop-on-teardown. Log format: `u.Log.Infow("boundary timer scheduled", ...)` matching `scheduleDefaults` at line 222.
- **Test scenarios:**
  - Happy path: With two windows (day 06:00–21:59, night 22:00–05:59), at 12:00 the boundary timer is scheduled for 22:00 today
  - Happy path: The boundary timer fires and submits an `IntentTick`
  - Wrap-around: With two windows at 23:00 (night active), the boundary timer targets 06:00 tomorrow (day start)
  - R5: Start at exact boundary (06:00) — the boundary timer targets 22:00 today, not 06:00 today
  - Cancellation: Calling `stopWindowsTicker` cancels the boundary timer
  - Cancellation: Window change restart calls `startWindowsTicker`, which calls `stopWindowsTicker` first, cancelling the old boundary timer
  - Integration: Boundary-triggered tick detects window change in `Tick()` and restarts the ticker with new window params
  - Edge case: Single window — boundary timer wraps to same window's next start
  - Edge case: `time.AfterFunc` override (via `newAfterFunc` var) allows deterministic testing without real clock
- **Verification:** `make test` passes. Covers AE2, AE3 (boundary timer portion), and AE5 (boundary timer portion).

---

## Scope Boundaries

### Deferred to Follow-Up Work

- Aligning periodic ticks to window start boundaries (e.g., 06:00, 06:30, 07:00 regardless of launch time). First-day cadence depends on when sbam launched; subsequent days inherit alignment from boundary-triggered transitions.

### Out of scope

- The `crontab` scheduler mode is unaffected.
- The `set_defaults` timer and cooldown logic are not modified.

## Sources / Research

- `pkg/cmd/schedule_runner.go:155-190` — `startWindowsTicker` / `stopWindowsTicker` lifecycle
- `pkg/cmd/schedule_runner.go:196-233` — `scheduleDefaults` one-shot timer pattern
- `pkg/cmd/schedule_runner.go:262-278` — `Tick` window-change detection
- `pkg/cmd/schedule.go:272-278` — `StartWindowsTicker` call site
- `pkg/power/window.go:61-122` — `ValidateWindows` overlap detection
- `pkg/power/window.go:155-191` — `ResolveActiveWindow` inclusive bounds
- `docs/plans/2026-06-11-001-feat-scheduler-mode-selector-plan.md` — original ticker architecture
- `docs/implementations/archive/146-issue-multi-window-charging/` — overlap detection and boundary semantics
