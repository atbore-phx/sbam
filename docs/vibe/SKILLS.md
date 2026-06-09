# Available Compound Engineering skills

This project uses Compound Engineering (CE) skills as the canonical contributor workflow. Each skill is invoked as a slash command in Claude Code.

## Core skills

| Skill | Purpose |
|---|---|
| `/ce-strategy` | Create or update `STRATEGY.md` — product target problem, approach, users, metrics, tracks. Run first to ground direction before planning features. |
| `/ce-brainstorm` | Explore requirements and define what to build. Produces a requirements doc in `docs/brainstorms/`. Use for new ideas or under-specified feature requests. |
| `/ce-plan` | Create a technical implementation plan from a requirements doc, an issue, or a local feature description. Produces a plan in `docs/plans/`. |
| `/ce-work` | Execute a plan — implements changes, runs tests, commits incrementally. Reads the plan's implementation units and ships them. |
| `/ce-code-review` | Structured code review using tiered persona agents. Run before opening a PR to catch correctness, security, performance, and maintainability issues. |
| `/ce-commit-push-pr` | Commit all changed files, push, and open a pull request with a value-first description. |

## Supporting skills

| Skill | Purpose |
|---|---|
| `/ce-debug` | Systematic root-cause analysis and bug fixing. Use when investigating test failures, errors, or unexpected behavior. |
| `/ce-simplify-code` | Review recently changed code for reuse, simplification, and efficiency improvements. Quality-only — does not hunt for bugs. |
| `/ce-compound` | Document a recently solved problem in `docs/solutions/` to compound team knowledge. |
| `/ce-clean-gone-branches` | Clean up local branches whose remote tracking branch is gone. |

## Mapping from old prompts

| Old prompt / command | CE equivalent |
|---|---|
| `/generate-plan-from-issue <N>` | `/ce-plan` from the issue |
| `/generate-plan-local <slug>` | `/ce-brainstorm` then `/ce-plan` |
| `/implement-plan <slug>` | `/ce-work <plan-path>` |
| `/create-pr` | `/ce-commit-push-pr` |

The old prompts (`.github/prompts/`) and Claude Code shims (`.claude/commands/`) have been removed.
