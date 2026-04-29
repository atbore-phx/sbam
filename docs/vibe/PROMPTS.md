Available prompts (.github/prompts)

This repository includes a set of Copilot prompts designed to generate TASKs,
PLANs and help implement features. Edit prompts carefully and include examples.

Known prompts
- `.github/prompts/generate-plan-from-issue.prompt.md` — Generate a TASK and
  PLAN from a GitHub issue reference (fetches the issue, asks clarifying
  questions, writes docs to `docs/implementations/<feature>/`).
- `.github/prompts/generate-plan-local.prompt.md` — Generate a TASK and PLAN
  from a local feature slug (useful for small, local features or experiments).
- `.github/prompts/implement-plan.prompt.md` — Implement the PLAN: reads the
  PLAN and TASK and can create documentation and make suggestions for code
  changes (manual review required before merging).

Editing and versioning
- When changing prompts, add an example output to `docs/vibe/EXAMPLES.md` and
  describe the change in the corresponding TASK/PLAN.
- Keep prompts small and explicit about safety checks (tests, draft PRs,
  manual review).

Safety note
- Prompts can produce code; never treat generated code as production-ready.
  Always validate, add tests, and require human review before merging.
