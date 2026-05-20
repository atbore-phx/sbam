# Feature: pkg/mqtt scaffold

> Source issue: [#84](https://github.com/atbore-phx/sbam/issues/84)
> Fetched: 2026-05-05
>
> Slug: `84-issue-mqtt-scaffold` · Created: 2026-05-03

## Summary
Create the initial `pkg/mqtt` package scaffold for the v2.0.0 MQTT feed effort tracked in #64. The package must define MQTT configuration and payload types, expose a `Client` interface, provide a Paho-backed implementation, return a noop client when MQTT is disabled, and include typed publish helpers that do not let MQTT failures break the scheduling workflow.

## Motivation / User Story
As an sbam operator preparing the v2.0.0 MQTT feed, I need a standalone MQTT package foundation so later discovery, command parsing, and schedule-runner integration work can build on stable typed interfaces without coupling MQTT code to the existing command, Fronius, power, or storage packages.

## Scope
- In scope: Create `pkg/mqtt/types.go` with `Config`, `StatePayload`, `ErrorPayload`, `AckPayload`, `Intent`, `IntentKind`, and `DiscoveryEntity`.
- In scope: Create `pkg/mqtt/client.go` with a `Client` interface, Paho-backed implementation, and a `noop{}` implementation returned by `New(cfg)` when `mqtt_enabled=false`.
- In scope: Create `pkg/mqtt/publisher.go` with typed helpers `PublishState`, `PublishError`, and `PublishAvailability`.
- In scope: Add unit/integration tests in `pkg/mqtt/mqtt_test.go` using `github.com/mochi-mqtt/server/v2` as an in-process broker and `defer broker.Close()` cleanup.
- Out of scope: Home Assistant discovery payload implementation, tracked separately in #85.
- Out of scope: MQTT command parser implementation, tracked separately in #86.
- Out of scope: Schedule runner refactor/integration, tracked separately in #87.
- Out of scope: GitHub workflow, branch, PR, issue comment, or issue close automation.

## Functional Requirements
- `New(cfg)` returns a noop client when MQTT is disabled.
- The noop client must not open broker connections.
- The noop client must not emit new log lines for disabled MQTT operation.
- When MQTT is enabled, the client connects to the configured broker using `github.com/eclipse/paho.mqtt.golang`.
- When MQTT is enabled, the client configures a retained QoS 1 last-will message of `<base>/availability=offline`.
- The client supports auto-reconnect with backoff from 1 second to 60 seconds and jitter.
- The client must support selecting between the existing custom jittered reconnect loop and Paho's built-in auto-reconnect behavior through a package-level configuration value, without CLI/config.yaml/Home Assistant wiring in this issue.
- The custom jittered reconnect strategy remains the default unless explicitly overridden, preserving the original issue 84 acceptance criteria.
- The Paho auto-reconnect strategy must be opt-in and isolated enough that either strategy can be removed later with a small, obvious code change.
- The client supports TLS with optional `mqtt_tls_ca_file`.
- `Config` must include the parent MQTT feed keys needed by later integration: enabled, broker, client ID, username, password, TLS CA file, TLS client cert, TLS client key, TLS insecure skip, topic prefix, and Home Assistant discovery toggle.
- Default topic prefix is `sbam`; normalized publish topics are `<prefix>/state`, `<prefix>/error`, and `<prefix>/availability`.
- `StatePayload` must use the parent #64 state fields even though schedule integration is deferred: last decision, reason, battery SoC %, battery capacity Wh, today's forecast Wh, paused flag, and next scheduled run as RFC3339-compatible time data.
- Publish helpers must publish typed state, error, and availability messages.
- Publish helper errors must be swallowed and logged at warn level so the schedule loop never sees an MQTT error.
- `pkg/mqtt` must not import `pkg/cmd`, `pkg/fronius`, `pkg/power`, or `pkg/storage`.

## Non-functional Requirements
- Backward compatibility: MQTT remains disabled by default and existing CLI behavior is unchanged until later integration work enables or wires the package.
- Safety / defaults: MQTT failures must not interrupt battery scheduling or Modbus/Fronius behavior.
- Performance: MQTT publish operations should use bounded waits/timeouts so broker issues do not hang callers indefinitely.
- Maintainability: Keep `pkg/mqtt` independent and testable, with typed payloads and a small public interface.
- Maintainability: Keep reconnect strategy selection separated from MQTT publish/subscribe behavior so the custom strategy or Paho strategy can be removed later without rewriting the client surface.

## Configuration Impact
- New CLI flags: None. Issue 84 is package scaffold only; cobra/viper wiring is deferred to later MQTT integration work.
- New config keys (`config.yaml`): None in this issue. The package `Config` type should still model the eventual keys from #64 (`mqtt_enabled`, `mqtt_broker`, `mqtt_client_id`, `mqtt_username`, `mqtt_password`, `mqtt_tls_ca_file`, `mqtt_tls_client_cert`, `mqtt_tls_client_cert_key`, `mqtt_tls_insecure_skip`, `mqtt_topic_prefix`, `mqtt_ha_discovery`).
- New env vars: None in this issue. Later CLI/config wiring should use the existing uppercase Viper convention.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): None in this issue; deferred to later integration work.

## External Integrations Touched
- MQTT broker: Add optional Paho client support for broker connections, retained availability, TLS CA configuration, and reconnect behavior.
- Solcast: Not touched.
- Fronius Solar API: Not touched.
- Fronius Modbus registers: Not touched.

## Acceptance Criteria
- [ ] `mqtt_enabled=false` -> `New(cfg)` returns a noop client; zero broker connections, zero new log lines.
- [ ] `mqtt_enabled=true` -> connects with LWT (`<base>/availability=offline`, retained, QoS 1), auto-reconnect (1s -> 60s, jittered), TLS support with optional `mqtt_tls_ca_file`.
- [ ] Reconnect strategy selection supports the default custom jittered strategy and an opt-in Paho auto-reconnect strategy, with tests proving both paths.
- [ ] In-process broker test exercises publish round-trip on `state`, retained availability, reconnect after broker stop/start, and bad credentials.
- [ ] No imports of `pkg/cmd`, `pkg/fronius`, `pkg/power`, or `pkg/storage` from `pkg/mqtt`.
- [ ] `make test` and `make build` (`CGO_ENABLED=0`) are green.

## Test Strategy
- Unit tests: Exercise noop client construction and publish helper behavior without broker side effects.
- Integration tests: Use `github.com/mochi-mqtt/server/v2` for in-process MQTT broker round-trip tests, retained availability checks, reconnect tests, and bad credentials.
- Edge cases: MQTT disabled, empty/normalized base topic, TLS CA file omitted, broker restart during reconnect, default reconnect strategy omitted.
- Failure cases: Bad broker credentials, connection failure, publish timeout, invalid TLS CA file.

## Risks / Open Questions
- `Config` fields should mirror the parent #64 MQTT key set, but defaults are applied by future CLI/config wiring rather than this package scaffold.
- Topic layout for command/ack/discovery remains deferred to #85/#86/#87; this issue only needs state/error/availability publish helpers.
- Paho reconnect/backoff behavior may need wrapping if built-in options do not provide jitter directly.
- Comparing custom reconnect and Paho auto-reconnect may reveal different timing, pending-token, or subscription behavior; keep both paths explicit until a later decision removes one.
- Mochi MQTT v2 test setup may require a small test helper to start/stop listeners cleanly for reconnect tests.

## Clarifications
- 2026-05-03: User confirmed issue 84 should stay limited to the `pkg/mqtt` package scaffold. Do not include config.yaml, cobra/viper, startup redaction, Home Assistant add-on, or run.sh wiring in this plan.
- 2026-05-03: User confirmed `StatePayload` should use the parent #64 state fields now, even though schedule integration is deferred.
- 2026-05-03: No additional constraints were provided.
- 2026-05-05: User asked to refresh the already implemented issue 84 plan by adding Paho's built-in auto-reconnect as an opt-in strategy alongside the existing custom jittered reconnect loop. The plan should check parent issue #64, test both strategies, and keep the separation simple so the less desirable strategy can be removed later.

## References
- https://github.com/atbore-phx/sbam/issues/84
- https://github.com/atbore-phx/sbam/issues/64
- https://github.com/atbore-phx/sbam/blob/release/v2.0.0/docs/implementations/64-issue-mqtt-feed/64-issue-mqtt-feed-PLAN.md#6-implementation-blueprint
- https://github.com/eclipse/paho.mqtt.golang
- https://github.com/mochi-mqtt/server
