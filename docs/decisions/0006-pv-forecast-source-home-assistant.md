# ADR-0006: Selectable PV forecast source; Home Assistant reads outside optimize mode

| Field | Value |
|---|---|
| Status | Proposed |
| Date | 2026-10-26 |
| Related | SPEC-0003, `docs/design-reviews/2026-10-26-ha-solcast-forecast-source.md`, ADR-0001 |

## Context

sbam has always obtained PV forecasts by downloading them from the Solcast API (`pkg/power`). SPEC-0003 adds a second source: the HACS integration *Solcast PV Forecast*, which exposes a dampened, site-aggregated, half-hourly forecast as the `detailedForecast` attribute of HA sensor entities. The integration and sbam share the free-plan quota of 10 requests/day when both call Solcast.

Three existing decisions constrain the design:

- **ADR-0001** introduced `pkg/homeassistant` as a GET-only, transport-only client "used only in `scheduler_mode: optimize`" (also stated in `integration-contracts.md` and `data-classification.md`). The client returns raw HA values and contains no domain logic.
- **Package rules (CLAUDE.md):** business logic lives in `pkg/power`, `pkg/fronius` and `pkg/storage`. Decision logic must not depend on HTTP details.
- **Inverter safety:** a missing forecast must lead to the existing "no forecast" paths, never to a 0 Wh forecast treated as valid.

The PV forecast is consumed in three places with two different shapes. The classic runner and `estimate` use a daily Wh total via `power.Handler` → `GetDayPowerEstimate`. Optimize mode uses `[][]power.Forecast` + `power.ForecastMeta` via `forecastLoader` → `PVPeriodsFromSolcast` → `BucketPV`.

## Decision

We will:

1. **Add `pv_source`** (`solcast_api` default, `ha_solcast`) as a startup-time choice. `solcast_api` keeps the existing code path unchanged.
2. **Extend the use of the HA client** (ADR-0001) to every command that needs PV when `pv_source: ha_solcast`: `schedule` in all scheduler modes, `estimate` and `optimize`. Access resolution, hardening, limits and the GET-only rule stay as in ADR-0001. The only new call pattern is `GET /api/states/<entity>` for up to three configured `sensor.*` entities per run. `estimate` gains the `ha_url`/`ha_token` keys. ADR-0001's "used only in optimize mode" scope statement is superseded by this ADR. Its other decisions remain in force.
3. **Normalize at the edge into the existing `power.Forecast` shape.** `pkg/power` parses the integration's elements (`period_start`, `pv_estimate`) into `Forecast{PeriodEnd: period_start+30 min (UTC, RFC 3339), Period: "PT30M", PVEstimate}`. It merges today and tomorrow, deduplicating by instant. All downstream code (day total, PV periods, slot bucketing) is then reused unchanged.
4. **Keep transport and domain apart.** `pkg/power` defines a minimal `EntityReader` interface (`ReadEntity(ctx, id) (EntityState{State, Attributes}, error)`) and does not import `pkg/homeassistant`. `pkg/cmd` adapts the HA client to it. `pkg/homeassistant` stays unchanged and logic-free.
5. **Report degraded states uniformly.** The HA source reports `Missing` and `Stale` through `power.ForecastSourceMeta`, the same structure the Solcast cache path uses. Optimize mode thus reuses `optimizer_pv_forecast_missing`/`_stale` without change. Classic mode gets a source-neutral `pvDaySource` seam in `pkg/cmd` with `DayProduction(ctx, horizon, now) (wh, ok, err)`. On the HA path, a resolved day with no periods is "no forecast", never 0 Wh.
6. **No fallback between sources and no caching of HA reads.** A failed HA read degrades like a failed Solcast download. sbam never calls Solcast when `ha_solcast` is selected.

## Consequences

### Positive
- The integration's dampening is used without reimplementing it. Solcast quota is no longer shared.
- One parser and one merge serve all consumers. Equality with the Solcast path (SPEC-0003 AC-3/AC-4/AC-9) follows from reusing the same downstream functions.
- `pkg/homeassistant` and the Solcast code path are unchanged. The default configuration behaves exactly as before.
- A second source is a template for future sources (e.g. Forecast.Solar via HA) without touching consumers.

### Negative / trade-offs
- The HA token (Secret) is now used in classic modes and accepted on `estimate`. More deployments hold it, and `--ha_token` on the command line is visible in the process list. This needs a security review; the help text should prefer the env var.
- `integration-contracts.md` and `data-classification.md` must change ("HA used only in optimize mode" no longer holds; HA receives entity-state reads for PV in any mode).
- sbam depends on the integration's attribute format, which is not a formal API. Recorded fixtures in tests detect drift, and the user must keep the integration's half-hourly breakdown enabled.
- The HA path has a coverage rule that the Solcast path does not have, a small asymmetry kept to honour "default unchanged".

### Follow-ups
- Security review for SPEC-0003 (token scope extension, attribute validation bounds, `estimate` flag exposure).
- Update `integration-contracts.md` (HA row: PV forecast reads in all modes when `pv_source: ha_solcast`), `data-classification.md` (rule list) and ADR-0001's status line ("scope amended by ADR-0006").
- Record real HA states from the maintainer's installation as test fixtures (resolves SPEC-0003 open question 1).

## Alternatives Considered

### Alternative 1: Parse the forecast in `pkg/homeassistant`
Rejected. It puts Solcast semantics into the transport package, contrary to ADR-0001, and couples the HA client to forecast types.

### Alternative 2: Normalize to `optimize.PVPeriod` and add a separate classic day sum
Rejected. It needs a second day-assignment implementation, or a `pkg/power` → `pkg/optimize` dependency. AC-3/AC-9 equality would then have to be tested rather than guaranteed.

### Alternative 3: Feed the HA forecast through the Solcast cache file
Rejected. It reuses all existing code, but it violates SPEC-0003 AC-5 (no cache read or write), mixes data from two sources in one file, and hides staleness behind the cache's mtime.

### Alternative 4: Call the integration's `query_forecast_data` service
Rejected. It is a POST service call, excluded by ADR-0001 (GET-only) and by the SPEC-0003 non-goals.
