# Feature: Configurable Forecast and Consumption Horizons

> Slug: `145-issue-configurable-forecast-consumption-horizons` · Created: 2026-06-01
> Source issue: [#145](https://github.com/atbore-phx/sbam/issues/145)
> Fetched: 2026-06-01

## Summary

Introduce explicit, configurable forecast and consumption horizons so charging decisions during night and daylight tariff windows are unambiguous and testable. Current `CheckSun` logic in `pkg/power/estimate.go` picks today before 12:00 and tomorrow from 12:00 onward. That works for cross-midnight night windows (both sides target the same upcoming solar day) but is unclear for daylight cheap-tariff windows where the relevant quantity is the PV still expected today, paired with the matching remaining consumption.

## Motivation / User Story

As an sbam user with time-dependent grid pricing, I want forecast and consumption horizons to be explicit and configurable per use case, so that night charging, daylight cheap-grid charging, and future tariff patterns can be modeled without relying on a hardcoded noon threshold.

This issue replaces #122.

## Scope

- In scope:
  - Add `forecast_horizon` setting with explicit, named modes.
  - Add `consumption_horizon` model so forecast and load assumptions stay aligned.
  - Preserve current behavior under `default`.
  - CLI flag, env var, `config.yaml`, Home Assistant add-on schema, README, tests.
- Out of scope:
  - Multi-window scheduling (separate issue).
  - `scheduler.mode` selector (separate issue).
  - Fronius-based dynamic consumption (separate issue).
  - Auto mode end-to-end (separate issue).
  - Modbus charge-control register changes.
  - `auto` and `custom` horizon modes (delivered in auto-mode and multi-window issues respectively).

## Functional Requirements

- Introduce `forecast_horizon` with these modes:
  - `default` — current compatibility behavior; forecast today before 12:00 and tomorrow from 12:00 onward (`next_solar_day` semantics as used today).
  - `next_solar_day` — forecast the upcoming useful solar day, appropriate for night charging.
  - `remaining_today` — forecast only periods from now through the end of the local day.
  - `today` — forecast the full local calendar day.
  - `tomorrow` — forecast the full next local calendar day.
  - `off` — disable forecast computation entirely; skip Solcast API calls and set forecast to 0. Battery reads and reserve charging within the window continue to work.
- Define `consumption_horizon` with at least:
  - `full_day` — today's behavior using static `pw_consumption`.
  - `remaining_today` — proportional remainder of the local day from `pw_consumption`.
- Treat Solcast `period_end` timestamps using local-day semantics where needed.
- Publish the active horizon on the MQTT state payload so HA users can see which horizon was used for each decision.
- Validation rejects unknown horizon values at startup with a clear error.

## Non-functional Requirements

- Backward compatibility:
  - `default` must preserve today's noon threshold behavior bit-for-bit.
  - Existing installations keep current behavior unless the user opts into a new mode.
- Safety / defaults:
  - Charging decisions remain conservative when forecast or consumption data is unavailable.
  - Unknown horizon values produce clear user-facing errors.
- Performance / reliability:
  - No additional Solcast API calls; reuse the existing cache.
  - Deterministic, fully covered by unit tests.

## Configuration Impact

- New CLI flags:
  - `--forecast_horizon` (default: `"default"`)
  - `--consumption_horizon` (default: `"full_day"`)
- New `config.yaml` keys:
  - `forecast_horizon`
  - `consumption_horizon`
- New env vars:
  - `FORECAST_HORIZON`
  - `CONSUMPTION_HORIZON`
- Home Assistant add-on config schema impact:
  - Add `forecast_horizon` and `consumption_horizon` options with enum validation.

## External Integrations Touched

- Solcast: forecast interval selection and local-day grouping.
- Fronius Solar API: none in this issue.
- Fronius Modbus registers: none.
- MQTT topics: state payload gains horizon metadata.

## Acceptance Criteria

- [ ] Night charging behavior is unchanged under `forecast_horizon=default`.
- [ ] Cross-midnight windows (e.g. 22:00–06:00) continue to target the upcoming solar day correctly.
- [ ] `off` skips all Solcast API calls and sets forecast to 0 (forecast-based charging is disabled).
- [ ] `next_solar_day`, `remaining_today`, `today`, `tomorrow` are documented and covered by unit tests.
- [ ] `consumption_horizon=remaining_today` produces a deterministic proportional value from `pw_consumption`.
- [ ] Invalid horizon configuration is rejected at startup with an actionable error.
- [ ] README and Home Assistant add-on docs explain the modes and the default behavior.
- [ ] MQTT state payload includes the active horizon names.

## Test Strategy

- Unit tests:
  - Expected case: night/cross-midnight window under `default` preserves current today-before-noon / tomorrow-after-noon behavior.
  - Expected case: `remaining_today` includes only forecast periods at or after the current time on the local day.
  - Expected case: `today` and `tomorrow` group Solcast timestamps by local calendar day.
  - Edge case: calls near midnight and near noon are deterministic.
  - Edge case: empty/partial forecast data returns a conservative decision.
  - Failure case: invalid horizon mode returns a validation error.
- Validation commands:
  - `make test`
  - `make build`
  - `go vet ./...`

## Risks / Open Questions

- Confirm timezone handling matches the fix in #115 (cross-midnight crontab/window logic).
- Decide MQTT field names for the active horizon so they stay stable across the auto-mode and multi-window issues.

## References

- Replaces #122.
- Related: cross-midnight window work (#115, #116).
- Current behavior: `CheckSun` in `pkg/power/estimate.go`.
- Epic: #151.

## Clarifications

### 2026-06-01 — Initial bootstrap

- **MQTT field names**: Use `forecast_horizon` and `consumption_horizon` directly in the state payload (no `active_` prefix). These names are stable for auto-mode and multi-window issues.
- **`forecast_horizon=off` behavior**: Skip Solcast API calls and set forecast to 0. Battery reads and reserve charging within the window still operate. This is not a full tick-skip — the runner still evaluates the charge window and reserve conditions.
- **User requested `off` mode**: Added as a first-class `forecast_horizon` value.
