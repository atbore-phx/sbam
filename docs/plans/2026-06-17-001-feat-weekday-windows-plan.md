---
title: "feat: Add weekday filtering to charge windows"
type: feat
date: 2026-06-17
origin: docs/brainstorms/2026-06-17-weekday-windows-requirements.md
---

## Summary

Add an optional `weekdays` field to charge windows so users can configure
different charging behavior on different days of the week. The feature is
gated behind `weekday_feature` (default `true`) as a maintainer kill switch.

## Problem Frame

Beta tester feedback from
[discussion #164](https://github.com/atbore-phx/sbam/discussions/164): the
multi-window scheduler supports cross-midnight windows but cannot distinguish
weekdays from weekends. A user with off-peak rates Monday–Friday needs
different charge windows than on Saturday–Sunday. Without weekday filtering,
the only workaround is running separate sbam instances with different configs.

## Requirements

### Configuration

- R1. Each window accepts an optional `weekdays` field. When absent or empty,
  the window is active every day.
- R2. `weekdays` accepts any combination of single days (`mon`), comma lists
  (`mon,fri`), inclusive ranges (`mon-fri`), and mixed (`mon-fri,sun`).
- R3. Day names use lowercase 3-letter English abbreviations.

### Resolution (Start-Day Model)

- R4. A window's weekday applies to the day the window starts. Cross-midnight
  windows inherit the start day throughout — `23:00–03:00` on `fri` means
  Friday 23:00 through Saturday 03:00.

### Validation

- R5. `ValidateWindows` rejects unknown weekday tokens.
- R6. `ValidateWindows` rejects empty elements (`mon,,fri`).
- R7. `ValidateWindows` does not flag two windows as overlapping when their
  weekday sets are disjoint, even if clock ranges overlap.

### Scheduling

- R8. `ResolveActiveWindow` returns `nil` for a window whose weekday set does
  not include the resolved start day.
- R9. `scheduleBoundaryTick` computes the next future time a window starts on
  a matching weekday, across multiple days if needed, and picks the earliest.

### Feature Flag

- R10. `weekday_feature` is a top-level boolean (default `true`). When
  `false`, the `weekdays` field is ignored at validation and resolution.
- R11. When `weekday_feature` is `false`, `ValidateWindows` skips weekday
  token validation and does not adjust overlap detection.

---

## Key Technical Decisions

- **Weekday stored as a raw string, parsed on demand.** The `Weekdays` field
  is `string` with `omitempty`. Empty string means all days. Parsing into a
  `map[time.Weekday]bool` set happens at validation and resolution time, not
  at config load. This follows the existing `ForecastHorizon` /
  `ConsumptionHorizon` pattern (string stored, validated at use).
- **Start-day model for cross-midnight.** When a window crosses midnight, the
  post-midnight portion uses the start day's weekday. Implementation: in
  `ResolveActiveWindow`, for a cross-midnight window where `now` is before
  `endAt` (post-midnight), resolve the weekday against `startAt` which is on
  the previous calendar day.
- **Feature flag gating at two chokepoints.** The `weekday_feature` bool is
  checked at (a) `ValidateWindows` — controls whether weekday token validation
  runs and whether disjoint-weekday overlap skipping activates, and (b)
  `ResolveActiveWindow` call sites — controls whether the weekday filter is
  applied. `scheduleBoundaryTick` inherits gating from `ResolveActiveWindow`.
- **Weekday name resolution via explicit map.** A package-level
  `map[string]time.Weekday` maps `"mon"`..`"sun"` to `time.Monday`..
  `time.Sunday`. This avoids depending on `time.Weekday.String()` casing and
  makes validation self-contained.

---

## Implementation Units

### U1. Add Weekdays field and weekday parsing

- **Goal:** Add the `Weekdays` field to the `Window` struct and implement
  parsing/validation of the weekday string format.
- **Requirements:** R1, R2, R3, R5, R6
- **Dependencies:** none
- **Files:**
  - `pkg/power/window.go` — add `Weekdays` field, weekday name map,
    `parseWeekdays` and `validateWeekdays` helpers
  - `pkg/power/window_test.go` — parsing and validation tests
- **Approach:**
  - Add `Weekdays string` to the `Window` struct with
    `json:"weekdays,omitempty" yaml:"weekdays,omitempty" mapstructure:"weekdays,omitempty"`
  - Define `var weekdayNames = map[string]time.Weekday{...}` as a package-level map
  - `parseWeekdays(s string) (map[time.Weekday]bool, error)` splits on comma,
    expands ranges, deduplicates
  - `validateWeekdays(s string) error` wraps `parseWeekdays` and returns an
    error for unknown tokens or empty elements
  - Call `validateWeekdays` from `validateWindowFields` when `w.Weekdays != ""`
  - Ranges expand inclusively: `"mon-wed"` → `{Monday, Tuesday, Wednesday}`
  - Overlapping ranges flatten: `"mon-wed,tue-thu"` → `{Mon, Tue, Wed, Thu}`
- **Patterns to follow:** `validateWindowFields` early-return error pattern;
  `ForecastHorizon`/`ConsumptionHorizon` validation pattern (validate at
  struct level, use at resolution)
- **Test scenarios:**
  - Parse `"mon"` → `{Monday: true}`
  - Parse `"mon,fri"` → `{Monday: true, Friday: true}`
  - Parse `"mon-fri"` → 5 weekdays
  - Parse `"mon-fri,sun"` → 6 days
  - Parse `"mon-wed,tue-thu"` → flattened to 4 days
  - Parse `""` → empty set (all days)
  - Reject `"mon,xyz"` — unknown token
  - Reject `"mon,,fri"` — empty element
  - Reject `"MON"` — case-sensitive, lowercase only
  - Reject `"mon-FRI"` — mixed case in range
  - Parse `"mon-mon"` → `{Monday: true}` (single-day range)

### U2. Add weekday filtering to ResolveActiveWindow

- **Goal:** `ResolveActiveWindow` skips windows whose weekday set does not
  include the start day.
- **Requirements:** R4, R8, R10
- **Dependencies:** U1
- **Files:**
  - `pkg/power/window.go` — modify `ResolveActiveWindow` to accept
    `weekdayFeature bool` and apply the weekday filter
  - `pkg/power/window_test.go` — resolution tests with weekday filtering
- **Approach:**
  - Add `weekdayFeature bool` parameter to `ResolveActiveWindow`
  - When `weekdayFeature` is `true` and `w.Weekdays != ""`:
    - Parse the weekday set (cache in a local var to avoid re-parsing across
      loop iterations, or parse once)
    - For cross-midnight windows where `now` is post-midnight (before `endAt`
      on the same calendar day), determine the start day as
      `now.Add(-24 * time.Hour).Weekday()`
    - For same-day windows or the pre-midnight portion of cross-midnight,
      use `now.Weekday()`
    - If the resolved weekday is not in the set, `continue` to the next window
  - When `weekdayFeature` is `false`, skip the weekday check entirely
  - Update all call sites (`Runner.resolveActiveWindow`, test helpers)
- **Patterns to follow:** Existing `ResolveActiveWindow` iteration and
  cross-midnight detection logic.
- **Test scenarios:**
  - Window with `weekdays: "mon-fri"` active at Monday 12:00
  - Window with `weekdays: "mon-fri"` not active at Saturday 12:00
  - Cross-midnight `weekdays: "fri"`, `start: "22:00"`, `end: "04:00"`:
    active at Friday 23:00 and Saturday 03:00
  - Cross-midnight `weekdays: "fri"` not active at Saturday 23:00 (start day
    is Sat, not in set)
  - Empty `weekdays` — active every day (backward compatible)
  - `weekdayFeature: false` — window active regardless of `weekdays` value

### U3. Add weekday-aware overlap skip to ValidateWindows

- **Goal:** `ValidateWindows` skips overlap detection when two windows have
  disjoint weekday sets.
- **Requirements:** R7, R11
- **Dependencies:** U1
- **Files:**
  - `pkg/power/window.go` — modify `ValidateWindows` to accept
    `weekdayFeature bool` and add the disjoint-weekday guard
  - `pkg/power/window_test.go` — overlap tests with weekday sets
- **Approach:**
  - Add `weekdayFeature bool` parameter to `ValidateWindows`
  - In the overlap detection inner loop (where `a` and `b` are compared),
    when `weekdayFeature` is `true` and both windows have non-empty
    `Weekdays`, parse both weekday sets. If they have no intersection, skip
    the overlap check for this pair (`continue`)
  - When either window has an empty `Weekdays` (all days), the sets always
    intersect — proceed with normal overlap detection
  - When `weekdayFeature` is `false`, skip the weekday-set check entirely
  - Update all call sites (`checkScheduleschedule`, tests)
- **Patterns to follow:** Existing `ValidateWindows` segment-sorting and
  overlap-detection logic.
- **Test scenarios:**
  - Same clock range, different weekdays (`mon-fri` vs `sat,sun`) — no
    overlap error
  - Same clock range, overlapping weekdays (`mon-fri` vs `fri,sat`) —
    overlap error
  - Same clock range, both empty weekdays — overlap error (backward compatible)
  - Same clock range, one empty one set — overlap error (empty = all days)
  - `weekdayFeature: false`, overlapping weekdays exists — no overlap error
    (weekday logic disabled, overlap passes)

### U4. Add weekday-aware boundary tick advancement

- **Goal:** `scheduleBoundaryTick` finds the next window start on a matching
  weekday, advancing across days when needed.
- **Requirements:** R9
- **Dependencies:** U2
- **Files:**
  - `pkg/cmd/schedule_runner.go` — modify `scheduleBoundaryTick` to advance
    past non-matching weekdays
  - `pkg/cmd/schedule_runner_test.go` — boundary tick tests with weekdays
- **Approach:**
  - In the "no active window" branch (iterating all windows for the next
    start), after computing `startAt` for a candidate window:
    - If `startAt` is not after `now`, advance by 24h
    - If the window has a weekday set and `weekdayFeature` is enabled, advance
      `startAt` day-by-day until its weekday matches the set
  - In the "active window → next window" branch, after computing `nextAt`:
    - If the next window has a weekday set, advance day-by-day until match
    - The max scan is 7 days per window
  - A helper `nextMatchingWeekday(t time.Time, weekdays map[time.Weekday]bool) time.Time`
    advances `t` by 24h increments until `t.Weekday()` is in the set
  - Read `weekdayFeature` from `r.cfg.WeekdayFeature`
- **Patterns to follow:** Existing `scheduleBoundaryTick` structure —
  candidate loop, `startAt` computation, `newTimerFunc` delay scheduling.
- **Test scenarios:**
  - Active window `mon-fri`, today is Friday — boundary targets the next
    window's start on Monday
  - No active window, today is Saturday, only weekday windows exist —
    boundary targets Monday's first window start
  - No active window, today is Saturday, weekend windows exist — boundary
    targets Saturday's first matching window
  - Single window `wed`, today is Tuesday — boundary targets Wednesday
  - Windows with empty weekdays — boundary tick works as before (no
    day-advancement)
  - All windows have non-matching weekdays for the next 7 days — should
    still find a valid target (weekday set is non-empty by validation)

### U5. Wire weekday_feature flag through config and runner

- **Goal:** Add `weekday_feature` as a top-level config key available via
  CLI flag, env var, and config.yaml, and pass it through to all gated
  call sites.
- **Requirements:** R10, R11
- **Dependencies:** U2, U3, U4
- **Files:**
  - `pkg/cmd/schedule.go` — package-level var, constant, CLI flag
    registration, Viper read, pass to `checkScheduleschedule` and
    `RunnerConfig`
  - `pkg/cmd/schedule_runner.go` — add `WeekdayFeature bool` to
    `RunnerConfig`, pass to `ResolveActiveWindow` and `scheduleBoundaryTick`
  - `pkg/cmd/schedule_validation_test.go` — test `weekday_feature` gating
    in `checkScheduleschedule`
  - `pkg/cmd/schedule_runner_test.go` — test runner with
    `WeekdayFeature` enabled/disabled
- **Approach:**
  - Add `var weekday_feature bool` to the package-level var block
  - Add `const const_weekday_feature = true` to the constants block
  - Register `--weekday_feature` flag via
    `scdCmd.Flags().BoolVar(&weekday_feature, "weekday_feature", const_weekday_feature, "Enable weekday filtering on charge windows")`
  - Read via `weekday_feature = viper.GetBool("weekday_feature")` in the
    `Run` handler
  - Add `WeekdayFeature bool` to `RunnerConfig`
  - Update `checkScheduleschedule` to accept and forward `weekdayFeature bool`
    to `pw.ValidateWindows`
  - Update `Runner.resolveActiveWindow` to pass
    `r.cfg.WeekdayFeature` to `pw.ResolveActiveWindow`
  - Auto-binds to `WEEKDAY_FEATURE` env var via Viper's `AutomaticEnv`
- **Patterns to follow:** `mqtt_enabled` flag pattern (top-level bool, CLI +
  env + config.yaml, passed to struct).
- **Test scenarios:**
  - `weekday_feature: true` — windows with invalid weekdays are rejected
  - `weekday_feature: false` — windows with invalid weekdays are accepted
    (validation skipped)
  - Flag `--weekday_feature=false` overrides config `weekday_feature: true`
  - Env `WEEKDAY_FEATURE=false` overrides config
  - Default (no config, no flag) — `weekday_feature` is `true`

### U6. Add HA add-on schema entries

- **Goal:** Update the Home Assistant add-on `config.yaml` to include the
  `weekday_feature` option and the per-window `weekdays` schema entry.
- **Requirements:** R1, R10
- **Dependencies:** U5
- **Files:**
  - `home-assistant/addons/sbam/config.yaml` — add `weekday_feature` to
    options and schema, add `weekdays` to the windows schema list
- **Approach:**
  - Add `weekday_feature: true` to the `options:` section
  - Add `weekday_feature: bool` to the `schema:` section
  - Add `weekdays: str?` to the `windows:` schema list
- **Patterns to follow:** Existing `mqtt_enabled: bool` and `scheduler_mode:`
  schema entries.
- **Test scenarios:**
  - Test expectation: none — schema-only change; the existing
    `config_schema_test.go` validates regex patterns but `weekdays` uses
    `str?` (no regex)

### U7. Add weekday documentation page

- **Goal:** Create a dedicated documentation page explaining weekday
  filtering with multiple worked examples.
- **Requirements:** R1–R4
- **Dependencies:** U1–U6
- **Files:**
  - `docs/site/weekdays.md` — new documentation page
  - `mkdocs.yml` — add `weekdays.md` to the nav
- **Approach:**
  - Follow the structure of `docs/site/mqtt.md` as a template
  - Cover: enabling the feature, weekday format reference, the start-day
    model explanation with a diagram-like example, five worked config
    examples
  - Examples: beta tester weekday off-peak, weekend-only, same-time
    different-days (no overlap), single-day special rate, mixed
    weekday/weekend
  - Include a note that `weekday_feature: false` disables all filtering
  - Link from `mkdocs.yml` nav under a new "Weekday Filtering" entry
- **Test scenarios:**
  - Test expectation: none — documentation only; MkDocs build verified via
    `make docs` or CI

---

## Scope Boundaries

- Weekday filtering applies to window activation only. It does not affect
  forecast horizon resolution, MQTT discovery payloads, or Modbus operations.
- Crontab mode with legacy `start_hr`/`end_hr` is unaffected.
- Holiday calendars, timezone-aware day-boundary logic, and UI-based
  scheduling are out of scope.

---

## Sources / Research

- Origin requirements:
  `docs/brainstorms/2026-06-17-weekday-windows-requirements.md`
- Beta tester feedback:
  [discussion #164](https://github.com/atbore-phx/sbam/discussions/164)
- Window struct and validation patterns: `pkg/power/window.go:46-275`
- Schedule runner and boundary tick: `pkg/cmd/schedule_runner.go:255-345`
- Config loading and flag registration: `pkg/cmd/schedule.go:124-333`
- HA add-on schema: `home-assistant/addons/sbam/config.yaml:14-86`
- Prior multi-window feature plan:
  `docs/plans/2026-06-11-001-feat-scheduler-mode-selector-plan.md`
