# USAGE: Compound Engineering workflow

## Purpose

- Quick reference for contributors who want to use CE skills to brainstorm, plan, and iterate on implementations.

## Prerequisites

- Claude Code with the Compound Engineering plugin installed and configured.
- The `sbam` repository checked out locally.

## Quick start

1. Open Claude Code in the repo.
2. Run `/ce-brainstorm <your idea>` to define requirements and scope.
3. When the requirements doc is ready, run `/ce-plan` to produce a technical plan (or `/ce-plan` directly from a GitHub issue).
4. Review the plan under `docs/plans/`.
5. When the plan is ready, run `/ce-work <plan-path>` to implement.
6. Run `/ce-code-review` to review the diff before shipping.
7. Ship with `/ce-commit-push-pr`.

## Implementer checklist (before opening a PR)

- Review and confirm the plan's intent.
- Ensure the plan contains concrete implementation units with test scenarios.
- Implement changes on a feature branch.
- Add/adjust unit and integration tests as appropriate.
- Run validation commands locally (see below).
- Run `/ce-code-review` to catch issues before human review.
- Open a **draft PR** referencing the issue and plan, and request human review.

## Validation commands (run locally)

```bash
go mod tidy
go vet ./...
make test
make build
```

## Notes

- Plans live in `docs/plans/` and follow the naming convention `YYYY-MM-DD-NNN-<type>-<name>-plan.md`.
- The strategy doc (`STRATEGY.md`) is the grounding document read by planning skills.
- Generated code is a starting point; tests and manual review are required.
