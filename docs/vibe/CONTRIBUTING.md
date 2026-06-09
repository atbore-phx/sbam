# Contributing with Compound Engineering

## Purpose

- Provide guidance for contributors using Compound Engineering skills to propose and implement changes.
- Keep the workflow consistent, reviewable, and safe.

## Principles

- Iterate: brainstorm requirements with `/ce-brainstorm`, then plan with `/ce-plan`, then implement with `/ce-work`.
- Validate: always run tests and perform manual review of generated code.
- Safety first: prefer draft PRs and require human approval before merging.

## Recommended workflow

1. Ground yourself in product direction by reading `STRATEGY.md` and `CLAUDE.md`.
2. For new features, use `/ce-brainstorm` to define requirements and scope.
3. Generate a technical plan with `/ce-plan` (from an issue or local idea).
4. Review the plan under `docs/plans/` and answer any clarifications.
5. Execute the plan with `/ce-work <plan-path>` on a feature branch.
6. Add or update unit and integration tests as appropriate.
7. Run validation: `make test`, `make build`, `make all`.
8. Review with `/ce-code-review` before opening a PR.
9. Ship with `/ce-commit-push-pr` — opens a draft PR referencing the issue and plan.
10. Request at least one human reviewer and ensure CI passes before converting from draft.

## Guardrails (required before opening PR)

- Tests: run `make test` locally; the PR must pass CI tests.
- Review: at least one reviewer must sign off on generated code.
- Secrets: never commit real API keys or credentials; use placeholders.
- Scope: keep each generated PR small and focused; prefer multiple smaller PRs over one large change.

## Editing strategy and docs

- `STRATEGY.md` is the product direction anchor — update it via `/ce-strategy`.
- `CLAUDE.md` carries coding standards and CE workflow documentation.
- When adding new patterns or learnings, use `/ce-compound` to document them in `docs/solutions/`.

## Contact

- For questions about the workflow, open an issue referencing this documentation.
