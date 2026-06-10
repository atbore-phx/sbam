# Feature: Read inverter status before writing Modbus defaults

> Slug: `101-issue-read-inverter-status-before-writing-modbus-defaults` · Created: 2026-05-28
> Source issue: [#101](https://github.com/atbore-phx/sbam/issues/101)
> Fetched: 2026-05-28

## Summary
When a classifier error occurs (DecisionSkip), the code unconditionally writes default Modbus registers
to reset the inverter to normal mode. This is wasteful and invasive: it opens a TCP connection, reads
then writes five registers, and can overwrite the inverter's current state. Instead, the code should
first read the inverter's charging-mode register (`StorCtl_Mod` / 40349) and only write defaults if
the inverter is actually in a force-charge state (value != 0).

## Motivation / User Story
An sbam operator occasionally hits DecisionSkip (unexpected power state), at which point the code
blasts all default Modbus registers whether the inverter needs it or not. If the inverter is already
idle/normal, this is a no-op that still opens a Modbus connection and performs unnecessary writes.
Reading before writing avoids needless Modbus traffic and respects the inverter's current state.

## Scope
- In scope: read `StorCtl_Mod` (40349) before writing defaults on classifier-error path; skip write if
  the inverter is already in normal mode (value 0).
- Out of scope: changing other code paths (e.g., normal `ForceCharge` calls, `Setdefaults` command),
  changing `Connectmodbus` helpers, or adding config flags.

## Functional Requirements
- On classifier error (DecisionSkip), open a Modbus connection and read register 40349.
- If the register value is 0 (normal mode), log and skip the write; close the connection immediately.
- If the register value is non-zero (e.g., 2 = charging limited), proceed with writing defaults as
  today.

## Non-functional Requirements
- Backward compatibility: no functional change — when defaults must be written, they still are.
- Safety: a Modbus read failure should NOT suppress the defaults write. If we can't read, we write
  (fail-safe).
- Performance: adding one register read before the write adds negligible latency (single Modbus TCP
  round-trip).

## Configuration Impact
- New CLI flags: none
- New config keys (`config.yaml`): none
- New env vars: none
- Home Assistant add-on schema changes: none

## External Integrations Touched
- Solcast: none
- Fronius Solar API: none
- Fronius Modbus registers: `StorCtl_Mod` (40349) — read-only check before writing defaults

## Acceptance Criteria
- [ ] When `ClassifyDecision` errors and the inverter's `StorCtl_Mod` is 0, no Modbus write occurs.
- [ ] When `ClassifyDecision` errors and the inverter's `StorCtl_Mod` is non-zero, defaults are written.
- [ ] When the Modbus read fails, defaults are written (fail-safe).
- [ ] Covered by unit tests (expected, edge, failure cases).

## Test Strategy
- Unit tests with mock Modbus server (`mbserver`):
  - Classifier error + StorCtl_Mod=0 → no write (test log or verify registers unchanged)
  - Classifier error + StorCtl_Mod=2 → defaults written
  - Modbus read failure → defaults written (fail-safe)
- Edge cases: connection failure, read timeout.

## Risks / Open Questions
- Low risk: the change adds one read before a write; if the read fails we fall back to writing.

## References
- [#101](https://github.com/atbore-phx/sbam/issues/101)
- `pkg/fronius/schedule.go` — current classifier-error default handling
- `pkg/fronius/configure.go` — `Setdefaults`, `ForceCharge`, `Connectmodbus`, Modbus register constants