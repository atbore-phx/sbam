# ADR-0004: Optimizer fail-safe — inverter revert timeout plus software safe-state triggers

| Field | Value |
|---|---|
| Status | Accepted (spec amended 2026-10-02: register 40359 added to the write allowlist). Inverter-side behaviour is pending hardware verification **[V6]**. |
| Date | 2026-10-02 |
| Related | SPEC-0001 (Technical Constraints, AC-16, AC-17), ADR-0003, ADR-0005, security review S-001, S-002, S-006 |

## Context

With `scheduler_mode: optimize`, sbam can leave the inverter in a *forced* state: `charge`, `hold`, `standby`, or the new `discharge`. Before this feature the worst persistent state was a forced charge inside a window that the crontab/windows defaults reset at window end.

A forced discharge or standby that outlives its controller drains the battery to the BMS floor or blocks self-consumption, possibly for days. The security review raised this as blocker S-001: nothing on the inverter side reverts a forced state, and SIGTERM, a crash, OOM or a startup validation error all leave the last action in force.

SunSpec model 124 provides `InOutWRte_RvrtTms` (40359), a timeout after which the rate settings revert. The user approved adding it to the write allowlist on 2026-10-02.

## Decision

We will layer an inverter-side dead-man switch with software safe-state triggers. Every trigger writes **ordered defaults** (`StorCtl_Mod=0` first; ADR-0003). In dry-run, which is the default, none of them writes. The only exceptions are explicit user commands (ADR-0005).

1. **Inverter-side revert timer.** Every forced action writes `InOutWRte_RvrtTms = 3900` s before `StorCtl_Mod`. The hourly dispatch refreshes it. If sbam stops for any reason, including kill -9, OOM, host power loss or a hung process, the inverter reverts within about 65 min. **[V6]** Verify on Gen24+ that the revert happens, what it reverts (the mode or only the rates), and that rewriting refreshes the timer.
2. **Graceful stop.** On SIGTERM/SIGINT in optimize mode, when not in dry-run and the last applied action ≠ `self_consume`, the runner writes defaults with a 5 s deadline before `Run` returns.
   - Implementation: `schedule.go` passes a dedicated shutdown hook. The runner performs it on its own goroutine when `ctx.Done()` fires, which preserves the single-writer rule.
3. **Startup validation failure.** If `scheduler_mode` resolves to `optimize`, `opt_dry_run` is not `true`, and `fronius_ip` is non-empty and valid, `sbam schedule` attempts `SetDefaultsOrdered` (5 s deadline) before exiting with code 1. This covers a config edit that breaks validation after a live run left a forced state.
4. **Pause.** In optimize mode, `pause` writes defaults *before* the paused state is published, unless dry-run (ADR-0005).
5. **Missing input, invalid plan or write failure.** These restore defaults (AC-16) and retry at the next hour.
6. **Intra-hour SoC watchdog** (S-006c). While the applied action is `discharge`, a runner timer submits an internal `watchdog` intent every 15 min. It reads SoC through `pkg/storage`. If `SoC ≤ opt_min_soc_pct + 1`, or the read fails, it writes defaults with reason `optimizer_watchdog_min_soc` and cancels further watchdog ticks until the next hourly run. The watchdog never starts a forced action.

## Consequences

### Positive
- Every exit path is covered: graceful stop, crash, hang, config error, pause, input outage, and SoC near the floor. The inverter-side timer works even when sbam is gone.
- No new dependency. Each trigger is a small, testable runner hook with injected timers and clock.

### Negative / trade-offs
- If V6 fails, layer 1 is ineffective. Crash or kill protection then depends on the add-on restart policy and the next startup run. The docs must state this, and S-001 stays High until it is verified.
- Writing on startup validation failure means a misconfigured sbam still touches the inverter, but only to restore defaults, and only when not in dry-run.
- A 3900 s timeout means a hung sbam leaves a forced action for at most about 65 min. A shorter timeout would need sub-hourly refresh writes, which the hourly model does not justify.

### Follow-ups
- Test cases TC-001a/b/c, TC-002a and TC-006b, plus a manual hardware test for V6: write `discharge`, stop sbam with kill -9, and observe the revert time.
- Docs: `docs/site/configuration.md` explains the fail-safe layers and their dependency on V6.

## Alternatives Considered

### Alternative 1: Software-only safety (SIGTERM handler + pause defaults)
This does not cover kill -9, OOM, host power loss or a hung process, which are exactly the cases where forced discharge is most harmful. Rejected.

### Alternative 2: Shorter revert timeout (e.g. 900 s) with 15-minute refresh writes
This would tighten the worst case, but it quadruples Modbus writes and adds a second write path between hourly decisions. With the watchdog for the SoC floor, the extra protection is marginal. Rejected for v1. It can be revisited after V6 shows how the timer behaves.

### Alternative 3: Never use forced discharge (restrict actions to charge/hold/standby/self_consume)
This removes most of the risk but drops AC-11 (export at high price), which is part of the approved spec. Rejected.
