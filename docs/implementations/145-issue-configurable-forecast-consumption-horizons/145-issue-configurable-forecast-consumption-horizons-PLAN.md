# Implementation Plan: Configurable Forecast and Consumption Horizons

> **Plan date**: 2026-06-01
> **TASK**: [145-issue-configurable-forecast-consumption-horizons-TASK.md](145-issue-configurable-forecast-consumption-horizons-TASK.md)
> **Issue**: [#145](https://github.com/atbore-phx/sbam/issues/145)
> **Epic**: [#151](https://github.com/atbore-phx/sbam/issues/151)

---

## 1. Task Analysis

### Goals

Replace the hardcoded `CheckSun` noon-threshold logic (`pkg/power/estimate.go:152–160`) with explicit, configurable `forecast_horizon` and `consumption_horizon` settings. Users can now name the exact forecast window and consumption model they want per use case (night charging, daylight cheap-tariff, reserve-only).

### Non-goals (out of scope)

- Multi-window scheduling, `scheduler.mode` selector, Fronius-based dynamic consumption, auto mode, Modbus register changes.
- `auto` and `custom` horizon modes (future issues).

### Acceptance Criteria (from TASK)

1. Night charging unchanged under `forecast_horizon=default`.
2. Cross-midnight windows continue targeting the upcoming solar day.
3. All modes (`default`, `next_solar_day`, `remaining_today`, `today`, `tomorrow`, `off`) are tested.
4. `consumption_horizon=remaining_today` produces deterministic proportional value from `pw_consumption`.
5. Invalid horizon config rejected at startup with actionable error.
6. README and HA add-on docs explain the modes.
7. MQTT state payload includes `forecast_horizon` and `consumption_horizon` fields.
8. `off` mode skips Solcast API calls, sets forecast to 0, but battery reads and reserve charging still operate.

---

## 2. Current State

### `CheckSun` — the hardcoded noon threshold

`pkg/power/estimate.go:152–160`:
```go
func CheckSun(now time.Time) time.Time {
    switch time := now; {
    case time.Hour() < 12:
        return now
    default:
        return now.AddDate(0, 0, 1)
    }
}
```

Called once per `Handler` invocation at `pkg/power/handler.go:30`:
```go
day := CheckSun(time.Now())
```

This determines which calendar day's Solcast intervals are summed in `GetTotalDayPowerEstimate`.

### Data flow (schedule tick)

```
Runner.Tick()
  ├─ checkTimeRangeAt(now, startHR, endHR)          → inChargeWindow
  ├─ checkTimeRangeAt(now, brStartHR, brEndHR)      → reserveWindowActive
  ├─ storage.Handler(froniusIP)                     → capacityToCharge, capacityMax, socPct
  ├─ power.Handler(apiKey, url, ...)                 → solarPowerProduction, forecastRetrieved
  │    └─ CheckSun(now) → day
  │    └─ GetTotalDayPowerEstimate(forecasts, day)  → dailyProduction (Wh)
  ├─ fronius.Handler(solarPowerProduction, ...)      → chargePct, decision, reason, powerState
  │    └─ ClassifyDecision(pwForecast, pwConsumption, ...)
  └─ publishState(payload)
```

### Key observation

`pwConsumption` (static `pw_consumption` config value) flows directly into `ClassifyDecision` without any time-of-day adjustment. The consumption horizon operates on this value before it reaches the classifier.

---

## 3. Target Architecture

### New package: horizon types in `pkg/power/`

Two new string-enum types with validation and resolution logic live in `pkg/power/horizon.go`:

```
ForecastHorizon: "default" | "next_solar_day" | "remaining_today" | "today" | "tomorrow" | "off"
ConsumptionHorizon: "full_day" | "remaining_today"
```

### Modified data flow

```
Runner.Tick()
  ├─ resolveForecastHorizon(cfg.ForecastHorizon) → skip Solcast if "off"
  ├─ resolveConsumption(cfg.PWConsumption, cfg.ConsumptionHorizon, now) → effectiveConsumption
  ├─ power.Handler(..., forecastHorizon, now)     → solarPowerProduction, forecastRetrieved
  │    └─ resolveForecastDay(horizon, now) → target day + optional after-filter
  │    └─ GetTotalDayPowerEstimate or GetRemainingDayPowerEstimate
  ├─ fronius.Handler(solarPowerProduction, effectiveConsumption, ...)
  │    └─ ClassifyDecision(pwForecast, effectiveConsumption, ...)
  └─ publishState(payload with forecast_horizon + consumption_horizon)
```

### Horizon semantics

| Mode | Target day | Filter |
|------|-----------|--------|
| `default` | before 12:00 → today; after 12:00 → tomorrow | none (all periods) |
| `next_solar_day` | same as `default` | none |
| `remaining_today` | today | only periods with `period_end > now` |
| `today` | today | none |
| `tomorrow` | tomorrow | none |
| `off` | N/A (skip Solcast) | N/A |

| Consumption mode | Effective value |
|-----------------|----------------|
| `full_day` | `pw_consumption` (unchanged) |
| `remaining_today` | `pw_consumption × (seconds_remaining / 86400)` |

---

## 4. Dependency Choices

No new Go modules. All changes use the existing stack:

- `time` (stdlib) — local-day arithmetic, period-end parsing
- `github.com/spf13/cobra` + `github.com/spf13/viper` — CLI flags, env vars, config binding
- `github.com/stretchr/testify` — assertions

---

## 5. Configuration Changes

### CLI flags (registered in `pkg/cmd/schedule.go:registerScdCmd`)

```
--forecast_horizon     string   "default"   // FORECAST_HORIZON
--consumption_horizon  string   "full_day"  // CONSUMPTION_HORIZON
```

### `config.yaml` keys

```yaml
forecast_horizon: "default"
consumption_horizon: "full_day"
```

### Env vars

```
FORECAST_HORIZON
CONSUMPTION_HORIZON
```

### Home Assistant add-on schema (`config.json`)

```json
"forecast_horizon": "list(default|next_solar_day|remaining_today|today|tomorrow|off)",
"consumption_horizon": "list(full_day|remaining_today)"
```

### HA add-on `run.sh` exports

```bash
export FORECAST_HORIZON=$(bashio::config 'forecast_horizon')
export CONSUMPTION_HORIZON=$(bashio::config 'consumption_horizon')
```

### Precedence (standard Viper)

flag > env var > config.yaml > default

---

## 6. Implementation Blueprint

### Step 1 — New file: `pkg/power/horizon.go`

Define the two horizon types, constants, validation, and resolution functions.

**Public API:**

```go
type ForecastHorizon string

const (
    ForecastHorizonDefault        ForecastHorizon = "default"
    ForecastHorizonNextSolarDay   ForecastHorizon = "next_solar_day"
    ForecastHorizonRemainingToday ForecastHorizon = "remaining_today"
    ForecastHorizonToday          ForecastHorizon = "today"
    ForecastHorizonTomorrow       ForecastHorizon = "tomorrow"
    ForecastHorizonOff            ForecastHorizon = "off"
)

// ValidateForecastHorizon returns an error if the string is not a known mode.
func ValidateForecastHorizon(s string) (ForecastHorizon, error)

// ResolveForecastDay returns the target calendar day for forecast summation
// and an optional "after" time filter (used by remaining_today).
// Returns (day, after, skipForecast).
func ResolveForecastDay(h ForecastHorizon, now time.Time) (day time.Time, after *time.Time, skip bool)
```

```go
type ConsumptionHorizon string

const (
    ConsumptionHorizonFullDay        ConsumptionHorizon = "full_day"
    ConsumptionHorizonRemainingToday ConsumptionHorizon = "remaining_today"
)

// ValidateConsumptionHorizon returns an error if the string is not a known mode.
func ValidateConsumptionHorizon(s string) (ConsumptionHorizon, error)

// ResolveConsumption returns the effective consumption value for the given
// horizon and static daily consumption. For full_day it returns pwConsumption
// unchanged. For remaining_today it returns a proportional value based on
// seconds remaining in the local day.
func ResolveConsumption(h ConsumptionHorizon, pwConsumption float64, now time.Time) float64
```

**Rationale:** Types, validation, and resolution are co-located in `pkg/power/` because they are tightly coupled to forecast semantics. Validation functions are separate from resolution so the schedule command can validate at startup before any tick runs.

**`ResolveForecastDay` logic:**

| Horizon | day | after | skip |
|---------|-----|-------|------|
| `default` | `CheckSun(now)` | nil | false |
| `next_solar_day` | `CheckSun(now)` | nil | false |
| `remaining_today` | truncated `now` (local date) | `&now` | false |
| `today` | truncated `now` (local date) | nil | false |
| `tomorrow` | truncated `now` + 1 day | nil | false |
| `off` | zero Time | nil | true |

`CheckSun` is preserved as a private helper since `default` and `next_solar_day` share its logic.

**`ResolveConsumption` logic:**

- `full_day` → return `pwConsumption`
- `remaining_today` → compute `secondsRemaining / 86400 * pwConsumption`
  - `secondsRemaining = 86400 - (now.Hour()*3600 + now.Minute()*60 + now.Second())`
  - Clamp to `[0, 86400]`

### Step 2 — New file: `pkg/power/horizon_test.go`

Test cases:

| Test | Category |
|------|----------|
| `default` before noon returns today | expected |
| `default` after noon returns tomorrow | expected |
| `default` at exactly 12:00:00 returns tomorrow | edge |
| `next_solar_day` mirrors `default` | expected |
| `remaining_today` returns today with after=now | expected |
| `remaining_today` near midnight — after filter catches last intervals | edge |
| `today` returns today with nil after | expected |
| `tomorrow` returns tomorrow with nil after | expected |
| `off` returns skip=true | expected |
| Invalid forecast horizon string → error | failure |
| Invalid consumption horizon string → error | failure |
| `ResolveConsumption` full_day returns unchanged value | expected |
| `ResolveConsumption` remaining_today at start of day ≈ full value | edge |
| `ResolveConsumption` remaining_today at end of day ≈ 0 | edge |
| `ResolveConsumption` remaining_today clamps negative (after midnight wrap) | edge |

### Step 3 — Modify: `pkg/power/handler.go`

**Changes:**

1. Add `ForecastHorizon` and `now time.Time` parameters to `Handler`.
2. At the top, call `ResolveForecastDay(h, now)`. If `skip` is true, return `(0, false, nil)`.
3. Pass the resolved `day` and `after` to a new or modified estimation function.

**New signature:**

```go
func (power *Power) Handler(apiKey string, urls string, cache_forecast bool,
    cache_file_prefix string, cache_time int32,
    forecastHorizon ForecastHorizon, now time.Time) (float64, bool, error)
```

**Internal logic change:**
Replace:
```go
day := CheckSun(time.Now())
```
With:
```go
day, after, skip := ResolveForecastDay(forecastHorizon, now)
if skip {
    return 0, false, nil
}
```

And for each URL, replace:
```go
dailyProduction, err := GetTotalDayPowerEstimate(forecasts, day)
```
With a new function that optionally applies the `after` filter:
```go
dailyProduction, err := GetDayPowerEstimate(forecasts, day, after)
```

### Step 4 — Modify: `pkg/power/estimate.go`

**New function: `GetDayPowerEstimate`** (replaces direct `GetTotalDayPowerEstimate` calls from the handler):

```go
// GetDayPowerEstimate sums PV estimates for periods on the given day.
// If after is non-nil, only periods with period_end strictly after `after`
// are included (used by remaining_today).
func GetDayPowerEstimate(forecasts Forecasts, day time.Time, after *time.Time) (float64, error)
```

**Refactor:** `GetTotalDayPowerEstimate` becomes a thin wrapper:
```go
func GetTotalDayPowerEstimate(forecasts Forecasts, day time.Time) (float64, error) {
    return GetDayPowerEstimate(forecasts, day, nil)
}
```

This preserves backward compatibility for any existing callers and tests.

**Logic for `GetDayPowerEstimate`:**
```
totalPower = 0
for each forecast:
    parse period_end
    if period_end.Year() == day.Year() && period_end.YearDay() == day.YearDay():
        if after == nil || period_end.After(*after):
            totalPower += pv_estimate * 0.5
return totalPower * 1000, nil
```

Note: `period_end` from Solcast is in RFC3339 (UTC). The day comparison uses `Year()` + `YearDay()` which is timezone-independent for UTC timestamps — but we compare against `day` which is in local time. The `ResolveForecastDay` returns local dates, so we need to convert `period_end` to local time before comparing, OR convert `day` to UTC.

**Decision:** Parse `period_end` as UTC (as it comes from Solcast), then convert to local time for day comparison. This matches the timezone-awareness from #115.

Actually — looking at the existing code more carefully:

```go
periodEnd, err := time.Parse(time.RFC3339, forecast.PeriodEnd)
// ...
if periodEnd.Year() == day.Year() && periodEnd.YearDay() == day.YearDay() {
```

`period_end` is parsed as RFC3339 (which includes timezone info). The `day` parameter comes from `CheckSun(now)` which returns local time. Since `YearDay()` returns the day-of-year in the value's location, and `periodEnd` after `time.Parse(RFC3339, ...)` is in UTC, this comparison would be wrong for non-UTC timezones!

Wait, this is actually a pre-existing issue. When `day` is in CET (UTC+1) and `periodEnd` is UTC, the `YearDay()` comparison shifts by one day near midnight. But this is the existing behavior — I should not fix pre-existing issues in this PR unless they block the feature.

Actually, looking more carefully at RFC3339 parsing in Go: `time.Parse(time.RFC3339, "2023-06-29T00:00:00Z")` produces a time in UTC (because of the `Z` suffix). The `Year()` and `YearDay()` methods return values in that time.Location. And `CheckSun` returns `now` which is local time.

So if it's 01:00 CET on June 29 (which is 00:00 UTC on June 29), `CheckSun` returns June 29 in CET. A UTC period ending at 2023-06-29T00:00:00Z has `YearDay()` matching June 29 in UTC. The comparison works by coincidence for CET because both are June 29. But for periods near UTC midnight, CET dates differ.

However, this is the existing behavior and not in scope to fix. I'll note it as a gotcha.

**For the new `remaining_today` after-filter**, I need to compare `period_end` (UTC) against `after` (local time provided by `now`). I'll convert `after` to UTC for the comparison, or convert `period_end` to local. Using local for consistency: convert `period_end` to `now.Location()` via `.In(loc)`.

Actually, the simplest approach: parse `period_end` as RFC3339 (it has timezone), then call `.In(day.Location())` to get local time, then do the Year/YearDay comparison in local time. This is the correct fix and aligns with #115's timezone awareness.

I should fix this in the new `GetDayPowerEstimate` since I'm touching this code anyway. `GetTotalDayPowerEstimate` can be updated to call the new function with `after=nil`, which also fixes the timezone issue for existing callers.

### Step 5 — Modify: `pkg/power/power_test.go`

Add tests for `GetDayPowerEstimate`:
- Expected: day match, no after filter → sums all periods
- Expected: day match with after filter → sums only periods after cutoff
- Edge: after filter exactly at a period_end boundary
- Edge: empty forecasts
- Failure: invalid period_end string

Add tests for `TestResolveForecastDay` (horizon → day resolution) — covered in horizon_test.go.

Update `TestHandler` and `TestHandlerCache` to pass the new parameters. Add a test for `forecast_horizon=off` returning `(0, false, nil)` without hitting the mock server.

### Step 6 — Modify: `pkg/cmd/schedule.go`

**New variable declarations** (add near L22–27):
```go
var forecast_horizon, consumption_horizon string
```

**New constants** (add near L40–45):
```go
const (
    const_forecast_horizon    = "default"
    const_consumption_horizon = "full_day"
)
```

**Read from viper** (in `scdCmd.Run`, add after existing viper reads):
```go
forecast_horizon = viper.GetString("forecast_horizon")
consumption_horizon = viper.GetString("consumption_horizon")
```

**Validation** (add to `checkScheduleschedule`):
```go
} else if _, err := pw.ValidateForecastHorizon(forecast_horizon); err != nil {
    return err
} else if _, err := pw.ValidateConsumptionHorizon(consumption_horizon); err != nil {
    return err
```

**Add to `RunnerConfig`** (in schedule_runner.go's `RunnerConfig` struct):
```go
ForecastHorizon    string
ConsumptionHorizon string
```

**Pass in `runnerCfg`** (in `scdCmd.Run`):
```go
runnerCfg := RunnerConfig{
    // ... existing fields ...
    ForecastHorizon:    forecast_horizon,
    ConsumptionHorizon: consumption_horizon,
}
```

**Register flags** (in `registerScdCmd`):
```go
scdCmd.Flags().StringVar(&forecast_horizon, "forecast_horizon", const_forecast_horizon, "Forecast horizon mode")
scdCmd.Flags().StringVar(&consumption_horizon, "consumption_horizon", const_consumption_horizon, "Consumption horizon mode")
```

### Step 7 — Modify: `pkg/cmd/schedule_runner.go`

**Update `RunnerConfig`** (add fields):
```go
ForecastHorizon    string
ConsumptionHorizon string
```

**Update `powerClient` interface** to match new Handler signature:
```go
type powerClient interface {
    Handler(apiKey string, url string, cache_forecast bool, cache_file_prefix string,
        cache_time int32, forecastHorizon pw.ForecastHorizon, now time.Time) (float64, bool, error)
}
```

Wait — this creates a circular dependency concern. `pkg/cmd` already imports `pw "sbam/pkg/power"`. So `pw.ForecastHorizon` is fine.

**Update `Runner.Tick()` — consumption computation:**

After line 225 (`r.cfg.PWConsumption`), compute effective consumption:
```go
effectiveConsumption := pw.ResolveConsumption(
    pw.ConsumptionHorizon(r.cfg.ConsumptionHorizon),
    r.cfg.PWConsumption,
    now,
)
```

**Update `Runner.Tick()` — forecast horizon handling:**

Replace the power handler call (L209–216) with:
```go
fh := pw.ForecastHorizon(r.cfg.ForecastHorizon)

var solarPowerProduction float64
var forecastRetrieved bool

if fh != pw.ForecastHorizonOff {
    powerHandler := newPower()
    solarPowerProduction, forecastRetrieved, forecastErr = powerHandler.Handler(
        r.cfg.APIKey, r.cfg.URL, r.cfg.CacheForecast,
        r.cfg.CacheFilePrefix, r.cfg.CacheTime,
        fh, now,
    )
    if forecastErr != nil {
        u.HandleError(forecastErr, "power forecast retrieval failed; disabling forecast for this run")
        r.publishError(ctx, "power", forecastErr)
        solarPowerProduction = 0.0
        forecastRetrieved = false
    }
}
```

**Update `froniusHandler.Handler` call** — pass `effectiveConsumption` instead of `r.cfg.PWConsumption`:
```go
chargePct, decision, reason, powerState, froniusErr := froniusHandler.Handler(
    solarPowerProduction,
    capacityToCharge,
    capacityMax,
    effectiveConsumption,  // was: r.cfg.PWConsumption
    // ... rest unchanged ...
)
```

**Update MQTT payload** — add horizon fields:
```go
payload := makeBasePayload(decision.String(), reason.String(), inChargeWindow, reserveWindowActive)
// ... existing fields ...
payload.ForecastHorizon = r.cfg.ForecastHorizon
payload.ConsumptionHorizon = r.cfg.ConsumptionHorizon
```

### Step 8 — Modify: `pkg/mqtt/types.go`

Add to `StatePayload`:
```go
ForecastHorizon    string `json:"forecast_horizon"`
ConsumptionHorizon string `json:"consumption_horizon"`
```

### Step 9 — Modify: `pkg/mqtt/discovery.go`

Add two diagnostic sensor entities for the horizon values:

```go
entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "forecast_horizon",
    sensorPayload(base, deviceID, "forecast_horizon", "Forecast Horizon",
        "{{ value_json.forecast_horizon }}", "", "", "", "diagnostic"))
entities = appendDiscoveryEntity(entities, discoveryPrefix, "sensor", "consumption_horizon",
    sensorPayload(base, deviceID, "consumption_horizon", "Consumption Horizon",
        "{{ value_json.consumption_horizon }}", "", "", "", "diagnostic"))
```

### Step 10 — Modify: `home-assistant/addons/sbam/config.json`

**Add to `options`:**
```json
"forecast_horizon": "default",
"consumption_horizon": "full_day"
```

**Add to `schema`:**
```json
"forecast_horizon": "list(default|next_solar_day|remaining_today|today|tomorrow|off)",
"consumption_horizon": "list(full_day|remaining_today)"
```

### Step 11 — Modify: `home-assistant/addons/sbam/run.sh`

Add exports:
```bash
export FORECAST_HORIZON=$(bashio::config 'forecast_horizon')
export CONSUMPTION_HORIZON=$(bashio::config 'consumption_horizon')
```

### Step 12 — Modify: `home-assistant/addons/sbam/CHANGELOG.md`

Add entry under `## Unreleased`:
```markdown
### Configurable forecast and consumption horizons

New `forecast_horizon` and `consumption_horizon` options replace the hardcoded noon-threshold forecast selection with explicit, named modes. See the README for details on each mode. Existing installations keep current behavior under `forecast_horizon=default` and `consumption_horizon=full_day`.
```

### Step 13 — Modify: `README.md`

Add a section documenting the new configuration options. Locate near the existing configuration documentation (or add after the existing flag descriptions if they exist in the README).

---

## 7. Test Plan

### Package `pkg/power` — `horizon_test.go` (new)

| # | Test | Type | Mocks |
|---|------|------|-------|
| 1 | `ValidateForecastHorizon("default")` → ok | expected | none |
| 2 | `ValidateForecastHorizon("next_solar_day")` → ok | expected | none |
| 3 | `ValidateForecastHorizon("remaining_today")` → ok | expected | none |
| 4 | `ValidateForecastHorizon("today")` → ok | expected | none |
| 5 | `ValidateForecastHorizon("tomorrow")` → ok | expected | none |
| 6 | `ValidateForecastHorizon("off")` → ok | expected | none |
| 7 | `ValidateForecastHorizon("bogus")` → error | failure | none |
| 8 | `ValidateConsumptionHorizon("full_day")` → ok | expected | none |
| 9 | `ValidateConsumptionHorizon("remaining_today")` → ok | expected | none |
| 10 | `ValidateConsumptionHorizon("bogus")` → error | failure | none |
| 11 | `ResolveForecastDay(default, 10:00)` → today, nil, false | expected | none |
| 12 | `ResolveForecastDay(default, 14:00)` → tomorrow, nil, false | expected | none |
| 13 | `ResolveForecastDay(default, 12:00)` → tomorrow, nil, false | edge | none |
| 14 | `ResolveForecastDay(remaining_today, now)` → today, &now, false | expected | none |
| 15 | `ResolveForecastDay(off, now)` → zero, nil, true | expected | none |
| 16 | `ResolveConsumption(full_day, 10000, now)` → 10000 | expected | none |
| 17 | `ResolveConsumption(remaining_today, 12000, noon)` → ~6000 | expected | none |
| 18 | `ResolveConsumption(remaining_today, 12000, 23:59:59)` → ~0 | edge | none |
| 19 | `ResolveConsumption(remaining_today, 12000, 00:00:00)` → 12000 | edge | none |

### Package `pkg/power` — `power_test.go` (modify)

| # | Test | Type | Mocks |
|---|------|------|-------|
| 1 | `GetDayPowerEstimate` all periods match day | expected | none |
| 2 | `GetDayPowerEstimate` with after filter excludes earlier periods | expected | none |
| 3 | `GetDayPowerEstimate` with after filter at exact boundary | edge | none |
| 4 | `GetDayPowerEstimate` invalid period_end → error | failure | none |
| 5 | `Handler` with `forecast_horizon=off` returns (0, false, nil) | expected | none (no HTTP) |
| 6 | `Handler` with `remaining_today` filters correctly | expected | `httptest.NewServer` |
| 7 | Update existing Handler tests for new signature | regression | `httptest.NewServer` |

### Package `pkg/cmd` — `schedule_test.go` / `schedule_runner_test.go` (modify)

| # | Test | Type | Mocks |
|---|------|------|-------|
| 1 | Invalid `forecast_horizon` rejected at validation | failure | none |
| 2 | Invalid `consumption_horizon` rejected at validation | failure | none |
| 3 | Default horizons pass validation | expected | none |
| 4 | Runner passes horizon values to MQTT payload | expected | fake clients |

### Package `pkg/mqtt` — `discovery_test.go` (modify)

Update tests to expect the two new diagnostic sensor entities.

### Cleanup

All mock servers use `defer server.Close()`.

---

## 8. Validation Gates

```bash
make fmt        # go fmt ./...
make vet        # go vet ./...
make test       # go test -cover -race ./...
make build      # CGO_ENABLED=0 go build -o bin/sbam
make all        # fmt + tidy + vet + test + build
```

Also run focused tests on affected packages:
```bash
go test -race ./pkg/power/...
go test -race ./pkg/cmd/...
go test -race ./pkg/mqtt/...
```

---

## 9. Rollout / Backward Compatibility

- **Default values**: `forecast_horizon=default`, `consumption_horizon=full_day` — identical to current behavior.
- **Existing configs**: No migration needed; new keys are optional and default to current behavior.
- **`default` mode**: Preserves `CheckSun` bit-for-bit (before noon → today, after noon → tomorrow).
- **`GetTotalDayPowerEstimate`**: Preserved as a wrapper around `GetDayPowerEstimate`, keeping existing tests passing.
- **HA add-on**: New options appear in the add-on config UI with defaults matching current behavior.
- **MQTT**: New fields are additive; existing consumers ignore unknown JSON keys.

---

## 10. Security Considerations

- No new secret handling — horizon values are plain strings with strict enum validation.
- Input validation: invalid horizon strings are rejected at startup before any Modbus or HTTP operations.
- `forecast_horizon=off` prevents all Solcast API calls, useful for air-gapped or rate-limited environments.

---

## 11. Gotchas

1. **Solcast `period_end` timezone**: Solcast returns UTC RFC3339 timestamps. `GetDayPowerEstimate` must compare against the local day after converting to the local timezone. The existing `GetTotalDayPowerEstimate` does `Year()`/`YearDay()` comparison directly on the parsed UTC time, which works by coincidence for most hours but may be off near UTC midnight. The new function converts to local time first.
2. **`CheckSun` must be preserved**: `default` and `next_solar_day` both use the existing noon threshold. Don't delete `CheckSun` — keep it as a private helper called by `ResolveForecastDay`.
3. **`remaining_today` after-filter**: The `after` parameter is compared against the parsed `period_end` using `time.After`. Ensure both are in the same timezone (convert `period_end` to local before comparing).
4. **Consumption remaining fraction**: Compute as `secondsRemaining / 86400`. At exactly 00:00:00, `secondsRemaining = 0` and consumption is 0. At 23:59:59, consumption is near 0. This is correct — more remaining time = more expected consumption.
5. **`off` mode still allows reserve charging**: The runner skips the power handler but still reads battery state and evaluates the charge window. Reserve charging via `ClassifyDecision` remains functional because `forecast_charge_enabled=false` and `batt_reserve_charge_enabled` depends only on the window.
6. **Interface change**: `powerClient` interface in `schedule_runner.go` changes signature. All implementations and mocks in tests must be updated.

---

## 12. Open Questions / Risks

| Question | Status |
|----------|--------|
| Timezone handling match with #115 | **RESOLVED** — `GetDayPowerEstimate` converts `period_end` to local time before day comparison, matching the local-time approach from #115. |
| MQTT field names for active horizon | **RESOLVED** — `forecast_horizon` and `consumption_horizon` directly in StatePayload. |

---

## 13. Confidence Score

**9/10** — The change is well-scoped to the power package and configuration wiring. The main risk is the interface change in `powerClient` requiring test mock updates across `schedule_runner_test.go`, but the pattern is mechanical. The `remaining_today` after-filter logic is the only new algorithm and is simple to get right with table-driven tests.

---

## 14. Revision History

| Date | Change |
|------|--------|
| 2026-06-01 | Initial PLAN from issue #145 |
