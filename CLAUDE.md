# Claude Code Instructions – sbam (Smart Battery Advanced Manager)

## Project Overview

This project is a **Go (Golang) application** that intelligently manages Fronius Gen24+ solar battery charging using weather forecasts.

The system:

- Retrieves daily solar production forecasts from the Solcast API
- Monitors battery state of charge via the Fronius local Solar API (REST)
- Controls battery charging via Fronius Modbus TCP protocol (register writes)
- Dynamically decides when and how much to charge the battery from the grid based on:
  - Weather forecasts (solar production estimates)
  - Daily household consumption
  - Current battery charge level
  - Configurable battery reserve thresholds
  - Time windows for grid charging
  - Net power calculations (solar production − consumption)

Deployment targets:

- Standalone CLI application
- Home Assistant add-on
- Docker container

The application must be:

- Modular
- Testable
- Secure
- Idiomatic Go
- Deployable as a Home Assistant add-on and Docker container

---

## Tech Stack

- Go 1.26+
- net/http (default HTTP client for Solcast and Fronius Solar API)
- github.com/simonvetter/modbus (Modbus TCP client for Fronius inverter control)
- github.com/spf13/cobra (CLI framework)
- github.com/spf13/viper (configuration: YAML, env vars, CLI flags)
- go.uber.org/zap (structured logging)
- github.com/robfig/cron/v3 (cron scheduling for recurring charge cycles)
- github.com/stretchr/testify (testing assertions)
- github.com/tbrandon/mbserver (mock Modbus server for testing)
- Docker support (standalone + Home Assistant add-on)

---

## Project Structure

```
.claude/
.github/
  ISSUE_TEMPLATE/
    bug_report.yml            - GitHub issue form for bug reports
    feature_request.yml       - GitHub issue form for feature requests
    config.yml                - Issue template chooser policy (blank issues disabled)
  dependabot.yml              - Dependabot updates for Go modules, GitHub Actions, and Dockerfiles
  workflows/                  - CI/CD workflow definitions
    test.yml                  # Go tests on every push
    release.yml               # Tag-based release (HA add-on + GoReleaser binaries)
    dev-image.yml             # Dev Docker image on release branches
    docs-deploy.yml           # MkDocs build + GitHub Pages deploy (main) / preview artifact (branches)
  actions/
    build-ha-addon/           - Composite action: build Go binary + HA add-on Docker image

mkdocs.yml                    # MkDocs configuration (Material theme, GitHub Pages deploy)
docs/
  site/                         # MkDocs source — user-facing documentation
    index.md                    # Landing page
    prerequisites.md            # Inverter setup and Solcast prerequisites
    installation.md             # HA add-on, Docker, and standalone installation
    configuration.md            # Single config reference with deployment annotations
    mqtt.md                     # MQTT guide (topic map, payloads, commands, discovery)
    cli.md                      # Full CLI command and flag reference
    optimizer.md                # Cost-optimized dispatch (scheduler_mode: optimize): how it works, dry run, hardware checklist, fail-safes, reason codes
    changelog.md                # Release history
  vibe/                       - Contributor workflow docs for Compound Engineering skills
  brainstorms/                # CE brainstorm artifacts
  plans/                      # CE implementation plans
  implementations/            # Archived pre-CE implementation plans
  decisions/                  - ADRs (HA REST client, DP solver, dispatch register mapping, fail-safes, optimize-mode MQTT commands)

main.go                      # Entry point, version vars, delegates to pkg/cmd
main_test.go                 # CLI-level tests

pkg/
  cmd/
    root.go                   - Cobra root command, Viper config loading
    root_test.go              - Unit tests for the root command
    configure.go              - `configure` command: battery defaults & force charge
    configure_test.go         - Unit tests for the configure command
    sunspec.go                - `sunspec_type` option wiring shared by schedule and configure
    sunspec_test.go           - Unit tests for the sunspec_type option wiring
    reserve_hold_test.go      - Unit tests for reserve_horizon until_solar and hold_battery runner wiring
    hold_command_test.go      - Unit tests for the hold MQTT command and battery writer hold
    grid_limit.go             - grid_import_limit: charge allowance from the meter, 30 s limit loop, post-decision follow-up
    reserve.go                - Window reserve and hold, until_solar reserve, reserve-at-risk and reserve telemetry
    grid_limit_test.go        - Unit tests for grid_import_limit, the limit loop and window-end defaults while paused
    dst_test.go               - Unit tests for window/clock arithmetic across daylight-saving changes
    estimate.go               - `estimate` command: display forecast & battery state
    estimate_test.go          - Unit tests for the estimate command
    optimize.go               - `optimize` command: compute and print the cost-optimal plan once (always dry run; exit 0/1/2)
    optimize_test.go          - Unit tests for the optimize command (table golden, JSON, exit codes)
    optimize_config.go        - ha_*/opt_* flag registration, viper load and aggregated validation
    optimize_config_test.go   - Unit tests for optimize config parsing and validation
    optimize_types.go         - OptimizeConfig and optimizer run result types
    optimize_run.go           - One optimizer run: fetch inputs, build profile and grid, solve, map fallbacks
    optimize_run_test.go      - Unit tests for optimizer runs and fallback reason codes
    optimize_adapters.go      - Production adapters (HA client, Solcast forecast cache, battery reader) for optimizer runs
    schedule.go               - `schedule` command: main intelligent charging workflow
    schedule_runner.go        - Single-goroutine runner that serializes schedule ticks and command intents
    schedule_optimize.go      - Optimize-mode runner: hourly optimizer tick, dispatch, fail-safes, watchdog, optimize-mode commands
    schedule_optimize_test.go - Unit tests for the runner in scheduler_mode: optimize (hourly ticks, fail-safes, watchdog, commands)
    schedule_optimize_hold_test.go - Unit tests for hold in optimize mode, exhaustive intent handling and timer retries
    schedule_evguard.go       - EV guard for optimize mode: holds the battery in self_consume hours while the car charges
    schedule_evguard_test.go  - Unit tests for the EV guard
    legacy_additivity_test.go - Legacy-path tests for intentional shared changes (Solcast non-200 handling)
    schedule_runner_test.go   - Unit tests for runner command handling and queue behavior
    schedule_test.go           - Unit tests for the schedule command
    schedule_validation_test.go - Unit tests for schedule parameter validation
    schedule_mqtt_wiring_test.go - Unit tests for MQTT command subscription wiring and latest-state re-publish behavior
    schedule_lifecycle_test.go - Unit tests for runner lifecycle behavior in no-cron MQTT/no-MQTT modes
    schedule_cron_test.go     - Unit tests for cron-based scheduling
    precedence_test.go        - Unit tests for flag > env > yaml viper precedence
    config_schema_test.go     - Unit tests for HA add-on config.json schema regex validation
    testdata/                 - Golden files (optimize command table output)
  fronius/
    types.go                  - Fronius struct definitions
    handler.go                - Main battery control dispatcher
    modbus.go                 - Modbus TCP client (open, read, write, close)
    classify.go               - Battery charge classification logic
    classify_test.go          - Unit tests for charge classification
    configure.go              - Modbus register writing (SetDefaults, ForceCharge)
    schedule.go               - Battery charging algorithm
    error.go                  - Error handling utilities
    fronius_test.go           - Unit tests with mock Modbus server
    sunspec.go                - SunSpec model type selector, storage model discovery, inverter model type check
    sunspec_storage.go        - Battery writes dispatched by SunSpec type (legacy int+SF or discovered addresses)
    sunspec_test.go           - Unit tests for SunSpec discovery and dispatch with mock Modbus server
    hold.go                   - Battery hold write (discharge blocked) for hold_battery windows
    hold_test.go              - Unit tests for the battery hold write and hold decision path
    faults_test.go            - Unit tests for Modbus read/write fault paths with a fault-injecting mock inverter
    dispatch.go               - Optimizer action→register mapping, ordered writes with read-back, revert timeout; addresses per sunspec_type (ADR-0003)
    dispatch_test.go          - Register-level tests for dispatch actions with mock Modbus server
    dispatch_sunspec_test.go  - Dispatch tests on the float SunSpec layout (discovered addresses, no-write failures)
    error_test.go             - Error handling tests
  mqtt/
    types.go                  - MQTT config and payload carrier types
    client.go                 - MQTT client interface, factory, and topic helpers
    commands.go               - MQTT command topic parser, payload validation, and ack publisher
    discovery.go              - Home Assistant MQTT discovery payload builder
    identity.go               - Persisted discovery device identity (stable across fronius_ip changes)
    noop.go                   - Disabled MQTT client implementation
    paho.go                   - Paho-backed MQTT client with reconnect, TLS, and helpers
    init.go                   - Client initialization, non-blocking connect, cleanup
    publisher.go              - Typed state, error, and availability publishers
    mqtt_test.go              - In-process MQTT broker tests
    commands_test.go          - Unit tests for command parsing and ack publishing
    discovery_test.go         - Unit tests for Home Assistant discovery generation
    identity_test.go          - Unit tests for identity persistence, override, and fallback
    init_test.go              - Unit tests for client initialization and async connect
    intent_kinds_test.go      - Keeps IntentKinds complete against the declared intent kinds
    paho_currency_test.go     - Unit tests for Paho currency setter and discovery republish
    testdata/                 - Golden discovery, state and plan payloads
  homeassistant/
    types.go                  - GET-only Home Assistant REST client types (ADR-0001)
    access.go                 - HA access resolution (Supervisor token vs ha_url/ha_token), URL and entity ID validation
    access_test.go            - Unit tests for HA access resolution and validation
    client.go                 - States and history client with timeouts, body limits and no redirects
    client_test.go            - HTTP mock tests for the HA client
    testdata/                 - Recorded HA state and history fixtures
  optimize/
    reasons.go                - Package doc and stable optimizer reason codes
    types.go                  - Plan, grid, battery and parameter types; solver defaults
    input_types.go            - History sample and Solcast PV period input types
    inputs_validate.go        - Unit conversion and plausibility checks for HA power samples
    inputs_validate_test.go   - Unit tests for input validation
    consumption.go            - Hourly consumption profile from load/EV history with static fallback
    consumption_test.go       - Unit tests for the consumption profile
    price.go                  - Nordpool price parsing, hourly means, buy/sell derivation
    price_test.go             - Unit tests for price parsing and buy/sell
    pv.go                     - Solcast PV periods to hourly slot energy
    pv_test.go                - Unit tests for PV slotting
    grid.go                   - Hourly slot and grid construction (DST-aware)
    grid_test.go              - Unit tests for slots and grid
    solver.go                 - Backward dynamic-programming solver (ADR-0002)
    label.go                  - Action labelling and deterministic reason classification
    label_test.go             - Unit tests for labelling
    simulate.go               - Independent plan re-evaluation
    validate.go               - Grid and plan invariant checks
    validate_test.go          - Unit tests for grid and plan validation
    solver_test.go            - Unit tests for the solver
    bruteforce_test.go        - Brute-force reference comparison for the solver
    race_on_test.go           - Race-detector build flag for timing tests
    race_off_test.go          - Race-detector build flag for timing tests
    testdata/                 - Recorded Nordpool price fixture
  power/
    types.go                  - Solcast forecast struct definitions
    handler.go                - Get daily solar production estimate
    horizon.go                - Forecast and consumption horizon types, validation, resolution
    horizon_test.go           - Unit tests for horizon resolution
    window.go                 - Charge window types, validation, active-window resolution
    window_test.go            - Unit tests for window validation and resolution
    reserve.go                - Reserve horizon (until_solar): morning gap from 30-minute forecast periods
    reserve_test.go           - Unit tests for the until_solar reserve calculation
    estimate.go               - Forecast retrieval, caching, power calculations
    power_test.go             - HTTP mock tests for Solcast API
    forecasts.go              - Per-URL Solcast forecasts via the cache with age metadata (optimizer)
    forecasts_test.go         - Unit tests for forecast cache loading
  storage/
    types.go                  - Fronius battery JSON response structs
    handler.go                - Main handler fetching battery data
    charge.go                 - Parse battery capacity & charge state
    storage_test.go           - Unit tests
    powerflow.go              - Solar API power flow (grid and battery power) for grid_import_limit
    powerflow_test.go         - HTTP mock tests for the power flow reader

src/
  utils/
    log.go                    - Centralized zap logger initialization
    error.go                  - Error handling helpers (HandleError, HandleErrorPanic)
    error_test.go             - Unit tests for error handling helpers
    startup.go                - Startup parameters dump (DumpStartupParams, SecretKeys)
    startup_test.go           - Unit tests for the startup dump helper

config.yaml                  - Configuration file
Makefile                     - Build targets (test, build, test-build)
Dockerfile                   - Standalone container image
home-assistant/addons/sbam/  - Home Assistant add-on files (config.yaml, translations/en.yaml with option labels for the UI editor)
```

Important:

If you add, remove, rename, or move files or directories in this repository, update the "Project Structure" list above to reflect those changes.

Do not track:
- docs/plans/ in the project structure as these are generated and managed by the Compound Engineering workflow.

Rules:

- Business logic lives in pkg/ packages (fronius, power, storage)
- CLI command definitions live in pkg/cmd/
- Reusable utilities live in src/utils/
- No circular dependencies
- Charging logic must not depend directly on HTTP or Modbus implementation details

---

## Coding Standards

### Idiomatic Go

- Always return (value, error)
- Never panic in business logic
- Use context.Context in all external calls
- Use interfaces for abstractions
- Keep functions small and focused
- Avoid global state where possible

### Constructor Pattern

- Use `New()` factory functions returning `*TypeName` for package types

### Error Handling

- Always check and handle errors
- Use `handleError()` for non-fatal errors (log + return)
- Reserve panics strictly for unrecoverable initialization failures

### Testing

- Always create unit tests for components you introduce into the application
- After changing or introducing new logic make sure that the tests are updated to match the new situation
- Use `testify` for assertions (`assert.NoError`, `assert.Equal`, `assert.Contains`, `assert.Error`)
- Use `httptest.NewServer` for mocking HTTP APIs (Solcast, Fronius Solar API)
- Use `tbrandon/mbserver` for mocking Modbus TCP servers (Fronius Modbus)
- Include at least the following types of test cases:
  - 1 test case for the expected use
  - 1 edge case
  - 1 failure case
- Always `defer server.Close()` for mock servers

### Configuration

- Configuration hierarchy: CLI flags > environment variables > config.yaml
- All config is managed via Viper with `AutomaticEnv()` and cobra flag binding
- Key parameters: `url`, `apikey`, `fronius_ip`, `pw_consumption`, `start_hr`, `end_hr`, `crontab`, `pw_batt_reserve`, `max_charge`, `pw_lwt`, `pw_upt`, `mqtt_enabled`, `mqtt_broker`, `mqtt_client_id`, `mqtt_username`, `mqtt_password`, `mqtt_topic_prefix`, `mqtt_ha_discovery`, `mqtt_ha_discovery_prefix`, `scheduler_mode`, `sunspec_type`, `ha_url`, `ha_token`, `opt_price_entity`, `opt_consumption_entity`, `opt_ev_entity`, `opt_ev_guard_w`, `opt_history_days`, `opt_battery_efficiency`, `opt_max_charge_rate_w`, `opt_max_discharge_rate_w`, `opt_min_soc_pct`, `opt_vat_rate`, `opt_sell_margin`, `opt_min_gain`, `opt_wear_cost`, `opt_purchase_surcharge`, `opt_dry_run`

### Build

- Binary built with `CGO_ENABLED=0` for portability
- Version injected via `-ldflags` from git tags at build time
- Build output: `bin/sbam`

---

## Compound Engineering Workflow

This project uses Compound Engineering skills for feature development. The workflow is anchored by `STRATEGY.md` (product direction) and `CLAUDE.md` (coding standards).

### Available skills

| Skill | Purpose | Old prompt equivalent |
|---|---|---|
| `/ce-strategy` | Create or update product strategy (`STRATEGY.md`) | (none) |
| `/ce-brainstorm` | Explore requirements and define what to build | `/generate-plan-local` (without an issue) |
| `/ce-plan` | Create a technical implementation plan | `/generate-plan-from-issue` or `/generate-plan-local` |
| `/ce-work` | Execute the plan and implement changes | `/implement-plan` |
| `/ce-code-review` | Structured code review before merging | (none) |
| `/ce-commit-push-pr` | Commit, push, and open a pull request | `/create-pr` |

Plans are written to `docs/plans/YYYY-MM-DD-NNN-<type>-<name>-plan.md`. Historical TASK+PLAN files from the pre-CE prompt workflow live under `docs/implementations/archive/`.

### Workflow

1. Optionally run `/ce-strategy` to ground product direction.
2. For new ideas, start with `/ce-brainstorm` to define requirements, then `/ce-plan` to produce a technical plan.
3. For work scoped in an existing GitHub issue, run `/ce-plan` directly from the issue.
4. Execute the plan with `/ce-work <plan-path>`.
5. Add or update unit and integration tests as appropriate.
6. Run validation: `make test`, `make build`, `make all` to ensure everything works locally.
7. Run `/ce-code-review` to review changes before opening a PR.
8. Ship with `/ce-commit-push-pr` — opens a draft PR with a value-first description.
9. Request at least one human reviewer and ensure CI passes before converting from draft.

### Guardrails

- **Format:** run `make fmt` to format Go code.
- **Tidy:** run `make tidy` to clean up module dependencies.
- **Vet:** run `make vet` to catch suspicious constructs.
- **Test:** run `make test` locally; the PR must pass CI tests.
- **Build:** run `make build` to verify the binary compiles.
- **All:** run `make all` to execute the full pipeline (fmt → tidy → vet → test → build).
- **Review:** at least one reviewer must sign off on generated code.
- **Secrets:** never commit real API keys or credentials; use placeholders.
- **Scope:** keep each generated PR small and focused.

---

## Additional Instructions

- If I tell you that you are wrong, please check and think about whether or not you think that's true and respond with facts.
- Avoid apologizing or making conciliatory statements.
- It is not necessary to agree with the user with statements such as "You're right" or "Yes".
- Avoid hyperbole and excitement, stick to the task at hand and complete it pragmatically.
- Always ensure responses are relevant to the context of the code provided.
- Avoid unnecessary detail not related to the task.
- when you use gh tool --repo flag, use "atbore-phx/sbam".
