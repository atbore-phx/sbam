# Feature: wire MQTT into schedule subcommand

> Source issue: [#88](https://github.com/atbore-phx/sbam/issues/88)  
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)  
> Reconciled: 2026-05-10  
> Slug: `88-issue-wire-mqtt-schedule`

## Summary

Finish the schedule command MQTT integration after #87 introduces the single-goroutine runner. The codebase already has most flags, config loading, MQTT initialization, state publication, discovery publication, startup redaction, and basic docs from #85, so this issue should focus on command subscriptions, ack routing, runner hookup, and complete precedence tests.

## Scope

- Replace direct cron schedule callbacks with runner submissions.
- Subscribe to `<mqtt_topic_prefix>/cmd/+` when MQTT is enabled and connected.
- Parse commands with `pkg/mqtt.ParseIntent`.
- Publish rejected acks for parser failures.
- Submit accepted intents to the runner and let the runner publish execution acks.
- Preserve `homeassistant/status=online` discovery re-publication and add latest-state re-publication when available.
- Extend config precedence tests to all twelve standalone MQTT keys.
- Keep disabled MQTT behavior unchanged: no broker connection, no command subscription, no extra INFO logs, no Modbus behavior changes.

Out of scope:

- Home Assistant add-on service auto-discovery (#89).
- Full README release docs (#91).
- `set_reserve` command support.

## Acceptance Criteria

- [ ] With `mqtt_enabled=false`, schedule behavior is unchanged and no MQTT connection is attempted.
- [ ] With MQTT enabled and a reachable broker, `schedule` connects, publishes availability, subscribes to command topics, and publishes state after ticks.
- [ ] `cmd/trigger_now` submits an immediate tick.
- [ ] `cmd/pause`, `cmd/resume`, `cmd/force_charge`, and `cmd/set_defaults` route through the runner.
- [ ] Bad command payloads publish rejected acks and do not reach the runner.
- [ ] `homeassistant/status=online` re-publishes discovery and latest state when known.
- [ ] All twelve MQTT keys observe flag > env > yaml > default precedence.
- [ ] `make test` and `make build` are green.
