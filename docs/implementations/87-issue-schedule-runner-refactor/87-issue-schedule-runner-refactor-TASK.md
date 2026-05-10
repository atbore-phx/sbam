# Feature: schedule runner refactor

> Source issue: [#87](https://github.com/atbore-phx/sbam/issues/87)  
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)  
> Reconciled: 2026-05-10  
> Slug: `87-issue-schedule-runner-refactor`

## Summary

Refactor the current `schedule` workflow into a single-goroutine runner so cron ticks and MQTT commands cannot access Fronius Modbus concurrently. The runner must build on the existing MQTT state publication landed by #85 and the parser/ack APIs landed by #86.

## Reconciled Decisions

- Use the existing `pkg/mqtt.Intent` and `IntentKind` types instead of introducing a duplicate command intent type in `pkg/cmd`.
- `set_reserve` is deferred to >= v2.1; keep the existing placeholder type but do not implement a v2.0 MQTT command path for it.
- `pause {}` must mean an indefinite pause; `pause {"until":"1h"}` or an RFC3339 timestamp means pause until that deadline; `resume` clears either form.
- The current HA Discovery pause button publishes `{}`, so this issue must make that payload usable, either by extending the #86 parser or by a small parser adapter in the schedule wiring.
- Existing state payload fields from #85 (`pw_net_wh`, `charge_pct`, window flags, timestamps) must be preserved.

## Scope

- Create a runner file under `pkg/cmd` (recommended: `schedule_runner.go`).
- Move the body of the current schedule workflow behind a `Runner` that consumes a buffered channel serially.
- Ensure only the runner goroutine performs Fronius Modbus writes.
- Support cron tick, trigger_now, pause, resume, force_charge, set_defaults, and shutdown intents.
- Publish state snapshots and command acks from the runner.
- Publish `mqtt/error` for non-fatal schedule/runner failures.
- Keep `mqtt_enabled=false` behavior unchanged.

Out of scope:

- Home Assistant add-on file changes (#89).
- README/project-structure release docs (#91).
- Reintroducing `set_reserve`.

## Acceptance Criteria

- [ ] Single-shot mode still runs one schedule cycle and exits cleanly.
- [ ] Cron mode submits ticks to the runner instead of running Modbus work in the cron callback.
- [ ] `pause {}` prevents later ticks from writing Modbus until `resume`.
- [ ] `pause {"until":"1h"}` auto-resumes after the deadline.
- [ ] `trigger_now` runs the same cycle as a cron tick and publishes an accepted ack.
- [ ] `force_charge` invokes `fronius.ForceCharge` exactly once and publishes an accepted ack.
- [ ] `set_defaults` invokes `fronius.Setdefaults` exactly once and publishes an accepted ack.
- [ ] Invalid command payloads publish rejected acks and never reach Fronius code.
- [ ] Concurrent cron/MQTT submissions are serialized; race tests pass.
- [ ] `make test` is green.
