# Compound Engineering — Contributor workflow

This directory documents the project's **Compound Engineering workflow**: using CE skills to brainstorm, plan, implement, review, and ship changes.

## Core idea

- Use CE skills to generate structured plans from issues or local feature ideas.
- Iterate on plans via clarifying questions, then implement on a feature branch.
- Always run validation gates and require human review before merging.

## Docs in this folder

- [USAGE.md](USAGE.md) — Quick start and implementer checklist.
- [CONTRIBUTING.md](CONTRIBUTING.md) — Guidelines and guardrails for contributors.
- [SKILLS.md](SKILLS.md) — List of available CE skills and project-specific usage.
- [EXAMPLES.md](EXAMPLES.md) — Worked examples and recommended workflows.

## Where things live

- Plans: `docs/plans/YYYY-MM-DD-NNN-<type>-<name>-plan.md`
- Strategy: `STRATEGY.md` (product grounding document)
- Coding standards: `CLAUDE.md`
- Historical pre-CE plans: `docs/implementations/archive/`

## Notes

- When in doubt, prefer small, test-covered PRs and ask for human review.
- The old prompts (`/generate-plan-*`, `/implement-plan`, `/create-pr`) have been removed — use CE skills instead.
