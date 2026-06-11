---
title: "feat: Add scheduler_mode selector with crontab deprecation path"
type: feat
date: 2026-06-11
origin: docs/brainstorms/2026-06-11-scheduler-mode-selector-requirements.md
---

## Summary

Add a `scheduler_mode` config key (crontab | windows | auto) that explicitly selects how the schedule runner is driven. Crontab mode preserves v2.1 behavior with a deprecation warning on MQTT and logs. Windows mode replaces cron with an internal `time.Ticker` (default 60 min, per-window overridable) and per-window set_defaults scheduling via new Window fields. The `auto` value is defined internally but hidden — manually setting it produces a rejection pointing to #149.

## Problem Frame

The schedule runner currently decides between legacy single-window and multi-window behavior implicitly by checking whether `windows:` is populated. As sbam moves from v2.1's cron-only model toward v3.0's smart scheduler, users need an explicit switch to opt into new behavior incrementally and a visible deprecation signal for the crontab path scheduled for removal in v3.0.0.

## Requirements

**Configuration and CLI:**
- R1. `scheduler_mode` accepts values `crontab`, `windows`, and `auto`. The default is `crontab`.
- R2. CLI flag `--scheduler-mode` and env var `SCHEDULER_MODE` follow the existing precedence (flag > env > config.yaml). The `auto` value is accepted at all three layers but omitted from CLI help text.
- R3. The HA add-on `config.json` schema exposes `scheduler_mode` as a list enum containing `crontab` and `windows` only. `auto` is excluded from the schema options.

**Crontab mode:**
- R4. When mode is `crontab`, the runner is driven by cron. `windows:` may be populated — cron drives the ticks, and each tick resolves the active window for its charge parameters via `resolveActiveWindow`. When `windows:` is empty, the legacy `start_hr`/`end_hr` path is used.
- R5. Crontab mode logs a one-shot WARN at startup naming it as deprecated and publishes that warning as a field on the MQTT state payload.

**Windows mode:**
- R6. When mode is `windows`, the runner starts an internal `time.Ticker` and does not call `crontabSchedule`. If `crontab` is set to a non-default value, a one-shot WARN is logged naming the ignored field.
- R7. The default tick interval is 60 minutes. Each window may override this via an optional `tick_minutes` field (integer, minimum 1). When the active window changes, the ticker resets to the new window's interval.
- R8. Each window may specify `defaults: true` (default `false`) to enable a set_defaults reset. When enabled, the reset fires `before_end_defaults` minutes before the window's end time. `before_end_defaults` defaults to 5 minutes (integer, minimum 1). These fields apply only in windows mode.
- R9. When mode is `windows`, `windows:` must contain at least one entry or startup validation fails.

**Auto mode:**
- R10. When mode is `auto`, startup validation rejects with an error message that references GitHub issue #149 as the tracking issue.

**MQTT:**
- R11. `StatePayload` includes a `scheduler_mode` string field populated with the active mode value.
- R12. When crontab mode is active and the deprecation warning fires, `StatePayload` includes a `deprecation_warning` string field with the deprecation message.

**Backward compatibility:**
- R13. Configs without `scheduler_mode` default to `crontab` — no behavior change on upgrade. If `windows:` is also populated, cron drives the ticks and windows parameterize the charge decision (same as R4).

---

## Key Technical Decisions

- **Flat key `scheduler_mode`.** Matches the existing config.yaml convention (`start_hr`, `max_charge`, `mqtt_enabled`). CLI flag is `--scheduler-mode`, env var is `SCHEDULER_MODE`.

- **Crontab mode is compatible with `windows:`.** The branch already supports this: cron drives the ticks, `resolveActiveWindow` dispatches to the windows list for charge parameters. Forbidding this combination would add unnecessary validation and break a working configuration.

- **Per-window tick interval, default 60 min.** A single global tick would over-tick short windows or under-tick long ones. Per-window override gives each window the cadence it needs. The value is optional on the Window struct and only used in windows mode.

- **Set_defaults in windows mode uses `time.AfterFunc`, with the cooldown keeping the reset in place.** A periodic ticker cannot reliably hit the exact `before_end_defaults` boundary. When a window with `defaults: true` becomes active, the runner schedules a one-shot `time.AfterFunc` for `window.end - before_end_defaults`. The existing `isInCooldown` check in Tick must be extended to use the active window's end time and `before_end_defaults` as the cooldown duration — so after set_defaults fires at end−N minutes, any subsequent tick within the same window is suppressed by the cooldown and cannot override the reset. The timer is canceled and re-scheduled when the active window changes. In crontab mode, set_defaults continues through the existing `crontabSchedule` cron entry with no change.

- **Window fields `defaults` and `before_end_defaults` are windows-mode only.** In crontab mode these fields are ignored — the existing `crontabSchedule` end_hr-based set_defaults cron handles the reset. This keeps crontab-mode behavior identical to v2.1.

- **`auto` value defined but hidden.** The value exists so early adopters can manually set it and get a useful rejection message, but it is excluded from HA schema and CLI help to avoid presenting a non-functional option.

- **`tick_minutes`, `defaults`, and `before_end_defaults` are optional pointer fields on Window.** Using `*int` / `*bool` with `omitempty` tags so zero values don't serialize and existing configs are unaffected. Validation rejects negative values and `tick_minutes` of zero. `before_end_defaults` of zero is valid (fires at window end).

- **Deprecation warning on MQTT, not just logs.** HA users monitor dashboards, not container logs. The `StatePayload` gains a `deprecation_warning` string field, published once at startup when crontab mode is active.

---

## Implementation Units

### U1. Extend Window struct with new fields

- **Goal:** Add `tick_minutes`, `defaults`, and `before_end_defaults` fields to the Window struct with validation.
- **Requirements:** R7, R8
- **Dependencies:** None
- **Files:**
  - `pkg/power/window.go` — add fields to Window struct, update `validateWindowFields`
- **Approach:** Add three pointer fields with `omitempty` tags to the Window struct: `TickMinutes *int`, `Defaults *bool`, `BeforeEndDefaults *int`. Update `validateWindowFields` to reject `TickMinutes <= 0` when set and reject `BeforeEndDefaults < 0` when `Defaults` is set. A value of `0` is valid — it means set_defaults fires at the exact window end time. Use the existing field-validation pattern (early return on first error).
- **Patterns to follow:** Existing optional fields `ForecastHorizon` and `ConsumptionHorizon` on Window struct (`pkg/power/window.go:34-41`). The `validateWindowFields` pattern at `pkg/power/window.go:111-132`.
- **Test scenarios:**
  - Window with no new fields set — passes validation (backward compatible)
  - Window with `tick_minutes: 30` — passes validation
  - Window with `tick_minutes: 0` — validation fails
  - Window with `tick_minutes: -1` — validation fails
  - Window with `defaults: true`, `before_end_defaults: 10` — passes validation
  - Window with `defaults: true`, `before_end_defaults: 0` — passes validation (fires at window end)
  - Window with `before_end_defaults` set but `defaults` unset — passes validation (field ignored at runtime but structurally valid)
  - Cross-midnight window with `defaults: true` — passes validation (timer scheduling tested in U5)
- **Verification:** `make test` passes. `ValidateWindows` accepts windows with new fields and rejects invalid values.

### U2. Add scheduler_mode config key and CLI flag

- **Goal:** Add the `scheduler_mode` config key, `--scheduler-mode` CLI flag, env var binding, and startup validation.
- **Requirements:** R1, R2, R10, R13
- **Dependencies:** None
- **Files:**
  - `pkg/cmd/schedule.go` — add `scheduler_mode` variable, register flag, read in `scdCmd.Run`, add to `checkScheduleschedule`
  - `pkg/cmd/schedule_validation_test.go` — mode validation tests
  - `pkg/cmd/precedence_test.go` — flag/env/config precedence tests
- **Approach:** Add `scheduler_mode` to the package var block with default `"crontab"`. Register `--scheduler-mode` string flag in `registerScdCmd` (no short form). Read in `scdCmd.Run` via `viper.GetString("scheduler_mode")`. In `checkScheduleschedule`, add validation: reject empty string, reject unknown values, reject `auto` with error referencing #149. The `windows` value requires `windows:` to be non-empty (R9 — validated here). Pass the validated mode into `RunnerConfig`.
- **Patterns to follow:** Flag registration pattern at `pkg/cmd/schedule.go:269-303`. Validation in `checkScheduleschedule` at `pkg/cmd/schedule.go:414-476`. Viper binding via `bindFlags` at `pkg/cmd/root.go:79-90`.
- **Test scenarios:**
  - Default (no flag, no env, no config) → mode resolves to `crontab`
  - CLI `--scheduler-mode windows` → overrides env and config
  - Env `SCHEDULER_MODE=windows` → overrides config
  - Config `scheduler_mode: windows` → used when flag and env absent
  - `scheduler_mode: auto` → startup error referencing #149
  - `scheduler_mode: invalid` → startup error about unknown value
  - `scheduler_mode: ""` → defaults to crontab (or error, decide in impl)
- **Verification:** `make test` passes. CLI help shows `--scheduler-mode` without `auto` in the help text. `make build` succeeds.

### U3. Add scheduler_mode to Runner plumbing and MQTT state

- **Goal:** Thread the mode value through RunnerConfig into the runner, and expose it on the MQTT StatePayload.
- **Requirements:** R11, R12
- **Dependencies:** U2 (needs the validated mode value)
- **Files:**
  - `pkg/cmd/schedule_runner.go` — add `SchedulerMode` field to `RunnerConfig`
  - `pkg/mqtt/types.go` — add `SchedulerMode` and `DeprecationWarning` fields to `StatePayload`
  - `pkg/cmd/schedule.go` — populate `SchedulerMode` from viper value, update `makeBasePayload` to include `scheduler_mode`
- **Approach:** Add `SchedulerMode string` to `RunnerConfig`. In `scdCmd.Run`, set it from the validated `scheduler_mode`. In `makeBasePayload`, set `SchedulerMode` from the runner config. Add `DeprecationWarning *string` to `StatePayload` (set in U4 when crontab deprecation fires).
- **Patterns to follow:** `RunnerConfig` fields at `pkg/cmd/schedule_runner.go:30-52`. `StatePayload` fields at `pkg/mqtt/types.go:23-41`. `makeBasePayload` at `pkg/cmd/schedule.go:524-535`.
- **Test scenarios:**
  - MQTT state payload includes `scheduler_mode: "crontab"` when mode is crontab
  - MQTT state payload includes `scheduler_mode: "windows"` when mode is windows
  - `deprecation_warning` field is absent (null/omitted) when no deprecation is active
- **Verification:** Existing MQTT tests pass. New assertions in runner tests verify state payload carries the mode field.

### U4. Wire mode dispatch with crontab deprecation

- **Goal:** Branch the schedule command's Run function on `scheduler_mode`, add crontab deprecation warning.
- **Requirements:** R4, R5, R6, R9, R10
- **Dependencies:** U2 (config key exists), U3 (RunnerConfig carries mode)
- **Files:**
  - `pkg/cmd/schedule.go` — modify `scdCmd.Run` to branch on mode, add deprecation log + MQTT publish
  - `pkg/cmd/schedule_runner.go` — publish deprecation warning on MQTT state
  - `pkg/cmd/schedule_lifecycle_test.go` — mode dispatch tests
- **Approach:** In `scdCmd.Run`, after validation: if mode is `crontab`, follow the existing path (call `crontabSchedule` when crontab is not `const_ct`), but first emit a one-shot WARN via `u.Log.Warn` and set `DeprecationWarning` on the MQTT state. If mode is `windows`, skip `crontabSchedule`, log a one-shot WARN if crontab is non-default, and call a new method on Runner to start the ticker (the actual ticker logic lives in U5). The `auto` case was already rejected in `checkScheduleschedule` (U2).
- **Patterns to follow:** The `var` block and command flow at `pkg/cmd/schedule.go:92-259`. The deprecation pattern is net-new for this codebase; follow the one-shot log convention (`sync.Once` or a `deprecationLogged` bool on Runner).
- **Test scenarios:**
  - Crontab mode with crontab set → cron installed, deprecation WARN logged once, MQTT state shows `deprecation_warning`
  - Crontab mode with crontab default (`const_ct`) → cron not installed, deprecation WARN still logged (mode is crontab)
  - Windows mode with crontab non-default → WARN logged about ignored crontab, cron not installed
  - Windows mode with crontab default → no crontab WARN, cron not installed
  - Windows mode with empty windows list → validation error (tested in U2)
  - Deprecation WARN fires exactly once across multiple ticks
- **Verification:** `make test` passes. Runner starts in the correct mode. Deprecation WARN appears in structured log output and MQTT state.

### U5. Implement windows-mode ticker and per-window set_defaults

- **Goal:** Implement the internal `time.Ticker` in the runner's Run loop for windows mode, with per-window interval override and per-window set_defaults scheduling.
- **Requirements:** R6, R7, R8
- **Dependencies:** U1 (Window fields exist), U3 (RunnerConfig carries mode), U4 (dispatch calls the ticker start)
- **Files:**
  - `pkg/cmd/schedule_runner.go` — add `startTicker` method, modify `Run` to accept ticker channel, add set_defaults timer logic, add `resolveActiveWindow` change detection
  - `pkg/cmd/schedule_runner_test.go` — ticker behavior, set_defaults timer, window transition tests
- **Approach:**
  - Add a `startWindowsTicker(ctx)` method on Runner that returns a `<-chan time.Time`. It computes the tick interval from the active window's `tick_minutes` (or 60 default), creates a `time.Ticker`, and returns its channel. If the active window has `defaults: true`, it also schedules a `time.AfterFunc` for `window.end - before_end_defaults` that submits `IntentSetDefaults`.
  - Extend the Run loop with a third `case <-ticker.C:` that submits `IntentTick`.
  - On each Tick, re-resolve the active window. If the window changed (name differs), stop the current ticker + timer and call `startWindowsTicker` with the new window's parameters.
  - The set_defaults `AfterFunc` fires exactly once per window activation. A `defaultsFired` flag on Runner prevents re-firing on subsequent ticks within the same window. The flag is cleared when the active window changes.
  - When no window is active (nil from `resolveActiveWindow`), set the ticker to the default 60-minute interval and do not schedule a set_defaults timer.
  - **Cooldown integration:** Extend `isInCooldown` to accept the active window's end time and a cooldown-duration parameter. In windows mode, the Tick method passes the active window's end time and `before_end_defaults` (or 5 when `defaults` is unset) as the cooldown duration. This ensures that after set_defaults fires at end−N minutes, any subsequent tick within the same window finds the cooldown active and returns early without making a new charge decision — the reset is not overridden. In crontab mode, `isInCooldown` continues to use the legacy `StartHR`/`EndHR` and the hardcoded 5-minute constant.
- **Patterns to follow:** The `Runner` struct at `pkg/cmd/schedule_runner.go:77-83`. The `Run` loop select at line 124-134. The `resolveActiveWindow` call at line 171. Use `sync/atomic` for the `defaultsFired` flag (matching the existing `paused` atomic pointer pattern).
- **Test scenarios:**
  - Windows mode starts ticker at 60 min default when no window has `tick_minutes`
  - Active window with `tick_minutes: 30` → tick interval is 30 min
  - Window transition from one with `tick_minutes: 30` to one with `tick_minutes: 15` → ticker resets to 15 min
  - Window with `defaults: true`, `before_end_defaults: 5` ending at 06:00 → at simulated 05:55, set_defaults fires
  - Set_defaults fires exactly once per window activation — second tick within same window does not re-fire
  - Window transition clears the `defaultsFired` flag — new window's set_defaults can fire
  - No active window → ticker runs at 60 min default, no set_defaults timer
  - Ticker stops on context cancellation (shutdown)
  - Cross-midnight window with `defaults: true` → set_defaults timer computed correctly across the midnight boundary
  - Window 06:00-07:00, `tick_minutes: 1`, `defaults: true`, `before_end_defaults: 5` — at 06:55, set_defaults fires; at 06:56, tick finds cooldown active (end=07:00 minus 5 min = 06:55→07:00 window) and returns early without a new charge decision
- **Verification:** `make test` passes. Ticker fires periodic ticks in windows mode. Set_defaults fires at the correct boundary. `go test -race ./pkg/cmd` passes.

### U6. MQTT discovery entity and HA add-on schema

- **Goal:** Add a `scheduler_mode` sensor to MQTT discovery, update the HA add-on schema, and extend the windows sub-schema with new fields.
- **Requirements:** R3, R11
- **Dependencies:** U3 (StatePayload has the field)
- **Files:**
  - `pkg/mqtt/discovery.go` — add `scheduler_mode` sensor entity
  - `home-assistant/addons/sbam/config.json` — add `scheduler_mode` to options and schema, add `tick_minutes`, `set_defaults`, `before_end_defaults_minutes` to windows sub-schema
  - `pkg/cmd/config_schema_test.go` — schema validation tests
- **Approach:**
  - In `BuildDiscovery`, add a diagnostic sensor entity for `scheduler_mode` using the existing `sensorPayload` helper. The value template is `{{ value_json.scheduler_mode }}`. Bump the `make` capacity from 20 to 21.
  - In `config.json` options, add `"scheduler_mode": "crontab"`. In schema, add `"scheduler_mode": "list(crontab|windows)"` (auto hidden per R3).
  - In the windows sub-schema, add optional fields: `"tick_minutes": "int?"`, `"set_defaults": "bool?"`, `"before_end_defaults_minutes": "int?"`. Note: the yaml/mapstructure field name `defaults` conflicts with a common keyword in JSON schema contexts — use `set_defaults` as the JSON/YAML key while keeping the struct field unambiguous. This naming choice should be documented in the requirements trace. Actually, looking back at the requirements doc, it uses `defaults` — but for the HA schema JSON key, `set_defaults` is clearer and won't confuse the HA config UI. The mapstructure tag maps the YAML key; the struct field name is internal. Use `set_defaults` as the external config key.
  - Add schema validation tests that read the actual `config.json` and verify the `scheduler_mode` list values.
- **Patterns to follow:** Discovery entity construction at `pkg/mqtt/discovery.go:84-94`. HA schema list enums at `home-assistant/addons/sbam/config.json:73-74` (`forecast_horizon`, `consumption_horizon`). Schema test pattern at `pkg/cmd/config_schema_test.go`.
- **Test scenarios:**
  - `BuildDiscovery` returns a `scheduler_mode` sensor with correct unique ID and value template
  - HA config.json `scheduler_mode` schema accepts `crontab` and `windows`, rejects `auto` and arbitrary strings
  - Window sub-schema accepts `tick_minutes: 30`, `set_defaults: true`, `before_end_defaults_minutes: 5`
  - Window sub-schema rejects `tick_minutes: 0`, `tick_minutes: "abc"`
  - All new window fields are optional — a window with no new fields is valid
- **Verification:** `make test` passes. HA add-on config validation accepts the new schema. Discovery payload is valid JSON with the `scheduler_mode` entity.

---

## Scope Boundaries

### Deferred to Follow-Up Work

- `auto` mode logic — owned by issue #149 (`scheduler.mode: auto` — derive windows from forecast/consumption/SoC).
- Crontab removal — deferred to v3.0.0 cleanup per `STRATEGY.md`.
- README and add-on documentation updates for the deprecation timeline — handled as a separate docs PR or paired with the auto-mode launch.

---

## Risks & Dependencies

- **The `set_defaults` key name in HA config.** The requirements doc uses `defaults` as the field name, but in JSON/YAML configuration surfaces, `set_defaults` is clearer and avoids keyword confusion. This plan uses `set_defaults` for the external config key and `Defaults` for the internal struct field (with `mapstructure:"set_defaults"` tag). Confirm with the requirements doc if this deviates.
- **Depends on the multi-window infrastructure from #146** — the `windows:` list, `ValidateWindows`, and `ResolveActiveWindow` must exist before windows mode can function. These are already merged on the branch.
- **Depends on the forecast/consumption horizon infrastructure from #145** — per-window horizon overrides are already in the Window struct and `validateWindowFields`.
- **The `time.Ticker` reset on window change** uses a new ticker for each transition. The Go runtime handles this efficiently, but verify no ticker leak on rapid window transitions (the `Stop()` call on the old ticker prevents the leak).

---

## Sources / Research

- Requirements document: `docs/brainstorms/2026-06-11-scheduler-mode-selector-requirements.md`
- `pkg/cmd/schedule.go:24-30` — flag variable block
- `pkg/cmd/schedule.go:269-303` — `registerScdCmd` flag registration
- `pkg/cmd/schedule.go:414-476` — `checkScheduleschedule` validation
- `pkg/cmd/schedule.go:524-535` — `makeBasePayload`
- `pkg/cmd/schedule.go:571-619` — `crontabSchedule`
- `pkg/cmd/schedule_runner.go:30-52` — `RunnerConfig`
- `pkg/cmd/schedule_runner.go:77-83` — `Runner` struct
- `pkg/cmd/schedule_runner.go:119-135` — `Run` loop
- `pkg/cmd/schedule_runner.go:164-300` — `Tick` method
- `pkg/cmd/schedule_runner.go:540-576` — `resolveActiveWindow`
- `pkg/power/window.go:34-41` — `Window` struct
- `pkg/power/window.go:111-132` — `validateWindowFields`
- `pkg/mqtt/types.go:23-41` — `StatePayload`
- `pkg/mqtt/discovery.go:51-125` — `BuildDiscovery`
- `pkg/mqtt/publisher.go:12-17` — `PublishState`
- `home-assistant/addons/sbam/config.json` — HA add-on schema
- `pkg/cmd/root.go:41-64` — viper and cobra init
- `pkg/cmd/root.go:79-90` — `bindFlags`
- `STRATEGY.md` — v2.1.0 milestone, v3.0.0 crontab removal
- Plan archive: `docs/implementations/archive/146-issue-multi-window-charging/` — multi-window precedent
- Plan archive: `docs/implementations/archive/87-issue-schedule-runner-refactor/` — runner architecture
