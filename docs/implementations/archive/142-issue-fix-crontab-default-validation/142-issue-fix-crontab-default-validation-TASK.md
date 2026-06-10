# Feature: Fix crontab default validation in HA addon config

> Slug: `142-issue-fix-crontab-default-validation` · Created: 2026-05-27
> Source issue: [#142](https://github.com/atbore-phx/sbam/issues/142)
> Fetched: 2026-05-27

## Summary

The Home Assistant add-on `config.json` regex for the `crontab` field rejects `0 0 0 0 0` — the project's designated "disabled" default value. Users setting this value through the HA add-on configuration UI get a regex validation error.

## Motivation / User Story

As a Home Assistant add-on user, I want to disable scheduled cron execution by setting the crontab to `0 0 0 0 0` (the project's disabled sentinel), so that I can run sbam purely via MQTT commands without a recurring schedule.

## Scope

- In scope:
  - Fix the `crontab` regex in `home-assistant/addons/sbam/config.json` to accept `0 0 0 0 0`
  - Add a Go unit test that validates the crontab regex against key inputs
- Out of scope:
  - Handling blank/empty crontab input (requires Go-side validation changes)
  - Changes to cron expression parsing in the Go application
  - Changes to `robfig/cron` dependency
  - Other config.json schema changes

## Functional Requirements

- `0 0 0 0 0` must pass the HA add-on config schema validation for the `crontab` field
- All previously accepted crontab expressions must continue to be accepted

## Non-functional Requirements

- Backward compatibility: all previously accepted crontab expressions must still be accepted
- Safety / defaults: the default crontab value in `config.json` options remains `"00 00-05 * * *"` (the example shown to new users); the disabled sentinel `0 0 0 0 0` must be an accepted (but not the default) value
- Performance: N/A (regex evaluated once at config load)

## Configuration Impact

- New CLI flags: none
- New config keys (`config.yaml`): none
- New env vars: none
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`):
  - Update the `crontab` `match` regex to accept `0 0 0 0 0`

## External Integrations Touched

- None. This is purely a config schema fix.

## Acceptance Criteria

- [ ] Setting `crontab` to `0 0 0 0 0` in the HA add-on configuration UI passes schema validation
- [ ] All previously valid crontab expressions continue to be accepted
- [ ] `make test` and `make build` pass

## Test Strategy

- Add a Go unit test (e.g., in `pkg/cmd/`) that loads the crontab regex from `config.json` and validates key inputs
- Expected pass cases: `0 0 0 0 0`, `00 00-05 * * *`, `*/5 * * * *`, `@every 1h`, `@daily`, `1,15,30 * * * *`
- Expected fail cases: `not a cron expression`, empty string, `0` (too few fields)
- Use Go's `regexp.Compile` on the schema regex (extracted or replicated from `config.json`)

## Risks / Open Questions

- Why does the current regex (which appears to match `0 0 0 0 0` in standard PCRE and Python `re` engines) fail in Home Assistant? The HA `voluptuous` `match` filter may handle certain constructs differently. Regardless, the fix should explicitly add `0 0 0 0 0` as an accepted alternative so there is no ambiguity.
- RESOLVED: blank/empty crontab handling is out of scope for this fix.

## References

- Issue: [#142](https://github.com/atbore-phx/sbam/issues/142)
- Current crontab regex: `home-assistant/addons/sbam/config.json:47`
- Go crontab validation: `pkg/cmd/schedule.go:376-379`
- Disabled default constant: `pkg/cmd/schedule.go:40` (`const_ct = "0 0 0 0 0"`)

## Clarifications

- **2026-05-27**: Scope narrowed to `0 0 0 0 0` only — blank/empty crontab handling is out of scope (would require Go-side validation changes). Tests will be added as Go unit tests validating the regex from `config.json` against key inputs.
