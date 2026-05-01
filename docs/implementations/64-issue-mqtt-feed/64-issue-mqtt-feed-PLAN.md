# PLAN: MQTT feed (v2.0.0)

> Feature slug: `64-issue-mqtt-feed`
> TASK: [64-issue-mqtt-feed-TASK.md](64-issue-mqtt-feed-TASK.md)
> Issue: https://github.com/atbore-phx/sbam/issues/64
> Milestone: [v2.0.0](https://github.com/atbore-phx/sbam/milestone/7)
> Created: 2026-05-01

---

## 1. Task Analysis

**Goal.** Ship a first-class, opt-in MQTT feed for the `schedule` subcommand
that publishes machine-readable state, accepts validated inbound commands
through a single-goroutine `Intent` channel, and emits Home Assistant MQTT
Discovery payloads. Default behaviour is unchanged for v1.x users.

**Non-goals.** Custom HA Python integration; REST control API; multi-broker
fan-out; persistence of `set_reserve`; MQTT 5 RESPONSE_TOPIC routing;
metrics exposition.

**Acceptance criteria.** Reproduced verbatim in
[64-issue-mqtt-feed-TASK.md](64-issue-mqtt-feed-TASK.md#acceptance-criteria);
the implementer MUST keep them ticked through PR review.

---

## 2. Current State

| Concern                   | File                                                                                  | Note                                                                          |
| ------------------------- | ------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| CLI root + viper init     | [pkg/cmd/root.go](../../../pkg/cmd/root.go)                                           | `viper.AutomaticEnv()`, `bindFlags(cmd)` for flag > env > yaml > default.      |
| Schedule subcommand       | [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go)                                   | Reads 16 viper keys, runs `crontabSchedule()` with `robfig/cron/v3`, blocks on SIGTERM. |
| Charge decision           | [pkg/fronius/schedule.go](../../../pkg/fronius/schedule.go)                           | `SetFroniusChargeBatteryMode(...)` inlines the charge/idle/skip choice.       |
| Modbus writes             | [pkg/fronius/configure.go](../../../pkg/fronius/configure.go)                         | `Setdefaults`, `ForceCharge` — current single-caller assumption.              |
| Modbus client             | [pkg/fronius/modbus.go](../../../pkg/fronius/modbus.go)                               | Module-level `simonvetter/modbus` client; not concurrency-safe.               |
| Storage (SoC) handler     | [pkg/storage/handler.go](../../../pkg/storage/handler.go)                             | `(*Storage).Handler(ip) (capacity_2_charge, capacity_max, error)`.            |
| Power (forecast) handler  | [pkg/power/handler.go](../../../pkg/power/handler.go)                                 | `(*Power).Handler(...) (Wh, retrieved bool, error)`.                          |
| Logging                   | [src/utils/log.go](../../../src/utils/log.go)                                         | `utils.Log` (zap).                                                            |
| Startup dump + redaction  | [src/utils/startup.go](../../../src/utils/startup.go)                                 | `SecretKeys` map + `DumpStartupParams`.                                       |
| HA add-on schema          | [home-assistant/addons/sbam/config.json](../../../home-assistant/addons/sbam/config.json) | bashio-style schema with `str`, `password`, `bool`, `int`, `match(...)`.   |
| HA add-on entrypoint      | [home-assistant/addons/sbam/run.sh](../../../home-assistant/addons/sbam/run.sh)       | `bashio::config 'key'` → uppercase env var.                                   |
| Tests for cmd precedence  | [pkg/cmd/precedence_test.go](../../../pkg/cmd/precedence_test.go)                     | flag > env > yaml proof; extend with new keys.                                |
| Build                     | [Makefile](../../../Makefile)                                                         | `test`, `build`, `test-build`. CGO disabled.                                  |
| Module / Go version       | [go.mod](../../../go.mod)                                                             | `module sbam`, `go 1.26`.                                                     |

No prior `mqtt`, `paho`, or HA-Discovery code exists; this is greenfield.

---

## 3. Target Architecture

```mermaid
flowchart LR
    Cron[cron.Cron tick] -->|Intent{Tick}| Inbox
    MQTTSub[paho subscriber<br/>sbam/cmd/*] --> Parser
    Parser -->|Intent{Pause/Resume/Force/Defaults/Reserve/TriggerNow}| Inbox
    Inbox[(Intent chan, buffered=16)] --> Runner
    Runner -- decision --> Classify[fronius.ClassifyDecision]
    Runner -- write --> Modbus[fronius.ForceCharge / Setdefaults]
    Runner -- snapshot --> Pub[mqtt.Client.Publish<br/>sbam/state, sbam/cmd/*/ack]
    Pub --> Broker[(MQTT broker)]
    HA[homeassistant/status=online] --> Runner
    Runner -- discovery --> Broker
```

New / modified packages:

- `pkg/mqtt/` (new): `Client` interface + `paho`, `noop` impls + discovery
  payloads + command parser + ack publisher.
- `pkg/fronius/` (modified): extract `ClassifyDecision`; existing
  `SetFroniusChargeBatteryMode` calls it.
- `pkg/cmd/` (modified): `schedule.go` builds the runner, wires the MQTT
  client via factory, registers all 11 keys; new `pkg/cmd/intent.go` owns
  the `Intent` types + the runner loop.
- `src/utils/` (modified): two new entries in `SecretKeys`.
- `home-assistant/addons/sbam/` (modified): `config.json` schema (+ `services`),
  `run.sh` exports + Mosquitto auto-discovery, `DOCS.md`, `CHANGELOG.md`,
  `version` bump.

---

## 4. Dependency Choices

Add to [go.mod](../../../go.mod) via `go get`:

| Module                                       | Version (latest @ planning time) | Purpose                                              | Godoc                                                                  |
| -------------------------------------------- | -------------------------------- | ---------------------------------------------------- | ---------------------------------------------------------------------- |
| `github.com/eclipse/paho.mqtt.golang`        | `v1.5.x`                         | Production MQTT client.                              | https://pkg.go.dev/github.com/eclipse/paho.mqtt.golang                 |
| `github.com/mochi-mqtt/server/v2`            | `v2.7.x` (test-only)             | In-process MQTT broker for `pkg/mqtt` Paho tests.    | https://pkg.go.dev/github.com/mochi-mqtt/server/v2                     |

No other new dependencies. Continue using `cobra`, `viper`, `zap`,
`cron/v3`, `simonvetter/modbus`, `testify`, `mbserver`. Pin exact versions
via `go get module@vX.Y.Z` and commit `go.mod` + `go.sum`.

---

## 5. Configuration Changes

Eleven new keys. Defaults in
[config.yaml](../../../config.yaml), schema in
[home-assistant/addons/sbam/config.json](../../../home-assistant/addons/sbam/config.json),
exports in [run.sh](../../../home-assistant/addons/sbam/run.sh), flag
bindings in [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go).

| Key                        | Flag (long / short)              | Env                          | YAML default     | HA schema type      |
| -------------------------- | -------------------------------- | ---------------------------- | ---------------- | ------------------- |
| `mqtt_enabled`             | `--mqtt_enabled`                 | `MQTT_ENABLED`               | `false`          | `bool`              |
| `mqtt_broker`              | `--mqtt_broker`                  | `MQTT_BROKER`                | `""`             | `str?`              |
| `mqtt_client_id`           | `--mqtt_client_id`               | `MQTT_CLIENT_ID`             | `""`             | `str?`              |
| `mqtt_username`            | `--mqtt_username`                | `MQTT_USERNAME`              | `""`             | `str?`              |
| `mqtt_password`            | `--mqtt_password`                | `MQTT_PASSWORD`              | `""`             | `password?`         |
| `mqtt_tls_ca_file`         | `--mqtt_tls_ca_file`             | `MQTT_TLS_CA_FILE`           | `""`             | `str?`              |
| `mqtt_tls_client_cert`     | `--mqtt_tls_client_cert`         | `MQTT_TLS_CLIENT_CERT`       | `""`             | `str?`              |
| `mqtt_tls_client_cert_key` | `--mqtt_tls_client_cert_key`     | `MQTT_TLS_CLIENT_CERT_KEY`   | `""`             | `password?`         |
| `mqtt_tls_insecure_skip`   | `--mqtt_tls_insecure_skip`       | `MQTT_TLS_INSECURE_SKIP`     | `false`          | `bool`              |
| `mqtt_topic_prefix`        | `--mqtt_topic_prefix`            | `MQTT_TOPIC_PREFIX`          | `"sbam"`         | `str`               |
| `mqtt_ha_discovery`        | `--mqtt_ha_discovery`            | `MQTT_HA_DISCOVERY`          | `true`           | `bool`              |

Precedence stays **flag > env > yaml > default** through
[`bindFlags`](../../../pkg/cmd/root.go) (already enforced by
[precedence_test.go](../../../pkg/cmd/precedence_test.go)).

`SecretKeys` (in [src/utils/startup.go](../../../src/utils/startup.go)) gains
`mqtt_password` and `mqtt_tls_client_cert_key`.

`config.json` add-on changes:

```json
"services": ["mqtt:need"],
"options": {
  "mqtt_enabled": false,
  "mqtt_broker": "",
  "mqtt_client_id": "",
  "mqtt_username": "",
  "mqtt_password": "",
  "mqtt_tls_ca_file": "",
  "mqtt_tls_client_cert": "",
  "mqtt_tls_client_cert_key": "",
  "mqtt_tls_insecure_skip": false,
  "mqtt_topic_prefix": "sbam",
  "mqtt_ha_discovery": true
},
"schema": {
  "mqtt_enabled": "bool",
  "mqtt_broker": "match(^(tcp|tls|ws|wss)://[a-zA-Z0-9._-]+(:[0-9]{1,5})?$)?",
  "mqtt_client_id": "str?",
  "mqtt_username": "str?",
  "mqtt_password": "password?",
  "mqtt_tls_ca_file": "str?",
  "mqtt_tls_client_cert": "str?",
  "mqtt_tls_client_cert_key": "password?",
  "mqtt_tls_insecure_skip": "bool",
  "mqtt_topic_prefix": "str",
  "mqtt_ha_discovery": "bool"
}
```

`run.sh` additions (snippet, full ordering preserved):

```bash
export MQTT_ENABLED=$(bashio::config 'mqtt_enabled')
export MQTT_BROKER=$(bashio::config 'mqtt_broker')
# ... etc

# Auto-discover HA Mosquitto if user did not set mqtt_broker explicitly.
if [ "$MQTT_ENABLED" = "true" ] && [ -z "$MQTT_BROKER" ]; then
  if bashio::services.available 'mqtt'; then
    HOST=$(bashio::services 'mqtt' 'host')
    PORT=$(bashio::services 'mqtt' 'port')
    export MQTT_BROKER="tcp://${HOST}:${PORT}"
    [ -z "$MQTT_USERNAME" ] && export MQTT_USERNAME=$(bashio::services 'mqtt' 'username')
    [ -z "$MQTT_PASSWORD" ] && export MQTT_PASSWORD=$(bashio::services 'mqtt' 'password')
  fi
fi
```

---

## 6. Implementation Blueprint

Execution order matches the sub-issue dependency graph in TASK §Work
Breakdown. Each step ends with passing `make test`.

### Step 1 — Extract `ClassifyDecision` (sub-issue #90, no deps)

File: [pkg/fronius/schedule.go](../../../pkg/fronius/schedule.go) (modified)

Add at top of the file:

```go
type Decision string

const (
    DecisionCharge Decision = "charge"
    DecisionIdle   Decision = "idle"
    DecisionSkip   Decision = "skip"
)

// ClassifyDecision returns the charging decision and a human-readable
// reason given the current household/PV state. It is pure: no I/O.
func ClassifyDecision(
    pwForecast, pwBatt2Charge, pwBattMax,
    pwConsumption, maxCharge, pwBattReserve float64,
    startHr, endHr string,
    battReserveChargeEnabled bool,
    pwLwt, pwUpt float64,
    forecastChargeEnabled bool,
    now time.Time,
) (Decision, string) { /* moved from SetFroniusChargeBatteryMode */ }
```

Refactor `SetFroniusChargeBatteryMode` to call `ClassifyDecision` and
keep the same Modbus side effects + return type. **No behaviour change.**

New file: `pkg/fronius/classify_test.go` (table-driven, see §7).

### Step 2 — `pkg/mqtt` scaffold (sub-issue #84)

New directory `pkg/mqtt/` with these files:

- `types.go`:

  ```go
  package mqtt

  type Client interface {
      Connect(ctx context.Context) error
      Disconnect(ctx context.Context) error
      Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error
      Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error
      IsConnected() bool
  }

  type MessageHandler func(topic string, payload []byte)

  type Config struct {
      Enabled            bool
      Broker             string // tcp://, tls://, ws://, wss://
      ClientID           string
      Username, Password string
      TLSCAFile          string
      TLSClientCert      string
      TLSClientCertKey   string
      TLSInsecureSkip    bool
      TopicPrefix        string
      HADiscovery        bool
  }
  ```

- `noop.go`:

  ```go
  type Noop struct{}
  func NewNoop() *Noop { return &Noop{} }
  // All methods return nil; IsConnected() returns false.
  ```

- `paho.go`:

  ```go
  type Paho struct {
      cfg    Config
      client paho.Client // github.com/eclipse/paho.mqtt.golang
      mu     sync.Mutex
  }
  func NewPaho(cfg Config) (*Paho, error) // validates cfg, builds *paho.ClientOptions
  ```

  - `Connect`: builds `paho.NewClientOptions().AddBroker(cfg.Broker)`,
    sets `ClientID` (default `"sbam-" + os.Hostname()`),
    `Username`/`Password`, TLS config if scheme is `tls://` or `wss://`,
    `SetAutoReconnect(true)`, `SetMaxReconnectInterval(60*time.Second)`,
    `SetWill(prefix+"/availability", "offline", 1, true)`,
    `SetOnConnectHandler` republishes availability `online` (retained).
  - `Publish`: respects `ctx.Done()`; honours `ctx.Deadline` via
    `Token.WaitTimeout`.
  - `Subscribe`: wraps the paho callback to invoke our `MessageHandler`.
  - `Disconnect`: idempotent; `client.Disconnect(250)`.

- `factory.go`:

  ```go
  // New returns a Paho client when cfg.Enabled is true, else a Noop.
  func New(cfg Config) (Client, error)
  ```

### Step 3 — HA MQTT Discovery payloads (sub-issue #85)

New file `pkg/mqtt/discovery.go` (pure functions):

```go
type DiscoveryEntity struct {
    Component string // "sensor" | "binary_sensor" | "button"
    ObjectID  string // e.g. "battery_soc"
    Payload   []byte // marshalled JSON config
    Topic     string // "homeassistant/<component>/sbam/<object_id>/config"
}

func BuildDiscovery(prefix string, device DeviceInfo) []DiscoveryEntity
```

`DeviceInfo` carries `identifiers`, `name`, `manufacturer`, `model`,
`sw_version` (from `main.version`). Payload helpers:

- `sensor.battery_soc`, `unit_of_measurement: "%"`, `state_topic: <prefix>/state`, `value_template: "{{ value_json.battery_soc_pct }}"`.
- `sensor.battery_capacity_wh`, unit `Wh`.
- `sensor.forecast_today_wh`, unit `Wh`.
- `sensor.last_decision`, `value_template: "{{ value_json.last_decision }}"`, `json_attributes_template: "{{ value_json.last_decision_reason | tojson }}"`.
- `sensor.next_run`, device_class `timestamp`.
- `binary_sensor.paused`, `payload_on: "true"`, `payload_off: "false"`.
- `button.trigger_now` / `pause` / `resume` / `set_defaults`: `command_topic: <prefix>/cmd/<name>`, `payload_press: "{}"`.
- `button.force_charge`: `command_topic: <prefix>/cmd/force_charge`, `payload_press: '{"target_pct":100,"duration_s":3600}'`.

Add `discovery_test.go` with golden-file assertions
(`testdata/discovery_*.json`).

### Step 4 — Command parser + ack publisher (sub-issue #86)

New file `pkg/mqtt/command.go`:

```go
type CommandName string
const (
    CmdPause       CommandName = "pause"
    CmdResume      CommandName = "resume"
    CmdForceCharge CommandName = "force_charge"
    CmdSetDefaults CommandName = "set_defaults"
    CmdSetReserve  CommandName = "set_reserve"
    CmdTriggerNow  CommandName = "trigger_now"
)

type Command struct {
    Name        CommandName
    TargetPct   int16   // force_charge
    DurationS   int32   // force_charge (advisory; not enforced in v2.0.0)
    PwBattReserve float64 // set_reserve (Wh)
}

const MaxPayloadBytes = 4096

// Parse validates and decodes a command payload from topic+bytes.
// Returns ErrUnknownCommand, ErrPayloadTooLarge, ErrInvalidPayload.
func Parse(topic, prefix string, payload []byte) (Command, error)

// Ack publishes {"status":"ok"|"error","error":"...","ts":"<RFC3339>"}.
func Ack(ctx context.Context, c Client, prefix string, name CommandName, err error) error
```

Sentinel errors via `errors.New`. `command_test.go` tables: valid each
command, oversize payload, malformed JSON, unknown command, out-of-range
`target_pct`, negative `duration_s`.

### Step 5 — Schedule runner refactor (sub-issue #87)

New file `pkg/cmd/intent.go`:

```go
package cmd

type IntentKind int
const (
    IntentTick IntentKind = iota
    IntentPause
    IntentResume
    IntentForceCharge
    IntentSetDefaults
    IntentSetReserve
    IntentTriggerNow
    IntentShutdown
)

type Intent struct {
    Kind          IntentKind
    TargetPct     int16
    PwBattReserve float64
    Source        string // "cron" | "mqtt" | "signal"
    AckTopic      string // empty for cron
}

type Runner struct {
    Cfg       RunnerConfig
    Inbox     chan Intent      // buffered, cap 16
    Mqtt      mqtt.Client
    Storage   *storage.Storage
    Power     *power.Power
    Fronius   *fronius.Fronius
    paused    atomic.Bool
    reserve   atomic.Int64    // Wh, mirrors cfg.PwBattReserve
}

func NewRunner(cfg RunnerConfig, m mqtt.Client) *Runner
func (r *Runner) Run(ctx context.Context) error // single goroutine consumes Inbox
func (r *Runner) Submit(i Intent)               // non-blocking, drops with WARN if full
```

Behaviour:

- `Run` loops `for { select { case <-ctx.Done(); case i := <-r.Inbox } }`.
- `IntentTick`: if `paused`, log skip and `publishState`; else call
  `tickCharge` (existing logic moved verbatim from `schedule()`).
- `IntentForceCharge`: validate range, call `fronius.ForceCharge`, publish
  state + ack.
- `IntentSetDefaults`: call `fronius.Setdefaults`, publish state + ack.
- `IntentSetReserve`: store in `r.reserve`, publish state + ack.
- `IntentPause` / `IntentResume`: flip `r.paused`, publish state + ack.
- `IntentTriggerNow`: same as `IntentTick` but ack-publishing.
- After every Modbus-touching intent, call `publishState` (retained, QoS 1).

`publishState` builds:

```json
{
  "battery_soc_pct": 47,
  "battery_capacity_wh": 9600,
  "forecast_today_wh": 21500,
  "last_decision": "charge",
  "last_decision_reason": "net power < pw_lwt and reserve below threshold",
  "next_run": "2026-05-01T13:00:00+02:00",
  "paused": false,
  "ts": "2026-05-01T12:34:56+02:00"
}
```

### Step 6 — Wire `schedule` cobra command (sub-issue #88)

Modify [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go):

1. Add `StringVarP` / `BoolVarP` registrations for the 11 new flags
   (mirrors existing pattern around lines 132–156 of the file).
2. Read the 11 new viper keys inside `scdCmd.Run`.
3. Build `mqtt.Config`, instantiate `mqtt.New(cfg)`, `Connect(ctx)` if
   `mqtt_enabled`. On failure: log error, fall back to `mqtt.NewNoop()`
   (do **not** crash — keeps backward compatibility).
4. Build `Runner`, start `go runner.Run(ctx)`.
5. Replace the existing `cron.AddFunc` body with
   `runner.Submit(Intent{Kind: IntentTick, Source: "cron"})`.
6. Subscribe to `<prefix>/cmd/+`. Each message → `mqtt.Parse` →
   `runner.Submit(Intent{...})`. Ack publication is the runner's
   responsibility.
7. Subscribe to `homeassistant/status`; on `"online"` republish discovery
   + state.
8. On SIGTERM/SIGINT: cancel ctx → `runner.Run` exits → publish
   availability `offline` (handled by Paho LWT, but also explicit
   `Disconnect`).

Append `mqtt_password` and `mqtt_tls_client_cert_key` to
[`SecretKeys`](../../../src/utils/startup.go).

### Step 7 — HA add-on (sub-issue #89)

- Edit [home-assistant/addons/sbam/config.json](../../../home-assistant/addons/sbam/config.json)
  with `services` array, options + schema from §5, bump `"version"` to
  `"2.0.0"`.
- Edit [home-assistant/addons/sbam/run.sh](../../../home-assistant/addons/sbam/run.sh)
  with the 11 exports + Mosquitto auto-discovery snippet from §5.
- Edit [home-assistant/addons/sbam/CHANGELOG.md](../../../home-assistant/addons/sbam/CHANGELOG.md)
  adding a `## 2.0.0 - 2026-MM-DD` section (MQTT feed, opt-in,
  backward compatible).
- Edit [home-assistant/addons/sbam/DOCS.md](../../../home-assistant/addons/sbam/DOCS.md):
  document each new option, the topic schema, the auto-discovery
  behaviour, and the migration note.

### Step 8 — README + Project Structure (sub-issue #91)

- Add an `## MQTT feed` section to [README.md](../../../README.md)
  covering: enable, topics, payload schema, command examples
  (`mosquitto_pub` snippets), HA Discovery screenshot caption
  placeholder, migration note.
- Update the `Project Structure` block in
  [.github/copilot-instructions.md](../../../.github/copilot-instructions.md)
  to add `pkg/mqtt/` and `pkg/cmd/intent.go` (and any new test files
  whose existence is structurally relevant).

---

## 7. Test Plan

For each new file, ship at least one expected, one edge, one failure case.
All servers `defer close`.

### `pkg/fronius/classify_test.go`

- expected: forecast ≫ consumption + reserve OK → `DecisionIdle`.
- edge: SoC exactly at `pw_batt_reserve` → `DecisionCharge`, reason
  contains `"reserve threshold"`.
- failure: negative `pwForecast` → `DecisionSkip`, reason contains
  `"invalid"`.
- regression: existing `pkg/fronius/fronius_test.go` Modbus expectations
  must remain green.

### `pkg/mqtt/noop_test.go`

- expected: every method returns `nil`; `IsConnected()` is `false`.

### `pkg/mqtt/paho_test.go`

- expected: spin up `mochi-mqtt/server/v2` on `127.0.0.1:0`,
  `NewPaho` + `Connect`, `Subscribe("sbam/test")`, `Publish` round-trip
  within 2 s.
- edge: `retained=true` then disconnect/reconnect → handler receives
  retained payload.
- failure: `Connect` against `tcp://127.0.0.1:1` (closed port) returns
  error within 2 s; `IsConnected()` stays `false`. Use
  `t.Deadline()`-aware contexts.
- always `defer broker.Close()` and `defer paho.Disconnect(ctx)`.

### `pkg/mqtt/discovery_test.go`

- expected: `BuildDiscovery("sbam", DeviceInfo{...})` returns the full
  set; assert each topic + JSON shape via golden files in `testdata/`.
- edge: empty prefix → defaults to `"sbam"`.

### `pkg/mqtt/command_test.go`

- expected: each of the six commands round-trips through `Parse`.
- edge: `force_charge` `target_pct=100`, `duration_s=0`.
- failure: payload > 4 KiB → `ErrPayloadTooLarge`; bad JSON →
  `ErrInvalidPayload`; `target_pct=0` and `target_pct=101` → error;
  unknown command name → `ErrUnknownCommand`.

### `pkg/cmd/intent_test.go` (new) & `pkg/cmd/schedule_intent_test.go` (new)

Use `mbserver.NewServer()` (defer `Close`) and a `mqtt.Noop` swapped for
a recording fake (`type recClient struct { mqtt.Client; pubs []rec }`):

- expected: `IntentTick` produces exactly one Modbus write set and one
  `sbam/state` publish.
- edge: `IntentPause` then `IntentTick` → no Modbus writes; one state
  publish.
- failure: out-of-range `IntentForceCharge` (target=200) → no Modbus
  write; ack publish has `status=error`.
- concurrency: spawn 10 goroutines submitting `IntentTick` and
  `IntentPause`; assert (a) no race detector errors and (b) Modbus write
  count ≤ 1 per tick (verified by `mbserver` register inspection).

### `pkg/cmd/precedence_test.go` (extended)

Extend the existing test table with the 11 new keys, asserting flag > env
> yaml > default for each.

### `src/utils/startup_test.go` (extended)

Extend with `mqtt_password=secret` and `mqtt_tls_client_cert_key=/path`
to assert both render as `***` in `DumpStartupParams`.

---

## 8. Validation Gates

The implementer MUST run and pass, in order:

```bash
go mod tidy
make test                       # `go test -cover ./...`
go test -race ./pkg/...         # race detector specifically for runner
make build                      # CGO_ENABLED=0 binary in bin/sbam
docker build -f Dockerfile -t sbam:dev .
docker build -f home-assistant/addons/sbam/Dockerfile \
  -t sbam-addon:dev home-assistant/addons/sbam/
```

CI workflows under `.github/workflows/` must remain green. If any
workflow needs the new test-only dependency cached, document the change
in the PR description (no new workflow file is required).

---

## 9. Rollout / Backward Compatibility

- Default config changes nothing observable: `mqtt_enabled=false`. All
  existing v1.x deployments produce byte-identical Modbus traffic.
- The 11 new keys are additive; existing `config.yaml`, env vars, and
  CLI invocations are untouched.
- HA add-on `version` bumps to `2.0.0`; users see a normal add-on update.
- HA add-on `CHANGELOG.md` `## 2.0.0` entry must contain:
  - "Added: MQTT feed (opt-in, off by default)."
  - "Added: Home Assistant MQTT Discovery sensors, paused
    binary_sensor, command buttons."
  - "Added: 11 new options (`mqtt_*`); see DOCS for details."
  - "Note: no breaking changes for users who do not enable
    `mqtt_enabled`."
- README MQTT section + a `Migration from v1.x` callout.

---

## 10. Security Considerations

- **Secrets.** `mqtt_password` and `mqtt_tls_client_cert_key` MUST be in
  `SecretKeys` so `DumpStartupParams` redacts them. HA add-on schema
  uses `password?`. Never log raw payloads at INFO; redact at DEBUG too
  for these two keys.
- **TLS.** When the broker URL scheme is `tls://`, build `tls.Config`
  from `mqtt_tls_ca_file`; `InsecureSkipVerify` only when
  `mqtt_tls_insecure_skip=true` AND log a `WARN` on connect.
- **Input validation (OWASP A03).** Inbound payloads are untrusted:
  - Reject payloads > 4 KiB (`MaxPayloadBytes`) before `json.Unmarshal`.
  - Strict numeric ranges: `target_pct ∈ [1,100]`, `duration_s ∈ [0,86400]`,
    `pw_batt_reserve ∈ [0, 1e6]`.
  - Topic match must be exact (`<prefix>/cmd/<name>`); reject `+`/`#`
    in command names.
- **DoS resilience.** `Inbox` is buffered (cap 16) and `Submit` is
  non-blocking; floods drop excess intents with a `WARN` log instead of
  blocking the Paho receive goroutine.
- **No code execution paths.** Commands map to a fixed switch; no
  reflection, no `exec`, no template evaluation against user input.

---

## 11. Gotchas

- `simonvetter/modbus` keeps a module-level client (see
  [pkg/fronius/modbus.go](../../../pkg/fronius/modbus.go)). The
  single-goroutine `Runner` is the **only** thing that may call
  `OpenModbusClient` / `WriteFroniusModbusRegisters` / `ClosemodbusClient`.
  Document this invariant in a comment at the top of `intent.go`.
- Paho's `Token.Wait()` blocks forever if the network is half-open; always
  use `Token.WaitTimeout(...)` or wrap with `ctx.Done()`.
- `paho.NewClient` does not validate the broker URL scheme until
  `Connect`; pre-validate in `NewPaho` to fail fast.
- `viper.AutomaticEnv` lowercases keys but uppercases env names; the 11
  new keys must use snake_case to map cleanly (`mqtt_topic_prefix` →
  `MQTT_TOPIC_PREFIX`).
- HA Discovery `unique_id` MUST be globally unique; build it as
  `"sbam_" + cfg.ClientID + "_" + objectID` so multiple sbam instances
  on one HA do not collide.
- Bashio `bashio::services 'mqtt'` returns empty when the user has no
  Mosquitto add-on; the conditional in `run.sh` MUST tolerate that.
- `cron/v3` triggers callbacks in its own goroutines; the callback must
  stay tiny (`r.Submit(Intent{Kind: IntentTick})`) so cron's pool is
  never blocked by Modbus I/O.
- Solcast and Fronius Solar API requests stay synchronous inside the
  runner — they MUST run on the runner goroutine, not in the cron pool,
  to preserve serialisation.
- LWT (`sbam/availability=offline`) requires the broker to be configured
  with the will at `Connect` time; setting it after `Connect` is a no-op.

---

## 12. Open Questions / Risks (carried from TASK)

- **R1** (Paho ↔ in-process broker flakiness on slow CI) — DEFERRED;
  mitigate with short keepalive in tests.
- **R2** (HA Mosquitto auto-discovery vs external broker) — RESOLVED:
  external `mqtt_broker` always wins over `bashio::services` lookup.
- **R3** (semantics of pause mid-cycle) — RESOLVED: an in-flight cycle
  finishes; subsequent ticks short-circuit. Documented in `intent.go`.
- **R4** (`mochi-mqtt/server/v2` test-only dep) — RESOLVED: kept as a
  regular dependency; `go mod tidy` will mark it as `// indirect` if
  unused outside tests, otherwise it stays direct.
- **OQ1** (`sbam/state` field names + units) — RESOLVED in §5 and §6:
  `_pct` for percentages, `_wh` for energy, `_w` would be reserved for
  power if added later.
- **OQ2** (persist `set_reserve` to YAML) — DEFERRED to ≥ v2.1.

---

## 13. Confidence Score

**8/10.**

Raisers (would push to 9–10):

- A short spike against a real Fronius gen24+ to confirm that
  `fronius.ForceCharge` invoked from a non-cron goroutine produces
  identical Modbus register writes as the current code path.
- Confirmation from the issue author / @atbore-phx on the exact
  `sbam/state` JSON field names (so v2.0.0 doesn't need a breaking
  rename in v2.1).
- A check that `mochi-mqtt/server/v2`'s default port allocator works
  inside the GitHub Actions runner used by `.github/workflows/test.yml`.

If any of these is blocking at implementation time, raise it before
starting Step 5 (the runner refactor) — earlier steps are independent
and safe to ship behind the `mqtt_enabled=false` default.
