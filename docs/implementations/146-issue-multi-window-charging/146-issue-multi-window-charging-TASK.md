# Feature: Multi-window charging schedule with per-window max_charge and horizon

> Slug: `146-issue-multi-window-charging` · Created: 2026-06-03
>
> Source issue: [#146](https://github.com/atbore-phx/sbam/issues/146)
> Fetched: 2026-06-03

## Summary

Replace the single `start_hr` / `end_hr` / `max_charge` triple with a list of configurable charge windows, each with its own `max_charge` and optional `forecast_horizon` / `consumption_horizon`. This allows users with multi-tariff import pricing to define multiple charge windows per day (e.g. 02:00–06:00 night and 12:00–15:00 midday) without running the binary multiple times or hand-rolling cron.

## Motivation / User Story

As an sbam user with multi-tariff import pricing,
I want to define multiple charge windows per day with independent `max_charge` and horizon,
so that I can favor cheap-grid charging at different times without running the binary multiple times or hand-rolling cron.

This replaces #62 and unblocks #48 (price-driven scheduling).

## Scope

- In scope:
  - New `windows:` list in `config.yaml` (and matching CLI/env/HA add-on options).
  - Per-window `name`, `start`, `end`, `max_charge`, optional `forecast_horizon`, optional `consumption_horizon`.
  - Validation: overlap detection, time format, max_charge bounds.
  - Engine that selects the active window for the current tick.
  - MQTT state payload exposes the active window id/label.
- Out of scope:
  - `scheduler.mode` selector (separate issue — this issue only defines the `windows:` schema and engine).
  - Auto / price-driven population of windows (separate issues).
  - Removing legacy `start_hr` / `end_hr` / `max_charge` — they remain as a compatibility shim mapped to a single implicit window.
  - Modbus charge-control register changes.

## Functional Requirements

- [x] `windows:` accepts a non-empty ordered list. Each entry has:
  - `name` (string, optional, used for MQTT/logs; auto-generated if absent, e.g. "window-1")
  - `start` (HH:MM, local)
  - `end` (HH:MM, local; cross-midnight allowed, same semantics as #116)
  - `max_charge` (W, required)
  - `forecast_horizon` (enum, optional; defaults to top-level `forecast_horizon`)
  - `consumption_horizon` (enum, optional; defaults to top-level `consumption_horizon`)
- [x] Window resolution per tick:
  - Exactly one window is active at a time. Validation rejects overlapping windows.
  - If no window is active, the runner behaves as it does today outside the charge window (no force-charge).
- [x] Legacy compatibility: if `windows:` is absent, sbam synthesizes a single window from `start_hr` / `end_hr` / `max_charge` (and inherits top-level horizons). Behavior matches today bit-for-bit.
- [x] CLI flag `--windows` accepts a YAML string (flow or block style). Env var `WINDOWS` accepts the same YAML format. All three surfaces (`config.yaml`, `--windows` flag, `WINDOWS` env var) share the same YAML schema with standard viper precedence (flag > env > yaml).
- [x] HA add-on: writes `config.yaml` from Supervisor `/data/options.json` at startup (JSON is valid YAML 1.2). Scalar options no longer use per-key env var exports.
- [x] MQTT state payload includes the active window `name` (or synthetic id) and its resolved `max_charge` / horizons.
- [x] Discovery payloads expose the active-window label as a sensor.

## Non-functional Requirements

- Backward compatibility:
  - Configs without `windows:` keep current behavior.
  - Mixing `windows:` with legacy `start_hr` / `end_hr` is rejected at startup with a clear error pointing at the chosen surface.
- Safety / defaults:
  - `max_charge` validation enforces existing bounds.
  - Overlap, inverted time, and zero-width windows are rejected.
- Performance / reliability:
  - O(n) window selection per tick over a small fixed list; no allocation in the hot path.
  - Deterministic, fully covered by unit tests including cross-midnight cases.

## Configuration Impact

- New CLI flag:
  - `--windows` (YAML string, flow or block style)
- New `config.yaml` keys:
  - `windows` (list of objects)
- New env var:
  - `WINDOWS` (YAML string — used by standalone Docker/CLI users; viper's `AutomaticEnv()` binds it to the `windows` key)
- Home Assistant add-on changes:
  - `run.sh` now generates `config.yaml` from `/data/options.json` at startup (JSON is valid YAML 1.2, viper reads it natively). Per-key env var exports replaced by single file copy.
  - MQTT autofill and RESET handling remain in `run.sh`.
  - New `windows` list option in `home-assistant/addons/sbam/config.json` schema.
  - Legacy `start_hr` / `end_hr` / `max_charge` kept for backward compatibility.

## External Integrations Touched

- Solcast: unchanged (horizon selection logic already exists from #159).
- Fronius Solar API: unchanged.
- Fronius Modbus registers: unchanged.
- MQTT topics: state payload gains `active_window`, `active_window_max_charge`, `active_window_forecast_horizon`.

## Acceptance Criteria

- [x] Configs without `windows:` behave identically to v2.1.
- [x] A two-window config (e.g. night + midday) selects the correct window across the day in unit tests.
- [x] Cross-midnight window remains correct when listed alongside a daytime window.
- [x] Overlap and invalid-time configs are rejected with actionable errors.
- [x] MQTT discovery exposes the active window as a sensor in Home Assistant.
- [x] README and add-on docs document the `windows:` schema with examples.

## Test Strategy

- Unit tests:
  - Expected case: two non-overlapping windows; resolver picks each at the correct time.
  - Expected case: legacy compatibility — no `windows:` → synthetic single window matches today's behavior.
  - Edge case: cross-midnight window + daytime window in the same config.
  - Edge case: tick exactly at boundary times (`start`, `end`).
  - Failure case: overlapping windows rejected at startup.
  - Failure case: mixing `windows:` with `start_hr`/`end_hr` rejected.
- Mocks: `httptest.NewServer` for Solcast/Fronius HTTP; `tbrandon/mbserver` for Modbus; in-process MQTT broker for MQTT tests.

## Risks / Open Questions

- Horizon precedence: per-window `forecast_horizon` / `consumption_horizon` overrides top-level. Top-level remains the default when per-window value is absent. (Resolved.)
- Horizons dependency: #159 is merged — `pkg/power/horizon.go` provides `ValidateForecastHorizon`, `ValidateConsumptionHorizon`, `ResolveForecastDay`, and `ResolveConsumption`. (Resolved.)
- HA add-on schema for nested lists: verified with `[{...}]` syntax in `config.json`. (Resolved.)
- CLI `--windows` YAML format: simplified from CSV/JSON to YAML-only on all three surfaces (`config.yaml`, `--windows` flag, `WINDOWS` env var). CSV parsing removed as over-engineered. (Resolved.)
- HA add-on config bridge: replaced per-key env var exports with `cp /data/options.json config.yaml` — JSON is valid YAML 1.2, viper reads it natively. (Resolved.)

## References

- Epic: [#151](https://github.com/atbore-phx/sbam/issues/151) — smart scheduler v2.x → v3.0.
  - #146 is v2.1.0 Foundation; #147 (`scheduler.mode` selector) follows; #149 (auto mode) in v2.2.0; #150 (price-driven) in v2.3.0.
  - v3.0.0 removes deprecated crontab mode and legacy single-window keys.
  - Crontab deprecation: v2.1 logging, v2.2 doc warnings, v3.0 removal.
- Replaces #62.
- Depends on #159 (horizons — merged).
- Unblocks #48 (price-driven scheduling).
- Related: cross-midnight window work (#115, #116).
