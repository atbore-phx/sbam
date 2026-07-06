# Plan: Add Copilot agent workflow for feature requests (plan + execution)

> Issue: [#76](https://github.com/atbore-phx/sbam/issues/76)
> TASK: [docs/implementations/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution-TASK.md](docs/implementations/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution-TASK.md)
> Date: 2026-04-29

## 1) Task Analysis
- Goal: Provide documented, repeatable workflow for using the repository's Copilot prompts to generate implementation TASKs and execute them safely (draft PRs, manual review).
- Scope: generate prompt-driven plan documents and contribution guidance; postpone remote/cloud agent execution.
- Acceptance criteria (copied from TASK):
  - Agent generates a clear plan of action for each feature request
  - Plan is visible under the repo
  - Agent executes the plan (documentation + examples demonstrating the prompts and minimal local execution)

## 2) Current State (relevant files)
- TASK created: [docs/implementations/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution-TASK.md](docs/implementations/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution-TASK.md)
- Prompts and prompt templates: [.github/prompts/generate-plan-from-issue.prompt.md](.github/prompts/generate-plan-from-issue.prompt.md)
- CLI commands: [pkg/cmd/root.go](pkg/cmd/root.go), [pkg/cmd/configure.go](pkg/cmd/configure.go), [pkg/cmd/schedule.go](pkg/cmd/schedule.go)
- Example of existing prompt-driven changes referenced in issue: PR https://github.com/atbore-phx/sbam/pull/77

## 3) Target Documentation Deliverables
- A clear documentation page under `docs/implementations/<feature>/` describing:
  - How to run `generate-plan-from-issue` and `generate-plan-local` (VS Code Copilot Chat usage and recommended options)
  - The expected review/validation gates (run tests, draft PRs, manual code review)
  - Example walkthrough (take issue #76 as worked example)
  - Contribution guidelines for "vibe coding" with Copilot prompts
- A short `USAGE.md` with copy-paste steps to run the prompts locally in VS Code and to inspect the produced TASK/PLAN files.

## 4) Dependency Choices
- No new Go dependencies required for documentation.
- Prefer referencing existing tooling and patterns: Cobra/Viper for CLI, Zap for logging. No runtime changes expected.

## 5) Configuration Changes
- None required for documentation-only work.

## 6) Implementation Blueprint (file-by-file steps)
1. (This PR) Add the PLAN file at `docs/implementations/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution-PLAN.md` (this file).
2. Add `USAGE.md` in the same directory with a short, copyable walkthrough. Suggested contents:
   - How to invoke: open VS Code -> Copilot Chat -> run `/generate-plan-from-issue 76` (or use the provided Copilot prompt command palette entry).
   - Example dialogue and expected outcomes (TASK created, clarifications asked, PLAN generated).
   - How to run the implementer checklist: `make test`, `go test ./...`, `make build`.
3. Add a documentation section `docs/CONTRIBUTING-COPILOT.md` (or append to `README.md`) that captures:
   - The agent workflow (generate -> clarify -> approve -> implement)
   - Guardrails (draft PRs, require test pass and human review before merge)
   - Where prompts live: `.github/prompts/` and how to edit them safely.
4. Create a short example: copy the `TASK.md` and `PLAN.md` generated here into `docs/implementations/76-.../example-usage.md` to show the full flow end-to-end.
5. Optional: Add a small CI job or local script to lint / validate docs (e.g. check for missing links) — out of scope for initial doc PR, but suggested as follow-up.

For each file above include a short header describing the purpose, then the concrete content. Keep changes small and focused; each doc file should be a separate commit.

## 7) Test Plan (how to validate this work)
- Manual verification:
  - Read `USAGE.md` and follow the instructions in a local VS Code instance with Copilot Chat installed.
  - Run `make test` and `go test ./...` to ensure no code regressions.
- Documentation checks:
  - Verify links to `.github/prompts` resolve.
  - Verify that `TASK.md` and `PLAN.md` render correctly on GitHub (markdown preview).

## 8) Validation Gates (commands to run)
- Local checks:
  - `make test`
  - `make build`
  - `go test ./...`
- Doc checks (manual): preview the generated markdown in the GitHub web UI or use a markdown linter locally.

## 9) Rollout / Backward Compatibility
- This change is documentation-only; no runtime or config breakage expected.
- Add a short `CHANGELOG.md` entry under `home-assistant/addons/sbam/CHANGELOG.md` noting the documentation addition (optional).

## 10) Security Considerations
- The documentation must stress that generated code must be reviewed before merging.
- Recommend enabling draft PR mode by default for the agent.

## 11) Gotchas
- Prompt outputs vary by model and may require iteration; the docs must emphasize manual review and tests.
- Don't rely on generated tests — ensure test coverage is added by humans.
- Keep prompts in `.github/prompts` as the single source of truth for the workflow.

## 12) Open Questions (from TASK and issue comments)
- Is there appetite to later run the agent as a remote/cloud service? (DEFERRED)
- Should a CI validation step be added to auto-run the prompts in a sandbox? (DEFERRED)

## 13) Confidence Score
- Confidence: 8/10 — documentation work is straightforward; remaining unknowns relate to reviewer preferences and how-to details for contributors.

---

## Next steps (what I can do now)
- I created the TASK and PLAN documents under `docs/implementations/76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution/`.
- If you want, I can also:
  - Create the `USAGE.md` and `CONTRIBUTING-COPILOT.md` drafts now and open a PR (requires your approval).
  - Or stop here so you can review the TASK and PLAN before any further files are added.

Run `/implement-plan 76-issue-add-copilot-agent-workflow-for-feature-requests-plan-execution` next to apply the PLAN.
