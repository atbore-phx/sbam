# PLAN: wire MQTT into schedule subcommand

> Feature slug: `88-issue-wire-mqtt-schedule`  
> TASK: [88-issue-wire-mqtt-schedule-TASK.md](88-issue-wire-mqtt-schedule-TASK.md)  
> Issue: https://github.com/atbore-phx/sbam/issues/88  
> Parent issue: https://github.com/atbore-phx/sbam/issues/64  
> Reconciled: 2026-05-10

## 1. Current State

`pkg/cmd/schedule.go` already reads the twelve MQTT keys, builds `mqtt.Config`, calls `mqtt.InitWithCleanup`, and publishes basic schedule state. `src/utils/startup.go` already redacts MQTT secrets. `config.yaml` and the HA add-on already contain non-TLS MQTT options.

The missing pieces are command subscription, runner integration, ack routing, latest-state re-publication, and broad precedence tests.

## 2. Implementation Steps

1. After #87, construct `Runner` in `scdCmd.Run` and start `go runner.Run(ctx)`.
2. Change cron callbacks to submit runner tick intents.
3. Subscribe to `<prefix>/cmd/+` after MQTT connect. Keep the callback tiny: copy payload, parse, publish rejected ack if needed, submit accepted intent.
4. Ensure accepted command acks are published after runner execution, not before.
5. Track latest state in the runner or a small holder so HA birth messages can re-publish it.
6. Keep `mqtt.InitWithCleanup` fallback-to-noop behavior on setup/connect failures.
7. Extend `pkg/cmd/precedence_test.go` with table-driven coverage for the twelve MQTT keys.
8. Add focused tests with fake MQTT client and fake runner/client adapters.

## 3. Test Plan

- Disabled MQTT: no connect/subscribe path and existing schedule tests remain green.
- Enabled MQTT: command subscription topic is `<prefix>/cmd/+`.
- Parse failure: rejected ack is published immediately.
- Accepted command: runner receives the expected intent.
- HA status `online`: discovery and latest state are re-published.
- Precedence: flag > env > yaml > default for all twelve keys.

## 4. Validation

```bash
go test ./pkg/cmd -run 'Test.*MQTT|Test.*Precedence|Test.*Schedule'
make test
make build
```
