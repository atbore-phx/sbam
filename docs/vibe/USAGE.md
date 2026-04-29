USAGE: Copilot prompt-driven workflow

Purpose
- Quick reference for contributors who want to use the repository's Copilot
  prompts to generate TASKs, PLANs and iterate on implementations.

Prerequisites
- VS Code with GitHub Copilot Chat enabled and configured.
- The `sbam` repository checked out locally.

Quick start
1. Open Copilot Chat in VS Code.
2. Run the prompt: `/generate-plan-from-issue <issue-number>` to generate a TASK
   and a PLAN from a GitHub issue.
3. Answer interactive clarifications from the agent.
4. Review the generated files under `docs/implementations/<feature>/`.
5. When the PLAN is ready, run `/implement-plan <feature-slug>` to apply the
   documented blueprint (this may create docs or example files; implementation
   of code changes is manual unless the agent is explicitly configured to open
   PRs).

Implementer checklist (before opening a PR)
- Review and confirm the `TASK.md` intent.
- Ensure the `PLAN.md` contains concrete implementation steps.
- Implement changes on a feature branch.
- Add/adjust unit and integration tests as appropriate.
- Run validation commands locally (see below).
- Open a **draft PR** referencing the issue and generated PLAN and request
  human review.

Validation commands (run locally)
```bash
go mod tidy
go vet ./...
make test
make build
```

Notes
- Prompts live in `.github/prompts/`. Editing prompts changes agent behavior;
  add examples when you update a prompt.
- Generated code is a starting point; tests and manual review are required.
