# Feature: Restore CLI flag precedence over env vars and config.yaml

> Slug: `68-issue-cli-flags-precedence` · Created: 2026-04-27

> Source issue: [#68](https://github.com/atbore-phx/sbam/issues/68)
> Fetched: 2026-04-27

## Summary
sbam currently ignores command-line flags when the same key is defined in `config.yaml`. The maintainer expects Viper's documented precedence: `flag > env > config > default`. The fix must restore that precedence for every subcommand (`estimate`, `schedule`, `configure`) without breaking existing flag names, env names or config keys.

## Motivation / User Story
As an operator running sbam from the CLI, I want `bin/sbam estimate --url http://override/` (or `--fronius_ip`, `--apikey`, `--defaults`, `--cache_*`, `--start_hr`, etc.) to actually take effect, so I can override config values for one-off runs and debugging without editing `config.yaml`. Today the override is silently discarded, which made the issue reporter chase a phantom Solcast 429 against the wrong URL.

## Scope
- In scope:
  - Fix CLI flag precedence for every viper-bound key in `pkg/cmd/{estimate,schedule,configure}.go`.
  - Add unit tests in `pkg/cmd/` exercising `flag > env > yaml > default` for at least one representative key per subcommand and for the keys shared across subcommands (`url`, `apikey`, `fronius_ip`, `defaults`, `cache_forecast`, `cache_file_prefix`, `cache_time`).
  - Extend `main_test.go` with at least one end-to-end CLI invocation that proves a flag overrides a value present in a temporary `config.yaml`.
  - Fix the latent bug in `pkg/cmd/configure.go` where `defaults` is bound to `scdCmd`'s flag (`viper.BindPFlag("defaults", scdCmd.Flags().Lookup("defaults"))`) instead of `cfgCmd`'s.
- Out of scope:
  - Renaming or removing any flag, env var, or config key.
  - Refactoring the package layout, the logging stack, or the cron scheduling.
  - Adding new features or new flags beyond what is needed to test the fix.
  - Modifying Modbus / Solcast / Fronius Solar API behavior.

## Functional Requirements
- A flag value explicitly passed on the command line MUST win over any value in env vars or `config.yaml`.
- An env var value MUST win over any value in `config.yaml`.
- When neither flag nor env nor yaml is set, the cobra default for the flag MUST apply.
- Behavior MUST be identical for `estimate`, `schedule`, and `configure`.
- Keys with the same name across subcommands (`url`, `apikey`, `fronius_ip`, `defaults`, `cache_forecast`, `cache_file_prefix`, `cache_time`) MUST resolve against the **currently executing** subcommand's flag set, not the last one initialized.

## Non-functional Requirements
- Backward compatibility: no flag rename, no env rename, no config key rename, no change to `home-assistant/addons/sbam/config.json` schema or `run.sh`.
- Safety / defaults: defaults that exist today (e.g. `crontab="0 0 0 0 0"`, `cache_time=7200`, `cache_file_prefix="cached_forecast"`, `defaults=true` for `schedule`, `defaults=false` for `configure`) MUST be preserved unchanged.
- Performance: no measurable runtime impact; the fix is purely about wiring order.
- Tests MUST be hermetic — no real Solcast/Fronius/Modbus calls. Use `httptest`, `mbserver`, and temp dirs / `t.Setenv`.

## Configuration Impact
- New CLI flags: none.
- New config keys (`config.yaml`): none.
- New env vars: none.
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): none.
- Documentation: `README.md` and `home-assistant/addons/sbam/DOCS.md` should explicitly state the precedence `flag > env > yaml > default` so users can rely on it.

## External Integrations Touched
- Solcast: none (tests use `httptest.NewServer` only when needed).
- Fronius Solar API: none.
- Fronius Modbus registers: none.

## Acceptance Criteria
- [ ] `bin/sbam estimate --url http://from-flag/ ...` uses `http://from-flag/` even when `config.yaml` defines `url:`.
- [ ] `bin/sbam schedule --fronius_ip 10.0.0.9 ...` uses `10.0.0.9` even when `config.yaml` defines a different `fronius_ip`.
- [ ] `bin/sbam configure --defaults` triggers Setdefaults regardless of the value in `config.yaml`.
- [ ] `URL=http://from-env/ bin/sbam estimate` uses `http://from-env/` when no `--url` flag is passed and the yaml has a different value.
- [ ] When neither flag, env, nor yaml is set, the cobra default applies.
- [ ] `viper.BindPFlag("defaults", ...)` in `configure.go` references `cfgCmd`, not `scdCmd`.
- [ ] `make test` passes; `make build` produces `bin/sbam`.
- [ ] New tests in `pkg/cmd/` and `main_test.go` cover expected, edge (env-only), and failure (flag wins over yaml that would otherwise cause an error) cases.

## Test Strategy
- Unit tests (`pkg/cmd/`):
  - Helper that constructs a cobra command tree, writes a temporary `config.yaml`, sets `t.Setenv`, parses args, and inspects the resolved viper values.
  - Cover `url`, `apikey`, `fronius_ip`, `defaults`, `cache_forecast`, `cache_file_prefix`, `cache_time`, `start_hr`, `end_hr`, `crontab` for the relevant subcommand(s).
  - Reset viper between tests (`viper.Reset()`) to avoid global-state leakage.
- End-to-end (`main_test.go`):
  - One test that writes a temp `config.yaml`, invokes `cmd.Execute()` with `os.Args` overriding one key, and asserts the resolved value via a deterministic, side-effect-free path (e.g. via the `--version` short-circuit + a tiny inspection helper, or by exporting a test-only accessor).
- Edge cases:
  - Flag absent, env present, yaml present → env wins.
  - Flag absent, env absent, yaml present → yaml wins.
  - Flag absent, env absent, yaml absent → cobra default wins.
  - Two subcommands defining the same key → the executing subcommand's flag is honored.
- Failure cases:
  - Required flag missing entirely → `CheckEstimate` / `checkScheduleschedule` / `checkConfigure` returns the existing error message.
  - Invalid `cache_time` passed via flag overrides a valid yaml value and triggers the existing validation error.
- Mocks:
  - `httptest.NewServer` if any test needs to reach a stub Solcast.
  - `tbrandon/mbserver` is **not** required (no Modbus path is being changed).
- `defer` cleanup:
  - `defer server.Close()` for any `httptest` server.
  - `defer viper.Reset()` and `defer func(){ os.Args = old }()` in tests that mutate global state.

## Risks / Open Questions
- Viper global state is shared across tests; parallel execution must be avoided in the new pkg/cmd tests (no `t.Parallel()`).
- The current code calls `viper.BindPFlag("defaults", scdCmd.Flags().Lookup("defaults"))` from inside `configure.go`'s `init()`. Init order across files is alphabetical (`configure.go` runs before `schedule.go`), so `scdCmd.Flags().Lookup("defaults")` may return `nil` at that moment, leaving the binding effectively unset — must be verified during implementation.
- Cobra `init()` runs once per process; tests that build a fresh command tree must not rely on `init()` re-running. The fix should move the per-command `viper.BindPFlag` calls to a place that runs every invocation (e.g. `PersistentPreRunE` or `PreRunE`), so tests can re-execute commands cleanly.

## References
- Issue: https://github.com/atbore-phx/sbam/issues/68
- Viper precedence: https://github.com/spf13/viper#why-viper
- Cobra `PreRunE` hooks: https://pkg.go.dev/github.com/spf13/cobra#Command
- Existing files:
  - [pkg/cmd/root.go](../../../pkg/cmd/root.go)
  - [pkg/cmd/estimate.go](../../../pkg/cmd/estimate.go)
  - [pkg/cmd/schedule.go](../../../pkg/cmd/schedule.go)
  - [pkg/cmd/configure.go](../../../pkg/cmd/configure.go)
  - [main_test.go](../../../main_test.go)

## Clarifications
> Captured 2026-04-27 from interactive interview:
> - Slug confirmed as `68-issue-cli-flags-precedence` (shortened from the full title).
> - Scope: fix applies to all three subcommands (`estimate`, `schedule`, `configure`).
> - Env var precedence (`flag > env > yaml`) must also be tested explicitly.
> - Backward compatibility: keep all current config keys, env names, and flag names unchanged.
> - Tests: add both `pkg/cmd/` unit tests AND end-to-end coverage in `main_test.go`.
> - Target release: `v1.6.0` (informational; no version files edited by this work).
