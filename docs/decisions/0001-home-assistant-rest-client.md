# ADR-0001: Home Assistant REST client package (`pkg/homeassistant`)

| Field | Value |
|---|---|
| Status | Accepted 2026-10-02 (security review incorporated) |
| Date | 2026-10-02 |
| Related | SPEC-0001, `docs/design-reviews/2026-10-02-cost-optimized-dispatch.md`, atbore-phx/sbam#148 (future reuse) |

## Context

SPEC-0001 (`scheduler_mode: optimize`) needs two things that only Home Assistant has:

- the Nordpool price sensor's current state and attributes (`raw_today`, `raw_tomorrow`, `tomorrow_valid`), read every hour;
- the recorder history of a load entity and an optional EV entity for the last `opt_history_days` days, used to build an hour-of-day consumption profile.

Until now sbam sent data only to Solcast, the inverter and the MQTT broker (`docs/guidelines/data-classification.md`). Home Assistant is therefore a **new outbound destination**, and it requires a bearer token (Secret tier). sbam runs in three deployment targets:

- HA add-on: the Supervisor injects `SUPERVISOR_TOKEN` when the manifest sets `homeassistant_api: true`, and Core is reachable at `http://supervisor/core/api`.
- Docker and the standalone CLI: no Supervisor. The user must supply a base URL and a long-lived access token.

MQTT cannot replace REST here. HA does not publish recorder history over MQTT, and mirroring the price sensor to MQTT would push an automation onto every user.

Constraints: standard library only (`docs/guidelines/supply-chain.md`), a `context.Context` and a timeout on every call, no secrets in logs or error strings, and transport kept out of the decision code (CLAUDE.md package rules).

## Decision

We will add a new transport-only package `pkg/homeassistant` built on `net/http`:

- `type Client interface { State(ctx, entityID) (State, error); History(ctx, entityID string, start, end time.Time) ([]HistoryPoint, error) }`, with the concrete `*HTTPClient` returned by `New(cfg Config) (*HTTPClient, error)`.
- **Access resolution** (`ResolveAccess(haURL, haToken string, getenv func(string) string) (Config, error)`):
  1. If `ha_url` is set, use `ha_url` + `/api` with `ha_token`. Explicit configuration wins, so an add-on user can point at a different HA.
  2. Otherwise, if `SUPERVISOR_TOKEN` is set, use `http://supervisor/core/api` with that token.
  3. Otherwise return an error. `scheduler_mode: optimize` fails at startup (AC-20).
- **Requests:**
  - `GET /api/states/<entity_id>`: 10 s timeout, 1 MiB response limit.
  - `GET /api/history/period/<start>?filter_entity_id=<id>&end_time=<end>&minimal_response&no_attributes&significant_changes_only=0`: 30 s timeout, 16 MiB response limit, at most one day per request. The caller loops over days.
  - The client streams responses through `io.LimitReader` into `encoding/json` and returns a typed error when a response exceeds the limit.
- **Hardening** (security review S-012 … S-015):
  - **GET-only, two paths.** The client has exactly two methods, which issue `GET /api/states/<id>` and `GET /api/history/period/<ts>`. It has no generic request API. URLs are built with `url.JoinPath` and `url.Values`.
  - **Entity IDs** are validated against `^[a-z0-9_]+\.[a-z0-9_]+$` at config load and again in the client.
  - **`ha_url`:** must be `http` or `https`, with no userinfo, query or fragment. A Warn is logged when `http://` is used with a host that is neither loopback nor private.
  - **Redirects are not followed:** `CheckRedirect` returns `http.ErrUseLastResponse`, and any 3xx is reported as an error. The token can never reach a second host. TLS verification is always on, and there is no skip option.
  - **Status before decode:** only `200` is decoded. `401`/`403` → `ErrAuth`, `404` → `ErrNotFound`, anything else → `ErrStatus`. Error strings carry the status code and path only, never the response body or headers.
  - **Supervisor token:** read with `os.Getenv("SUPERVISOR_TOKEN")` (injected as `getenv`), never through viper, so the startup dump can't show it. `ha_token` is added to `utils.SecretKeys`.
  - **Token placement:** the `Authorization` header is set in exactly one function. Requests and headers are never logged.
  - **One shared client:** a single `http.Client` per process, with per-request timeouts. Stream decoding runs through `io.LimitReader`, and exceeding the limit returns `ErrTooLarge`.
- **History is fetched at most once per local day per entity.** The cache, with a 26 h max age, lives in the runner (design review). Hourly runs only call `State`.
- The package returns raw HA values (state strings, attribute maps, timestamps). It contains no price or consumption logic: that lives in `pkg/optimize` as pure functions.

## Consequences

### Positive
- No new dependency. The package is small and fully testable with `httptest.NewServer`.
- Add-on users configure nothing (AC-21). Docker and CLI users get the same code path with two keys.
- The package can be reused later for #148 (smart consumption) and for other HA-sourced signals.

### Negative / trade-offs
- New Secret-tier credential and new outbound destination. These need a security review and a change to `data-classification.md`, whose rule list gains "Home Assistant (price/history read)".
- History payloads are large for high-frequency power sensors (≈ 5 MB per entity per 10 days at 10 s updates). This is mitigated by chunking per day and by rebuilding the profile only once per local day (see the design review).
- The HA REST API is not formally versioned. History query flags (`minimal_response`, `no_attributes`) have been stable since HA 2021.x, but tests use recorded payloads to detect drift.

### Follow-ups
- Security findings S-012, S-013, S-014 and S-015 are addressed by the hardening list above. S-017: update `data-classification.md` with HA history (Private) and `ha_token` (Secret).
- Update `docs/guidelines/data-classification.md` and `integration-contracts.md` (consumed systems table) in the implementation PR.
- Add the add-on manifest `homeassistant_api: true`.

## Alternatives Considered

### Alternative 1: HA WebSocket API
It supports `history/history_during_period` with server-side filtering and smaller payloads, but it needs a WebSocket library (a new dependency) or a hand-written client, plus connection lifecycle management. The benefit (smaller payloads) is already obtained by per-day chunking and daily caching. Rejected.

### Alternative 2: Read prices and consumption via MQTT (user-built HA automation publishing to sbam topics)
This avoids a token and a new destination, but recorder history is not available over MQTT. sbam would have to accumulate 10 days of history itself (persistence, cold start of 10 days), and every user would have to write an automation. It contradicts AC-1/AC-21 ("without user configuration"). Rejected.

### Alternative 3: Query the recorder database directly (SQLite / MariaDB)
This couples sbam to HA's internal schema, needs DB drivers (cgo for SQLite unless a pure-Go driver is added), and is not reachable from Docker/CLI deployments. Rejected.
