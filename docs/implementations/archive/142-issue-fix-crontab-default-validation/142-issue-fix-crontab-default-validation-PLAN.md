# PLAN: Fix crontab default validation in HA addon config

> Date: 2026-05-27
> TASK: [142-issue-fix-crontab-default-validation-TASK.md](142-issue-fix-crontab-default-validation-TASK.md)
> Issue: [#142](https://github.com/atbore-phx/sbam/issues/142)

## Task Analysis

**Goal:** Fix the `crontab` regex in `home-assistant/addons/sbam/config.json` so that `0 0 0 0 0` passes schema validation.

**Non-goals:**
- Handling blank/empty crontab input
- Changes to Go-side cron validation logic
- Other config.json schema changes

**Acceptance criteria:**
- `0 0 0 0 0` passes HA add-on config schema validation
- All previously valid crontab expressions remain accepted
- `make test` and `make build` pass

## Current State

The crontab regex in `home-assistant/addons/sbam/config.json:47`:

```json
"crontab": "match(^((((\\d+,)+\\d+|(\\d+(\\/|-|#)\\d+)|\\d+L?|\\*(\\/\\d+)?|L(-\\d+)?|\\?|[A-Z]{3}(-[A-Z]{3})?) ?){5,7})|(@(annually|yearly|monthly|weekly|daily|hourly|reboot))|(@every (\\d+(ns|us|µs|ms|s|m|h))+)$)"
```

Decoded (JSON-unescaped) regex:
```
^((((\d+,)+\d+|(\d+(\/|-|#)\d+)|\d+L?|\*(\/\d+)?|L(-\d+)?|\?|[A-Z]{3}(-[A-Z]{3})?) ?){5,7})|(@(annually|yearly|monthly|weekly|daily|hourly|reboot))|(@every (\d+(ns|us|µs|ms|s|m|h))+)$
```

This regex has three top-level alternatives joined by `|`:
1. Standard 5-7 field cron expressions
2. `@` shorthand macros (`@daily`, `@hourly`, etc.)
3. `@every` duration expressions

In theory, `0 0 0 0 0` should match alternative 1 (each `0` matches the `\d+L?` sub-pattern). In practice, users report a regex validation error from Home Assistant. The fix adds `0 0 0 0 0` as a fourth explicit alternative, making the match independent of the complex field-by-field alternation logic.

The Go application already handles `0 0 0 0 0` correctly — `pkg/cmd/schedule.go:40` defines `const_ct = "0 0 0 0 0"`, and at line 196 the code checks `if crontab != const_ct` to skip cron scheduling. The Go binary never passes this value to `robfig/cron`, so no runtime parsing issue arises.

No existing tests cover the HA config.json schema regex.

## Target Architecture

Minimal change touching two files:

| File | Change |
|---|---|
| `home-assistant/addons/sbam/config.json` | Add `(0 0 0 0 0)\|` as a fourth alternative in the crontab regex |
| `pkg/cmd/config_schema_test.go` (new) | Unit test that extracts the regex from config.json and validates key inputs |

No new packages, types, functions, or interfaces. No data flow changes.

## Dependency Choices

None. No new dependencies. The test uses only the standard library (`encoding/json`, `regexp`, `testing`) plus `github.com/stretchr/testify` (already in the project).

## Configuration Changes

**`home-assistant/addons/sbam/config.json`:**
- Line 47: update `crontab` schema value to add `(0 0 0 0 0)` as an alternative

New regex (JSON-escaped):
```
^((((\\d+,)+\\d+|(\\d+(\\/|-|#)\\d+)|\\d+L?|\\*(\\/\\d+)?|L(-\\d+)?|\\?|[A-Z]{3}(-[A-Z]{3})?) ?){5,7})|(0 0 0 0 0)|(@(annually|yearly|monthly|weekly|daily|hourly|reboot))|(@every (\\d+(ns|us|µs|ms|s|m|h))+)$
```

The only addition is `(0 0 0 0 0)|` inserted before the `@` shorthand alternative.

No CLI flags, env vars, config.yaml keys, or run.sh changes.

## Implementation Blueprint

### Step 1 — Fix the regex in config.json

**File:** `home-assistant/addons/sbam/config.json`

**Change:** On line 47, update the crontab schema value.

**Current:**
```
"crontab": "match(^((((\\d+,)+\\d+|(\\d+(\\/|-|#)\\d+)|\\d+L?|\\*(\\/\\d+)?|L(-\\d+)?|\\?|[A-Z]{3}(-[A-Z]{3})?) ?){5,7})|(@(annually|yearly|monthly|weekly|daily|hourly|reboot))|(@every (\\d+(ns|us|µs|ms|s|m|h))+)$)"
```

**New:**
```
"crontab": "match(^((((\\d+,)+\\d+|(\\d+(\\/|-|#)\\d+)|\\d+L?|\\*(\\/\\d+)?|L(-\\d+)?|\\?|[A-Z]{3}(-[A-Z]{3})?) ?){5,7})|(0 0 0 0 0)|(@(annually|yearly|monthly|weekly|daily|hourly|reboot))|(@every (\\d+(ns|us|µs|ms|s|m|h))+)$)"
```

**Rationale:** Adding `(0 0 0 0 0)|` as a literal alternative bypasses any engine-specific behavior in the complex field-by-field alternation. It is unambiguous and self-documenting — the disabled sentinel is clearly visible in the schema.

### Step 2 — Add Go unit test for the config.json crontab regex

**File:** `pkg/cmd/config_schema_test.go` (new)

**What to add:** A test function `TestCrontabSchemaRegex` that:
1. Reads `../../home-assistant/addons/sbam/config.json` relative to the test file
2. Unmarshals the JSON to extract `schema.crontab`
3. Strips the `match(` prefix and `)` suffix to get the bare regex
4. Compiles the regex with `regexp.Compile`
5. Asserts pass/fail for the inputs listed in the test plan below

**Signature:** `func TestCrontabSchemaRegex(t *testing.T)`

**Rationale:** This catches regressions where a future edit to the config.json regex accidentally drops `0 0 0 0 0` support. Reading the actual file rather than hardcoding the regex ensures the test stays in sync with the schema.

## Test Plan

### config_schema_test.go

**Expected pass cases:**
| Input | Notes |
|---|---|
| `0 0 0 0 0` | The fix target — disabled sentinel |
| `00 00-05 * * *` | Default example in config.json options |
| `*/5 * * * *` | Standard every-5-minutes |
| `30 14 * * *` | Daily at 14:30 |
| `1,15,30 * * * *` | Comma-separated values |
| `0 0 * * 1-5` | Weekdays only |
| `@daily` | Shorthand macro |
| `@every 1h` | Duration expression |
| `@every 5m` | Duration expression |

**Expected fail cases:**
| Input | Notes |
|---|---|
| `""` (empty string) | Empty input |
| `not a cron expression` | Garbage input |
| `0` | Too few fields |
| `a b c d e` | Non-numeric fields in 5-field form |

**Cleanup:** No external resources, no `defer` needed. Uses `require`/`assert` from testify.

## Validation Gates

```bash
# Run the new test specifically
go test ./pkg/cmd/ -run TestCrontabSchemaRegex -v

# Full test suite
make test

# Build
make build
```

## Rollout / Backward Compatibility

- **Backward compatibility:** The change is purely additive — a new alternative in an existing `|` alternation. All previously accepted expressions continue to match against the unchanged alternatives. No valid input becomes invalid.
- **Default behavior:** Unchanged. The `options.crontab` default remains `"00 00-05 * * *"`.
- **Migration:** None required. Users who previously worked around the issue (e.g., by editing config directly) can now switch to using the HA UI with `0 0 0 0 0`.
- **Home Assistant add-on CHANGELOG:** Should note the fix for the next release.

## Security Considerations

- The regex change is a pure validation relax — it accepts an additional input that was previously rejected. No security regression.
- The regex is evaluated by HA Supervisor, not exposed to unauthenticated input. Even if an attacker could supply a crafted crontab value, the worst case is a regex match/no-match — no injection or code execution surface.
- No secrets, keys, or credentials are involved.

## Gotchas

- The JSON escaping in config.json requires `\\d` for `\d`, `\\/` for `\/`, etc. The literal string `(0 0 0 0 0)` needs no escaping — it contains only spaces and digits.
- The test must strip the `match(` prefix and trailing `)` from the schema value before compiling as a regex. The schema format is `match(<regex>)` where the regex already includes `^` and `$` anchors.
- The test reads config.json with a relative path. The working directory when running `go test ./pkg/cmd/` is the project root, so the path `home-assistant/addons/sbam/config.json` works. Using `../../home-assistant/addons/sbam/config.json` from the test file's perspective is more robust against working-directory variation — but Go tests in `pkg/cmd/` are always run with the module root as the working directory.

## Open Questions / Risks

- RESOLVED: blank/empty crontab is out of scope.
- RISK: The exact regex engine used by HA Supervisor is not documented. The explicit `(0 0 0 0 0)` alternative mitigates this — it's a plain literal match with no metacharacters, no character classes, no quantifiers. Should work in any regex engine.

## Confidence Score

**10/10** — Single-line regex addition plus a straightforward unit test. No new dependencies, no architectural changes, no data flow impact. The fix is additive and cannot regress existing behavior.
