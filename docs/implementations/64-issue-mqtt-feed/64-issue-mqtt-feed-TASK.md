# Feature: MQTT feed (v2.0.0)

> Slug: `64-issue-mqtt-feed` · Created: 2026-05-01

> Source issue: [#64 — MQTT feed](https://github.com/atbore-phx/sbam/issues/64)
> Fetched: 2026-05-01
> Milestone: [v2.0.0](https://github.com/atbore-phx/sbam/milestone/7)

## Summary

Add a first-class MQTT feed to sbam so that operators can both **observe** what
the scheduler is doing and **control** it remotely. The feature publishes
machine-readable state (battery SoC, capacity, daily forecast, last decision,
next scheduled run, paused flag) and accepts inbound commands (`pause`, `resume`,
`force_charge`, `set_defaults`, `set_reserve`, `trigger_now`) on a single MQTT
broker. When deployed as a Home Assistant add-on, sbam additionally publishes
**Home Assistant MQTT Discovery** payloads so that all sensors, the
`paused` `binary_sensor`, and the command `button` entities appear automatically
in the HA UI without manual YAML.

This is the umbrella feature for v2.0.0. Implementation is broken into
sub-issues #84–#91 (work breakdown is reproduced under "Work Breakdown" below).

## Motivation / User Story

From the original issue (verbatim user need):

> Currently the only way to see what's happening with sbam is to look at the
> logs. If sbam could send & receive MQTT messages then we would be able to
> 1) monitor & record what it's doing
> 2) Enable the control of sbam for complex tasks via Node-RED or similar
> (eliminating the requirement for complex logic within sbam itself).

Owner decision (issue comment, 2026-04-30): a custom HA Python integration +
REST API was considered and rejected for v2.0.0. MQTT + HA Discovery gives HA
users entities for free, also supports Node-RED / Grafana / n8n out of the box,
and avoids maintaining a Python codebase. A thin custom HA integration that
subscribes to the same MQTT topics can come in v3.0 if there is demand.

## Scope

In scope (v2.0.0):

- New `pkg/mqtt` package: `Client` interface + Eclipse Paho implementation +
  no-op implementation (used when `mqtt_enabled=false` and in tests).
- HA MQTT Discovery payloads (sensors, binary_sensor, buttons) emitted on
  connect and on `homeassistant/status = online`.
- Inbound command parser + ack publisher.
- Refactor of the `schedule` runner into a single goroutine driven by an
  `Intent` channel, so that cron ticks and MQTT commands cannot race on the
  Modbus TCP client.
- Wiring into the existing `schedule` cobra subcommand: 11 new
  flag/env/yaml keys, secret registry updates.
- Home Assistant add-on schema, `run.sh` exports, `DOCS.md`, `CHANGELOG.md`
  bump to `2.0.0`.
- Auto-discovery of the Home Assistant Mosquitto add-on via the supervisor
  services API (when running inside the HA add-on), with manual
  host/port/credentials override always available.
- Pure helper extraction: `pkg/fronius.ClassifyDecision` (currently inlined in
  `SetFroniusChargeBatteryMode`) so both the Modbus path and the MQTT
  publisher consume one source of truth for the "last decision" string.
- README MQTT section + Project Structure update + migration note.

Out of scope (deferred to ≥ v2.1):

- A custom Home Assistant Python integration (Zigbee2MQTT-style) that wraps
  the MQTT topics. The owner has explicitly deferred this to v3.0.
- A REST/HTTP control API for sbam.
- Prometheus / OpenTelemetry exposition (separate effort).
- Multi-broker fan-out, MQTT bridging, or persistent message stores.
- MQTT v5 features beyond what Paho enables by default (e.g.
  `RESPONSE_TOPIC`-based ack routing — sbam will use a fixed
  `sbam/cmd/<name>/ack` topic instead).
- Authentication via OAuth/JWT or external secret managers.

## Functional Requirements

1. Provide a `pkg/mqtt.Client` interface with at minimum:
   `Connect(ctx) error`, `Disconnect(ctx) error`,
   `Publish(ctx, topic string, qos byte, retained bool, payload []byte) error`,
   `Subscribe(ctx, topic string, qos byte, handler func(topic string, payload []byte)) error`,
   `IsConnected() bool`.
2. Ship two implementations of `pkg/mqtt.Client`:
   - `paho` — wraps `github.com/eclipse/paho.mqtt.golang`.
   - `noop` — used when `mqtt_enabled=false` and as the default test double.
3. Default topic prefix is `sbam`. Exact published topics:
   - `sbam/state` (retained, QoS 1) — JSON snapshot of last decision,
     battery SoC %, battery capacity Wh, today's forecast Wh, paused flag,
     next scheduled run (`time.RFC3339`).
   - `sbam/availability` (retained, QoS 1) — `online` / `offline` LWT.
   - `sbam/cmd/<name>` — inbound command payloads (JSON object).
   - `sbam/cmd/<name>/ack` (retained=false, QoS 1) — JSON
     `{ "status": "ok"|"error", "error": "...", "ts": "<RFC3339>" }`.
4. Inbound commands (v2.0.0 set):
   - `pause` — set runner to paused; cron ticks return early; no Modbus writes.
   - `resume` — clear paused flag.
   - `force_charge` — payload `{ "target_pct": 1..100, "duration_s": >=0 }`;
     issues a one-shot `fronius.ForceCharge`.
   - `set_defaults` — clears any forced charge by calling
     `fronius.Setdefaults`.
   - `set_reserve` — payload `{ "pw_batt_reserve": <Wh> }`; updates the
     in-memory reserve consumed by the next schedule tick. Persistence to
     `config.yaml` is **not** required.
   - `trigger_now` — runs one schedule cycle immediately (same code path as
     a cron tick).
5. Publish HA MQTT Discovery configs (retained, QoS 1) under
   `homeassistant/<component>/sbam/<object_id>/config` for:
   - `sensor`: battery SoC %, battery capacity Wh, today's forecast Wh,
     last decision (with reason), next scheduled run.
   - `binary_sensor`: paused.
   - `button`: `trigger_now`, `pause`, `resume`, `force_charge`,
     `set_defaults`. (Buttons publish a fixed payload to the matching
     `sbam/cmd/<name>` topic.)
6. Re-publish discovery + state when receiving `homeassistant/status = online`.
7. Wire MQTT into the `schedule` subcommand only. `configure` and `estimate`
   remain MQTT-free in v2.0.0.
8. Refactor the schedule runner into a single goroutine that owns Modbus
   access. Cron ticks and MQTT commands feed a buffered `Intent` channel;
   the runner drains it serially.
9. Provide the pure helper
   `pkg/fronius.ClassifyDecision(...) (Decision, string)` returning a
   typed `Decision` (`charge` / `idle` / `skip`) plus a human-readable
   reason. Both the Modbus write path and the MQTT publisher must call it.

## Non-functional Requirements

- **Backward compatibility**: `mqtt_enabled` defaults to `false`. Users
  upgrading from v1.x with no config change must observe **byte-identical**
  behaviour — no new TCP connections, no new logs at INFO level, no
  changes to Modbus traffic. v1.x config files must continue to validate
  unchanged.
- **Safety**:
  - The runner MUST serialise all Modbus writes through a single goroutine.
    Concurrent Modbus access is not safe on Fronius gen24+.
  - Inbound commands MUST be validated before dispatch (numeric ranges,
    JSON shape, payload size ≤ 4 KiB). Invalid commands publish an `error`
    ack and are dropped.
  - `force_charge` with `target_pct=0` must be rejected (use `set_defaults`
    instead). `target_pct > 100` must be rejected.
  - Secrets (`mqtt_password`, `mqtt_client_cert_key`) MUST be redacted by
    `src/utils/startup.go` `SecretKeys`.
- **Performance**: state snapshots published at most once per schedule tick
  plus once per inbound command ack; no busy loops; reconnect with
  exponential backoff handled by Paho's auto-reconnect.
- **Resource use**: no goroutine leaks on disconnect; `Disconnect` must be
  idempotent and respect `ctx`.
- **Observability**: all MQTT events log via `src/utils.Log` (zap) with
  structured fields (`topic`, `qos`, `retained`, `payload_bytes`).
- **Testability**: every new file gets unit tests; the Paho client is tested
  against an in-process broker (`github.com/mochi-mqtt/server/v2`) so CI
  needs no external services.

## Configuration Impact

Eleven new keys (flag > env > yaml precedence, mirroring existing pattern):

| YAML / flag                | Env                       | Type   | Default       | Notes                                                              |
| -------------------------- | ------------------------- | ------ | ------------- | ------------------------------------------------------------------ |
| `mqtt_enabled`             | `MQTT_ENABLED`            | bool   | `false`       | Master switch. When false, the noop client is used.                |
| `mqtt_broker`              | `MQTT_BROKER`             | string | `""`          | URL: `tcp://host:1883`, `tls://host:8883`, `ws://`, `wss://`.      |
| `mqtt_client_id`           | `MQTT_CLIENT_ID`          | string | `sbam-<host>` | Defaults to `sbam-<hostname>`.                                     |
| `mqtt_username`            | `MQTT_USERNAME`           | string | `""`          | Optional.                                                          |
| `mqtt_password`            | `MQTT_PASSWORD`           | string | `""`          | **Secret.**                                                        |
| `mqtt_tls_ca_file`         | `MQTT_TLS_CA_FILE`        | string | `""`          | PEM CA bundle path for `tls://`.                                   |
| `mqtt_tls_client_cert`     | `MQTT_TLS_CLIENT_CERT`    | string | `""`          | PEM client cert path.                                              |
| `mqtt_tls_client_cert_key` | `MQTT_TLS_CLIENT_CERT_KEY`| string | `""`          | PEM client key path. **Secret.**                                   |
| `mqtt_tls_insecure_skip`   | `MQTT_TLS_INSECURE_SKIP`  | bool   | `false`       | Skip server cert verification (dev only).                          |
| `mqtt_topic_prefix`        | `MQTT_TOPIC_PREFIX`       | string | `sbam`        | Top-level prefix for state and command topics.                     |
| `mqtt_ha_discovery`        | `MQTT_HA_DISCOVERY`       | bool   | `true`        | Publish HA MQTT Discovery configs when `mqtt_enabled=true`.        |

Supported transports: `tcp://` (plaintext), `tls://` (CA + optional client
cert), `ws://`, `wss://`. Auth modes: anonymous, username/password, mTLS.

Home Assistant add-on (`home-assistant/addons/sbam/config.json`):

- All 11 keys added to `options` (defaults above) and `schema` with
  appropriate types (`bool`, `str`, `password` for the two secrets,
  `match(...)` for the broker URL).
- `run.sh` exports each as the matching uppercase env var.
- When `mqtt_broker` is empty AND the HA Mosquitto add-on is installed,
  `run.sh` MUST resolve the broker via the supervisor services API
  (`bashio::services 'mqtt'`) and export `MQTT_BROKER`, `MQTT_USERNAME`,
  `MQTT_PASSWORD` accordingly. Manual override always wins.

`src/utils/startup.go` `SecretKeys` adds `mqtt_password` and
`mqtt_tls_client_cert_key`.

## External Integrations Touched

- **Solcast**: unchanged.
- **Fronius Solar API**: unchanged.
- **Fronius Modbus registers**: unchanged set of registers; the only change
  is that all writes flow through the single-goroutine runner.
- **MQTT broker**: any broker speaking MQTT 3.1.1 / 5.0 (Mosquitto, EMQX,
  HiveMQ, the HA Mosquitto add-on).
- **Home Assistant**: MQTT Discovery (`homeassistant/<component>/...`)
  and birth-message handling (`homeassistant/status`).

## Acceptance Criteria

- [ ] With `mqtt_enabled=false` (default), no MQTT TCP connection is
      attempted and no new INFO log lines are emitted.
- [ ] With `mqtt_enabled=true` and a reachable broker, sbam connects,
      publishes `sbam/availability=online` (retained), and publishes the
      first `sbam/state` snapshot within 5 s.
- [ ] After every schedule tick (cron or `trigger_now`), an updated
      `sbam/state` payload is published.
- [ ] `pause` causes the next cron tick to return early; `resume`
      restores normal operation; `paused` `binary_sensor` reflects the
      state.
- [ ] `force_charge {"target_pct":50,"duration_s":3600}` invokes
      `fronius.ForceCharge(ip, 50)` exactly once and publishes an `ok`
      ack on `sbam/cmd/force_charge/ack`.
- [ ] `set_defaults` invokes `fronius.Setdefaults(ip)` exactly once.
- [ ] `set_reserve {"pw_batt_reserve":1500}` updates the runner's
      in-memory reserve; the next schedule tick uses the new value.
- [ ] Invalid command payloads (bad JSON, out-of-range values, payload
      > 4 KiB) publish an `error` ack and never reach Modbus code.
- [ ] When `mqtt_ha_discovery=true`, every entity from the
      "Functional Requirements §5" list has a retained
      `homeassistant/<component>/sbam/<object_id>/config` payload, and
      they appear automatically in HA.
- [ ] Receiving `homeassistant/status=online` re-publishes discovery
      configs and the latest state.
- [ ] Cron ticks and MQTT commands never run concurrently against
      Modbus (verified by a unit test against `mbserver`).
- [ ] `mqtt_password` and `mqtt_tls_client_cert_key` are rendered as
      `***` by `DumpStartupParams`.
- [ ] Home Assistant add-on `CHANGELOG.md` has a `2.0.0` entry; add-on
      `version` in `config.json` is bumped to `2.0.0`.
- [ ] README has an MQTT section documenting topics, payload schemas,
      and the migration note.
- [ ] `make test` and `make build` pass; new tests cover the
      expected / edge / failure cases for each new file.

## Test Strategy

Unit tests (per package):

- `pkg/mqtt`:
  - `noop_test.go` — every method returns `nil`, `IsConnected()` returns
    `false`. (expected case)
  - `paho_test.go` — boots an in-process broker
    (`github.com/mochi-mqtt/server/v2`), connects the Paho client,
    publishes / subscribes round-trip, asserts retained-flag handling.
    (expected case)
  - `paho_test.go` — broker refuses connection → `Connect` returns
    error with structured log; `IsConnected()` stays `false`. (failure
    case)
  - `discovery_test.go` — golden-file comparison of HA Discovery JSON
    payloads. (expected + edge: empty topic prefix falls back to `sbam`)
  - `command_test.go` — table-driven validation: valid `force_charge`,
    out-of-range `target_pct`, malformed JSON, oversize payload, unknown
    command name. (expected / edge / failure)
- `pkg/fronius`:
  - `classify_test.go` — table-driven test that `ClassifyDecision`
    returns each of `charge`/`idle`/`skip` for representative inputs.
    (expected, edge — exact threshold, failure — negative power values)
  - Existing `fronius_test.go` extended to assert that the previously
    inlined classifier still produces identical Modbus writes
    (regression guard).
- `pkg/cmd`:
  - `schedule_intent_test.go` — fakes `pkg/mqtt.Client` and
    `pkg/fronius`, fires a cron tick concurrently with a `pause`
    command, asserts the pause was honoured and only one Modbus write
    happened. Uses `mbserver` for the Modbus side. (expected + edge)
  - `precedence_test.go` extended with the 11 new keys to verify
    flag > env > yaml precedence.
- `src/utils`:
  - `startup_test.go` extended to assert `mqtt_password` and
    `mqtt_tls_client_cert_key` are redacted.

All HTTP/Modbus/MQTT mocks must `defer server.Close()`.

## Risks / Open Questions

- **R1** — Paho's auto-reconnect interplay with the in-process broker
  used in tests can be flaky on slow CI runners. Mitigation: use
  short keepalive (5 s) only in tests; bound test duration with
  `t.Deadline`.
- **R2** — HA Mosquitto auto-discovery requires the add-on to declare
  `services: mqtt:need` in `config.json`. Confirm this does not break
  installs where users already point at an external broker.
- **R3** — The `Intent`-channel refactor changes behaviour when a tick
  is in flight and the runner is paused mid-cycle: define the
  semantics (the in-flight cycle finishes; subsequent ticks short-circuit).
- **R4** — Choice of `mochi-mqtt/server/v2` for tests adds a test-only
  dependency. Mitigation: tag with `//go:build mqtt_inproc` if needed,
  but default to plain test build.
- **OQ1** — Topic schema for `sbam/state`: confirm field names and
  units (Wh vs W) during PLAN review with consumers (Node-RED, Grafana
  users on the issue).
- **OQ2** — Whether `set_reserve` should also be persisted to
  `config.yaml` is deferred (current decision: no, runtime-only).

## References

- Issue: https://github.com/atbore-phx/sbam/issues/64
- Milestone: https://github.com/atbore-phx/sbam/milestone/7
- Eclipse Paho Go client: https://github.com/eclipse/paho.mqtt.golang
- HA MQTT Discovery: https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery
- HA add-on services API: https://developers.home-assistant.io/docs/add-ons/communication/#services
- In-process broker for tests: https://github.com/mochi-mqtt/server
- Existing related design: see [.github/copilot-instructions.md](../../../.github/copilot-instructions.md)

## Work Breakdown (sub-issues)

Mirrors the owner's v2.0.0 comment on issue #64. The PLAN sequences these
sub-issues into ordered implementation steps; the umbrella PLAN is the
source of truth and individual sub-issues can link back to it.

| #   | Title                                                                                  | Depends on    | Notes               |
| --- | -------------------------------------------------------------------------------------- | ------------- | ------------------- |
| #84 | `pkg/mqtt` scaffold (Client interface, Paho impl, noop, in-process broker tests)       | —             | foundation          |
| #85 | `pkg/mqtt`: HA MQTT Discovery payloads                                                 | #84           | pure functions      |
| #86 | `pkg/mqtt`: `cmd/*` parser + ack publisher                                             | #84           | input validation    |
| #87 | schedule runner refactor: single goroutine, intent channel, pause state                | #86           | serialises Modbus   |
| #88 | wire MQTT into `schedule` subcommand: flags / env / yaml / secret registry             | #84 #85 #87   | adds 11 keys        |
| #89 | Home Assistant add-on: schema, `run.sh`, DOCS, CHANGELOG bump to 2.0.0                 | #88           | add-on UX           |
| #90 | extract `pkg/fronius.ClassifyDecision` (pure helper, shared by Modbus + MQTT)          | —             | good first issue    |
| #91 | docs: README MQTT section + Project Structure update + migration note                  | #88           | release polish      |

## Clarifications

> Captured 2026-05-01 via the prompt's clarification interview.

- Slug: `64-issue-mqtt-feed` (matches the path the owner pre-announced
  in the v2.0.0 comment on issue #64).
- Scope: single umbrella TASK/PLAN covering sub-issues #84–#91.
- MQTT library: Eclipse Paho (`github.com/eclipse/paho.mqtt.golang`).
- Broker support inside HA add-on: external broker configuration **plus**
  auto-discovery of the HA Mosquitto add-on via the supervisor services
  API.
- Default topic prefix: `sbam`.
- Transport / auth: MUST support `tcp://`, `tls://` (CA + optional
  client cert), username/password, anonymous. `ws://` / `wss://` is
  out of scope for v2.0.0.
- Inbound commands set: `pause`, `resume`, `force_charge`,
  `set_defaults`, `set_reserve`, `trigger_now`.
- HA Discovery entities: battery SoC %, battery capacity Wh, today's
  forecast Wh, last decision (charge/idle/skip + reason), next
  scheduled run; `binary_sensor: paused`; buttons for
  `trigger_now`/`pause`/`resume`/`force_charge`/`set_defaults`.
- Command ack: published to `sbam/cmd/<name>/ack` (no MQTT v5
  RESPONSE_TOPIC).
- No additional deadlines or constraints recorded.
