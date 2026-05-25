# Feature: Remove custom MQTT reconnect strategy, keep only Paho auto-reconnect

> Source issue: [#138](https://github.com/atbore-phx/sbam/issues/138)
> Fetched: 2026-05-25
> Slug: `138-issue-remove-custom-mqtt-reconnect-strategy` · Created: 2026-05-25

## Summary
Remove the `custom` MQTT reconnect strategy (currently the default) and make the Paho library's built-in `AutoReconnect` the sole reconnect mechanism.

## Motivation / User Story
As a **maintainer**,
I want **a single, well-tested MQTT reconnect code path**,
So that **there is less bespoke code to maintain, debug, and test, and reconnect behavior is delegated entirely to the mature Paho library**.

PR #103 surfaced the `custom` strategy as the default and `paho` as opt-in. The `custom` strategy is a jittered-exponential-backoff loop that reintroduces reconnect logic the Paho library already handles. Removing it simplifies the codebase and reduces test burden.

## Scope
- In scope:
  - Remove `pkg/mqtt/reconnect_custom.go` entirely.
  - Remove the `ReconnectStrategyCustom` and `ReconnectStrategyPaho` constants from `pkg/mqtt/types.go`.
  - Remove the `ReconnectStrategy` type from `pkg/mqtt/types.go`.
  - Remove the `reconnectManager` interface from `pkg/mqtt/reconnect.go` and inline `pahoReconnectManager.configure()` logic directly into `NewPaho()`.
  - Remove `normalizeReconnectStrategy(...)` from `pkg/mqtt/reconnect.go`.
  - Remove the `ReconnectStrategy` field from `pkg/mqtt.Config`.
  - Delete `pkg/mqtt/reconnect.go` if empty after removal, or merge remaining code into `paho.go`.
  - Remove `pkg/mqtt/reconnect_paho.go` and inline its logic into `paho.go`.
  - Update all unit tests to test only the Paho reconnect path.
  - Remove the Mochi-broker reconnect test table that iterates over both strategies; retain only the Paho variant.
  - Remove `reconnect_paho_test.go` after inlining the one meaningful assertion into `paho` tests.
- Out of scope:
  - Any new reconnect behavior or reconnect-related configuration knobs.
  - Exposing new CLI flags or configuration keys — this is a removal, not an addition.

## Functional Requirements
- [ ] `mqtt.NewPaho(cfg)` always configures Paho `AutoReconnect(true)` with `MaxReconnectInterval(reconnectMaxDelay)` — no branching on strategy.
- [ ] The `custom` reconnect loop (`customReconnectManager`) is deleted.
- [ ] The `ReconnectStrategy` type and its constants are removed.
- [ ] The `normalizeReconnectStrategy` function is removed.
- [ ] The `Config.ReconnectStrategy` field is removed.
- [ ] All call sites that set `ReconnectStrategy` or reference strategy constants are updated or removed.
- [ ] No user-facing `mqtt_reconnect_strategy` config key exists (confirmed: it was never wired through Cobra/Viper/HA add-on schema).

## Non-functional Requirements
- Backward compatibility: `mqtt_reconnect_strategy` was never a user-facing key, so no migration needed.
- MQTT reconnect behavior after the change is equivalent to today's `paho` strategy.
- Paho auto-reconnect is the only reconnect path. No fallback to custom logic.
- Reconnect timing remains bounded by `reconnectMaxDelay` (60 s), same as today.
- No additional goroutines beyond what Paho manages internally.

## Configuration Impact
- No user-facing config keys, CLI flags, env vars, or HA add-on schema entries to remove (confirmed: `ReconnectStrategy` was package-only, never wired through Cobra/Viper).
- Removed struct field: `pkg/mqtt.Config.ReconnectStrategy`.

## External Integrations Touched
- MQTT (Paho library reconnect configuration only — no broker or topic changes).

## Acceptance Criteria
- [ ] `pkg/mqtt/reconnect_custom.go` no longer exists.
- [ ] `pkg/mqtt/reconnect_paho.go` no longer exists (inlined into `paho.go`).
- [ ] `reconnectManager` interface and `normalizeReconnectStrategy()` no longer exist.
- [ ] `ReconnectStrategy` type and associated constants are removed.
- [ ] `Config.ReconnectStrategy` field is removed.
- [ ] `make test` passes with no strategy-related test cases.
- [ ] `make build` succeeds.

## Test Strategy
- Unit tests:
  - Expected case: `NewPaho(Config{Broker: "tcp://example.com:1883"})` produces options with `AutoReconnect=true` and `MaxReconnectInterval=reconnectMaxDelay`.
  - Edge case: reconnect with broker restart (Mochi) using Paho auto-reconnect only.
  - Failure case: invalid scheme, missing broker — unchanged from today.
- Integration/validation:
  - `make test`
  - `make build`
- Removed tests:
  - All `customReconnectManager`-specific unit tests (jitter, stop, reconnect-loop).
  - `TestNormalizeReconnectStrategy`.
  - `TestNewPahoReconnectStrategyConfiguration`.
  - Table-driven reconnect tests iterating both strategies.

## Risks / Open Questions
- [x] **Race-detector exclusion**: `mqtt_test.go:165` has a race-detector exclusion for the `paho` strategy. **Resolved:** remove the exclusion; the Paho-only reconnect test should run under `-race`.
- [x] `mqtt_reconnect_strategy` was never wired — no user-facing deprecation needed.

## References
- Issue [#84](https://github.com/atbore-phx/sbam/issues/84) — MQTT scaffold (introduced the reconnect strategy abstraction)
- PR [#103](https://github.com/atbore-phx/sbam/pull/103) — Configured custom reconnect as default, Paho as opt-in
- PLAN: `docs/implementations/84-issue-mqtt-scaffold/84-issue-mqtt-scaffold-PLAN.md`
