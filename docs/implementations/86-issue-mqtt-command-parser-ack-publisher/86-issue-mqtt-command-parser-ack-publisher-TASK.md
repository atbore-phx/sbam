# Feature: MQTT command parser and ack publisher

> Slug: `86-issue-mqtt-command-parser-ack-publisher` · Created: 2026-05-10

> Source issue: [#86](https://github.com/atbore-phx/sbam/issues/86)
> Fetched: 2026-05-10

## Summary

Implement the `pkg/mqtt` command parsing and acknowledgement layer for the v2.0.0 MQTT feed effort. This feature turns inbound MQTT command topics and payloads into validated typed intents, rejects malformed or unsafe input without panics, and publishes a structured JSON acknowledgement for every command attempt.

## Motivation / User Story

This is part of the v2.0.0 MQTT feed tracked by [#64](https://github.com/atbore-phx/sbam/issues/64). Operators need sbam to accept remote commands from MQTT tools such as Home Assistant, Node-RED, Grafana, or n8n while preserving Modbus safety. A pure parser plus ack publisher gives the later schedule runner a narrow, testable boundary: all attacker-controlled MQTT input is validated before any business logic or Modbus write path can see it.

## Scope

- In scope: add pure command parsing APIs in `pkg/mqtt`; define a minimal typed command intent shape; build ack topic/payload publishing helpers; add table-driven unit tests for valid, edge, and failure cases.
- In scope: document the boundary with the parent MQTT feed plan and keep this issue focused on parser and ack APIs only.
- Out of scope: real subscription lifecycle wiring into the schedule runner; Modbus actions; cron or runner refactors; Home Assistant discovery changes; new configuration keys; broker integration tests unless needed for the ack publisher helper already backed by the existing `pkg/mqtt.Client` abstraction.

## Functional Requirements

- Provide a `pkg/mqtt` parser API for MQTT command messages. The issue requests `func ParseIntent(topic string, payload []byte) (Intent, error)`; the implementation plan may adapt the exact name only if it matches existing `pkg/mqtt` conventions and keeps the public parser pure.
- Define a minimal typed intent carrying the canonical command name and applicable parsed parameters:
  - `force_charge`: `target_pct` and optional `duration_s`.
  - `pause`: parsed future deadline from `until`.
  - `trigger_now`, `set_defaults`, `resume`: no command-specific fields.
- Support these canonical topics below the command prefix: `cmd/trigger_now`, `cmd/force_charge`, `cmd/set_defaults`, `cmd/pause`, `cmd/resume`.
- Validate `cmd/force_charge` JSON payload:
  - `target_pct` is required and must be in `[1,100]`.
  - `duration_s` is optional and must be in `[0,86400]` when present.
- Validate `cmd/pause` JSON payload:
  - `until` must parse as either `time.RFC3339` or a Go `time.ParseDuration` string.
  - The resulting deadline must be in the future.
- Validate `cmd/trigger_now`, `cmd/set_defaults`, and `cmd/resume` payloads as either empty or `{}`.
- Bound command payload size with `MaxPayloadBytes = 4096`.
- Every command attempt must be representable as an ack published on `<base>/cmd/<name>/ack` with JSON matching the issue contract:

```json
{
  "ts": "<RFC3339>",
  "command": "<name>",
  "accepted": true,
  "error": "omitted when accepted=true"
}
```

- Unknown sub-topics must produce `accepted=false` with `error="unknown command"`.

## Non-functional Requirements

- Backward compatibility: existing MQTT APIs and tests must keep working; this feature must not change behavior when MQTT command parsing is unused.
- Safety / defaults: command payloads are attacker-controlled; the parser and ack helper must never panic on malformed topics, malformed JSON, wrong JSON types, oversized payloads, or out-of-range values.
- Performance: parsing must be pure, bounded, and table-driven; reject payloads larger than 4096 bytes before expensive decoding.
- Resource use: this feature should not introduce long-lived goroutines. No-goroutine-leak acceptance is satisfied through manual inspection plus focused unit tests; do not add `goleak` unless the implementation later introduces lifecycle-managed goroutines.
- Observability: errors should be structured enough for caller code to choose a stable ack error message without stringly typed branching where avoidable.

## Configuration Impact

- New CLI flags: none.
- New config keys (`config.yaml`): none.
- New env vars: none.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): none.

## External Integrations Touched

- Solcast: unchanged.
- Fronius Solar API: unchanged.
- Fronius Modbus registers: unchanged; invalid commands must not be able to reach any future Modbus dispatch path.
- MQTT broker: ack publishing uses the existing `pkg/mqtt.Client` abstraction and topic prefix conventions; no new external broker requirement should be introduced for parser tests.
- Home Assistant: command button payload compatibility should be preserved for the command topics described by the parent MQTT feed plan.

## Acceptance Criteria

- [ ] Table-driven tests cover every canonical command happy path.
- [ ] Tests cover malformed JSON, wrong JSON types, out-of-range values, oversized payloads, and unknown sub-topics.
- [ ] `force_charge` rejects missing `target_pct`, `target_pct=0`, `target_pct=101`, negative `duration_s`, and `duration_s>86400`.
- [ ] `pause` accepts future RFC3339 deadlines and positive Go duration strings, and rejects past deadlines, invalid timestamps, and non-positive durations.
- [ ] `trigger_now`, `set_defaults`, and `resume` accept empty payload and `{}` only.
- [ ] Ack payloads contain RFC3339 `ts`, canonical `command`, boolean `accepted`, and omit `error` when accepted.
- [ ] Unknown commands produce `accepted=false` with `error="unknown command"`.
- [ ] No new long-lived goroutines are introduced by this feature.
- [ ] `make test` is green.

## Test Strategy

- Unit tests (`pkg/mqtt`): table-driven parser tests for every canonical command.
- Unit tests (`pkg/mqtt`): ack payload builder/publisher tests with a fake `Client` implementation that captures topic, QoS, retained flag, and JSON payload.
- Edge cases: empty payload, `{}`, payload exactly at `MaxPayloadBytes`, `force_charge` boundary values `target_pct=1`, `target_pct=100`, `duration_s=0`, `duration_s=86400`, `pause` deadline barely in the future.
- Failure cases: malformed JSON, wrong field types, missing required fields, unknown topics, payload over `MaxPayloadBytes`, out-of-range integer values, past or invalid pause deadlines.
- Validation gate: run `make test`; optionally run focused `go test ./pkg/mqtt -run 'Test.*Command|Test.*Ack'` while implementing.

## Risks / Open Questions

- The parent [64-issue-mqtt-feed-PLAN.md](../64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md) uses older `Command`/`Ack` sketches and includes `set_reserve`, while issue #86 explicitly lists only `trigger_now`, `force_charge`, `set_defaults`, `pause`, and `resume`. For this issue, #86 plus the clarifications below are authoritative.
- The issue names `pkg/mqtt/commands.go`, while the parent plan names `command.go`; the implementation should choose the filename that best matches existing package naming and document the choice in the PLAN.
- The exact exported parser/type names should follow current `pkg/mqtt` conventions after codebase research.

## References

- Issue: [#86 pkg/mqtt: cmd/* parser + ack publisher](https://github.com/atbore-phx/sbam/issues/86)
- Parent issue: [#64 MQTT feed](https://github.com/atbore-phx/sbam/issues/64)
- Parent plan: [64-issue-mqtt-feed-PLAN.md](../64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md)
- MQTT scaffold dependency: [#84](https://github.com/atbore-phx/sbam/issues/84)

## Clarifications

2026-05-10:

- Use the shorter slug `86-issue-mqtt-command-parser-ack-publisher`.
- Scope this issue to parser and ack APIs only; real MQTT subscriber wiring belongs to a later runner/subscriber task.
- Use a minimal typed intent: command enum/name plus `TargetPct`, `Duration`, and `PauseUntil` as applicable.
- Check the parent `64-issue-mqtt-feed-PLAN.md` for context, but resolve conflicts in favor of issue #86 and these clarifications.
- Use `MaxPayloadBytes = 4096`.
- Satisfy no-goroutine-leak acceptance through manual inspection plus unit tests rather than adding `goleak` for this parser-only feature.
