# ADR-0005: MQTT command semantics in `scheduler_mode: optimize`

| Field | Value |
|---|---|
| Status | Accepted 2026-10-02 |
| Date | 2026-10-02 |
| Related | SPEC-0001 (AC-17), ADR-0004, security review S-002, S-011, UX design §7.2 (commands), `pkg/cmd/schedule_runner.go` |

## Context

AC-17 says `pause`, `force_charge`, `trigger_now` and `set_defaults` must keep working in optimize mode. Today's semantics are unsafe when the inverter can be held in forced discharge:

- `pause` only skips ticks and leaves the last action in force.
- `set_defaults` and `force_charge` are rejected while paused, so the user's safety command is blocked (S-002).
- `force_charge` is silently overwritten at the next run, and `duration_s` is ignored.
- `trigger_now` has no rate limit, so each call means HA fetches plus Modbus writes (S-011).

The UX design wants `set_reserve` rejected in optimize mode (O-5).

## Decision

Optimize mode gets the following command semantics. Other modes are unchanged. Every command is acked, as today.

| Command | Optimize-mode behaviour |
|---|---|
| `pause` | If not dry-run: **write ordered defaults first**, then set the pause and publish `last_decision=paused`, reason `optimizer_paused`. If the defaults write fails, the pause is still set and the ack carries the error. While paused, hourly runs compute and publish the plan but write nothing. |
| `resume` | Clear the pause and submit an immediate run. Pause **expiry** does the same: a runner-owned `pauseTimer` armed at `until` submits a tick. |
| `set_defaults` | **Always accepted**, paused or not. Writes ordered defaults. If an override is active (below), it ends it. The next hourly run (or resume) re-applies the plan. |
| `force_charge` | Rejected while paused, as today. Otherwise it is written through the existing `ForceCharge` path **plus the revert timeout 40359 = `duration_s + 300` s** (or seconds to the end of the current hour + 300 when no duration is given; capped at 65535 (the register is uint16)) and starts a **manual override** until `now + duration_s`. Without `duration_s`, the override lasts until the end of the current hour, which matches the UX "next xx:00 overrides it". While the override is active, optimizer runs compute and publish with reason `optimizer_manual_override` and write nothing. |
| `trigger_now` | Runs the optimizer immediately. Rate-limited to one accepted run per 60 s. Extra commands get `accepted:false, error:"rate limited"`. History is served from the daily cache. |
| `set_reserve` | Rejected: `accepted:false, error:"not supported in scheduler_mode=optimize"`. |

All state lives in runner-owned fields (`overrideUntil`, `pauseTimer`, `lastTriggerAt`) and is mutated only on the runner goroutine.

## Consequences

### Positive
- The user's safety commands always work, and pausing never freezes a forced discharge (S-002).
- A user's manual charge intent is honoured for its stated duration (S-011a).
- `trigger_now` can't be used to hammer HA or the inverter (S-011b).

### Negative / trade-offs
- `pause` behaves differently in optimize mode than in windows/crontab: it writes. This is documented in `docs/site/mqtt.md` and the add-on changelog.
- `force_charge` in optimize mode writes the revert timeout (user decision 2026-10-02), so a manual forced charge can't outlive sbam by more than its duration + 5 min. In the other modes `force_charge` is unchanged.

### Follow-ups
- TC-002a/b/c, TC-011a/b; UX copy for the ack errors.

## Alternatives Considered

### Alternative 1: Keep today's semantics unchanged in every mode
A paused optimizer could hold forced discharge indefinitely, with `set_defaults` blocked. Rejected (S-002 blocker).

### Alternative 2: Make `pause` write defaults in all modes
This silently changes behaviour for existing windows/crontab users, which violates the compatibility rule in `integration-contracts.md`. Rejected.
