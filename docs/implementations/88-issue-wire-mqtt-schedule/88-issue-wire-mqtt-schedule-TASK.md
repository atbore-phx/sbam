# Feature: wire MQTT into schedule subcommand

> Source issue: [#88](https://github.com/atbore-phx/sbam/issues/88)  
> Fetched: 2026-05-13  
> Parent issue: [#64](https://github.com/atbore-phx/sbam/issues/64)  
> Parent fetched: 2026-05-13  
> Reconciled: 2026-05-13  
> Slug: `88-issue-wire-mqtt-schedule`

## Summary

Finish the `schedule` command MQTT integration after #87 introduces the long-lived single-goroutine runner. The codebase already has most flags, config loading, MQTT initialization, availability/state publishing, Home Assistant discovery publishing, startup redaction, and basic documentation from #84/#85/#86, so this issue focuses on command subscriptions, parser-failure acknowledgements, runner handoff, and complete MQTT config precedence tests.

## Motivation / User Story

Issue #64 asks for MQTT so operators can monitor and record what sbam is doing, and control sbam from Home Assistant, Node-RED, Grafana, n8n, or similar automation tools without adding complex logic inside sbam. Issue #88 is the schedule-command wiring piece of that umbrella: once MQTT is enabled, inbound command topics should become a safe remote-control path into the same serialized runner used by cron ticks.

## Reconciliation Notes

- The issue body originally described broader `schedule` MQTT wiring, including flag/env/yaml registration, `mqtt.Config` construction, connection setup, availability, command subscriptions, Home Assistant status handling, and startup redaction.
- The latest #64 and #88 comments narrow the remaining work because #84/#85/#86/#87 already delivered much of the original surface.
- Current #88 scope is subscription plumbing: subscribe to `<mqtt_topic_prefix>/cmd/+`, parse with `pkg/mqtt.ParseIntent`, publish rejected acks immediately, submit accepted intents to the runner, and let the runner publish execution acks.
- #87 owns runner lifecycle behavior. Its follow-up note says the runner remains active in no-cron mode when `mqtt_enabled=true`; #88 should wire MQTT command messages to `Runner.HandleCommand` or the equivalent accepted-intent path.
- Standalone MQTT config has twelve keys, including `mqtt_ha_discovery_prefix`, so precedence coverage must cover all twelve keys.
- `set_reserve` remains deferred to a later release and must not be exposed through MQTT in v2.0.0.

## Scope

In scope:

- Subscribe to `<mqtt_topic_prefix>/cmd/+` when MQTT is enabled and connected.
- Parse inbound command topics/payloads with `pkg/mqtt.ParseIntent`.
- Publish rejected command acks immediately when parsing fails.
- Submit accepted intents to the single-goroutine runner or call the runner command entry point added by #87.
- Let the runner publish accepted/error execution acks after commands complete.
- Ensure cron ticks submit through the runner when that is not already complete from #87.
 - Preserve `homeassistant/status=online` discovery re-publication.
- Extend `pkg/cmd` config precedence tests to all twelve standalone MQTT keys.
- Preserve disabled MQTT behavior: no broker connection, no command subscription, no extra INFO logs, and no Modbus behavior changes.

Out of scope:

- Home Assistant add-on Mosquitto service auto-discovery and add-on UX cleanup (#89).
- Full README MQTT topic/schema/command/migration documentation (#91).
- `set_reserve` command support.
- New MQTT client implementations or broker dependencies.
- Changes to Solcast, Fronius Solar API, or Fronius Modbus register semantics.

## Functional Requirements

- When `mqtt_enabled=true` and MQTT initialization returns a connected client, subscribe to `<mqtt_topic_prefix>/cmd/+` at QoS 1.
- Command messages must be copied out of the MQTT callback before parsing or dispatch so the callback does not retain broker-owned buffers.
- Accepted command set is the v2.0.0 command surface from #64/#86: `trigger_now`, `pause`, `resume`, `force_charge`, and `set_defaults`.
- Invalid topics, unknown commands, malformed JSON, oversize payloads, and out-of-range values must publish a rejected ack with the implemented #86 payload shape `{ts, command, accepted, error}` and must not reach the runner.
- Accepted commands must be serialized through the runner so cron ticks and MQTT commands cannot race on Modbus writes.
- `trigger_now` must run the same schedule path as a cron tick.
- `pause`, `resume`, `force_charge`, and `set_defaults` must route through the runner command handling path added by #87.
 - `homeassistant/status=online` must re-publish discovery.
- All twelve MQTT keys must continue to load through Viper with precedence `flag > env > yaml > default`.

## Non-functional Requirements

- Backward compatibility: default `mqtt_enabled=false` must preserve v1.x behavior, with no MQTT connection attempt and no Modbus behavior difference.
- Safety / defaults: MQTT commands are untrusted input and must be validated before runner submission; rejected commands must not call Fronius write helpers.
- Concurrency: all Modbus write paths must remain serialized by the runner.
- Observability: rejected acks and publish/subscribe failures should be logged with structured zap fields without logging secrets.
- Resource use: MQTT callbacks must remain small and non-blocking; a full runner queue should fail gracefully with an error ack or logged rejection rather than blocking the MQTT receive path.
- Security: `mqtt_password` and `mqtt_tls_client_cert_key` must remain redacted in startup dumps.

## Configuration Impact

Standalone MQTT keys to cover in precedence tests:

| YAML / flag | Env | Type | Default |
| --- | --- | --- | --- |
| `mqtt_enabled` | `MQTT_ENABLED` | bool | `false` |
| `mqtt_broker` | `MQTT_BROKER` | string | `""` |
| `mqtt_client_id` | `MQTT_CLIENT_ID` | string | `""` |
| `mqtt_username` | `MQTT_USERNAME` | string | `""` |
| `mqtt_password` | `MQTT_PASSWORD` | string | `""` |
| `mqtt_tls_ca_file` | `MQTT_TLS_CA_FILE` | string | `""` |
| `mqtt_tls_client_cert` | `MQTT_TLS_CLIENT_CERT` | string | `""` |
| `mqtt_tls_client_cert_key` | `MQTT_TLS_CLIENT_CERT_KEY` | string | `""` |
| `mqtt_tls_insecure_skip` | `MQTT_TLS_INSECURE_SKIP` | bool | `false` |
| `mqtt_topic_prefix` | `MQTT_TOPIC_PREFIX` | string | `sbam` |
| `mqtt_ha_discovery` | `MQTT_HA_DISCOVERY` | bool | `true` |
| `mqtt_ha_discovery_prefix` | `MQTT_HA_DISCOVERY_PREFIX` | string | `homeassistant` |

No new config keys are expected for this issue. Home Assistant add-on schema changes remain part of #89.

## External Integrations Touched

- Solcast: unchanged.
- Fronius Solar API: unchanged.
- Fronius Modbus registers: unchanged register semantics; write access remains serialized by the runner.
- MQTT broker: subscribe to command topics and publish command acks/state/discovery through the existing `pkg/mqtt.Client` interface.
- Home Assistant: respond to `homeassistant/status=online` by re-publishing discovery and latest state.

## Acceptance Criteria

- [ ] With `mqtt_enabled=false`, schedule behavior is unchanged and no MQTT connection is attempted.
- [ ] With MQTT enabled and a reachable broker, `schedule` connects, publishes availability, subscribes to `<mqtt_topic_prefix>/cmd/+`, and publishes state after ticks.
- [ ] `cmd/trigger_now` submits an immediate tick through the runner.
- [ ] `cmd/pause`, `cmd/resume`, `cmd/force_charge`, and `cmd/set_defaults` route through the runner.
- [ ] Bad command payloads publish rejected acks and do not reach the runner.
- [ ] Accepted commands publish accepted/error acks from runner execution, not directly from the MQTT callback.
- [ ] `homeassistant/status=online` re-publishes discovery and latest state when known.
- [ ] `mqtt_password` and `mqtt_tls_client_cert_key` remain rendered as `***` in `DEBUG=true sbam schedule ...` startup dumps.
- [ ] All twelve MQTT keys observe flag > env > yaml > default precedence.
- [ ] `make test` and `make build` are green.

## Test Strategy

- Unit tests in `pkg/cmd` should use recording fakes or the existing noop/factory test hooks rather than a required external broker.
- Expected case: MQTT enabled subscribes to `<prefix>/cmd/+`, accepted `trigger_now` reaches the runner, and a state snapshot is published after the tick.
- Expected case: `pause`, `resume`, `force_charge`, and `set_defaults` route to the runner command path and produce execution acks from the runner.
 - Edge case: `homeassistant/status=online` before any state exists re-publishes discovery without publishing an empty state.
- Edge case: disabled MQTT remains a no-connect/no-subscribe path and existing schedule lifecycle behavior remains green.
- Failure case: malformed JSON, unknown command, invalid topic, and out-of-range payloads publish rejected acks immediately and do not reach the runner.
- Failure case: MQTT subscribe/publish failures are logged and do not crash the `schedule` command.
- Precedence tests in `pkg/cmd/precedence_test.go` must cover flag, env, yaml, and default sources for all twelve MQTT keys.
- Validation commands: focused `go test ./pkg/cmd -run 'Test.*MQTT|Test.*Runner|Test.*Precedence|Test.*Schedule'`, `make test`, and `make build`.

## Risks / Open Questions

- Risk: the exact runner API from #87 may be `Submit`, `HandleCommand`, or both. The plan should bind to the actual code and avoid inventing a second command pathway.
- Risk: command ack ownership must remain clear. Parser failures belong in the MQTT callback; execution success/failure belongs in the runner.
- Risk: Paho callback goroutines must not block on a full runner queue.
 - Risk: Home Assistant birth re-publication must be reliable; avoid races with runner updates if a future state cache is added.
- No blocking open questions remain for planning; implementation should verify current #87 APIs before editing.

## References

- Issue #88: https://github.com/atbore-phx/sbam/issues/88
- Parent issue #64: https://github.com/atbore-phx/sbam/issues/64
- Parent TASK: [../64-issue-mqtt-feed/64-issue-mqtt-feed-TASK.md](../64-issue-mqtt-feed/64-issue-mqtt-feed-TASK.md)
- Parent PLAN: [../64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md](../64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md)

## Clarifications

- 2026-05-13: User confirmed the TASK should keep the narrowed remaining scope from the latest #88 comments and asked that parent issue #64 plus its TASK and PLAN be read before PLAN generation.
