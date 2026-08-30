---
name: sbam
last_updated: 2026-08-30
---

# sbam Strategy

## Target problem

Fronius Gen24+ battery owners get a static time-of-day charging scheduler from their inverter — it charges the same amount every night regardless of tomorrow's solar forecast or today's consumption. Over-charging wastes money when the sun would have provided free energy; under-charging leaves them short during the day. The crux is that solar production is weather-dependent and household consumption varies daily, so a fixed schedule is the wrong tool for the job.

## Our approach

Optimize charging by combining forecast, consumption, and rate windows into a single decision — all signals the inverter ignores — and keep the system composable via MQTT so power users wire it into their own automations rather than accepting a vendor black box.

## Who it's for

**Primary:** Home users with a PV system and battery — they're hiring sbam to make charging decisions that account for weather, consumption, and grid rates without having to manually check battery levels and guess how much to charge each night.

## Key metrics

- **GitHub downloads** — adoption reach; measured via GitHub package statistics.
- **Active MQTT-connected instances** — retained users running with telemetry; measured via MQTT broker session tracking.
- **Release issues reported** — quality signal per release cycle; measured via GitHub issues.
- **Charge decision quality** — how often the chosen charge level matches what the solar day actually delivers; aspirational, not yet instrumented.
- **Electric bill impact** — the ultimate outcome; tracked by end users in Home Assistant, not yet aggregated.

## Tracks

### Charging Intelligence

The core decision engine: multi-signal optimization across forecast, consumption, SOC, and rate windows. Covers horizon modes, window resolution, dynamic consumption detection, and grid price automation.

*Why it serves the approach:* This is the "combine all the signals" half of the bet — making the charge decision smarter than what the inverter ships.

### Integration & Composability

MQTT telemetry, Home Assistant discovery, command topics, payload schemas. The surface that lets users treat sbam as a component in their own automations.

*Why it serves the approach:* This is the "stay composable" half of the bet — sbam is a decision engine users can wire, not a black box they must accept.

### Reliability & Day-Two Operations

Logging, error handling, runner lifecycle, debug UX, and operational safety. Making sbam trustworthy enough to run unattended on home infrastructure.

*Why it serves the approach:* A smart decision engine that crashes or misbehaves erodes the trust the approach depends on.

### Deployment Reach

Home Assistant add-on packaging, Docker image, standalone binary, documentation, and configuration ergonomics. Getting sbam onto more systems with less friction.

*Why it serves the approach:* The multi-signal + MQTT bet only matters if users can get sbam running in the first place.

## Milestones

- **v2.1.0 (Foundation)** — shipped; umbrella [#151](https://github.com/atbore-phx/sbam/issues/151). Sub-issues:
  - [#145](https://github.com/atbore-phx/sbam/issues/145) Configurable forecast/consumption horizons ✅
  - [#146](https://github.com/atbore-phx/sbam/issues/146) Multi-window charging schedule ✅
  - [#147](https://github.com/atbore-phx/sbam/issues/147) `scheduler.mode` selector with crontab deprecation path ✅
- **v2.2.0** — shipped; content arrived outside the original roadmap:
  - [#187](https://github.com/atbore-phx/sbam/issues/187) `to_next_window` consumption horizon ([#188](https://github.com/atbore-phx/sbam/pull/188)) ✅
  - [#194](https://github.com/atbore-phx/sbam/issues/194) Stable HA discovery device identity across `fronius_ip` changes ([#195](https://github.com/atbore-phx/sbam/pull/195)) ✅
- **v2.3.0 (Smart)** — next; umbrella [#151](https://github.com/atbore-phx/sbam/issues/151). Sub-issues:
  - [#148](https://github.com/atbore-phx/sbam/issues/148) Smart consumption from Fronius Solar API
  - [#149](https://github.com/atbore-phx/sbam/issues/149) `scheduler.mode: auto` — derive windows from forecast/consumption/SoC
- **v2.4.0 (Tariff-aware)** — umbrella [#151](https://github.com/atbore-phx/sbam/issues/151). Sub-issues:
  - [#150](https://github.com/atbore-phx/sbam/issues/150) Price-driven scheduler (Tibber / aWATTar / evcc / MQTT)
  - [#152](https://github.com/atbore-phx/sbam/issues/152) HA add-on ingress UI (placeholder)
- **v3.0.0 (Cleanup)** — not yet scoped (no issue); aggregates cleanup from [#151](https://github.com/atbore-phx/sbam/issues/151), [#150](https://github.com/atbore-phx/sbam/issues/150), [#152](https://github.com/atbore-phx/sbam/issues/152). Remove deprecated `crontab` mode and legacy single-window shim.

## Not working on

- Wall box / electric car charging integration.

## Marketing

**Key messages:**
- "Stop guessing. Forecast, consumption, and rates — all in one charging decision."
- "Open-source battery intelligence. Weather-driven, MQTT-wired, no black box."
