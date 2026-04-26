---
agent: "agent"
description: "Interactively author a TASK and produce a comprehensive PLAN for an sbam feature stored under docs/implementations/<feature>/"
---

# Generate Plan (Local)

You are generating an implementation plan for the **sbam** project (Smart Battery Advanced Manager — see [.github/copilot-instructions.md](../copilot-instructions.md)). The user invokes this prompt as:

```
/generate-plan-local <feature-name>
```

Where `<feature-name>` is a short kebab-case slug. The canonical local slug format is `<NN>-local-<slug>` where `<NN>` is a zero-padded ordinal (examples: `01-local-init`, `02-local-prometheus-metrics`).

- If the user supplies a plain slug (e.g. `init` or `cache-forecast`), the agent will prefix it with the next free zero-padded ordinal and the literal `local` to produce the canonical form (e.g. `03-local-cache-forecast`).
- If the user supplies a full canonical slug already, it must match `^\d{2}-local-[a-z0-9-]+$`.

If the user did not pass a feature name, **ask for one** before doing anything else and generate the canonical slug from the provided title. Validate input: accept either a plain slug matching `^[a-z0-9][a-z0-9-]*$` or a canonical local slug matching `^\d{2}-local-[a-z0-9-]+$`. Do not proceed until you have a valid name.

## Conventions

- Working directory for this feature: `docs/implementations/<feature-name>/`
- Files this prompt manages:
  - `<feature-name>-TASK.md` — the human-facing feature request, requirements, constraints, open questions
  - `<feature-name>-PLAN.md` — the agent-facing implementation plan (consumed later by `implement-plan`)
## Workflow

Follow these phases **in order**. Use the todo list tool to track progress and keep the user oriented.

### Phase 1 — Bootstrap or load TASK

1. Check whether `docs/implementations/<feature-name>/<feature-name>-TASK.md` exists.
2. **If it exists:**
   - Read the TASK file in full.
   - Read also any sibling files (existing PLAN, notes, diagrams).
   - Summarize back to the user what the TASK currently says (3–6 bullets).
   - Identify gaps, ambiguities, missing acceptance criteria, missing non-goals, missing config/env impacts, missing test strategy.
   - Ask **only the necessary clarifying questions** (use the questions tool; batch them; prefer fixed-choice options when possible). Do not ask questions whose answers are already in the TASK or in `copilot-instructions.md`.
   - Apply the answers by editing the TASK file in place. Preserve any human-written sections; add a "Clarifications" section at the bottom if useful.
3. **If it does not exist:**
   - Create the directory `docs/implementations/<feature-name>/`.
   - Ask the user a focused interview to gather what is needed to write a strong TASK file. Cover at least:
     - **Goal** in one sentence.
     - **User story / motivation** (who benefits and why).
     - **Scope / non-scope** (what is explicitly out).
     - **Inputs/outputs**: new CLI flags, config keys, env vars, Modbus registers, Solcast/Fronius endpoints touched.
     - **Affected packages** (`pkg/cmd`, `pkg/fronius`, `pkg/power`, `pkg/storage`, `src/utils`, Home Assistant add-on, Dockerfile, Makefile, CI workflows).
     - **Acceptance criteria** (observable, testable).
     - **Constraints**: backward compatibility, default behavior, safety (e.g. must never write Modbus registers in dry-run).
     - **Risks / unknowns** worth flagging.
     - **References** (issues, PRs, vendor docs, datasheets).
   - Create the TASK file using the template below.

#### TASK template

```markdown
# Feature: <Title>

> Slug: `<feature-name>` · Created: <YYYY-MM-DD>

## Summary
One paragraph describing the feature.

## Motivation / User Story
Why this matters and for whom.

## Scope
- In scope: ...
- Out of scope: ...

## Functional Requirements
- ...

## Non-functional Requirements
- Backward compatibility: ...
- Safety / defaults: ...
- Performance: ...

## Configuration Impact
- New CLI flags: ...
- New config keys (`config.yaml`): ...
- New env vars: ...
- Home Assistant add-on schema changes (`home-assistant/addons/sbam/config.json`): ...

## External Integrations Touched
- Solcast: ...
- Fronius Solar API: ...
- Fronius Modbus registers: ...

## Acceptance Criteria
- [ ] ...
- [ ] ...

## Test Strategy
- Unit tests (packages, mocks): ...
- Edge cases: ...
- Failure cases: ...

## Risks / Open Questions
- ...

## References
- ...
```

### Phase 2 — Confirm TASK readiness

- Re-read the TASK file end-to-end.
- Score it against this checklist; do not proceed until every box is checked or explicitly waived by the user:
  - [ ] Goal is unambiguous and testable
  - [ ] Scope and non-scope are explicit
  - [ ] All affected packages and config surfaces are listed
  - [ ] Acceptance criteria are observable
  - [ ] Test strategy covers expected, edge, and failure cases
  - [ ] No blocking open questions remain (or the unknowns are explicitly accepted as risks)
- Show the user the final TASK summary and ask for explicit go-ahead before writing the PLAN.

### Phase 3 — Research

Now perform the research needed to write a high-confidence PLAN. Reuse `runSubagent` (Explore agent) for read-only sweeps when helpful.

1. **Codebase analysis** — find and cite existing patterns to mirror:
   - CLI command shape: see [pkg/cmd/root.go](../../pkg/cmd/root.go), [pkg/cmd/schedule.go](../../pkg/cmd/schedule.go).
   - Modbus access: see [pkg/fronius/modbus.go](../../pkg/fronius/modbus.go), [pkg/fronius/configure.go](../../pkg/fronius/configure.go).
   - HTTP client + caching: see [pkg/power/estimate.go](../../pkg/power/estimate.go).
   - Test patterns (httptest, mbserver): see [pkg/fronius/fronius_test.go](../../pkg/fronius/fronius_test.go), [pkg/power/power_test.go](../../pkg/power/power_test.go), [pkg/storage/storage_test.go](../../pkg/storage/storage_test.go).
   - Logging: see [src/utils/log.go](../../src/utils/log.go).
   - Build / CI: see [Makefile](../../Makefile), [.github/workflows/test.yml](../workflows/test.yml), [.github/workflows/release.yml](../workflows/release.yml).
   - Home Assistant add-on: see [home-assistant/addons/sbam/](../../home-assistant/addons/sbam/).
2. **External research** — capture concrete URLs (vendor docs, library godoc, register maps, RFCs). Note any Fronius firmware quirks, Modbus register addresses, Solcast quotas, or `simonvetter/modbus` API specifics.
3. **Conventions** — confirm naming, error handling (`handleError` / `handleErrorPanic`), constructor pattern (`New() *T`), config hierarchy (flag > env > yaml).

### Phase 4 — Write the PLAN

Create `docs/implementations/<feature-name>/<feature-name>-PLAN.md` using the structure below. The PLAN must be executable by another agent **without further clarification** and must reference real files in this repo.

#### PLAN structure

1. **Header** — title, date, link back to the TASK, link to any GitHub issue/PR.
2. **Task Analysis** — restate goals, non-goals, acceptance criteria from TASK.
3. **Current State** — what exists today (cite files with workspace-relative links).
4. **Target Architecture** — packages affected, new types/functions/interfaces, data flow. Include a small diagram if it helps (Mermaid is fine).
5. **Dependency Choices** — any new Go modules with godoc URL, version, and rationale. Prefer the existing stack (cobra, viper, zap, cron/v3, simonvetter/modbus, testify, mbserver) before adding new deps.
6. **Configuration Changes** — exact keys for `config.yaml`, env var names, cobra flag definitions, Home Assistant `config.json` schema entries, and `run.sh` exports. Document the precedence (flag > env > yaml).
7. **Implementation Blueprint** — ordered, numbered, file-by-file steps. For each step include: the target file, what to add/change, the public signature, and the rationale.
8. **Test Plan** — for every new/modified package:
   - expected case
   - at least one edge case
   - at least one failure case
   - mocks to use (`httptest.NewServer`, `tbrandon/mbserver`)
   - `defer` cleanup reminders
9. **Validation Gates** — exact commands the implementer must run and pass:
   - `make test`
   - `make build`
   - any focused `go test ./pkg/...` commands relevant to the change
   - `docker build` if the Dockerfile changes
10. **Rollout / Backward Compatibility** — defaults, migration notes, Home Assistant add-on `CHANGELOG.md` entry, README updates required.
11. **Security Considerations** — secrets in config, Modbus write safety, OWASP-relevant input validation.
12. **Gotchas** — Fronius firmware quirks, Modbus register read/write ordering, Solcast rate limits (HTTP 429), zap logger init order, viper env binding edge cases.
13. **Open Questions / Risks** — copy from TASK; mark as RESOLVED or DEFERRED.
14. **Confidence Score** — rate 1–10 the likelihood of single-pass implementation success. If below 9, list what would raise it and offer to gather it now.

### Phase 5 — Wrap-up

- Print a short summary with workspace-relative links to the TASK and PLAN.
- Remind the user the next step is `/implement-plan <feature-name>`.
- **Do not** modify any source files outside `docs/implementations/<feature-name>/` during this prompt.
- If you added or removed files anywhere in the repo, update the `Project Structure` section in [.github/copilot-instructions.md](../copilot-instructions.md) per its own rule.

## Operating Rules

- Use the questions tool for clarifications instead of long prose. Batch related questions; prefer multi-choice with a recommended default.
- Never invent Fronius register addresses, Solcast field names, or vendor URLs. If unknown, mark as an open question and ask the user.
- Never run destructive commands (no `git push`, no `git reset --hard`, no `rm -rf`). File creation under `docs/implementations/` is allowed.
- Keep diffs minimal and focused; do not refactor unrelated code.
- If a previous PLAN exists for this feature, **update it in place** rather than rewriting from scratch; preserve manual edits and append a brief "Revision history" entry at the bottom.
