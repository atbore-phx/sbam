# Feature: Schedule Runner Refactor

> Source issue: [#87](https://github.com/atbore-phx/sbam/issues/87)
> Fetched: 2026-05-10
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)
> Reconciled: 2026-05-10
> Slug: `87-issue-schedule-runner-refactor`

## Summary

Refactor the current `schedule` workflow into a single-goroutine runner so cron ticks and MQTT commands cannot access Fronius Modbus concurrently. The runner must build on the existing MQTT state publication landed by #85 and the parser/ack APIs landed by #86, while preserving cron behavior and keeping no-cron mode alive for MQTT command handling when `mqtt_enabled=true`.

## Motivation / User Story

As an sbam operator using MQTT commands and scheduled charging together, I need all battery-control actions to flow through one runner so Modbus writes are serialized, pause/resume state is respected consistently, and transient schedule failures are logged/published instead of panicking out of the cron path.

## Scope

- In scope: create a runner file under `pkg/cmd` (recommended: `schedule_runner.go`).
- In scope: move the body of the current schedule workflow behind a `Runner` that consumes a buffered channel serially.
- In scope: ensure only the runner goroutine performs Fronius Modbus writes.
- In scope: support cron tick, trigger_now, pause, resume, force_charge, set_defaults, and shutdown intents.
- In scope: publish state snapshots, command acks, and `mqtt/error` for non-fatal schedule/runner failures.
- In scope: keep `mqtt_enabled=false` behavior unchanged.
- Out of scope: Home Assistant add-on file changes (#89).
- Out of scope: README/project-structure release docs (#91).
- Out of scope: implementing `set_reserve`; keep it deferred to >= v2.1.
- Out of scope: command subscription wiring from MQTT topics into the runner; #88 consumes the runner API.

## Functional Requirements

- Add a `Runner` in `pkg/cmd` that holds grouped config, an `mqtt.Client`, `intents chan mqtt.Intent` with capacity 16, in-memory pause state, reserve state if still needed by existing config flow, and references/adapters for power, storage, and Fronius subsystems.
- Use the existing `pkg/mqtt.Intent` and `mqtt.IntentKind` types instead of introducing a duplicate command intent model in `pkg/cmd`.
- Provide a loop method (`Loop(ctx)` or `Run(ctx)`) that serially consumes intents; cron callbacks and MQTT command wiring must only submit intents.
- Provide `Tick(now)` or an equivalent runner method that performs one schedule cycle and returns `error` instead of panicking.
- Provide `Runner.Submit(intent mqtt.Intent)` as a non-blocking submit path; when the inbox is full, drop the intent, log a warning, and publish an error metric/message when MQTT is enabled.
- Support `IntentTick`, `IntentTriggerNow`, `IntentPause`, `IntentResume`, `IntentForceCharge`, `IntentSetDefaults`, and `IntentShutdown`.
- Preserve the #85 `mqtt.StatePayload` fields, including `pw_net_wh`, `charge_pct`, window flags, timestamps, and any current retained state behavior.
- Implement `pause {}` as indefinite pause; implement `pause {"until":"1h"}` and RFC3339 timestamps as timed pause; `resume` clears both pause forms.
- While paused, keep telemetry/state publication active with `last_decision="paused"`, but skip Fronius `ForceCharge` and other Modbus writes.
- Make the current HA Discovery pause button payload `{}` usable, either by extending the #86 parser or by adding a small parser adapter at the schedule wiring boundary.
- Publish accepted/rejected command acknowledgements using the #86 ack schema for command intents handled by the runner.
- Publish recoverable runner/schedule errors on `<base>/error` when MQTT is enabled.

## Non-functional Requirements

- Backward compatibility: when `crontab` is disabled and `mqtt_enabled=true`, the runner must remain active and wait for MQTT commands until shutdown.
- Backward compatibility: when `crontab` is disabled and `mqtt_enabled=false`, the command must exit cleanly.
- Backward compatibility: cron scheduling behavior must remain equivalent except that cron callbacks submit `IntentTick` rather than running Modbus work directly.
- Safety / defaults: only the runner may perform Fronius Modbus write helpers; document this single-writer invariant in the runner header comment.
- Safety / defaults: invalid command payloads must publish rejected acks and must never reach Fronius code.
- Reliability: no `panic()` calls may survive in the cron path; transient errors are logged and published.
- Concurrency: concurrent cron and MQTT submissions must be serialized by the runner and pass race testing.

## Configuration Impact

- New CLI flags: none.
- New config keys (`config.yaml`): none.
- New env vars: none.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): none for this issue; #89 owns add-on changes.
- Existing MQTT configuration must continue to follow flag > env > yaml precedence and `mqtt_enabled=false` must keep the MQTT client disabled/no-op.

## External Integrations Touched

- Solcast: no API contract change; existing forecast retrieval remains part of tick execution.
- Fronius Solar API: no endpoint contract change; existing battery/state retrieval remains part of tick execution.
- Fronius Modbus registers: no register map change; write calls must be serialized through the runner.
- MQTT: runner publishes state, error, and command ack messages through the existing `pkg/mqtt` client/publisher helpers; #88 owns topic subscription wiring.

## Acceptance Criteria

- [ ] No-cron mode with `mqtt_enabled=true` keeps the runner alive and waits for MQTT commands until process shutdown.
- [ ] No-cron mode with `mqtt_enabled=false` exits cleanly.
- [ ] Cron mode submits ticks to the runner instead of running Modbus work in the cron callback.
- [ ] `pause {}` prevents later ticks from writing Modbus until `resume`.
- [ ] `pause {"until":"1h"}` and RFC3339 deadlines auto-resume after the deadline.
- [ ] `trigger_now` runs the same cycle as a cron tick and publishes an accepted ack.
- [ ] `force_charge` invokes `fronius.ForceCharge` exactly once and publishes an accepted ack.
- [ ] `set_defaults` invokes `fronius.Setdefaults` exactly once and publishes an accepted ack.
- [ ] Invalid command payloads publish rejected acks and never reach Fronius code.
- [ ] Concurrent cron/MQTT submissions are serialized; `go test -race ./pkg/cmd` passes.
- [ ] No `panic()` calls survive in the cron path; transient errors are logged and published on `<base>/error`.
- [ ] `go test ./pkg/cmd -run TestRunner -v` covers tick behavior, pause/resume, force_charge ack/error flows, and serialized Modbus writes.
- [ ] `make test` is green.

## Test Strategy

- Unit tests in `pkg/cmd` should use fake power/storage/Fronius adapters where practical and a recording fake `mqtt.Client` for state, error, and ack publishes.
- Expected case: one successful tick publishes the preserved state payload and performs the expected Fronius action once.
- Expected case: `force_charge` and `set_defaults` intents publish accepted acks and call the corresponding Fronius helpers exactly once.
- Expected case: no-cron mode with `mqtt_enabled=true` blocks until SIGINT/SIGTERM while the runner loop is active.
- Edge case: `pause {}` then tick publishes paused state and performs no Modbus writes until `resume`.
- Edge case: timed pause with relative duration and RFC3339 deadline auto-resumes after the deadline.
- Edge case: submitting many intents concurrently keeps Modbus writes serialized and passes `go test -race ./pkg/cmd`.
- Failure case: invalid command payloads publish rejected acks and never reach Fronius code.
- Failure case: runner inbox full drops the intent, logs a warning, and publishes an error when MQTT is enabled.
- Failure case: storage, Solcast, or Fronius errors return from `Tick`, are logged/published, and do not panic in cron mode.
- Integration-style tests may use `httptest.NewServer` for Solcast/Fronius HTTP APIs and `tbrandon/mbserver` for Modbus, with `defer server.Close()`/cleanup.

## Risks / Open Questions

- The issue requests keeping `pkg/cmd` import boundaries narrow: `pkg/cmd` may import `pkg/mqtt`, but Fronius access should stay behind adapters owned by `pkg/cmd` where feasible. Current code may already import `pkg/fronius`; the plan should minimize any new coupling while respecting existing structure.
- The issue text mentions `reserve atomic.Int64` and `IntentSetReserve`, but the reconciliation comment defers `set_reserve` to >= v2.1. Treat reserve support as preserved internal state only if existing schedule logic requires it; do not expose a v2.0 command path.
- The issue body includes branch/PR instructions targeting `release/v2.0.0`; this planning workflow must not create branches, push, or comment on GitHub issues.

## References

- Issue: [#87](https://github.com/atbore-phx/sbam/issues/87)
- Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)
- Depends on: [#86](https://github.com/atbore-phx/sbam/issues/86)
- Blocks: [#88](https://github.com/atbore-phx/sbam/issues/88)
- Existing v2.0.0 MQTT feed plan: [64-issue-mqtt-feed-PLAN.md](../64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md)

## Clarifications

### 2026-05-10 issue/comment reconciliation

- Local docs use `docs/implementations/87-issue-schedule-runner-refactor/`, matching the issue comment rather than the full title-derived slug.
- Use the existing `pkg/mqtt.Intent` and `IntentKind` types instead of introducing a duplicate command intent type in `pkg/cmd`.
- `set_reserve` is deferred to >= v2.1; keep the existing placeholder type but do not implement a v2.0 MQTT command path for it.
- `pause {}` must mean an indefinite pause; `pause {"until":"1h"}` or an RFC3339 timestamp means pause until that deadline; `resume` clears either form.
- The current HA Discovery pause button publishes `{}`, so this issue must make that payload usable, either by extending the #86 parser or by a small parser adapter in the schedule wiring.
- Existing state payload fields from #85 (`pw_net_wh`, `charge_pct`, window flags, timestamps) must be preserved.
