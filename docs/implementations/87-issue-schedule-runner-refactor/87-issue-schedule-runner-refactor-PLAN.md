# PLAN: schedule runner refactor

> Feature slug: `87-issue-schedule-runner-refactor`  
> TASK: [87-issue-schedule-runner-refactor-TASK.md](87-issue-schedule-runner-refactor-TASK.md)  
> Issue: https://github.com/atbore-phx/sbam/issues/87  
> Parent issue: https://github.com/atbore-phx/sbam/issues/64  
> Reconciled: 2026-05-10

## 1. Task Analysis

The scheduler currently has MQTT config/state publication, but cron still calls the schedule workflow directly and MQTT commands are not routed through a single owner. This plan extracts a runner that serializes all schedule cycles and Modbus writes.

Key constraints:

- Preserve the current `mqtt.StatePayload` fields and publisher helpers.
- Use `pkg/mqtt.ParseIntent` / `PublishAck` from #86.
- Do not implement `set_reserve` in v2.0.0.
- Fix pause semantics so `{}` is an indefinite pause and `until` is optional auto-resume.

## 2. Implementation Steps

1. Add `pkg/cmd/schedule_runner.go` with `RunnerConfig`, `Runner`, `NewRunner`, `Run`, and `Submit`.
2. Move the current `schedule(...)` body into a runner method, preserving existing storage, power, Fronius, and state-publish behavior.
3. Represent pause state as nil/not paused, zero-time indefinite, or deadline pause.
4. Handle intents serially: tick/trigger_now, pause, resume, force_charge, set_defaults, shutdown.
5. Publish accepted/rejected acks with the #86 ack schema.
6. Publish retained state after each tick or state-changing command.
7. Publish `mqtt/error` for recoverable failures and avoid panics in the runner path.
8. Leave command subscription wiring to #88, but provide the runner API it needs.

## 3. Test Plan

- Unit test one successful tick with fake storage/power/Fronius clients and a recording MQTT client.
- Unit test `pause {}` then tick: no Modbus writes and state has `paused=true`.
- Unit test timed pause auto-resume.
- Unit test force charge and set defaults acks.
- Unit test parse/validation failure path does not reach Fronius.
- Concurrency test: submit many intents concurrently and run with `go test -race ./pkg/cmd`.

## 4. Validation

```bash
go test ./pkg/cmd -run 'Test.*Runner|Test.*Schedule'
go test -race ./pkg/cmd
make test
```
