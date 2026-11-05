# ADR-0002: Battery dispatch optimizer — dynamic programming over discretized stored energy

| Field | Value |
|---|---|
| Status | Accepted 2026-10-02 |
| Date | 2026-10-02 |
| Related | SPEC-0001 (AC-8 … AC-12, AC-14), `docs/design-reviews/2026-10-02-cost-optimized-dispatch.md`, ADR-0003 |

## Context

SPEC-0001 requires a per-hour battery plan over a horizon of up to 36 hours that minimizes Σ(import·buy − export·sell) − terminal value. The plan is subject to:

- an energy balance in every hour;
- SoC bounds;
- charge and discharge power limits;
- round-trip efficiency η applied on the charge side;
- no simultaneous import and export.

The plan must also:

- be deterministic (AC-12);
- match a brute-force reference within one SoC step on grids of up to 6 hours (AC-8);
- run in under 2 s on a Raspberry Pi 4.

Two features make the problem non-convex:

1. **The action set is discrete and defined by the inverter's behaviour, not by free flows.** With the five actions `charge`, `hold`, `standby`, `discharge` and `self_consume`, some battery flows can't be produced. In an hour with PV surplus `s > 0`, the battery either takes all of the surplus (`self_consume`/`hold`), takes none of it (`standby`), takes more than the surplus (`charge`), or discharges. "Store half the surplus and export the rest" is not one of the actions. A plan that schedules it can't be executed.
2. Prices can be negative, so the cost is not monotone in the flows.

sbam is built with `CGO_ENABLED=0` for amd64 and arm64, and prefers the standard library.

## Decision

We will implement a **backward dynamic program (DP) over discretized usable stored energy** in a new pure package `pkg/optimize`. It does no I/O and has no clock of its own.

- **State:** usable energy above the minimum SoC, `E ∈ [0, Emax]`, where `Emax = (100 − opt_min_soc_pct)/100 × capacity_Wh`. The range is split into `N = 200` equal steps, so `Δ = Emax/200` (≈ 46.5 Wh for a 10 kWh battery at 7 % min SoC). N is a package constant, not user config. Tests may override it.
- **Decision per hour = target state `j`** (end-of-hour energy). The battery flow follows from `ΔE = (j − i)·Δ`:
  - charge: grid/PV-side input `ΔE/η` (charge-side efficiency, AC-8);
  - discharge: output `−ΔE` (1:1).
- **Hour cost:**
  - `net = cons − pv + (ΔE > 0 ? ΔE/η : ΔE)`;
  - `cost = net > 0 ? net·buy : net·sell` (in Wh/1000 × price);
  - one net flow per hour means no simultaneous import and export.
- **Realizability filter.** Only transitions that one of the five actions can produce are allowed. With `s = pv − cons` and the self-consume transition `ΔE_sc` (absorb surplus or cover deficit, within limits and headroom, rounded to the grid):
  - `s ≥ 0`: `{ΔE_sc}` ∪ `{0}` (standby) ∪ `(ΔE_sc, η·Cmax]` (charge) ∪ `[−Dmax, 0)` (discharge);
  - `s < 0`: `{ΔE_sc}` ∪ `{0}` (hold) ∪ `(0, η·Cmax]` (charge) ∪ `[−Dmax, ΔE_sc)` (discharge beyond the deficit).
- **Labelling.** The chosen `ΔE` maps back to exactly one action and a rate (charge rate = `ΔE/η/f`, discharge rate = `−ΔE/f`, where `f` is the fraction of the hour remaining).
- **Terminal value:** `V_T(E) = −η · mean(buy over horizon) · E/1000` (AC-10).
- **Determinism and tie-breaking:**
  - Candidates are evaluated in a fixed order: `self_consume`, then `hold`/`standby`, then charge targets nearest-first, then discharge targets nearest-first.
  - A candidate replaces the incumbent only if it is cheaper by more than `1e-9`.
  - On ties, the least intrusive action wins.
  - No maps are iterated and nothing is random.
- **Complexity:** `O(T · N²)` ≈ 36 × 201 × 201 ≈ 1.5 M cost evaluations. On the order of 10–50 ms on a Pi 4, two orders of magnitude inside the 2 s budget. Memory: one `[T][N+1]int16` policy table (≈ 15 KB).
- **Deadline and invariants** (security review S-007):
  - `Solve(ctx, …)` checks `ctx.Err()` once per hour layer, and the caller passes a 2 s deadline.
  - Inputs must be finite. `ValidateGrid` runs first and rejects NaN/Inf and out-of-range values.
  - `ValidatePlan` is applied to every result:
    - horizon ≥ 1;
    - slot 0 contains `now`;
    - slots are contiguous one-hour absolute instants;
    - every rate is within its limit;
    - the SoC trajectory stays within `[min, 100]`;
    - all costs are finite;
    - each action is consistent with its flow sign.
  - A violation, or a missed deadline, makes the caller fall back to defaults (`optimizer_fallback_invalid_plan` / `optimizer_fallback_solver_error`).
- **Reason classification** (deterministic, for the UX reason codes, based only on plan values):
  - **`charge`:**
    - `buy[0] ≤ 0` → `optimizer_charge_negative_price`;
    - otherwise → `optimizer_charge_ahead_of_peak`.
  - **`hold`** → `optimizer_hold_for_later_peak`.
  - **`standby`** → `optimizer_standby_export_pv`.
  - **`discharge`:**
    - a later plan hour is `charge` → `optimizer_discharge_before_cheap_charge`;
    - otherwise → `optimizer_export_high_price`.
  - **`self_consume`:** the DP also records the best total cost among non-self-consume first actions from `i0`.
    - If that cost exceeds the chosen cost by more than `0.005` currency units → `optimizer_self_consume_optimal`;
    - otherwise → `optimizer_self_consume_no_gain`.
- **Verification seam:**
  - a test-only exhaustive enumerator over the same realizable transitions on small grids (N ≤ 10, T ≤ 6) must produce the same minimum cost;
  - an independent continuous simulator (`Simulate(plan, grid)`) re-evaluates the plan's cost and SoC trajectory and must agree within one Δ (AC-8).

## Amendment 2026-10-03: minimum gain for forced actions

The first dry-run day showed forced actions chosen for gains below the model's precision (for example a `discharge` that covered the house plus ~28 Wh of export for 0.04 DKK, caused by the 200-step discretization). In live mode each forced hour is a register write and, for discharge, a fixed minimum rate that ignores load changes inside the hour.

**Decision.** Every DP candidate other than the self-consumption target `j_sc` carries an extra decision penalty of `MinGain × Fraction` (DKK) in the backward pass. A forced action is therefore chosen only if it beats `self_consume` by at least `opt_min_gain` per hour (user setting, default 0.50 DKK/h, range [0, 10]; 0 restores the original behaviour). The penalty is excluded from the labelled costs, so `ExpectedCost`, `TerminalValue` and `Simulate` stay real model costs. Determinism and the tie-break order are unchanged.

## Amendment 2026-10-21: threshold per transition, wear cost per kWh

The flat penalty was charged on every hour that was not `self_consume`, including `hold` and `standby`. A plan that charges in the cheapest hour and holds until a later peak paid it once per waiting hour, so the DP preferred charging in a dearer hour closer to the peak; a morning `standby` that exports PV while midday PV fills the battery paid it for every morning hour. Neither action moves battery energy, so there is no wear to justify it.

**Decision.** The decision penalty of a transition `i → j` with self-consumption target `j_sc` is:

- `0` for `j = j_sc`;
- `0.1 × MinGain × Fraction` for `hold`/`standby` (`j = i`), a small floor against switching away from self-consumption for pennies;
- `MinGain × Fraction + WearCost × extra × Δ / 1000` for `charge` and `discharge`, where `extra` is the steps moved beyond what self-consumption would move in the same direction: `j − max(i, j_sc)` for a charge, `min(i, j_sc) − j` for a discharge.

`WearCost` (`opt_wear_cost`, currency per kWh, default 0, range [0, 5]) scales the threshold with the energy moved, so a small but real gain passes and back-and-forth for pennies does not. Both parts remain decision-only. The reason classification now takes the best forced alternative's cost without its own penalty directly, instead of subtracting one flat penalty from it.

## Amendment 2026-10-05: battery-side charge rate

The Gen24 charge and discharge setpoints are the power into and out of the battery, after conversion losses (confirmed by the user). The original design treated the charge rate as grid-side (stored ΔE = η × rate), which would command ~20 % too much charging and under-use the battery's charge limit by 17 %.

**Decision.** The charge limit per slot is `MaxChargeW × Fraction` of stored energy, and the dispatched charge rate is `ΔE / Fraction`. The grid energy for charging stays `ΔE / η` and is used only in the cost. PV surplus absorbed in self-consumption, `hold` and `charge` still stores `η × surplus`, capped at the battery-side limit. `Simulate` mirrors these semantics. Discharge is unchanged (1:1, battery-side).

## Amendment 2026-10-06: split round-trip loss

The first live night (2026-10-05/06) showed the battery dropping 7.96 kWh to deliver 7.25 kWh to the house in self-consumption hours (1.10 Wh per Wh), while the model assumed lossless discharge. Half of the night's 1.4 kWh planning gap came from this.

**Decision.** The round-trip efficiency η is split evenly: η_c = η_d = √η. Storing ΔE draws ΔE/η_c; delivering X Wh takes X/η_d from the battery. Self-consumption, `hold` and `charge` store η_c × PV surplus; self-consumption covers a deficit D by taking D/η_d from the battery; a `discharge` of ΔE delivers |ΔE|·η_d. Rates stay battery-side (AC-11c). The terminal price stays η·mean(buy) (AC-10, unchanged by user decision). `Simulate` mirrors these semantics.

## Consequences

### Positive
- The DP finds the exact optimum over the discretized, *executable* action set. Non-convexity and negative prices need no special handling.
- No dependency, no cgo, and a short, auditable solver of about 150 lines.
- Determinism comes from construction, not from solver tolerances.
- The plan maps 1:1 onto the inverter actions in ADR-0003, so no post-hoc rounding step can produce an infeasible command.

### Negative / trade-offs
- Discretization error is at most one Δ per hour in the SoC trajectory. Acceptable: SoC itself is reported in whole percent by the Fronius Solar API, and the plan is recomputed every hour.
- The hourly aggregation hides sub-hour PV/load variation. A `hold` hour with an overall deficit may still absorb some PV. This is inherent in the hourly model the spec chose (non-goal: sub-hourly control).
- Rates are continuous in Watts but quantized by Δ. With N = 200 the rate granularity is ≈ 56 W for a 10 kWh battery. That is coarser than the register resolution (0.01 % of `WChaMax`, ≈ 1 W) but far below the forecast error that drives the plan.

### Follow-ups
- `BenchmarkSolve36h` in `pkg/optimize` keeps a regression guard on performance.
- If a later spec adds partial PV storage (an `InWRte` charge cap), it is a new action with its own realizable interval. The DP structure is unchanged.

## Alternatives Considered

### Alternative 1: Linear programming (LP) with continuous flows
LP handles continuous rates exactly, but:

- (a) "no simultaneous import/export" and the action realizability constraints are disjunctive, so they need binaries (MILP), not LP;
- (b) negative sell prices make the LP relaxation choose infeasible simultaneous import/export;
- (c) there is no maintained pure-Go MILP solver in the stack. GLPK/CBC/HiGHS bindings need cgo, which `CGO_ENABLED=0` forbids;
- (d) an LP optimum can map onto a non-executable action.

Rejected.

### Alternative 2: Greedy price ranking (charge in the k cheapest hours, discharge in the k most expensive)
This is simple, and #150 plans it for import-only charging. It can't respect the SoC trajectory, the charge/discharge limits and the terminal value together. It also has no principled answer to AC-11a (sell PV now, refill from later PV) or to η-aware break-even across more than two hours. AC-8 requires optimality against brute force. Rejected.

### Alternative 3: DP over action × discretized rate (instead of target state)
This enumerates actions with, for example, 10 rate levels and rounds the resulting state to the grid. It has the same complexity class, but the rounding of every transition accumulates error, and a coarse rate grid misses the optimum. Using target states as decisions makes the discretization of state and rate the same thing. Rejected in favour of the target-state formulation.
