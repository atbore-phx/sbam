# Feature: Display effective startup parameters in debug logs

> Slug: `70-issue-display-startup-parameters` · Created: 2026-04-28
> Source issue: [#70](https://github.com/atbore-phx/sbam/issues/70)
> Fetched: 2026-04-28

## Summary
When sbam starts a subcommand, it should emit a single, well-formatted debug log
entry that lists every effective parameter the subcommand is going to operate
with, together with the source from which each value was resolved
(flag / env / yaml / default). Sensitive values (the Solcast `apikey`) must be
redacted. The dump must be produced for `schedule`, `configure` and `estimate`,
so that operators can disambiguate, from logs alone, exactly how a given run
was configured.

## Motivation / User Story
> "This would make it easier when perusing logs as there would be no ambiguity
> on which parameters were provided at the time of startup."
> — issue [#70](https://github.com/atbore-phx/sbam/issues/70)

Today the operator has to cross-reference CLI invocation, exported environment
variables, and the on-disk `config.yaml` (whose location may vary across the
standalone, Docker and Home Assistant deployments) to know what sbam is
actually using. Owner comment on the issue: *"we'll extend that for all
subcommands"*.

## Scope
- In scope:
  - All three subcommands: `schedule`, `configure`, `estimate`.
  - Logging the effective value of every parameter that the subcommand reads
    from viper (which already enforces the flag > env > yaml > default
    precedence).
  - Annotating each value with its resolution source.
  - Redacting `apikey`.
  - Implementing the dump as a single shared, reusable helper invoked by
    every subcommand. Any subcommand added in the future MUST be able to
    obtain the dump by calling that one helper — no bespoke, per-subcommand
    code path. The set of keys printed MUST be discovered automatically from
    viper / cobra state at call time, so adding a new flag does not require
    touching the helper.
- Out of scope:
  - Changing the resolution precedence (already covered by issue #68).
  - Persisting the dump to a file or shipping it anywhere outside zap.
  - A new top-level command (e.g. `sbam config print`).
  - Re-emitting the dump on each cron tick — the dump is a *startup* artifact.

## Functional Requirements
- FR1: Each subcommand MUST emit, immediately after parameter resolution and
  before any business logic / validation runs, a single multi-line debug log
  entry titled e.g. `effective startup parameters` that enumerates every
  configuration key the subcommand consumes.
- FR2: For every key the entry MUST show: the key name, the effective value as
  zap would render it, and the resolution source as one of
  `flag | env | yaml | default`.
- FR3: The `apikey` key MUST be redacted to the fixed literal `***` —
  regardless of the original value or its length. The same fixed `***` MUST
  be used for every key registered as a secret (see FR7).
- FR7: Secret keys MUST be declared in a single, shared registry in
  `src/utils` (e.g. `utils.SecretKeys`). Adding a new secret flag in the
  future MUST require only appending its key name to that registry — no
  changes to the dump helper or to any subcommand.
- FR4: The entry MUST be emitted at zap **debug** level only (i.e. only visible
  when `DEBUG=true`).
- FR5: The dump MUST run regardless of whether a `config.yaml` file exists.
- FR6: Pre-existing log lines, exit codes and validation behavior MUST NOT
  change.

## Non-functional Requirements
- Backward compatibility: no change to flags, env vars, config keys, or
  default behavior. No new dependencies.
- Safety / defaults: never log the raw `apikey`, even at debug level.
- Performance: O(number of keys) per startup; negligible.
- Idiomatic Go: the helper lives in `src/utils` (alongside the existing
  logger and error utilities) and is reused by all three subcommands. It
  takes a `*cobra.Command` (and optionally a `*viper.Viper`) and returns a
  formatted string; the secret registry is a package-level variable in the
  same package. No new global state beyond that registry.

## Configuration Impact
- New CLI flags: none.
- New config keys (`config.yaml`): none.
- New env vars: none (the existing `DEBUG=true` switch already gates the
  output).
- Home Assistant add-on schema changes
  (`home-assistant/addons/sbam/config.json`): none.

## External Integrations Touched
- Solcast: none.
- Fronius Solar API: none.
- Fronius Modbus registers: none.

## Acceptance Criteria
- [ ] Running `DEBUG=true sbam schedule …` prints a single multi-line debug
      entry that lists every key consumed by the schedule subcommand with its
      effective value and source label; `apikey` renders as the literal `***`.
- [ ] Running `DEBUG=true sbam configure …` prints the equivalent dump for the
      configure subcommand.
- [ ] Running `DEBUG=true sbam estimate …` prints the equivalent dump for the
      estimate subcommand.
- [ ] Without `DEBUG=true`, no parameter dump is emitted (and existing INFO/
      ERROR output is unchanged).
- [ ] The source column correctly reports `flag` when a flag was passed,
      `env` when only an env var was set, `yaml` when only the config file
      provided the value, and `default` otherwise — verified by unit tests
      that mirror [pkg/cmd/precedence_test.go](../../../pkg/cmd/precedence_test.go).
- [ ] The same shared helper is invoked by all three subcommands; no
      subcommand-specific dump code exists.
- [ ] Adding a new (non-secret) flag to any subcommand causes that flag to
      appear in the dump automatically, without editing the helper —
      demonstrated by a unit test that registers an ad-hoc flag and asserts
      it shows up.
- [ ] Existing test suite (`make test`) passes; new tests for the dump helper
      pass.

## Test Strategy
- Unit tests live next to the helper in `src/utils/` and cover:
  - Expected case: a fully-populated set of keys (mix of flag / env / yaml /
    default) produces a deterministic multi-line string with the correct
    source labels and the `apikey` value rendered as the literal `***`.
  - Edge case: numeric / bool keys at their zero value report source
    `default`; an unknown / unbound key is simply absent from the dump.
  - Failure case: a `*cobra.Command` with no flags and an empty viper still
    returns a non-empty (header-only) string and does not panic.
- Auto-discovery test: register a fresh ad-hoc flag on a probe command,
  invoke `bindFlags`, call the helper, assert the new key appears — proves
  the helper does not need to be modified when subcommands grow.
- Mocks: reuse the `resetViper(t)` / `writeConfig(t, …)` patterns from
  [pkg/cmd/precedence_test.go](../../../pkg/cmd/precedence_test.go); no
  network mocks are required for this feature.
- `defer` cleanup: rely on `t.Cleanup(viper.Reset)`.

## Risks / Open Questions
- Auto-discovery of keys (RESOLVED): the helper enumerates `viper.AllKeys()`
  after `bindFlags(cmd)` has run in `PersistentPreRunE`. At that point every
  pflag of the executing subcommand is bound to viper, plus every key from
  `config.yaml` and any `viper.SetDefault`. Adding a new flag therefore makes
  it appear in the dump automatically, with no edits to the helper. The
  only manual maintenance is appending to `utils.SecretKeys` when the new
  flag carries a secret.
- Note on env-only keys: `viper.AutomaticEnv()` resolves env vars on demand
  but does not enumerate them. In sbam every public knob already has a
  corresponding flag, so every key is discoverable via `viper.AllKeys()`.
  If a future env-only key is introduced without a matching flag/default, it
  will be invisible to the dump — flag this as a follow-up rather than
  papering over it in the helper.
- Risk: source detection for env vars uses `os.LookupEnv(strings.ToUpper(key))`
  to match viper's `AutomaticEnv()` behavior. We currently do not call
  `viper.SetEnvPrefix`, so this mapping is correct today. If a prefix is
  added later the helper must be updated.

## References
- Issue: [#70](https://github.com/atbore-phx/sbam/issues/70)
- Related precedence work: issue [#68](https://github.com/atbore-phx/sbam/issues/68)
  and [pkg/cmd/precedence_test.go](../../../pkg/cmd/precedence_test.go)
- Logger init: [src/utils/log.go](../../../src/utils/log.go)

## Clarifications
> 2026-04-28 — answers from the user via the questions tool:
> - Slug: `70-issue-display-startup-parameters`.
> - Scope: all three subcommands (`schedule`, `configure`, `estimate`).
> - Log level: debug only.
> - Secrets: redact `apikey` to `***[len=N]`.
> - Format: a single multi-line pretty-printed block per subcommand.
> - Source annotation: yes — annotate each value with `flag | env | yaml | default`.
>
> 2026-04-28 — follow-up revisions requested by the user:
> - FR3: drop the `[len=N]` suffix; use the fixed literal `***` for any
>   redacted secret.
> - Scope: explicitly require a single shared, reusable helper so that
>   future subcommands plug in without bespoke code; key list must be
>   auto-discovered from viper.
> - Helper location: `src/utils` (alongside `log.go` / `error.go`).
> - Auto-discovery: confirmed feasible via `viper.AllKeys()` after
>   `bindFlags(cmd)`. The only manual step for new flags is registering
>   them in `utils.SecretKeys` *if* they are secrets.
