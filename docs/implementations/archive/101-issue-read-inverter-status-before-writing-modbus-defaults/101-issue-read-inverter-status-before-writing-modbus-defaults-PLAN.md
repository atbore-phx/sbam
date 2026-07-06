# PLAN: Read inverter status before writing Modbus defaults

> TASK: [101-issue-read-inverter-status-before-writing-modbus-defaults-TASK.md](101-issue-read-inverter-status-before-writing-modbus-defaults-TASK.md)
> Issue: [#101](https://github.com/atbore-phx/sbam/issues/101)
> Date: 2026-05-28

## 1. Task Analysis

**Goal**: On classifier error (DecisionSkip), read the inverter's `StorCtl_Mod` register (40349)
before writing default Modbus registers. Skip the write if the inverter is already in normal mode
(value 0).

**Non-goals**: Changing other code paths (`ForceCharge` with power>0, `Setdefaults` command),
changing the `Connectmodbus` helper, adding config flags.

**Acceptance criteria**:
- StorCtl_Mod=0 → no write (skip)
- StorCtl_Mod!=0 → write defaults
- Read failure → write defaults (fail-safe)
- Covered by unit tests

## 2. Current State

`pkg/fronius/schedule.go:21-30` — classifier error handler:

```go
if cerr != nil {
    u.Log.Errorf("Classifier error: %s - resetting Fronius to defaults (stop forced charge)", cerr)
    err = ForceCharge(fronius_ip, 0, p)  // always writes all 5 default registers
    if err != nil {
        u.Log.Errorf("Error setting defaults after classifier error: %s", err)
        return 0, decision, reason, pw, err
    }
    return 0, decision, reason, pw, nil
}
```

`ForceCharge(fronius_ip, 0, p)` calls `Setdefaults` → `Connectmodbus` → open, read-all,
write-all, close. No conditional check on inverter state.

Key registers (`pkg/fronius/configure.go:12-18`):
- `StorCtl_Mod = 40349` — storage control mode: 0=normal, 2=charge/discharge limited
- `OutWRte = 40356`, `InWRte = 40357`, `MinRsvPct = 40351`, `ChaGriSet = 40361`

Existing helpers available:
- `OpenModbusClient(proto, url, port)` — opens TCP connection, sets global `modbusClient`
- `ReadFroniusModbusRegister(address uint16) (int16, error)` — reads single register
- `ClosemodbusClient() error` — closes global client

## 3. Target Architecture

Two files change: `pkg/fronius/configure.go` (new helper) and `pkg/fronius/schedule.go` (caller).

### New helper: `ReadModbusRegister` in `configure.go`

```go
func ReadModbusRegister(modbusIP string, register uint16, port ...string) (int16, error)
```

Pattern matches `Connectmodbus`: open TCP → defer close → operate → return. Reads a single register
by address. Generic and reusable for any future single-register read.

### Data flow (classifier error path in `schedule.go`)

```
ClassifyDecision errors
  → storCtlVal, readErr := ReadModbusRegister(fronius_ip, StorCtl_Mod, p)
  → if readErr == nil && storCtlVal == 0: log + skip, return nil
  → if readErr != nil or storCtlVal != 0: ForceCharge(fronius_ip, 0, p) [existing behavior]
```

No new types, no new packages.

## 4. Dependency Choices

No new dependencies. All needed functions already exist in `pkg/fronius/configure.go`:
- `OpenModbusClient`, `ReadFroniusModbusRegister`, `ClosemodbusClient` — already used by `Connectmodbus`
- `StorCtl_Mod` constant — already defined

## 5. Configuration Changes

None.

## 6. Implementation Blueprint

### Step 1 — Add `ReadModbusRegister` helper to `pkg/fronius/configure.go`

**File**: `pkg/fronius/configure.go`
**What**: Add a new exported function that opens a Modbus connection, reads a single register, and
closes the connection. Mirrors the `Connectmodbus` pattern (open → defer close → operate).

**Signature**: `func ReadModbusRegister(modbusIP string, register uint16, port ...string) (int16, error)`

**Location**: Add after `ReadFroniusModbusRegister` (line 69), before `Setdefaults` (line 71).

**Rationale**: Generic single-register reader, reusable for any future needs. Follows existing
conventions: default port "502", variadic port override, `u.HandleError` wrapping.

### Step 2 — Update classifier-error path in `pkg/fronius/schedule.go` (lines 21-30)

**File**: `pkg/fronius/schedule.go`
**What**: Insert a read-check before the unconditional `ForceCharge(fronius_ip, 0, p)`.

**New logic**:
```go
if cerr != nil {
    u.Log.Errorf("Classifier error: %s - checking inverter status before resetting defaults", cerr)
    storCtlVal, readErr := ReadModbusRegister(fronius_ip, StorCtl_Mod, p)
    if readErr == nil && storCtlVal == 0 {
        u.Log.Info("Inverter is not force-charging (StorCtl_Mod=0), skipping defaults write")
        return 0, decision, reason, pw, nil
    }
    // read failed or inverter is force-charging — write defaults
    err = ForceCharge(fronius_ip, 0, p)
    if err != nil {
        u.Log.Errorf("Error setting defaults after classifier error: %s", err)
        return 0, decision, reason, pw, err
    }
    return 0, decision, reason, pw, nil
}
```

**Rationale**: If the read succeeds and StorCtl_Mod is 0, the inverter is already in normal mode —
no need to write defaults. If the read fails or the value is non-zero, fall through to the existing
`ForceCharge` call (fail-safe).

## 7. Test Plan

### `pkg/fronius/fronius_test.go` — add 3 test cases

**Test 1: Classifier error + StorCtl_Mod=0 → skip write (expected case)**
- Set up mock Modbus server (registers default to 0, including StorCtl_Mod)
- Call `SetFroniusChargeBatteryMode` with classifier-error-triggering params
  (e.g., `pwBatt2charge=2000, pwForecast=100, pwConsumption=5000, pwBattMax=5000,
   forecastChargeEnabled=false, battReserveChargeEnabled=false`)
- Assert no error, result=0
- Verify defaults were NOT written: open a fresh connection and read back `StorCtl_Mod` → should still be 0

**Test 2: Classifier error + StorCtl_Mod!=0 → write defaults (edge case)**
- Set up mock Modbus server
- Pre-set `StorCtl_Mod` to 2 via a modbus write (simulate inverter in force-charge mode)
- Call `SetFroniusChargeBatteryMode` with classifier-error-triggering params
- Assert no error
- Verify defaults WERE written: read back `StorCtl_Mod` → should be 0 (reset by defaults)

**Test 3: Classifier error + Modbus read fails → write defaults (failure case)**
- Do NOT start mock server (connection refused)
- Call `SetFroniusChargeBatteryMode` with classifier-error-triggering params
- Assert error (ForceCharge also fails to connect)
- This confirms fail-safe: read failure does not suppress the write attempt

### Existing tests to update

**`TestBatteryChargeModeClassifierErrorResetsDefaults`** (line 301):
With the new code and StorCtl_Mod=0 on the mock server, this test will skip the write (previously it wrote). The test still passes (`assert.NoError`, `assert.Equal(0, result)`), but the name is misleading. Rename to `TestBatteryChargeModeClassifierErrorSkipsDefaultsWhenNotCharging`.

**`TestBatteryChargeModeClassifierErrorResetFails`** (line 328):
No mock server → read fails → ForceCharge also fails → still returns error. Behavior and test unchanged.

## 8. Validation Gates

```bash
make test          # all tests
make build         # verify compilation
go test ./pkg/fronius/... -v -count=1  # focused test run
```

## 9. Rollout / Backward Compatibility

- No config changes, no migration needed.
- Behavior change: classifier-error path no longer unconditionally writes Modbus registers when
  inverter is already in normal mode. This is the desired improvement.
- Home Assistant add-on: no `config.json` or `CHANGELOG.md` changes needed.
- README: no changes needed.

## 10. Security Considerations

- Modbus TCP is unauthenticated on the local network — no new attack surface.
- Writing Modbus registers is protected by the same connection logic as before; the read is
  purely informational and cannot cause harm.
- Fail-safe design: if the read fails for any reason, we write defaults (conservative).

## 11. Gotchas

- Global `modbusClient` variable in `modbus.go`: `OpenModbusClient` and `ClosemodbusClient` operate
  on the package-level `modbusClient`. Our read-check opens and closes it; then `ForceCharge` opens
  it again via `Connectmodbus`. This is safe because `OpenModbusClient` creates a new client each
  time and `ClosemodbusClient` cleans up before the next open.
- `ReadFroniusModbusRegister` wraps its error through `u.HandleError` which logs and returns the
  error — we can safely check the returned error value.
- Mock server (`mbserver`) starts with all holding registers at 0, so the default state naturally
  matches "inverter in normal mode" for Test 1.

## 12. Open Questions / Risks

- RESOLVED: The issue specifically asks for reading StorCtl_Mod before writing. No open questions.

## 13. Confidence Score

**9/10** — Single file change, well-understood existing helpers, clear test strategy with mock server.