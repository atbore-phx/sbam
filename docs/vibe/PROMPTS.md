Available prompts (.github/prompts) and Claude Code commands (.claude/commands)

This repository includes a set of Copilot prompts designed to generate TASKs,
PLANs and help implement features. Edit prompts carefully and include examples.

The same prompts are also exposed as **Claude Code slash commands** via thin
wrapper files under `.claude/commands/`. Each command delegates to the
corresponding prompt file in `.github/prompts/`, which is the single source of
truth. Commands and prompts stay in sync automatically — edit the prompt to
change behavior for both tools.

Known prompts / commands
- `.github/prompts/generate-plan-from-issue.prompt.md` → `/generate-plan-from-issue`
  Generate a TASK and PLAN from a GitHub issue reference (fetches the issue,
  asks clarifying questions, writes docs to `docs/implementations/<feature>/`).
- `.github/prompts/generate-plan-local.prompt.md` → `/generate-plan-local`
  Generate a TASK and PLAN from a local feature slug (useful for small, local
  features or experiments).
- `.github/prompts/implement-plan.prompt.md` → `/implement-plan`
  Implement the PLAN: reads the PLAN and TASK and can create documentation and
  make suggestions for code changes (manual review required before merging).

Editing and versioning
- **Source of truth:** `.github/prompts/*.prompt.md` — edit these to change
  behavior for both Copilot and Claude Code. The `.claude/commands/*.md` files
  are thin delegating shims and should not need separate edits.
- When changing prompts, add an example output to [EXAMPLES.md](EXAMPLES.md) and
  describe the change in the corresponding TASK/PLAN.
- Keep prompts small and explicit about safety checks (tests, draft PRs,
  manual review).

Safety note
- Prompts can produce code; never treat generated code as production-ready.
  Always validate, add tests, and require human review before merging.
