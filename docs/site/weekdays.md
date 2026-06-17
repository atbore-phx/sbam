# Weekday Filtering

Weekday filtering lets you assign each charge window to specific days of the
week. This is useful when your electricity rates vary between weekdays and
weekends, or when you have a special-rate day.

The feature is enabled by default via the `weekday_feature` flag. Set it to
`false` to disable all weekday logic without removing the `weekdays` fields
from your config.

## Quick Start

Add `weekdays` to any window in your `windows:` list:

```yaml
scheduler_mode: windows
weekday_feature: true
windows:
  - name: "weekday-offpeak"
    start: "21:00"
    end: "07:00"
    max_charge: 3500
    weekdays: "mon-fri"
```

With this config, the window is active Monday 21:00 through Tuesday 07:00,
Tuesday 21:00 through Wednesday 07:00, …, Friday 21:00 through Saturday
07:00. It is **not** active on Saturday or Sunday.

## Weekday Format

| Expression | Meaning |
|---|---|
| `mon` | Monday only |
| `mon,fri` | Monday and Friday |
| `mon-fri` | Monday through Friday (inclusive) |
| `mon-fri,sun` | Monday through Friday, plus Sunday |
| *(empty)* | Every day (backward compatible) |

Day names use lowercase 3-letter English abbreviations: `mon`, `tue`, `wed`,
`thu`, `fri`, `sat`, `sun`.

Ranges expand inclusively. Overlapping ranges are flattened: `mon-wed,tue-thu`
produces Monday, Tuesday, Wednesday, Thursday.

## The Start-Day Model

A window's weekday always refers to the **day the window starts**. This
matters for windows that cross midnight.

```
window: start "22:00", end "04:00", weekdays "fri"

Friday 22:00 ───┐
                │  WINDOW ACTIVE (start day is Friday)
Saturday 03:00 ─┘

Saturday 22:00 ─── NOT ACTIVE (start day is Saturday, not in set)
```

A Monday 23:00–03:00 with `weekdays: "mon"` is active Monday night/Tuesday
morning but **not** Tuesday night/Wednesday morning — because the Tuesday
night occurrence would have a Tuesday start day.

You never need to think about the "carry-over" day for the post-midnight
portion. The rule is: **the day the window opens determines the weekday.**

## Configuration Examples

### Weekday off-peak + weekend all-day off-peak

The beta tester's use case: cheaper off-peak rates Monday–Friday nights, and
all-day off-peak on weekends.

```yaml
windows:
  - name: "weekday-offpeak"
    start: "21:00"
    end: "07:00"
    max_charge: 3500
    weekdays: "mon-fri"
  - name: "weekend-offpeak"
    start: "00:00"
    end: "23:59"
    max_charge: 3500
    weekdays: "sat,sun"
```

### Same clock range, different max_charge by day

Charge at 3500 W on weekdays and 5000 W on weekends, using the same time
slot. Because the weekday sets are disjoint, validation does not flag an
overlap.

```yaml
windows:
  - name: "weekday-morning"
    start: "02:00"
    end: "05:00"
    max_charge: 3500
    weekdays: "mon-fri"
  - name: "weekend-morning"
    start: "02:00"
    end: "05:00"
    max_charge: 5000
    weekdays: "sat,sun"
```

### Single-day free-power window

Your provider gives free power every Wednesday 12:00–15:00.

```yaml
windows:
  - name: "free-power-wed"
    start: "12:00"
    end: "15:00"
    max_charge: 5000
    weekdays: "wed"
  - name: "normal"
    start: "02:00"
    end: "05:00"
    max_charge: 3500
```

The `free-power-wed` window is invisible on every day except Wednesday.
On Wednesday it takes priority because windows are evaluated in list order
and it comes first.

### Mixed weekday/weekend with different horizon strategies

Forecast horizon `off` on weekends (no solar forecast) and `next_solar_day`
on weekdays.

```yaml
windows:
  - name: "weekday"
    start: "02:00"
    end: "05:00"
    max_charge: 3500
    forecast_horizon: "next_solar_day"
    weekdays: "mon-fri"
  - name: "weekend"
    start: "02:00"
    end: "05:00"
    max_charge: 5000
    forecast_horizon: "off"
    weekdays: "sat,sun"
```

### All windows share the same day filter

When every window in your list uses the same weekday pattern, the scheduler
only ticks on those days. Be aware that on excluded days no window is
active — the battery will not charge from the grid.

```yaml
windows:
  - name: "morning"
    start: "06:00"
    end: "12:00"
    max_charge: 2000
    weekdays: "mon-fri"
  - name: "afternoon"
    start: "12:01"
    end: "18:00"
    max_charge: 0
    weekdays: "mon-fri"
```

On Saturday and Sunday neither window activates. The scheduler runs but
reports "outside all configured charge windows."

## Feature Flag

| Key | Type | Default | Description |
|---|---|---|---|
| `weekday_feature` | bool | `true` | Enable weekday filtering |

When `weekday_feature` is `false`:
- All windows are active every day, regardless of their `weekdays` field
- Weekday token validation is skipped at startup
- Overlap detection ignores weekday sets

This is a maintainer kill switch. If you encounter a bug, set it to `false`
to disable weekday logic without editing your window definitions.

The flag is available as a CLI flag (`--weekday_feature=false`) and env var
(`WEEKDAY_FEATURE=false`). See the [CLI Reference](cli.md) for details.

## Validation Rules

- Unknown weekday tokens (`mon,xyz`) are rejected at startup
- Empty elements (`mon,,fri`) are rejected
- Day names are case-sensitive — `MON` and `Mon` are rejected; use `mon`
- Two windows with the same clock range but disjoint weekday sets (e.g.,
  `mon-fri` and `sat,sun`) are **not** flagged as overlapping
- Two windows with overlapping weekday sets AND overlapping clock ranges
  are rejected as normal
- A window with no `weekdays` (or an empty value) is treated as "all days"
  and always overlaps with other windows at the same clock range
