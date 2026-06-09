# PLAN — Remove custom MQTT reconnect strategy, keep only Paho auto-reconnect

> TASK: [138-issue-remove-custom-mqtt-reconnect-strategy-TASK.md](./138-issue-remove-custom-mqtt-reconnect-strategy-TASK.md)
> Issue: [#138](https://github.com/atbore-phx/sbam/issues/138)
> Date: 2026-05-25 · Updated: 2026-05-27 (extended scope: async connect)

## Task Analysis

**Goal:** Delete the `custom` reconnect strategy and the `reconnectManager` abstraction entirely. Inline the Paho `AutoReconnect` configuration directly into `NewPaho()`, making it the single reconnect code path with no strategy dispatch. Additionally, replace the blocking `connectWithRetries` helper with Paho's `SetConnectRetry(true)` in a fire-and-forget goroutine so the cron schedule starts immediately — Paho handles all connect/retry logic internally.

**Non-goals:** New reconnect knobs, new config surfaces, deprecation warnings for stale keys (the key was never user-facing).

**Acceptance criteria (from TASK):**
- `pkg/mqtt/reconnect_custom.go` no longer exists
- `pkg/mqtt/reconnect_paho.go` no longer exists (inlined into `paho.go`)
- `reconnectManager` interface and `normalizeReconnectStrategy()` no longer exist
- `ReconnectStrategy` type and associated constants removed
- `Config.ReconnectStrategy` field removed
- `connectWithRetries` function removed from `init.go`
- `Client` interface has `OnConnect(func())` method
- `schedule.go` uses `OnConnect` callback for command subscription
- `make test` passes
- `make build` succeeds

## Current State

### Files to be deleted

| File | Contents |
|------|----------|
| `pkg/mqtt/reconnect.go` | `reconnectManager` interface (3 methods: `configure`, `stop`, `strategy`), `normalizeReconnectStrategy()`, `newReconnectManager()` factory |
| `pkg/mqtt/reconnect_custom.go` | `customReconnectManager` struct and jittered-exponential-backoff reconnect loop (~124 lines) |
| `pkg/mqtt/reconnect_paho.go` | `pahoReconnectManager` struct, `configure()` sets `AutoReconnect(true)` + `MaxReconnectInterval`, connection lost/reconnecting log handlers |
| `pkg/mqtt/reconnect_paho_test.go` | `TestPahoReconnectManagerConfigureSetsOptions`, `TestPahoReconnectManagerStopNoop` |
| `pkg/mqtt/race_disabled_test.go` | Build-tag-gated `raceDetectorEnabled = false` for non-race builds |
| `pkg/mqtt/race_enabled_test.go` | Build-tag-gated `raceDetectorEnabled = true` for race builds |

### Files to be modified

| File | What changes |
|------|-------------|
| `pkg/mqtt/types.go` | Remove `ReconnectStrategy` type, constants, `Config.ReconnectStrategy` field |
| `pkg/mqtt/paho.go` | Remove `reconnecter` field; inline `SetAutoReconnect(true)`, `SetConnectRetry(true)`, `SetMaxReconnectInterval`; remove `reconnecter.stop()` from `Disconnect`; move HA status subscription into `OnConnectHandler`; add `onConnectCallbacks` slice, invoke in `OnConnectHandler`; implement `OnConnect` method |
| `pkg/mqtt/client.go` | Add `OnConnect(func())` to `Client` interface |
| `pkg/mqtt/noop.go` | Add no-op `OnConnect(func())` implementation |
| `pkg/mqtt/init.go` | Remove `connectWithRetries`; `InitWithCleanup` fires `Connect()` in goroutine, returns Paho client immediately; move HA status subscription out (handled by `OnConnectHandler` in paho.go) |
| `pkg/mqtt/mqtt_test.go` | Remove strategy-loop tests; simplify to Paho-only |
| `pkg/mqtt/init_test.go` | Remove `connectWithRetries` tests and noop-fallback-on-connect-failure test; add async-connect tests |
| `pkg/cmd/schedule.go` | Replace `subscribeScheduleCommands` call with `mqttClient.OnConnect(callback)` registration; remove `subscribeScheduleCommands` function |
| `pkg/cmd/schedule_mqtt_wiring_test.go` | Rewrite tests for `OnConnect` callback pattern instead of `subscribeScheduleCommands` |

## Target Architecture

```
schedule.go
  ├── mqtt.InitWithCleanup(cfg) → returns Client immediately
  │     ├── New(cfg) → Paho client (with SetConnectRetry(true))
  │     └── go client.Connect()  ← fire-and-forget, Paho retries internally
  ├── mqttClient.OnConnect(func() { subscribe commands })
  ├── NewRunner(cfg, mqttClient)
  ├── go runner.Run(ctx)
  ├── crontabSchedule(ctx, runner, ...)  ← starts immediately, no blocking
  └── finalizeRunnerMode(...)

NewPaho(cfg)
  ├── validate broker / client ID (unchanged)
  ├── build TLS config if needed (unchanged)
  ├── opts.SetAutoReconnect(true)
  ├── opts.SetConnectRetry(true)          ← NEW: delegate initial connect retry to Paho
  ├── opts.SetMaxReconnectInterval(reconnectMaxDelay)
  ├── opts.SetConnectionLostHandler(...)   ← log warning
  ├── opts.SetOnConnectHandler(...)
  │     ├── publish availability "online"
  │     ├── publish HA discovery
  │     ├── subscribe to homeassistant/status  ← MOVED from init.go
  │     └── invoke onConnectCallbacks          ← NEW: command subscription, etc.
  └── create Paho client (unchanged)
```

After this change:
- `Paho.reconnecter` field is removed
- `ReconnectStrategy` type, constants, and `Config.ReconnectStrategy` are gone
- `normalizeReconnectStrategy` / `newReconnectManager` are gone
- `reconnectManager` interface is gone
- `connectWithRetries` is gone
- `subscribeScheduleCommands` is replaced by `OnConnect` callback
- The only reconnect code path is Paho `AutoReconnect` + `ConnectRetry`

No new interfaces, types, or abstractions beyond `OnConnect(func())` on the `Client` interface.

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

Remove the `reconnecter` field from `Paho` struct. Add `onConnectCallbacks []func()` field.

In `NewPaho()`, replace the strategy normalization and reconnectManager creation with inline configuration:

```go
opts.SetAutoReconnect(true)
opts.SetConnectRetry(true)
opts.SetMaxReconnectInterval(reconnectMaxDelay)
opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
    if p.closed.Load() {
        return
    }
    utils.Log.Warnw("mqtt connection lost", "broker", sanitizeBroker(cfg.Broker), "error", err)
})
```

The `OnConnectHandler` (already exists at line 95) is extended to:
1. Publish availability "online" (already present)
2. Publish HA discovery (already present)
3. Subscribe to `homeassistant/status` (moved from `init.go`)
4. Invoke `p.onConnectCallbacks` (new)

```go
opts.SetOnConnectHandler(func(client paho.Client) {
    utils.Log.Debugw("mqtt onConnect handler", "broker", sanitizeBroker(p.cfg.Broker), "client_id", p.cfg.ClientID)
    if err := waitToken(context.Background(), client.Publish(availabilityTopic(p.cfg.TopicPrefix), qosAtLeastOnce, true, []byte("online"))); err != nil {
        utils.Log.Warnw("mqtt availability publish failed", ...)
    } else {
        utils.Log.Debugw("mqtt availability published", ...)
    }

    PublishDiscovery(context.Background(), p, p.cfg, p.discoveryVersion)

    // Subscribe to HA status for discovery re-publish (moved from init.go)
    if p.cfg.HADiscovery {
        if err := waitToken(context.Background(), client.Subscribe(haStatusTopic(), byte(1), func(topic string, payload []byte) {
            _ = topic
            if strings.TrimSpace(string(payload)) != "online" {
                return
            }
            ctx, cancel := context.WithTimeout(context.Background(), defaultOpTimeout)
            PublishDiscovery(ctx, client, p.cfg, p.discoveryVersion)
            cancel()
        })); err != nil {
            utils.Log.Warnw("mqtt subscribe homeassistant/status failed", "error", err)
        }
    }

    // Invoke registered post-connect callbacks
    for _, cb := range p.onConnectCallbacks {
        cb()
    }
})
```

Add `OnConnect` method to `Paho`:

```go
func (p *Paho) OnConnect(cb func()) {
    p.onConnectCallbacks = append(p.onConnectCallbacks, cb)
}
```

In `Disconnect()`, remove the `reconnecter.stop()` calls (lines 148-150).

### Step 4 — Add `OnConnect` to `Client` interface and `Noop`

In `pkg/mqtt/client.go`, add to the `Client` interface:

```go
type Client interface {
    Connect(ctx context.Context) error
    Disconnect(ctx context.Context) error
    Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error
    Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error
    IsConnected() bool
    OnConnect(cb func())  // NEW
}
```

In `pkg/mqtt/noop.go`, add:

```go
func (n *Noop) OnConnect(cb func()) {}
```

### Step 5 — Replace `connectWithRetries` with async connect in `pkg/mqtt/init.go`

Remove the `connectWithRetries` function entirely.

In `InitWithCleanup`, replace the blocking connect + HA subscription logic with a fire-and-forget goroutine:

```go
func InitWithCleanup(cfg Config, version string, maxAttempts int, baseBackoff time.Duration) (Client, func(), error) {
    client, newErr := newClientFactory(cfg, version)
    var accErr error
    if newErr != nil {
        accErr = errors.Join(accErr, fmt.Errorf("mqtt client setup failed: %w", newErr))
        u.Log.Warnw("mqtt client setup failed, using noop", "error", newErr)
        client = newNoopFactory()
    }

    if cfg.Enabled {
        // Fire-and-forget: Paho handles retries internally via SetConnectRetry(true).
        // OnConnectHandler in paho.go handles subscriptions and discovery on success.
        go func() {
            if err := client.Connect(context.Background()); err != nil {
                u.Log.Warnw("mqtt connect failed", "error", err)
            }
        }()
    }

    cleanup := func() {}
    if cfg.Enabled {
        cleanup = func() {
            disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), defaultOpTimeout)
            _ = client.Disconnect(disconnectCtx)
            disconnectCancel()
        }
    }

    return client, cleanup, accErr
}
```

Key changes:
- `maxAttempts` and `baseBackoff` params become unused — keep the signature for backward compat, but ignore them. (Or remove them if no external callers — check: only `schedule.go:143` calls this.)
- No more noop fallback on connect failure — the real Paho client is returned and connects in the background.
- HA status subscription is no longer here — it moved to `OnConnectHandler` in paho.go.
- `newNoopFactory` is still referenced for setup errors (e.g., bad TLS config).

Remove the now-unused `maxAttempts` and `baseBackoff` params from `InitWithCleanup` signature and update the single call site in `schedule.go:143`.

### Step 6 — Replace `subscribeScheduleCommands` with `OnConnect` callback in `pkg/cmd/schedule.go`

In `schedule.go`, replace the `subscribeScheduleCommands(...)` call (lines 187-189) with:

```go
if mqttCfg.Enabled && mqttClient != nil {
    mqttClient.OnConnect(func() {
        subscribeScheduleCommands(runCtx, mqttClient, mqttCfg, runner)
    })
}
```

Update `subscribeScheduleCommands` to not check `IsConnected()` (since it's now only called from within `OnConnectHandler` where we're guaranteed connected). Actually, keep the check for safety but remove the early-return-on-not-connected — or better, keep it defensive but change the log level.

Actually, simpler: the `OnConnect` callback is invoked from Paho's `OnConnectHandler`, which only fires when connected. So `IsConnected()` is always true. We can remove that check from `subscribeScheduleCommands` and just validate the other preconditions. But keep it defensive since the function is still callable directly in tests.

Remove the `finalizeRunnerMode` time-based branching — keep MQTT-interactive mode since MQTT is always "enabled" via noop even when broker is down.

Actually wait — `finalizeRunnerMode` currently has two paths:
1. MQTT disabled → stops runner immediately (non-interactive)
2. MQTT enabled → blocks until signal

With the new approach, MQTT is "enabled" (Paho client exists) even if the broker isn't reachable yet. The runner should always block until signal since MQTT commands could arrive once the broker comes up. So remove the `!mqttEnabled` branch:

```go
func finalizeRunnerMode(runner *Runner, runDone <-chan error) error {
    u.Log.Info("MQTT integration enabled, waiting for commands... press ctrl+c to exit...")
    return waitForRunnerDone(runDone)
}
```

And update the call site in `schedule.go` to remove the `mqtt_enabled` param.

Wait, actually no. When MQTT is truly disabled (`mqtt_enabled: false`), the noop client is used. And the run loop is:
1. `runner.Run(ctx)` runs in a goroutine
2. If no MQTT, submit a tick immediately then shut down
3. If MQTT, wait for signal

This is a different behavior path. Let me keep the `finalizeRunnerMode` logic but just update the `mqttEnabled` check:

Actually, looking at the current code more carefully:
```go
if err := finalizeRunnerMode(mqtt_enabled, runner, runDone, stop); err != nil {
```

When `mqtt_enabled=false`:
- Shutdown intent submitted
- Runner processes it, returns
- Program exits

When `mqtt_enabled=true`:
- Blocks waiting for signal
- Program runs until Ctrl+C

With the new approach, this logic stays the same. The only difference is that when `mqttEnabled=true` but the broker is down, the program still runs (waiting for signal) — same as before when noop was used on connect failure.

Wait, before: when `mqttEnabled=true` and connect fails, noop was used. `finalizeRunnerMode(true, ...)` was called, which blocks waiting for signal. MQTT commands wouldn't work (noop), but cron ran.

After: when `mqttEnabled=true`, Paho client is always returned. `finalizeRunnerMode(true, ...)` is called, blocks waiting for signal. MQTT commands will eventually work when the broker comes up. Same behavior, just better.

So `finalizeRunnerMode` stays as-is.

### Step 7 — Add a Paho-only reconnect options test

The `reconnect_paho_test.go` assertion checked `AutoReconnect=true`, `ConnectRetry=false`, `MaxReconnectInterval=reconnectMaxDelay`. Add an equivalent test in `paho_test.go` that verifies `ConnectRetry=true`:

```go
func TestNewPahoReconnectOptions(t *testing.T) {
    client, err := NewPaho(Config{Broker: "tcp://example.com:1883"})
    require.NoError(t, err)
    require.NotNil(t, client)

    opts := client.client.OptionsReader()
    assert.True(t, opts.AutoReconnect())
    assert.True(t, opts.ConnectRetry())
    assert.Equal(t, reconnectMaxDelay, opts.MaxReconnectInterval())
}
```

### Step 8 — Simplify `pkg/mqtt/mqtt_test.go`

**Remove entire functions:**
- `TestNormalizeReconnectStrategy` — normalization is deleted
- `TestNewPahoReconnectStrategyConfiguration` — strategy dispatch is deleted

**Simplify `TestReconnectAfterBrokerRestart`:**
Remove the `strategies` slice/loop. Keep the test body for a single client using only Paho auto-reconnect. Remove the `raceDetectorEnabled` skip since the Paho skip was the only race-sensitive branch. Keep the 25-second reconnect deadline since Paho reconnect is slower.

**Simplify `TestConnectFailsWithBadCredentials`:**
Remove the strategies loop. Keep the test body without `ReconnectStrategy` in the config.

### Step 9 — Update `pkg/mqtt/init_test.go`

**Remove tests:**
- `TestConnectWithRetriesSucceedsAfterRetry` — `connectWithRetries` is deleted
- `TestConnectWithRetriesContextErrorsTriggerDisconnect` — `connectWithRetries` is deleted
- `TestInitWithCleanupConnectFailureFallsBackToNoop` — no more noop fallback on connect failure

**Keep tests:**
- `TestInitWithCleanupSetupErrorFallsBackToNoop` — setup errors (e.g., bad broker URL) still fall back to noop
- `TestInitWithCleanupSubscribeErrorIsReturned` — removed since subscription moved to OnConnectHandler (will be tested differently)
- `TestInitWithCleanupHADiscoveryPublishesOnOnline` — removed since this behavior moved into paho.go's OnConnectHandler (tested via Mochi integration tests or paho_test.go)

**Add tests:**
- `TestInitWithCleanupAsyncConnect` — verifies that `InitWithCleanup` returns immediately and the client is a Paho instance (not noop), even when broker is unreachable
- `TestPahoOnConnectCallbacks` — verifies registered callbacks fire when OnConnectHandler runs

The `fakeInitClient` will need an `OnConnect` method added.

### Step 10 — Update `pkg/cmd/schedule_mqtt_wiring_test.go`

The `recordingMQTTClient` needs an `OnConnect` method. The existing tests for `subscribeScheduleCommands` remain valid since the function still exists and is testable directly. Add a test that verifies the `OnConnect` callback registration pattern:

```go
func TestScheduleRegistersCommandSubscriptionViaOnConnect(t *testing.T) {
    // Verify that when OnConnect is registered, the callback subscribes commands
}
```

Or simpler: keep the existing `subscribeScheduleCommands` tests as-is since the function itself is still testable, and verify the wiring in an integration-style test.

Actually, the simplest approach: keep `subscribeScheduleCommands` as an exported (or testable) function. The existing tests already cover it. The only change is in `schedule.go` where the call is moved into an `OnConnect` callback. The wiring test just needs to verify that `OnConnect` stores the callback and it can be invoked.

### Step 11 — Update `recordingMQTTClient` and `fakeInitClient` mocks

Both test mocks need `OnConnect(func())` methods added:

```go
// recordingMQTTClient (schedule_mqtt_wiring_test.go)
func (c *recordingMQTTClient) OnConnect(cb func()) {
    c.onConnectCB = cb
}

// fakeInitClient (init_test.go)
func (f *fakeInitClient) OnConnect(cb func()) {
    f.onConnectCBs = append(f.onConnectCBs, cb)
}
```

## Test Plan

### New tests

| Case | Test | Package |
|------|------|---------|
| Expected | `NewPaho` configures `AutoReconnect=true`, `ConnectRetry=true`, `MaxReconnectInterval=reconnectMaxDelay` | `mqtt` — add to `paho_test.go` |
| Expected | `InitWithCleanup` returns Paho client immediately, connect in background | `mqtt` — add to `init_test.go` |
| Expected | `OnConnect` callbacks fire when `OnConnectHandler` runs | `mqtt` — add to `paho_test.go` |

### Modified tests

| Case | Test | Change |
|------|------|--------|
| Edge (reconnect after broker restart) | `TestReconnectAfterBrokerRestart` | Remove strategy loop; single Paho-only path; remove race skip; keep 25s deadline |
| Failure (bad credentials) | `TestConnectFailsWithBadCredentials` | Remove strategy loop; no `ReconnectStrategy` in config |
| Setup error fallback | `TestInitWithCleanupSetupErrorFallsBackToNoop` | Unchanged — setup errors still fall back to noop |

### Removed tests

| Test | Reason |
|------|--------|
| `TestNormalizeReconnectStrategy` | `normalizeReconnectStrategy()` deleted |
| `TestNewPahoReconnectStrategyConfiguration` | Strategy dispatch deleted |
| `TestPahoReconnectManagerConfigureSetsOptions` | `pahoReconnectManager` struct deleted; equivalent assertion added to paho test |
| `TestPahoReconnectManagerStopNoop` | `pahoReconnectManager` struct deleted |
| `TestConnectWithRetriesSucceedsAfterRetry` | `connectWithRetries` function deleted |
| `TestConnectWithRetriesContextErrorsTriggerDisconnect` | `connectWithRetries` function deleted |
| `TestInitWithCleanupConnectFailureFallsBackToNoop` | No more noop fallback on connect failure |
| `TestInitWithCleanupSubscribeErrorIsReturned` | HA status subscription moved to OnConnectHandler in paho.go |
| `TestInitWithCleanupHADiscoveryPublishesOnOnline` | HA discovery-on-online moved to OnConnectHandler in paho.go |
| `TestInitWithCleanupOnlineHandlerInvokedOnOnlineOnly` | Already a no-op placeholder; removed |
| `TestInitWithCleanupNoHandlersNoDiscoveryDoesNotSubscribe` | Subscription moved to OnConnectHandler |

### Mocks / helpers

- `fakeInitClient` — add `OnConnect` method
- `recordingMQTTClient` — add `OnConnect` method, `onConnectCB` field
- `tbrandon/mbserver` — not needed for this change
- `httptest.NewServer` — not needed for this change
- Existing Mochi broker test helpers (`newTestBroker`, `reserveTCPAddress`, `mustConnectClient`) — unchanged

## Validation Gates

```bash
make test          # all unit tests pass
make build         # binary compiles
go vet ./pkg/mqtt/ # no issues in modified package
go vet ./pkg/cmd/  # no issues in modified package
```

No Dockerfile or CI workflow changes.

## Rollout / Backward Compatibility

- `mqtt_reconnect_strategy` was never a user-facing config key — no migration needed.
- The `Config.ReconnectStrategy` field was package-internal — callers in `pkg/cmd/` never set it (confirmed: zero matches outside `pkg/mqtt/`).
- Post-change reconnect behavior matches today's `paho` strategy — identical to what users who explicitly opted into `paho` experience.
- `maxAttempts` and `baseBackoff` params removed from `InitWithCleanup` — only one call site in `schedule.go`; updated in-place.
- Async connect means MQTT may not be available for the first few seconds after startup. This is acceptable — cron ticks and commands will work once the broker connection succeeds.

## Security Considerations

None. This change removes code, does not introduce new input handling, and does not touch Modbus writes or API credentials.

## Gotchas

- `TestReconnectAfterBrokerRestart` currently uses 25 s as the Paho reconnect deadline (vs. 10 s for custom). Keep the 25 s timeout for the simplified Paho-only variant.
- The `raceDetectorEnabled` build-tag gated files (`race_disabled_test.go`, `race_enabled_test.go`) are removed since the only consumer (the Paho race skip) is being removed.
- Ensure `p.closed` is checked before logging in the connection-lost handler (inline from `pahoReconnectManager.configure()`).
- `SetConnectRetry(true)` means Paho's `Connect()` never returns on its own when the broker is down — the goroutine in `init.go` will run indefinitely. This is by design. The goroutine is a minimal resource cost (sleeping most of the time).
- HA discovery publish and `homeassistant/status` subscription both happen in `OnConnectHandler`. HA discovery calls `PublishDiscovery` which iterates components and publishes retained config messages. This is already the case today.
- The `recordingMQTTClient` in `schedule_mqtt_wiring_test.go` needs `OnConnect` added to satisfy the `Client` interface.

## Summary of Changes (Files Touched)

| File | Action |
|------|--------|
| `pkg/mqtt/reconnect.go` | Delete |
| `pkg/mqtt/reconnect_custom.go` | Delete |
| `pkg/mqtt/reconnect_paho.go` | Delete |
| `pkg/mqtt/reconnect_paho_test.go` | Delete |
| `pkg/mqtt/race_disabled_test.go` | Delete |
| `pkg/mqtt/race_enabled_test.go` | Delete |
| `pkg/mqtt/types.go` | Remove `ReconnectStrategy` type, constants, config field |
| `pkg/mqtt/paho.go` | Inline reconnect config; `SetConnectRetry(true)`; move HA subscription to OnConnectHandler; add `OnConnect` method |
| `pkg/mqtt/client.go` | Add `OnConnect(func())` to `Client` interface |
| `pkg/mqtt/noop.go` | Add no-op `OnConnect` |
| `pkg/mqtt/init.go` | Remove `connectWithRetries`; fire-and-forget goroutine; remove HA subscription |
| `pkg/mqtt/mqtt_test.go` | Simplify reconnect/bad-credential tests; remove strategy tests |
| `pkg/mqtt/init_test.go` | Remove connectWithRetries/noop-fallback tests; add async connect tests |
| `pkg/mqtt/paho_test.go` | Add reconnect options assertion test |
| `pkg/cmd/schedule.go` | Replace `subscribeScheduleCommands` call with `OnConnect` callback |
| `pkg/cmd/schedule_mqtt_wiring_test.go` | Add `OnConnect` to mock; update tests |

## Confidence Score

**9/10** — Mostly deletion + inlining. The `OnConnect` callback pattern adds a small new interface method but it's a well-established pattern. The async connect goroutine is trivial. The main risk is ensuring test mocks are updated correctly across two packages.
