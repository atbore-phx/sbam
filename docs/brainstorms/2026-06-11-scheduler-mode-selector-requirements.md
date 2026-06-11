---
date: 2026-06-11
topic: scheduler-mode-selector
---

## Summary

Add a `scheduler_mode` config key (crontab | windows | auto) that explicitly selects how the schedule runner is driven, with a deprecation path for the legacy crontab. Windows mode replaces cron with an internal Go ticker (default 60 min, per-window overridable) and per-window set_defaults scheduling. The `auto` value is defined internally but hidden from HA schema and CLI help — manually setting it produces a rejection pointing to #149.

## Problem Frame

The schedule runner currently decides between legacy single-window behavior and multi-window behavior by checking whether `windows:` is populated — an implicit, easy-to-misconfigure heuristic. As sbam moves from v2.1's cron-only model toward v3.0's smart scheduler (multi-window, then auto), users need an explicit switch to opt into new behavior incrementally, without surprise engine changes and without two schedulers running simultaneously. The crontab path also needs a visible deprecation signal so users know it will be removed.

## Requirements

**Configuration and CLI:**
- R1. `scheduler_mode` accepts values `crontab`, `windows`, and `auto`. The default is `crontab`.
- R2. CLI flag `--scheduler-mode` and env var `SCHEDULER_MODE` follow the existing precedence (flag > env > config.yaml). The `auto` value is accepted at all three layers but omitted from CLI help text.
- R3. The HA add-on `config.json` schema exposes `scheduler_mode` as a list enum containing `crontab` and `windows` only. `auto` is excluded from the schema options.

**Crontab mode:**
- R4. When mode is `crontab`, the runner is driven by cron (existing `crontabSchedule`). `windows:` may be populated — cron drives the ticks, and each tick resolves the active window for its charge parameters via the existing `resolveActiveWindow` dispatch. When `windows:` is empty, the legacy `start_hr`/`end_hr` path is used. Both paths are valid.
- R5. Crontab mode logs a one-shot WARN at startup naming it as deprecated and publishes that warning as a field on the MQTT state payload.

**Windows mode:**
- R6. When mode is `windows`, the runner starts an internal `time.Ticker` and does not call `crontabSchedule`. If `crontab` is set to a non-default value, a one-shot WARN is logged naming the ignored field.
- R7. The default tick interval is 60 minutes. Each window may override this via an optional `tick_minutes` field (integer, minimum 1). When the active window changes, the ticker resets to the new window's interval.
- R8. Each window may specify `defaults: true` (default `false`) to enable a set_defaults reset. When enabled, the reset fires `before_end_defaults` minutes before the window's end time. `before_end_defaults` defaults to 5 minutes (integer, minimum 0). A value of 0 fires set_defaults at the exact window end.
- R9. When mode is `windows`, `windows:` must contain at least one entry or startup validation fails.

**Auto mode:**
- R10. When mode is `auto`, startup validation rejects with an error message that references GitHub issue #149 as the tracking issue.

**MQTT:**
- R11. `StatePayload` includes a `scheduler_mode` string field populated with the active mode value.
- R12. When crontab mode is active and the deprecation warning fires, `StatePayload` includes a `deprecation_warning` string field with the deprecation message.

**Backward compatibility:**
- R13. Configs without `scheduler_mode` default to `crontab` — no behavior change on upgrade. If `windows:` is also populated, cron drives the ticks and windows parameterize the charge decision (same as R4).

## Key Decisions

- **Crontab mode is compatible with `windows:`.** The branch already supports this: cron drives the ticks, `resolveActiveWindow` dispatches to the windows list for charge parameters. Forbidding this combination would add unnecessary validation and break a working configuration. The only forbidden combination is `mode: windows` with an empty `windows:` list.
- **Flat key `scheduler_mode`.** Matches the existing config.yaml convention (`start_hr`, `max_charge`, `mqtt_enabled`). CLI flag is `--scheduler-mode`, env var is `SCHEDULER_MODE`.
- **Per-window tick interval, default 60 min.** A single global tick would over-tick short windows or under-tick long ones. Per-window override gives each window the cadence it needs.
- **Per-window set_defaults, not a global schedule.** Cron mode uses a single `end_hr - 5 min` reset. Windows mode has multiple end times; attaching the reset to each window keeps the behavior scoped and predictable.
- **`auto` value defined but hidden.** The value exists so early adopters can manually set it and get a useful rejection message, but it is excluded from HA schema and CLI help to avoid presenting a non-functional option.
- **Deprecation warning on MQTT, not just logs.** HA users monitor dashboards, not container logs. A persistent state field ensures the deprecation is visible until addressed.

## Key Flows

- F1. Startup mode resolution
  - **Trigger:** Runner initialization in `schedule` command.
  - **Steps:** Read `scheduler_mode` via Viper (flag > env > config). If unset, default to `crontab`. If `crontab` → call `crontabSchedule` (cron + windows is valid; cron drives ticks, windows parameterize them). If `windows` and `windows:` is empty → fail with validation error. If `windows` and `windows:` populated → start internal ticker; if crontab value is non-default, log one-shot WARN. If `auto` → fail with #149 pointer.
  - **Covered by:** R4, R6, R9, R10, R13

- F2. Windows mode ticker lifecycle
  - **Trigger:** Runner enters `Run` loop in windows mode.
  - **Steps:** Compute default tick interval (60 min). Resolve active window. If active window has `tick_minutes`, use that instead. Start `time.Ticker` at the resolved interval. On each tick, submit `IntentTick`. When `resolveActiveWindow` returns a different window (or nil), reset the ticker to the new window's interval (or the default). Ticker stops on shutdown intent or context cancellation.
  - **Covered by:** R6, R7

- F3. Windows mode set_defaults scheduling
  - **Trigger:** A window with `defaults: true` becomes active, or a tick fires within a window that has `defaults` enabled.
  - **Steps:** Compute the reset time as `window.end - before_end_defaults`. When the current tick time is at or past the reset time and no prior reset has fired for this window instance, submit `IntentSetDefaults`. Mark the reset as fired so it does not re-trigger on subsequent ticks within the same window. Reset the fired flag on the next window transition.
  - **Covered by:** R8

## Acceptance Examples

- AE1. No `scheduler_mode` set, no `windows:` — runner starts in crontab mode, cron installed, `crontabSchedule` called. Deprecation WARN logged once. MQTT state shows `scheduler_mode: crontab`.
  - **Covers R1, R5, R11, R13**

- AE2. `scheduler_mode: crontab` with `windows:` containing two valid windows — runner installs cron, `crontabSchedule` called. Each cron tick calls `resolveActiveWindow` which dispatches to the windows list and returns the active window's charge parameters. MQTT state shows `scheduler_mode: crontab`.
  - **Covers R4**

- AE3. `scheduler_mode: windows` with `windows:` containing two valid windows — runner starts internal ticker, cron is not installed, `crontab` is ignored. If crontab was set to a non-default value, a one-shot WARN names the ignored field.
  - **Covers R6, R9**

- AE4. Window with `tick_minutes: 30` is active — ticker fires every 30 minutes while that window remains the active window. When the active window changes to one without `tick_minutes`, ticker resets to 60 minutes.
  - **Covers R7**

- AE5. Window with `defaults: true` and `before_end_defaults: 5`, ending at 06:00 — at 05:55 a set_defaults reset fires. On the next tick (still within the window), no duplicate reset fires. On the next window's activation, the fired flag is cleared.
  - **Covers R8**

- AE6. `scheduler_mode: auto` — startup fails with error: "scheduler_mode 'auto' is not yet available; track progress at https://github.com/atbore-phx/sbam/issues/149".
  - **Covers R10**

- AE7. Config with `windows:` populated, no `scheduler_mode` set — defaults to crontab mode. Cron drives ticks, windows provide charge parameters. Same observable behavior as AE2.
  - **Covers R13**

- AE8. `scheduler_mode: windows` with empty `windows:` — startup fails with error: "scheduler_mode is windows but no windows are configured".
  - **Covers R9**

## Scope Boundaries

- `auto` mode logic is out of scope — owned by issue #149.
- Crontab removal is out of scope — deferred to v3.0.0 cleanup.
- The `tick_minutes`, `defaults`, and `before_end_defaults` fields are Window struct additions (net-new schema) — the multi-window schema was defined in #146; this issue extends it.

## Sources

- GitHub issue [#147](https://github.com/atbore-phx/sbam/issues/147) — feature specification.
- `pkg/cmd/schedule.go:244-254` — crontabSchedule call gated on crontab != const_ct.
- `pkg/cmd/schedule.go:414-475` — checkScheduleschedule validation.
- `pkg/cmd/schedule_runner.go:85-99` — NewRunner and Runner struct (intents channel, cfg).
- `pkg/cmd/schedule_runner.go:116-135` — Run loop: single-channel select on ctx.Done and intents.
- `pkg/cmd/schedule_runner.go:164-299` — Tick: window resolution, cooldown, storage, forecast, Fronius handler.
- `pkg/cmd/schedule_runner.go:540-576` — resolveActiveWindow: windows vs legacy dispatch.
- `pkg/cmd/schedule_runner.go:602-625` — isInCooldown: end_hr proximity check.
- `pkg/cmd/schedule.go:571-619` — crontabSchedule: cron installation for tick + set_defaults.
- `pkg/power/window.go:34-41` — Window struct definition.
- `pkg/power/window.go:46-107` — ValidateWindows.
- `pkg/mqtt/types.go:23-41` — StatePayload.
- `pkg/mqtt/types.go:7-21` — Config struct.
- `home-assistant/addons/sbam/config.json` — HA add-on schema.
- `STRATEGY.md` — v2.1.0 milestone, v3.0.0 crontab removal target.
