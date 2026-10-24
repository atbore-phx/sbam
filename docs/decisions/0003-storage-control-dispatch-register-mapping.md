# ADR-0003: Storage-control register mapping for optimizer dispatch actions (incl. forced discharge)

| Field | Value |
|---|---|
| Status | Accepted with hardware verification pending (register 40359 approved by the user 2026-10-02). Rows and rules marked **[V]** must be verified on Gen24+ hardware. |
| Date | 2026-10-02 |
| Related | SPEC-0001 (AC-14, AC-15, AC-16, Technical Constraints as amended 2026-10-02), ADR-0002, ADR-0004 (fail-safe), security review S-001, S-003, S-005, S-006, `pkg/fronius/configure.go` |

## Context

Today sbam writes two register sets through `pkg/fronius/configure.go` (SunSpec model 124, 1-based addresses, written at `address − 1`):

- **Defaults** (`mdsc`, `configure -d`): `StorCtl_Mod=0`, `OutWRte=10000`, `InWRte=10000`, `MinRsvPct=0`, `ChaGriSet=1`.
- **Force charge:** defaults with `StorCtl_Mod=2` and `OutWRte=−100·pct`.

SPEC-0001 adds `hold`, `standby` and `discharge(rate)`, and forced discharge is a new capability. The amended spec allows writes to `StorCtl_Mod`, `OutWRte`, `InWRte`, `MinRsvPct`, `ChaGriSet`, `WChaMax` and `InOutWRte_RvrtTms` (40359). Every forced action must also write the revert timeout (≈ 3900 s).

Existing weaknesses the security review flagged:

- **S-003:** `int16` wrap in `−100·power_prc`; float→int conversion of out-of-range values; `SetChargePower` divides by battery capacity rather than `WChaMax`.
- **S-005:** map-ordered writes, so the order is random; no read-back after writing.

SunSpec model 124 semantics we rely on:

- `StorCtl_Mod` is a bitfield: bit 0 means the charge limit (`InWRte`) is active, bit 1 means the discharge limit (`OutWRte`) is active.
- `InWRte` and `OutWRte` are percent of `WChaMax` × 10^`InOutWRte_SF`. On Fronius the scale factor is −2, so 10000 = 100 %.
- Fronius documents a negative `OutWRte` as a forced charge and a negative `InWRte` as a forced discharge.
- `InOutWRte_RvrtTms` (40359) is the timeout in seconds after which the rate settings revert.

## Decision

We will add `pkg/fronius/dispatch.go`. It has **one** pure function that is the single source of truth for action→registers, an ordered writer with read-back, and a dispatcher that wraps them.

### Mapping

`p` = percent·100 of `WChaMax` (algorithm below). `R` = `RevertSeconds` = 3900, a package constant, which exceeds one hourly cycle plus the worst-case tick duration.

| Action | Write order → | `OutWRte` 40356 | `InWRte` 40357 | `MinRsvPct` 40351 | `ChaGriSet` 40361 | `InOutWRte_RvrtTms` 40359 | `StorCtl_Mod` 40349 | Intended behaviour |
|---|---|---|---|---|---|---|---|---|
| `self_consume` | `StorCtl_Mod` **first**, then the rest | 10000 | 10000 | 0 | 1 | not written | 0 | Identical to `mdsc` defaults |
| `charge(r)` | rest first, `StorCtl_Mod` **last** | −p | 10000 | 0 | 1 | R | 2 | Same as today's `ForceCharge`: charge at **at least** p % from grid + PV |
| `hold` | rest first, `StorCtl_Mod` last | 0 | 10000 | 0 | 1 | R | 2 | No discharge; PV surplus may charge **[V1]** |
| `standby` | rest first, `StorCtl_Mod` last | 0 | 0 | 0 | 1 | R | 3 | No charge or discharge; PV surplus exported, not curtailed **[V2]** |
| `discharge(r)` | rest first, `StorCtl_Mod` last | 10000 | −p | 0 | 1 | R | 1 | Forced discharge at **at least** p % to house + grid **[V3]** |

### Revert timeout (40359)

It is written on every forced action, before `StorCtl_Mod`. The hourly re-dispatch rewrites it every hour, so the timer is refreshed while sbam is alive. If sbam stops re-asserting, the inverter reverts within about 65 minutes.

The following must be verified on hardware **[V6]**:

- whether the timer starts when 40359 is written or when `InWRte`/`OutWRte` are written (the write order above puts 40359 after the rates and before the mode, which works in both cases);
- whether the revert restores `StorCtl_Mod` or only the rates (if only the rates revert, `hold`/`standby` limits of 0 % return to 100 %, which is the safe direction);
- whether rewriting the same value restarts the timer.

`self_consume` does not write 40359, so `configure -d` stays byte-identical. A residual timeout value is harmless while `StorCtl_Mod=0`.

### Conversion algorithm (`BuildDispatchRegisters`, S-003)

1. Reject a non-finite `rateW` or `wChaMaxW`, and an unknown action.
2. Reject `wChaMaxW` outside `[100, 30000]` W, and `InOutWRte_SF ≠ −2`. These are read from 40346, 40362 and 40369. On rejection the caller restores defaults with `optimizer_fallback_inverter_limits`.
3. Clamp the rate **in float** to `[0, min(opt_max_*_rate_w, wChaMaxW)]`.
4. Compute `pf = math.Round(rate / wChaMaxW × 10000)` and clamp it to `[0, 10000]` **before** `int16(pf)`.
5. Apply the sign only to the documented register: charge → `OutWRte = −p`, discharge → `InWRte = −p`. All other values are constants from the table.
6. Return `[]RegisterWrite` in the order shown in the table.

A charge or discharge whose `p` rounds to 0 is emitted as `hold` or `standby` respectively, and the rounding is logged at Debug.

As separate hardening, `ForceCharge` rejects `power_prc > 100` and computes in `int32` (S-003, TC-003e).

### Ordered write and read-back (S-005)

`WriteFroniusModbusRegistersOrdered` writes the slice in order on one connection. It then reads every written address back and compares.

On any write error or read-back mismatch, the dispatcher attempts `SetDefaultsOrdered` once, with `StorCtl_Mod=0` first. It returns an error and the reason becomes `optimizer_fallback_modbus_write_failed`.

Whether Gen24 reads back the exact written values, especially negative rates, must be verified **[V7]**. If it does not, read-back is limited to `StorCtl_Mod`.

### Other rules

- **`MinRsvPct` stays 0** in every action, pending V4. Some firmware may grid-charge to reach a non-zero reserve. Minimum-SoC protection instead comes from:
  - the plan;
  - a dispatch-level clamp on hour-0 discharge, so it can't plan below `min + 1 %` with a 0.95 factor on usable energy (S-006a);
  - the intra-hour SoC watchdog in the runner (S-006c);
  - the user's inverter "SoC min" setting.
- **`ChaGriSet` stays 1**, as today. The inverter does not grid-charge on its own in self-consumption. Verify that `hold` never imports to charge **[V1]**.
- **`WChaMax` is read, never written.**
- **Dry-run** (now the default) opens no Modbus connection. It logs the exact `[]RegisterWrite` it would have written.

## Consequences

### Positive
- One table-tested pure function, with register-level `mbserver` tests per action (TC-003d), covers sign, clamping, order and the revert timer.
- `self_consume` and `charge` reuse register values already proven in the field. New behaviour is confined to `hold`, `standby`, `discharge` and 40359.
- The inverter has its own dead-man switch for every forced state, provided V6 confirms it (ADR-0004).
- Ordered writes plus read-back close a latent hazard that also affects `configure -d`, which now uses the ordered defaults writer too.

### Negative / trade-offs
- "At least p %" semantics mean actual flow can exceed the plan when PV or load deviate. This is accepted because of the hourly re-plan and the watchdog. A pinned rate would import power for house load during peak-price discharge.
- 7 writes + 7 reads per hour, versus 5 + 5 today.
- Until V1–V7 are verified, live use carries firmware uncertainty. Dry-run is the default, and the "would write" log supports side-by-side verification.

- **Amendment 2026-10-16 — setpoint sides.** The live trial showed that the two rate registers act on different sides of the inverter. A forced charge (`OutWRte = −p`) stores p % of `WChaMax` in the battery (stored/setpoint 0.98–1.01). A forced discharge (`InWRte = −p`) delivers p % of `WChaMax` as AC output, and the battery also supplies the conversion loss: SoC fell by 1.10 × the setpoint at 2.4–3.0 kW (= 1/√0.83). The register mapping is unchanged; the runner converts the planned battery-side discharge rate to the AC-side setpoint (× √η) before the percentage is computed (SPEC-0001 AC-11e). The ratio was constant across rates, so it is a loss factor and not the house load being added on top of the "at least p %" setpoint.

### Follow-ups
- Hardware verification checklist V1–V7 (design review). The Test Engineer writes a manual hardware test plan.
- If V6 fails (the firmware ignores 40359), S-001 stays High. Protection then relies on ADR-0004's software measures alone, and the docs must say so.

## Alternatives Considered

### Alternative 1: Discharge via `StorCtl_Mod=3`, `OutWRte=p`, `InWRte=−p` (pinned rate)
House deficit above p would be imported at peak price. Rejected.

### Alternative 2: Express `hold`/`standby` via `MinRsvPct` = current SoC or via `ChaGriSet`
A reserve may trigger grid charging [V4]. `ChaGriSet` only gates grid charging. Rejected.

### Alternative 3: Write `WChaMax` to scale rates
It is a persistent nameplate-like value that affects every percentage-based setting. Rejected.

### Alternative 4: No inverter-side revert; rely on software only (SIGTERM handler, pause defaults)
This does not cover kill -9, OOM, power loss of the host, or a hung process. Rejected now that the user has approved 40359.
