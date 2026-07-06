# Feature: Fix Crontab Timezone Window Evaluation

> Source issue: [#115](https://github.com/atbore-phx/sbam/issues/115)
> Fetched: 2026-05-16
> Slug: `115-issue-fix-crontab-timezone-window` · Created: 2026-05-16

## Summary
In `release/v2.0.0`, schedule ticks that occur inside the configured local charging window can be treated as outside the window because `start_hr` and `end_hr` are expressed as local wall-clock times while the runner compares them against `now.UTC()`. For a Central European timezone example, `00:00` and `01:00` local ticks for a `00:00`-`06:00` window are skipped as outside the charging window.

## Motivation / User Story
Users configure `start_hr`, `end_hr`, and cron schedules as local operating times. The scheduler should evaluate charge and reserve windows in the same timezone as the schedule tick/local runtime so grid charging decisions happen during the intended wall-clock period.

## Scope
- In scope: fix charge-window evaluation for `start_hr` / `end_hr` so valid local ticks are recognized as in-window.
- In scope: apply the same timezone semantics to reserve-window evaluation for `batt_reserve_start_hr` / `batt_reserve_end_hr`.
- In scope: add regression tests for a non-UTC timezone such as CET/CEST and boundary cases.
- Out of scope: adding a new explicit timezone configuration key.
- Out of scope: changing cron expression parsing or scheduling semantics beyond preserving the tick location for window checks.
- Out of scope: implementing cross-midnight window support; keep this issue focused on the UTC/local mismatch.

## Functional Requirements
- `start_hr` and `end_hr` must be compared against the schedule tick's local wall-clock time instead of a forced UTC time.
- With `start_hr: 00:00`, `end_hr: 06:00`, and a Central European schedule tick, local ticks from `00:00` through `06:00` must be considered inside the charge window.
- Local ticks after the configured end time, such as `07:00` and `08:00` for a `00:00`-`06:00` window, must be considered outside the charge window.
- Reserve-window checks must use the same timezone behavior as charge-window checks.
- Invalid time strings must continue to return errors rather than silently falling back to a default.

## Non-functional Requirements
- Backward compatibility: keep existing configuration keys, CLI flags, environment variables, and default values unchanged.
- Safety / defaults: do not broaden Modbus write behavior beyond the correctly evaluated charge/reserve windows.
- Performance: keep timezone/window checks local and allocation-light; no network or external service dependency is required.

## Configuration Impact
- New CLI flags: none.
- New config keys (`config.yaml`): none.
- New env vars: none.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): none.

## External Integrations Touched
- Solcast: none.
- Fronius Solar API: none.
- Fronius Modbus registers: no protocol or register changes; scheduling gates around existing behavior must be corrected.
- MQTT: no topic or payload schema changes expected, but published `in_charge_window` and reserve-window state should reflect the corrected timezone calculation.

## Acceptance Criteria
- [ ] A schedule tick at `00:00` CET/CEST with `start_hr: 00:00` and `end_hr: 06:00` is evaluated as inside the charging window.
- [ ] A schedule tick at `01:00` CET/CEST with the same window is evaluated as inside the charging window.
- [ ] Schedule ticks after the configured local end time, such as `07:00` and `08:00`, are evaluated as outside the charging window.
- [ ] Reserve-window checks use the same corrected timezone semantics.
- [ ] Existing UTC-based tests and invalid-time error behavior continue to pass.
- [ ] A regression unit test prevents the UTC/local mismatch from returning.

## Test Strategy
- Unit tests (`pkg/cmd`): add table-driven coverage for `checkTimeRangeAt` or the runner-level window calculation using a fixed non-UTC location.
- Expected case: `00:00` and `01:00` local ticks are in-window for `00:00`-`06:00`.
- Edge cases: exact start and end boundaries are inclusive; ticks immediately after the local end time are out-of-window.
- Failure cases: invalid `start_hr` or `end_hr` strings still return errors.
- Integration mocks: no `httptest` or `mbserver` needed for the focused timezone/window unit tests.
- Validation commands: `go test ./pkg/cmd`, `make test`, and `make build`.

## Risks / Open Questions
- Risk: `Runner.now()` currently normalizes time to UTC. The implementation should avoid unintentionally changing MQTT timestamp expectations or pause-deadline comparisons while fixing wall-clock window evaluation.
- Risk: daylight-saving transitions can create ambiguous local times; tests should use deterministic fixed dates and locations.
- Resolved: reserve-window evaluation is in scope.
- Resolved: new timezone configuration is out of scope.
- Resolved: cross-midnight window support is out of scope for this issue.

## References
- https://github.com/atbore-phx/sbam/issues/115

## Clarifications
- 2026-05-16: Include both charge-window and reserve-window checks in the fix.
- 2026-05-16: Treat `start_hr` / `end_hr` and reserve-window times as wall-clock values in the same timezone as the schedule tick/local runtime.
- 2026-05-16: Keep cross-midnight window behavior out of scope for this issue.