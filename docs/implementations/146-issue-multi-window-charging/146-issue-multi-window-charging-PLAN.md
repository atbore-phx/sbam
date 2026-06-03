# PLAN: Multi-window charging schedule with per-window max_charge and horizon

> **TASK**: [146-issue-multi-window-charging-TASK.md](./146-issue-multi-window-charging-TASK.md)
> **Issue**: [#146](https://github.com/atbore-phx/sbam/issues/146)
> **Epic**: [#151](https://github.com/atbore-phx/sbam/issues/151) — smart scheduler v2.x → v3.0
> **Date**: 2026-06-03
> **Confidence**: 9/10

---

## 1. Task Analysis

**Goal**: Replace the single `start_hr` / `end_hr` / `max_charge` triple with an ordered list of charge windows, each carrying its own `max_charge` and optional `forecast_horizon` / `consumption_horizon` overrides.

**Non-goals** (explicitly out of scope):
- `scheduler.mode` selector (separate issue #147 — this PLAN designs the window engine to be ready for it)
- Auto/price-driven window population
- Removing legacy keys (they remain as compat shim until v3.0.0)
- Modbus register changes

**Acceptance criteria** (from TASK):
- [ ] Configs without `windows:` behave identically to v2.1
- [ ] Two-window config selects the correct window per tick
- [ ] Cross-midnight window + daytime window coexist correctly
- [ ] Overlap and invalid-time configs rejected at startup
- [ ] MQTT discovery exposes active window sensor
- [ ] README and add-on docs updated

---

## 2. Current State

| Concern | Where | Notes |
|---|---|---|
| Window type | `pkg/cmd/schedule.go:49-52` | `clockSegment` is internal to cmd |
| Window check | `pkg/cmd/schedule_runner.go:552-568` | `checkTimeRangeAt(now, startHR, endHR)` supports cross-midnight |
| Window expansion | `pkg/cmd/schedule.go:312-320` | `expandClockWindow` returns 1 or 2 segments |
| Runner config | `pkg/cmd/schedule_runner.go:27-48` | `RunnerConfig` carries `StartHR`, `EndHR`, `MaxCharge`, `ForecastHorizon`, `ConsumptionHorizon` as scalars |
| Tick dispatch | `pkg/cmd/schedule_runner.go:160-275` | `Runner.Tick()` calls `checkTimeRangeAt`, passes `r.cfg.MaxCharge` and `r.cfg.ForecastHorizon` to fronius/power handlers |
| Horizon validation | `pkg/power/horizon.go` | `ValidateForecastHorizon`, `ValidateConsumptionHorizon`, `ResolveForecastDay`, `ResolveConsumption` |
| MQTT state | `pkg/mqtt/types.go:23-38` | `StatePayload` has `ChargeWindowActive *bool`, scalar horizon fields |
| MQTT discovery | `pkg/mqtt/discovery.go` | `charge_window_active` binary sensor; no active-window sensor |
| CLI flags | `pkg/cmd/schedule.go:227-259` | `registerScdCmd()` wires `start_hr`, `end_hr`, `max_charge`, etc. |
| Validation | `pkg/cmd/schedule.go:371-420` | `checkScheduleschedule` validates legacy window + horizons |
| HA schema | `home-assistant/addons/sbam/config.json` | No array-of-objects precedent; legacy `start_hr`/`end_hr`/`max_charge` as flat keys |

---

## 3. Target Architecture

### Package layout

```
pkg/power/
  window.go          ← NEW: Window types, validation, active-window resolution
  window_test.go     ← NEW: unit tests

pkg/cmd/
  schedule.go        ← MODIFY: add --windows / --windows-json flags, update checkScheduleschedule
  schedule_runner.go ← MODIFY: RunnerConfig gains Windows field, Tick resolves active window
  schedule_test.go   ← MODIFY: add multi-window selection tests

pkg/mqtt/
  types.go           ← MODIFY: StatePayload gains ActiveWindow fields
  discovery.go       ← MODIFY: add active_window sensor entity

home-assistant/addons/sbam/
  config.json        ← MODIFY: add windows array-of-objects schema
  run.sh             ← MODIFY: export WINDOWS_JSON env var
```

### Data flow

```
config.yaml                  Viper/CLI                Runner.Tick()
────────────                 ─────────                ────────────
windows:                     windows []power.Window   1. active := power.ResolveActiveWindow(windows, now)
  - name: "night"        →   ────────────────────→   2. max_charge = active.MaxCharge  (or top-level fallback)
    start: "02:00"             validated at startup   3. fh = active.ForecastHorizon (or top-level fallback)
    end: "06:00"                                     4. ch = active.ConsumptionHorizon (or top-level fallback)
    max_charge: 3500                                 5. pass resolved values to fronius.Handler + power.Handler
    forecast_horizon: tomorrow
```

### Key types

```go
// pkg/power/window.go

// Window describes a single charge window (YAML list entry).
type Window struct {
    Name               string  // optional; auto-generated "window-N" if empty
    Start              string  // "HH:MM" local
    End                string  // "HH:MM" local; cross-midnight allowed
    MaxCharge          float64 // W, required
    ForecastHorizon    string  // optional; empty means "inherit top-level"
    ConsumptionHorizon string  // optional; empty means "inherit top-level"
}

// ValidateWindows checks the full list: non-empty, no overlaps, valid times/bounds.
func ValidateWindows(windows []Window) error

// ResolveActiveWindow returns the window that contains `now`, or nil if none.
// It uses the same cross-midnight logic as checkTimeRangeAt.
func ResolveActiveWindow(windows []Window, now time.Time) *Window

// SyntheticLegacyWindow builds a single Window from legacy config keys.
func SyntheticLegacyWindow(startHR, endHR string, maxCharge float64, fh, ch string) Window
```

---

## 4. Dependency Choices

**No new dependencies.** All work uses the existing stack:
- `github.com/spf13/cobra` + `viper` — CLI flags and config binding
- `encoding/json` — `--windows-json` / `WINDOWS_JSON` unmarshalling
- `time` — clock parsing, minute-of-day arithmetic, cross-midnight logic
- `github.com/stretchr/testify` — assertions in new tests

The cross-midnight clock expansion logic already exists in `pkg/cmd/schedule.go` (`expandClockWindow`, `clockMinute`, `checkTimeRangeAt`). These will be **moved** (not duplicated) into `pkg/power/window.go` and re-exported, then `pkg/cmd/` will call into them.

---

## 5. Configuration Changes

### `config.yaml` — new `windows` key

```yaml
windows:
  - name: "night"
    start: "02:00"
    end: "06:00"
    max_charge: 3500
    forecast_horizon: "tomorrow"
    consumption_horizon: "full_day"
  - name: "midday"
    start: "12:00"
    end: "15:00"
    max_charge: 2000
    # forecast_horizon and consumption_horizon inherit top-level defaults
```

### CLI flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--windows` | `string` (repeatable) | `[]` | CSV: `name:start-end:max_charge[:forecast_horizon[:consumption_horizon]]` |
| `--windows-json` | `string` | `""` | JSON array of window objects |

### Env var

| Var | Description |
|---|---|
| `WINDOWS_JSON` | JSON array of window objects |

### `home-assistant/addons/sbam/config.json` schema

Add to both `"options"` and `"schema"`:

```json
"windows": [
  {
    "name": "str",
    "start": "match(^([01]?[0-9]|2[0-3]):[0-5][0-9]$)",
    "end": "match(^([01]?[0-9]|2[0-3]):[0-5][0-9]$)",
    "max_charge": "float",
    "forecast_horizon": "list(default|next_solar_day|remaining_today|today|tomorrow|off)?",
    "consumption_horizon": "list(full_day|remaining_today)?"
  }
]
```

### `home-assistant/addons/sbam/run.sh`

Export `WINDOWS_JSON` from add-on options JSON using `jq`.

### Validation rules (startup)

1. If both `windows:` and legacy `start_hr`/`end_hr` are explicitly set → **reject** with clear error
2. If `windows:` present → validate: non-empty, no overlaps, valid times, valid horizons, max_charge ≥ 0
3. If `windows:` absent → synthesize single window from legacy keys (unchanged behavior)
4. Overlap detection: sort by start minute; each window's end must be ≤ next window's start (with cross-midnight aware comparison)

---

## 6. Implementation Blueprint

### Step 1 — Create `pkg/power/window.go`

**New file.** Define `Window` struct and core functions:

```go
package power

import (
    "fmt"
    "sort"
    "time"
)

type Window struct {
    Name               string
    Start              string
    End                string
    MaxCharge          float64
    ForecastHorizon    string
    ConsumptionHorizon string
}
```

Functions to implement:
- `ValidateWindows(windows []Window) error` — validates non-empty, each window's times valid via `isValidClock()`, no overlaps, max_charge ≥ 0, and any explicit horizon values valid via `ValidateForecastHorizon`/`ValidateConsumptionHorizon`.
- `ResolveActiveWindow(windows []Window, now time.Time) *Window` — iterates windows in order, returns first whose time range contains `now`. Uses `clockMinute` + `expandClockWindow` logic (moved from `pkg/cmd/`).
- `SyntheticLegacyWindow(startHR, endHR string, maxCharge float64, fh, ch string) Window` — returns a single Window with the provided values.
- `parseClock(value, field string) (time.Time, error)` — parses "HH:MM".
- `clockMinute(t time.Time) int` — minute-of-day 0..1439.
- `expandClockMinutes(startMin, endMin int) []clockSegment` — handles cross-midnight.

Overlap detection algorithm:
1. For each window, compute its minute range as one or two `[startMin, endMin)` half-open segments.
2. Sort segments by start minute.
3. Scan sorted segments; if any segment's start < previous segment's end → overlap.

**Rationale**: Centralizing window types in `pkg/power/` keeps the domain model co-located with horizons. The `pkg/cmd/` package consumes these types without owning them.

### Step 2 — Create `pkg/power/window_test.go`

**New file.** Test cases:

| # | Case | Input | Expected |
|---|---|---|---|
| 1 | Expected: two non-overlapping windows | night 02:00–06:00, midday 12:00–15:00 | ValidateWindows returns nil; ResolveActiveWindow picks night at 04:00, midday at 13:00, nil at 09:00 |
| 2 | Expected: legacy synthesis | SyntheticLegacyWindow("02:00", "06:00", 3500, "default", "full_day") | Window with correct fields |
| 3 | Edge: cross-midnight + daytime | night 22:00–04:00, day 10:00–14:00 | no overlap; night active at 23:00 and 03:00, day at 12:00 |
| 4 | Edge: tick exactly at boundary | window 02:00–06:00 | active at 02:00 (inclusive), not active at 06:00 (exclusive, matching existing semantics) |
| 5 | Edge: adjacent windows (no gap) | 02:00–06:00, 06:00–10:00 | no overlap (end is exclusive) |
| 6 | Failure: overlapping windows | 02:00–06:00, 04:00–08:00 | ValidateWindows returns error containing "overlap" |
| 7 | Failure: cross-midnight overlap | 22:00–04:00, 03:00–08:00 | ValidateWindows returns error containing "overlap" |
| 8 | Failure: zero-width window | 02:00–02:00 | ValidateWindows returns error |
| 9 | Failure: invalid time format | start: "25:00" | ValidateWindows returns error |
| 10 | Failure: invalid horizon | forecast_horizon: "bogus" | ValidateWindows returns error |

Use `testify/assert` and `testify/require`. Mock time injection via `now` parameter to `ResolveActiveWindow`.

### Step 3 — Update `pkg/mqtt/types.go`

Add to `StatePayload`:

```go
ActiveWindow                *string  `json:"active_window,omitempty"`
ActiveWindowMaxCharge       *float64 `json:"active_window_max_charge,omitempty"`
ActiveWindowForecastHorizon *string  `json:"active_window_forecast_horizon,omitempty"`
```

**Rationale**: Pointerized so the JSON key is omitted entirely when no windows are configured (legacy mode). This avoids breaking existing MQTT consumers.

### Step 4 — Update `pkg/mqtt/discovery.go`

Add a new sensor entity in `BuildDiscovery`:

```go
entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "active_window",
    sensorPayload(base, deviceID, "active_window", "Active Window",
        "{{ value_json.active_window }}", "", "", "", "diagnostic"))
```

Also add `active_window_max_charge` and `active_window_forecast_horizon` as diagnostic sensors.

**Rationale**: Users can trigger automations based on which window is active. Diagnostic category keeps the default HA UI clean.

### Step 5 — Update `pkg/cmd/schedule.go`

**Add new CLI flags** in `registerScdCmd()`:

```go
scdCmd.Flags().StringSliceVar(&windowsCSV, "windows", nil,
    "Repeatable CSV charge windows: name:start-end:max_charge[:forecast_horizon[:consumption_horizon]]")
scdCmd.Flags().StringVar(&windowsJSON, "windows-json", "",
    "JSON array of charge window objects")
```

Bind env var: `viper.BindEnv("windows_json")` — but Viper's `AutomaticEnv()` handles `WINDOWS_JSON` automatically.

**Add new package-level vars**: `windowsCSV []string`, `windowsJSON string`.

**Update `checkScheduleschedule`** to:
1. Check for mixing of `windows:` (or CLI `--windows`/`--windows-json`) with explicit `start_hr`/`end_hr` → reject
2. If `windows:` present, parse and call `power.ValidateWindows()`
3. If `windows:` absent, legacy validation unchanged

**Add CSV parsing helper** (`parseWindowsCSV`) — parse `name:start-end:max_charge[:forecast_horizon[:consumption_horizon]]` into `power.Window`. Handle empty name (auto-generate "window-N").

**Add JSON parsing** via `encoding/json.Unmarshal` into `[]power.Window`.

**Wire into RunnerConfig**: add `Windows []power.Window` field, populate in `schedule` command `Run`.

### Step 6 — Update `pkg/cmd/schedule_runner.go`

**Modify `RunnerConfig`**: add `Windows []power.Window`.

**Modify `Runner.Tick()`**:

```go
// Resolve active window
activeWindow := power.ResolveActiveWindow(r.cfg.Windows, now)
effectiveMaxCharge := r.cfg.MaxCharge
effectiveForecastHorizon := r.cfg.ForecastHorizon
effectiveConsumptionHorizon := r.cfg.ConsumptionHorizon
var activeWindowName string

if activeWindow != nil {
    activeWindowName = activeWindow.Name
    effectiveMaxCharge = activeWindow.MaxCharge
    if activeWindow.ForecastHorizon != "" {
        effectiveForecastHorizon = activeWindow.ForecastHorizon
    }
    if activeWindow.ConsumptionHorizon != "" {
        effectiveConsumptionHorizon = activeWindow.ConsumptionHorizon
    }
}

// Replace inChargeWindow check:
inChargeWindow := activeWindow != nil

// Use effective* values in downstream calls
```

**Populate MQTT state payload** with active window fields:

```go
if activeWindow != nil {
    name := activeWindow.Name
    payload.ActiveWindow = &name
    maxCharge := activeWindow.MaxCharge
    payload.ActiveWindowMaxCharge = &maxCharge
    fh := effectiveForecastHorizon
    payload.ActiveWindowForecastHorizon = &fh
}
```

**Update `newCommandPayload`**: when windows are configured, use `power.ResolveActiveWindow` instead of `checkTimeRangeAt`.

### Step 7 — Remove duplicated clock helpers from `pkg/cmd/schedule.go`

After Step 1 moves clock helpers to `pkg/power/window.go`:
- Remove `clockSegment`, `expandClockWindow`, `clockMinute`, `segmentContains`, `isWindowContainedIn` from `pkg/cmd/schedule.go`
- Update existing call sites in `pkg/cmd/` to use `power.*` equivalents
- Keep `checkTimeRangeAt`, `parseScheduleClock`, `validateScheduleWindow` in `pkg/cmd/` — they serve the legacy code path and the batt_reserve window check

**Rationale**: `checkTimeRangeAt` is still needed for `batt_reserve_start_hr`/`batt_reserve_end_hr` which are not part of the multi-window feature. The clock helpers move to `pkg/power/` because they're domain logic shared by the new window resolver.

### Step 8 — Update `home-assistant/addons/sbam/config.json`

Add `windows` to both `"options"` (with an empty array default) and `"schema"` (as array-of-objects with the syntax from Section 5 above).

### Step 9 — Update `home-assistant/addons/sbam/run.sh`

Read from add-on options JSON and export `WINDOWS_JSON` env var.

### Step 10 — Update tests

- **`pkg/cmd/schedule_test.go`**: Add test cases for:
  - Two-window config selects correct window at different times
  - Legacy compatibility (no `windows:` → behavior unchanged)
  - Mixing `windows:` with `start_hr`/`end_hr` rejected
  - `--windows` CSV flag parsing
- **`pkg/cmd/schedule_mqtt_wiring_test.go`**: Verify active_window fields appear in state payload when windows are configured
- **`pkg/mqtt/discovery_test.go`**: Add test for new active_window discovery sensors

---

## 7. Test Plan

### New unit tests (`pkg/power/window_test.go`)

| # | Category | Description |
|---|---|---|
| 1 | Expected | Two non-overlapping windows → no validation error, correct resolution |
| 2 | Expected | Legacy synthesis produces correct single window |
| 3 | Edge | Cross-midnight + daytime windows coexist |
| 4 | Edge | Boundary ticks (exactly at start, exactly at end) |
| 5 | Edge | Adjacent non-overlapping windows (end == next start) |
| 6 | Edge | Empty name → auto-generated "window-1", "window-2" |
| 7 | Failure | Overlapping windows rejected |
| 8 | Failure | Cross-midnight overlap rejected |
| 9 | Failure | Zero-width window rejected |
| 10 | Failure | Invalid time format rejected |
| 11 | Failure | Invalid horizon rejected |
| 12 | Failure | Negative max_charge rejected |

### Modified tests (`pkg/cmd/schedule_test.go`)

Add at least:
- Multi-window tick: two windows, runner picks correct one with correct max_charge/horizons
- Legacy: no windows → behavior matches current

### Mock strategy

- `httptest.NewServer` for Solcast API mock (existing pattern in `pkg/power/power_test.go`)
- In-process MQTT broker for MQTT tests (existing pattern in `pkg/mqtt/mqtt_test.go`)
- Fake `froniusClient` / `storageClient` / `powerClient` for runner tests (existing pattern in `schedule_test.go`)

---

## 8. Validation Gates

```bash
make test          # all unit tests
make build         # compile check
go test ./pkg/power/ -run Window -v   # focused window tests
go test ./pkg/cmd/ -run Window -v     # focused command tests
go test ./pkg/mqtt/ -run Discovery -v # discovery tests
```

---

## 9. Rollout / Backward Compatibility

- **Default behavior unchanged**: configs without `windows:` work exactly as before
- **Mixing legacy + windows rejected at startup**: prevents silent misconfiguration
- **MQTT payload**: new keys are `omitempty` → absent from JSON in legacy mode, no breakage
- **HA add-on schema**: legacy `start_hr`/`end_hr`/`max_charge` remain in schema
- **Deprecation path (from epic #151)**: legacy keys removed in v3.0.0; `scheduler.mode` (#147) will introduce the formal deprecation logging

---

## 10. Security Considerations

- CSV/JSON input from CLI/env is untrusted → validate bounds, reject malformed input before any Modbus writes
- `max_charge` per window is validated against existing bounds (≥ 0)
- No new Modbus registers are written; the same `ForceCharge` path is used
- JSON unmarshalling uses `encoding/json` with strict struct tags

---

## 11. Gotchas

- **Cross-midnight overlap detection**: must expand each window into 1 or 2 segments then check all pairs. Simple linear scan over non-midnight windows won't catch `22:00–04:00` overlapping with `03:00–08:00`.
- **Viper `StringSlice` for `--windows`**: Viper's automatic env binding for `StringSlice` uses comma separation by default, which conflicts with CSV format. Use `StringArray` (space-separated) or parse manually from Viper string list.
- **HA add-on array-of-objects schema**: The Supervisor's voluptuous schema supports `[{...}]` syntax for arrays of objects. This is a valid pattern but the repo has no precedent — verify with `ha supervisor validate` or manual add-on install test.
- **`checkTimeRangeAt` uses inclusive bounds**: start ≤ now ≤ end. Cross-midnight: `now ≥ start OR now ≤ end`. Adjacent windows (e.g., `end` of window 1 = `start` of window 2) are non-overlapping because the end bound check uses `now.Before(endAt) || now.Equal(endAt)` (inclusive), but we define overlap as windows whose time ranges intersect at more than a single point.

---

## 12. Open Questions / Risks

| Question | Status |
|---|---|
| Horizon precedence (per-window vs top-level) | **RESOLVED**: per-window overrides top-level when set |
| Horizons dependency (#159) | **RESOLVED**: merged, types available in `pkg/power/horizon.go` |
| HA add-on nested-list schema syntax | **DEFERRED**: verify during implementation; fall back to manual test |
| CLI `--windows` CSV parsing with `:` delimiters | **RESOLVED**: hours/minutes are always 2-digit padded (`HH:MM`), so splitting on `:` is unambiguous when parsing `name:start-end:max_charge` because start-end uses `-` as delimiter |

---

## 13. Confidence Score

**9/10** — single-pass implementation success is likely because:
- The window resolution logic (cross-midnight, segment expansion) already exists and is tested in `pkg/cmd/`
- No new external dependencies
- Clear separation between legacy and new code paths
- Horizon types already validated and resolved by #159

The 1 point deduction is for the HA add-on schema syntax uncertainty (no prior art in this repo for array-of-objects). This is resolvable during implementation with a manual add-on test.
