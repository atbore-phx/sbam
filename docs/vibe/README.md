# Vibe Coding — Prompt-driven contributor workflow

This directory documents the repository's "Vibe Coding" workflow: using GitHub
Copilot Chat prompts to generate `TASK` and `PLAN` documents and to assist
contributors during implementation.

Core idea
- Use the provided prompts to generate a structured plan from an issue or a
  local feature slug.
- Iterate on the generated plan via clarifying questions, then implement on a
  small feature branch.
- Always run validation gates and require human review before merging.

Docs in this folder
- `USAGE.md` — Quick start and implementer checklist.
- `CONTRIBUTING-COPILOT.md` — Guidelines and guardrails for contributors.
- `PROMPTS.md` — List of available prompts and brief descriptions.
- `EXAMPLES.md` — Worked examples and recommended workflows.

Where the prompts live
- Prompts are stored under `.github/prompts/` in this repository. See
  `PROMPTS.md` for direct links and descriptions.

Notes
- This folder intentionally contains contributor-facing documentation only —
  generated TASKs and PLANs live under `docs/implementations/` per-feature.
- When in doubt, prefer small, test-covered PRs and ask for human review.
