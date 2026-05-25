# PLAN — Remove custom MQTT reconnect strategy, keep only Paho auto-reconnect

> TASK: [138-issue-remove-custom-mqtt-reconnect-strategy-TASK.md](./138-issue-remove-custom-mqtt-reconnect-strategy-TASK.md)
> Issue: [#138](https://github.com/atbore-phx/sbam/issues/138)
> Date: 2026-05-25

## Task Analysis

**Goal:** Delete the `custom` reconnect strategy and the `reconnectManager` abstraction entirely. Inline the Paho `AutoReconnect` configuration directly into `NewPaho()`, making it the single reconnect code path with no strategy dispatch.

**Non-goals:** New reconnect knobs, new config surfaces, deprecation warnings for stale keys (the key was never user-facing).

**Acceptance criteria (from TASK):**
- `pkg/mqtt/reconnect_custom.go` no longer exists
- `pkg/mqtt/reconnect_paho.go` no longer exists (inlined into `paho.go`)
- `reconnectManager` interface and `normalizeReconnectStrategy()` no longer exist
- `ReconnectStrategy` type and associated constants removed
- `Config.ReconnectStrategy` field removed
- `make test` passes
- `make build` succeeds

## Current State

### Files to be deleted

| File | Contents |
|------|----------|
| [pkg/mqtt/reconnect.go](../../pkg/mqtt/reconnect.go) | `reconnectManager` interface (3 methods: `configure`, `stop`, `strategy`), `normalizeReconnectStrategy()`, `newReconnectManager()` factory |
| [pkg/mqtt/reconnect_custom.go](../../pkg/mqtt/reconnect_custom.go) | `customReconnectManager` struct and jittered-exponential-backoff reconnect loop (~124 lines) |
| [pkg/mqtt/reconnect_paho.go](../../pkg/mqtt/reconnect_paho.go) | `pahoReconnectManager` struct, `configure()` sets `AutoReconnect(true)` + `MaxReconnectInterval`, connection lost/reconnecting log handlers |
| [pkg/mqtt/reconnect_paho_test.go](../../pkg/mqtt/reconnect_paho_test.go) | `TestPahoReconnectManagerConfigureSetsOptions`, `TestPahoReconnectManagerStopNoop` |
| [pkg/mqtt/race_disabled_test.go](../../pkg/mqtt/race_disabled_test.go) | Build-tag-gated `raceDetectorEnabled = false` for non-race builds |
| [pkg/mqtt/race_enabled_test.go](../../pkg/mqtt/race_enabled_test.go) | Build-tag-gated `raceDetectorEnabled = true` for race builds |

### Files to be modified

| File | What depends on the deletion targets |
|------|--------------------------------------|
| [pkg/mqtt/types.go](../../pkg/mqtt/types.go) | `ReconnectStrategy` type (line 7), `ReconnectStrategyCustom`/`ReconnectStrategyPaho` constants (lines 10-11), `Config.ReconnectStrategy` field (line 28) |
| [pkg/mqtt/paho.go](../../pkg/mqtt/paho.go) | `normalizeReconnectStrategy(cfg.ReconnectStrategy)` call at line 66, `cfg.ReconnectStrategy = strategy` at line 70, `newReconnectManager(cfg.ReconnectStrategy)` at line 115, `Paho.reconnecter` field (line 45), `reconnecter.stop()` in `Disconnect` (lines 148-150) |
| [pkg/mqtt/mqtt_test.go](../../pkg/mqtt/mqtt_test.go) | `TestReconnectAfterBrokerRestart` (lines 159-222) — table over strategies, `TestConnectFailsWithBadCredentials` (lines 224-264) — table over strategies, `TestNormalizeReconnectStrategy` (lines 276-304), `TestNewPahoReconnectStrategyConfiguration` (lines 307-342) |

## Target Architecture

```
NewPaho(cfg)
  ├── validate broker / client ID (unchanged)
  ├── build TLS config if needed (unchanged)
  ├── configure Paho AutoReconnect  ← INLINED from pahoReconnectManager.configure()
  │     opts.SetAutoReconnect(true)
  │     opts.SetMaxReconnectInterval(reconnectMaxDelay)
  │     opts.SetConnectionLostHandler(log + no-op)
  │     opts.SetReconnectingHandler(log)
  ├── set OnConnect handler (unchanged)
  └── create Paho client (unchanged)
```

After this change:
- `Paho.reconnecter` field is removed
- `paho.ReconnectStrategy` type, constants, and `Config.ReconnectStrategy` are gone
- `normalizeReconnectStrategy` / `newReconnectManager` are gone
- `reconnectManager` interface is gone
- The only reconnect code path is Paho `AutoReconnect`

No new interfaces, types, or abstractions are introduced. This is purely a removal + inlining simplification.

## Configuration Changes

None. `mqtt_reconnect_strategy` was never wired through Cobra/Viper/HA add-on schema. The `Config.ReconnectStrategy` field is package-internal and removed entirely.

## Implementation Blueprint

### Step 1 — Delete the reconnect strategy files

Delete these six files:
```
pkg/mqtt/reconnect.go
pkg/mqtt/reconnect_custom.go
pkg/mqtt/reconnect_paho.go
pkg/mqtt/reconnect_paho_test.go
pkg/mqtt/race_disabled_test.go
pkg/mqtt/race_enabled_test.go
```

### Step 2 — Strip `ReconnectStrategy` from `pkg/mqtt/types.go`

Remove lines 7-11 (type + constants) and line 28 (field from Config struct):

```go
// REMOVE:
type ReconnectStrategy string
const (
    ReconnectStrategyCustom ReconnectStrategy = "custom"
    ReconnectStrategyPaho   ReconnectStrategy = "paho"
)
// REMOVE from Config:
ReconnectStrategy ReconnectStrategy
```

### Step 3 — Inline Paho reconnect into `pkg/mqtt/paho.go`

Remove the `reconnecter` field from `Paho` struct (line 45):

```go
// REMOVE:
reconnecter reconnectManager
```

In `NewPaho()`, replace lines 66-70 and 115-117:

```go
// REMOVE:
strategy, err := normalizeReconnectStrategy(cfg.ReconnectStrategy)
if err != nil {
    return nil, err
}
cfg.ReconnectStrategy = strategy
// ...later...
manager := newReconnectManager(cfg.ReconnectStrategy)
manager.configure(opts, p)
p.reconnecter = manager
```

Replace with inline Paho auto-reconnect configuration:

```go
opts.SetAutoReconnect(true)
opts.SetConnectRetry(false)
opts.SetMaxReconnectInterval(reconnectMaxDelay)
opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
    if p.closed.Load() {
        return
    }
    utils.Log.Warnw("mqtt connection lost", "broker", sanitizeBroker(cfg.Broker), "error", err)
})
opts.SetReconnectingHandler(func(_ paho.Client, _ *paho.ClientOptions) {
    if p.closed.Load() {
        return
    }
    utils.Log.Warnw("mqtt reconnecting", "broker", sanitizeBroker(cfg.Broker))
})
```

In `Disconnect()`, remove lines 148-150:

```go
// REMOVE:
if p.reconnecter != nil {
    p.reconnecter.stop()
}
```

### Step 4 — Add a Paho-only reconnect options test in `pkg/mqtt/paho_test.go`

The `reconnect_paho_test.go` assertion (`TestPahoReconnectManagerConfigureSetsOptions`) had a standalone test checking that `AutoReconnect=true`, `ConnectRetry=false`, and `MaxReconnectInterval=reconnectMaxDelay`. Add an equivalent test:

```go
func TestNewPahoReconnectOptions(t *testing.T) {
    client, err := NewPaho(Config{Broker: "tcp://example.com:1883"})
    require.NoError(t, err)
    require.NotNil(t, client)

    opts := client.client.OptionsReader()
    assert.True(t, opts.AutoReconnect())
    assert.False(t, opts.ConnectRetry())
    assert.Equal(t, reconnectMaxDelay, opts.MaxReconnectInterval())
}
```

Or add a sub-test to the existing `TestNewPahoValidationAndDefaults`.

### Step 5 — Simplify `pkg/mqtt/mqtt_test.go`

**Remove entire functions:**
- `TestNormalizeReconnectStrategy` (lines 276-304) — normalization is deleted
- `TestNewPahoReconnectStrategyConfiguration` (lines 307-342) — strategy dispatch is deleted

**Simplify `TestReconnectAfterBrokerRestart` (lines 159-222):**
Remove the `strategies` slice/loop. Keep the test body for a single client using only Paho auto-reconnect. Remove the `raceDetectorEnabled` skip (lines 165-167) since the Paho skip was the only race-sensitive branch. Remove the `strategy == ReconnectStrategyCustom` branch (lines 184-188) — the `!client.IsConnected()` assertion after crash. Keep the 25-second reconnect deadline since Paho reconnect is slower. The simplified function:

```go
func TestReconnectAfterBrokerRestart(t *testing.T) {
    address := reserveTCPAddress(t)
    broker := newTestBroker(t, address, nil)
    defer broker.Close()

    client := mustConnectClient(t, Config{
        Enabled:   true,
        Broker:    "tcp://" + address,
        ClientID:  "reconnect-client",
    })
    defer disconnectClient(t, client)

    broker.Crash()

    // Client detects disconnection
    assert.Eventually(t, func() bool {
        return !client.IsConnected()
    }, testTimeout, 100*time.Millisecond)

    broker = newTestBroker(t, address, nil)

    // Paho auto-reconnect: allow up to 25 s
    assert.Eventually(t, func() bool {
        return client.IsConnected()
    }, 25*time.Second, 100*time.Millisecond)

    subscriber := mustConnectClient(t, Config{Enabled: true, Broker: "tcp://" + address, ClientID: "reconnect-subscriber"})
    defer disconnectClient(t, subscriber)

    messages := make(chan receivedMessage, 1)
    ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
    defer cancel()
    require.NoError(t, subscriber.Subscribe(ctx, stateTopic(""), qosAtLeastOnce, func(topic string, payload []byte) {
        messages <- receivedMessage{topic: topic, payload: payload}
    }))

    PublishState(context.Background(), client, "", StatePayload{
        BatterySOCPct:     f64(55),
        BatteryCapacityWh: f64(8000),
        ForecastTodayWh:   f64(3200),
        LastDecision:      "idle",
    })

    message := waitForMessage(t, messages)
    assert.Equal(t, stateTopic(""), message.topic)
}
```

**Simplify `TestConnectFailsWithBadCredentials` (lines 224-264):**
Remove the strategies loop. Keep the test body without `ReconnectStrategy` in the config:

```go
func TestConnectFailsWithBadCredentials(t *testing.T) {
    address := reserveTCPAddress(t)
    ledger := &brokerauth.Ledger{
        Users: brokerauth.Users{
            "good": {
                Username: "good",
                Password: "secret",
                ACL: brokerauth.Filters{
                    "#": brokerauth.ReadWrite,
                },
            },
        },
    }

    broker := newTestBroker(t, address, ledger)
    defer broker.Close()

    client, err := New(Config{
        Enabled:  true,
        Broker:   "tcp://" + address,
        ClientID: "bad-credentials",
        Username: "good",
        Password: "wrong",
    })
    require.NoError(t, err)

    ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
    defer cancel()

    err = client.Connect(ctx)
    assert.Error(t, err)
    assert.False(t, client.IsConnected())
}
```

## Test Plan

### New test

| Case | Test | Package |
|------|------|---------|
| Expected | `NewPaho` configures `AutoReconnect=true`, `MaxReconnectInterval=reconnectMaxDelay`, `ConnectRetry=false` | `mqtt` — add to existing `TestNewPahoValidationAndDefaults` or as standalone |

### Modified tests

| Case | Test | Change |
|------|------|--------|
| Edge (reconnect after broker restart) | `TestReconnectAfterBrokerRestart` | Remove strategy loop; single Paho-only path; remove race skip; keep 25s deadline |
| Failure (bad credentials) | `TestConnectFailsWithBadCredentials` | Remove strategy loop; no `ReconnectStrategy` in config |

### Removed tests

| Test | Reason |
|------|--------|
| `TestNormalizeReconnectStrategy` | `normalizeReconnectStrategy()` deleted |
| `TestNewPahoReconnectStrategyConfiguration` | Strategy dispatch deleted |
| `TestPahoReconnectManagerConfigureSetsOptions` | `pahoReconnectManager` struct deleted; equivalent assertion added to paho test |
| `TestPahoReconnectManagerStopNoop` | `pahoReconnectManager` struct deleted |

### Mocks / helpers

- `tbrandon/mbserver` — not needed for this change
- `httptest.NewServer` — not needed for this change
- Existing Mochi broker test helpers (`newTestBroker`, `reserveTCPAddress`, `mustConnectClient`) — unchanged

## Validation Gates

```bash
make test          # all unit tests pass
make build         # binary compiles
go vet ./pkg/mqtt/ # no issues in modified package
```

No Dockerfile or CI workflow changes.

## Rollout / Backward Compatibility

- `mqtt_reconnect_strategy` was never a user-facing config key — no migration needed.
- The `Config.ReconnectStrategy` field was package-internal — callers in `pkg/cmd/` never set it (confirmed: zero matches outside `pkg/mqtt/`).
- Post-change reconnect behavior matches today's `paho` strategy — identical to what users who explicitly opted into `paho` experience.

## Security Considerations

None. This change removes code, does not introduce new input handling, and does not touch Modbus writes or API credentials.

## Gotchas

- `TestReconnectAfterBrokerRestart` currently uses 25 s as the Paho reconnect deadline (vs. 10 s for custom). Keep the 25 s timeout for the simplified Paho-only variant.
- The `raceDetectorEnabled` build-tag gated files (`race_disabled_test.go`, `race_enabled_test.go`) are removed since the only consumer (the Paho race skip) is being removed.
- Ensure `p.closed` is checked before logging in the connection-lost and reconnecting handlers (inline from `pahoReconnectManager.configure()`).

## Open Questions / Risks

- [x] Race-detector exclusion for Paho removed — single-path test, no branching needed.
- [x] `mqtt_reconnect_strategy` never wired — no user-facing deprecation.

## Confidence Score

**10/10** — This is a pure-deletion + inlining change. All files to touch are known, no new logic is introduced, the inlined reconnect configuration is a direct copy of `pahoReconnectManager.configure()`. No external dependencies change.
