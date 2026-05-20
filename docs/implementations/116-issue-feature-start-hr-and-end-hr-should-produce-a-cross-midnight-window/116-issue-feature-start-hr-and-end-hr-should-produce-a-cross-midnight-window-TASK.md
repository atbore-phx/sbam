# Feature: start_hr and end_hr should produce a cross-midnight window

> Source issue: [#116](https://github.com/atbore-phx/sbam/issues/116)
> Fetched: 2026-05-16

> Slug: `116-issue-feature-start-hr-and-end-hr-should-produce-a-cross-midnight-window` · Created: 2026-05-16

## Summary
Support cross-midnight charging windows for `start_hr` and `end_hr`, so a range such as `22:00` to `06:00` is treated as a valid window spanning midnight instead of being rejected because the start time is later than the end time.

## Motivation / User Story
Users need charging and reserve windows that align with overnight electricity provider tariffs and longer overnight charging periods. The current implementation assumes `start_hr < end_hr` within the same day, which prevents charging between the configured start time and midnight when an intended window spans into the following day.

## Scope
- In scope: accept cross-midnight time ranges for the main charge window (`start_hr` / `end_hr`).
- In scope: apply the same cross-midnight semantics to the battery reserve window (`batt_reserve_start_hr` / `batt_reserve_end_hr`).
- In scope: preserve same-day time range behavior where the start time is before the end time.
- In scope: keep invalid time strings rejected with clear errors.
- In scope: keep equal start and end times invalid.
- Out of scope: changing the charging or reserve decision algorithm beyond time-window validation and active-window evaluation.
- Out of scope: adding, renaming, or changing precedence for CLI flags, environment variables, `config.yaml` keys, or Home Assistant add-on schema fields.

## Functional Requirements
- The scheduler must accept `start_hr` values later than `end_hr` as a valid cross-midnight charge window.
- The runtime charge-window check must treat a cross-midnight window as active from `start_hr` through `23:59:59...` and from `00:00` through `end_hr`.
- The reserve-window validation and runtime reserve-window check must use the same cross-midnight semantics as the charge window.
- Same-day windows where `start_hr` is before `end_hr` must keep their current behavior.
- Equal start and end times must remain invalid unless a future feature explicitly defines all-day semantics.
- Malformed time values must continue to fail validation instead of silently becoming inactive or active.
- MQTT state payload fields that report charge/reserve window activity must reflect the corrected active-window semantics.

## Non-functional Requirements
- Backward compatibility: existing valid same-day configurations must continue to work without migration.
- Safety / defaults: the change must not add any new path that writes Fronius Modbus registers outside the existing schedule decision flow.
- Performance: the window check should remain simple local time arithmetic and avoid network or persistent storage dependencies.
- Maintainability: prefer a shared helper for validation/evaluation so charge and reserve windows cannot drift in behavior.

## Configuration Impact
- New CLI flags: none.
- New config keys (`config.yaml`): none.
- New env vars: none.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): none.
- Existing keys and flags affected by semantics only: `start_hr`, `end_hr`, `batt_reserve_start_hr`, `batt_reserve_end_hr`.

## External Integrations Touched
- Solcast: none.
- Fronius Solar API: none.
- Fronius Modbus registers: no register address or write-sequence changes; existing writes may occur during newly valid overnight windows only through the existing scheduler decision flow.
- MQTT: state payload window-active booleans should report the corrected charge and reserve window status.

## Acceptance Criteria
- [ ] `start_hr: "22:00"` and `end_hr: "06:00"` are accepted as a valid schedule charge window.
- [ ] A `22:00`-`06:00` window is active at `23:00` and `02:00` local time.
- [ ] A `22:00`-`06:00` window is inactive at `12:00` local time.
- [ ] Same-day windows such as `00:00`-`06:00` and `08:00`-`18:00` keep their existing behavior.
- [ ] Invalid time strings still return validation errors.
- [ ] Equal start and end times remain invalid.
- [ ] Battery reserve windows support the same cross-midnight behavior as charge windows.
- [ ] The change does not add or rename config keys, env vars, CLI flags, or Home Assistant add-on schema fields.

## Test Strategy
- Unit tests (`pkg/cmd`): cover `checkTimeRangeAt` or its successor for same-day, cross-midnight, boundary, outside-window, equal-time, and malformed-time cases.
- Unit tests (`pkg/cmd`): cover schedule validation so cross-midnight charge and reserve windows are accepted while malformed and equal windows are rejected.
- Unit tests (`pkg/cmd`): cover runner/MQTT payload window flags for cross-midnight charge and reserve windows if those flags depend on the same helper.
- Expected cases: `22:00`-`06:00` active at `23:00` and `02:00`; same-day windows unchanged.
- Edge cases: just before start, exactly at start, exactly at end, just after end, and equal start/end.
- Failure cases: malformed `HH:MM` strings for charge and reserve windows.

## Risks / Open Questions
- RESOLVED: both charge and reserve windows are in scope.
- RESOLVED: equal start and end times remain invalid.
- RESOLVED: no new configuration surfaces are required.
- Risk: validation currently checks ordering relationships between charge and reserve windows; those comparisons may need to be reinterpreted carefully for nested cross-midnight windows.
- Risk: cron default-reset scheduling based on `end_hr` may need review so cross-midnight windows still reset defaults at the intended local end time.

## References
- [GitHub issue #116](https://github.com/atbore-phx/sbam/issues/116)
- Related prior plan: [115-issue-fix-crontab-timezone-window](../115-issue-fix-crontab-timezone-window/115-issue-fix-crontab-timezone-window-PLAN.md)

## Clarifications
- 2026-05-16: Use slug `116-issue-feature-start-hr-and-end-hr-should-produce-a-cross-midnight-window`.
- 2026-05-16: Apply cross-midnight support to both charge and reserve windows.
- 2026-05-16: Preserve current invalid behavior for equal start and end times.
- 2026-05-16: Do not add or rename configuration, environment, CLI, or Home Assistant schema fields.
- 2026-05-16: Include acceptance examples for validation acceptance, active times on both sides of midnight, outside-window behavior, invalid strings, and same-day behavior.
