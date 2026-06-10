---
title: "chore: Migrate from prompt-based workflow to Compound Engineering"
type: chore
date: 2026-06-09
---

# chore: Migrate from prompt-based workflow to Compound Engineering

## Summary

Replace the old GitHub Copilot / Claude Code prompt-driven workflow (`.github/prompts/`, `.claude/commands/`, `docs/vibe/`) with Compound Engineering plugin conventions. Remove the old prompt files and delegating shims, archive historical `docs/implementations/` directories, and rewrite contributor docs to document CE skills as the canonical workflow.

## Problem Frame

The project currently documents a "Vibe Coding" workflow using GitHub Copilot Chat prompts and Claude Code slash commands — both delegating to shared prompt files in `.github/prompts/`. This predates the Compound Engineering plugin installation. The old prompts, shims, and docs reference a workflow that is no longer in use, creating confusion for contributors and leaving stale files in the repo. The historical `docs/implementations/` directories (22 feature directories, each with `TASK.md` + `PLAN.md` from the old prompt system) clutter the working area with completed work.

The project now has Compound Engineering installed (v3.12.0), `STRATEGY.md` written, and `.compound-engineering/` configured. The docs and repo structure should reflect the current workflow.

## Requirements

**Removal:**
- R1. Old `.github/prompts/` directory is removed from the repo.
- R2. Old `.claude/commands/` delegating shims are removed.

**Archival:**
- R3. Historical `docs/implementations/` directories are archived under a single `archive/` subdirectory with a README explaining their provenance.

**Documentation:**
- R4. `CLAUDE.md` documents the CE skill workflow (`ce-plan`, `ce-work`, `ce-code-review`, `ce-commit-push-pr`) in place of the old prompt-based instructions.
- R5. `docs/vibe/` is rewritten to document CE conventions — skills, workflow, contribution guidelines, and examples.

**Constraints:**
- R6. No Go code, tests, CI/CD config, or build artifacts are modified.
- R7. All internal doc cross-references remain valid after the migration.

---

## Key Technical Decisions

- KTD1. **Archive location: `docs/implementations/archive/`.** Keeps historical files adjacent to their origin directory rather than introducing a new top-level `docs/archive/`. The archive carries a README explaining these are pre-CE historical records.
- KTD2. **CLAUDE.md: full rewrite of the workflow section.** Replace the entire "Claude Code Workflow" and "Available Prompts" sections with CE equivalents, preserving only coding standards, project structure, and tech stack documentation. Minimal cleanup would leave the doc split between two workflow systems.
- KTD3. **`docs/vibe/PROMPTS.md` renamed to `docs/vibe/SKILLS.md`.** The old file documented available prompts; the new file documents available CE skills with their project-specific usage.
- KTD4. **Old implementations are archived, not deleted.** They are the historical record of features built under the old workflow and may be useful for context. Deleting them would lose institutional memory.

---

## Implementation Units

### U1. Remove old prompts and .claude/commands shims

- **Goal:** Delete the old prompt files and Claude Code command shims that delegate to them.
- **Requirements:** R1, R2
- **Dependencies:** None
- **Files:**
  - Delete `.github/prompts/generate-plan-from-issue.prompt.md`
  - Delete `.github/prompts/generate-plan-local.prompt.md`
  - Delete `.github/prompts/implement-plan.prompt.md`
  - Delete `.github/prompts/create-pr.prompt.md`
  - Delete `.claude/commands/generate-plan-from-issue.md`
  - Delete `.claude/commands/generate-plan-local.md`
  - Delete `.claude/commands/implement-plan.md`
  - Delete `.claude/commands/create-pr.md`
- **Approach:** Remove the four prompt files and four command shims. If `.github/prompts/` and `.claude/commands/` become empty directories after deletion, remove the directories as well.
- **Patterns to follow:** Standard `git rm` file deletion — no special handling needed.
- **Test scenarios:**
  - Verify the deleted files no longer exist on disk.
  - Verify the parent directories are removed if empty.
  - Verify `grep -r "generate-plan\|implement-plan\|create-pr" --include="*.md" .github/ .claude/` returns no results for the old command names.
- **Verification:** `.github/prompts/` directory is gone; `.claude/commands/` directory is gone. No references to old prompt files remain in repo configuration.

### U2. Archive old implementations

- **Goal:** Move all historical `docs/implementations/<feature>/` directories into a single `docs/implementations/archive/` subdirectory with a README.
- **Requirements:** R3
- **Dependencies:** None
- **Files:**
  - Move `docs/implementations/<feature>/*` → `docs/implementations/archive/<feature>/*` (22 directories)
  - Create `docs/implementations/archive/README.md`
- **Approach:** Move all feature directories into `archive/` using `git mv` to preserve history. Write a short README in the archive directory explaining these are historical TASK+PLAN files generated under the pre-CE prompt workflow and are kept for reference.
- **Patterns to follow:** Standard `git mv` for tracking-preserving moves.
- **Test scenarios:**
  - Verify all 22 feature directories now live under `docs/implementations/archive/`.
  - Verify `docs/implementations/` contains only the `archive/` subdirectory (plus any CE plan directories created later).
  - Verify the archive README is present and explains the provenance.
- **Verification:** `ls docs/implementations/` shows only `archive/`. All historical feature directories are under `archive/`.

### U3. Update CLAUDE.md

- **Goal:** Replace the old prompt workflow documentation in `CLAUDE.md` with CE skill usage documentation.
- **Requirements:** R4, R6
- **Dependencies:** U1 (old files gone, so CLAUDE.md shouldn't reference them)
- **Files:**
  - Modify `CLAUDE.md`
- **Approach:** Replace the "Claude Code Workflow" section (lines ~116-143 in the current file) and the "Available Prompts" subsection with a new "Compound Engineering Workflow" section. The new section should document:
  - CE skills as the canonical workflow (`ce-plan` for planning, `ce-work` for implementation, `ce-code-review` for review, `ce-commit-push-pr` for shipping).
  - The mapping from old prompts to CE: `/generate-plan-from-issue` → `ce-plan` from an issue, `/generate-plan-local` → `ce-brainstorm` then `ce-plan`, `/implement-plan` → `ce-work`, `/create-pr` → `ce-commit-push-pr`.
  - `STRATEGY.md` as the product grounding document read by planning skills.
  - `docs/plans/` as the output directory for CE plans.
  - Preservation of the existing guardrails (test before PR, human review required, secrets handling).
- **Patterns to follow:** Existing CLAUDE.md section structure — keep the same heading depth, same prose style, same rule density.
- **Test scenarios:**
  - Verify `CLAUDE.md` no longer references `.github/prompts/`, `.claude/commands/`, or the old slash commands.
  - Verify `CLAUDE.md` references CE skills (`ce-plan`, `ce-work`, `ce-code-review`, `ce-commit-push-pr`).
  - Verify the coding standards, project structure, and tech stack sections remain intact.
  - Verify `grep -c "generate-plan\|implement-plan\|create-pr\|Available Prompts" CLAUDE.md` returns 0 for old workflow references.
- **Verification:** Read the updated `CLAUDE.md` — the workflow section describes CE usage, and all non-workflow content is preserved.

### U4. Rewrite docs/vibe/

- **Goal:** Replace the old Copilot/Claude Code prompt documentation with CE workflow documentation.
- **Requirements:** R5
- **Dependencies:** U1 (old prompts removed), U3 (CLAUDE.md updated, so vibe docs align)
- **Files:**
  - Rewrite `docs/vibe/README.md`
  - Rewrite `docs/vibe/CONTRIBUTING.md`
  - Rewrite `docs/vibe/USAGE.md`
  - Rename `docs/vibe/PROMPTS.md` → `docs/vibe/SKILLS.md` and rewrite
  - Rewrite `docs/vibe/EXAMPLES.md`
- **Approach:** Each file gets a focused rewrite:
  - **README.md:** Overview of the CE-driven contributor workflow. Link to `SKILLS.md`, `USAGE.md`, `EXAMPLES.md`. Reference `STRATEGY.md` and `docs/plans/` as the output surface.
  - **CONTRIBUTING.md:** Principles (iterate with `ce-plan`, validate with `make test`, safety-first with draft PRs). Recommended workflow using CE skills end-to-end. Guardrails (tests, review, secrets, scope).
  - **USAGE.md:** Quick start — `ce-brainstorm` for ideas, `ce-plan` for technical plans, `ce-work` for implementation, `ce-commit-push-pr` for shipping. Implementer checklist. Validation commands.
  - **SKILLS.md:** (was PROMPTS.md) Table of CE skills with project-specific usage notes. Include `ce-strategy`, `ce-brainstorm`, `ce-plan`, `ce-work`, `ce-code-review`, `ce-commit-push-pr`, `ce-debug`, `ce-simplify-code`.
  - **EXAMPLES.md:** Worked CE workflow examples: plan from an issue (`ce-plan` from #151), local brainstorming followed by planning, implementing a plan with `ce-work`, code review before PR.
- **Patterns to follow:** Match the existing docs/vibe/ tone — imperative, concrete, example-driven. Keep the same file names wherever possible (only PROMPTS.md is renamed).
- **Test scenarios:**
  - Verify no file references the old prompts (`.github/prompts/`) or old slash commands.
  - Verify each file references CE skills appropriate to its topic.
  - Verify `SKILLS.md` documents at least 6 CE skills with project-specific usage.
  - Verify `EXAMPLES.md` contains at least 3 worked CE workflow examples.
  - Verify all internal cross-references between vibe docs are valid.
- **Verification:** Read through all five files — they form a coherent CE contributor guide with no stale references.

### U5. Validate and verify

- **Goal:** Confirm the migration is complete and nothing is broken.
- **Requirements:** R6, R7
- **Dependencies:** U1, U2, U3, U4
- **Files:**
  - (No new files; validation-only unit)
- **Approach:**
  - Run `grep -r "generate-plan\|implement-plan\|create-pr" --include="*.md" .` from repo root to confirm no stale references remain (excluding `docs/implementations/archive/` which is intentionally historical).
  - Run `make test` to confirm no Go tests broke.
  - Run `make build` to confirm the binary still builds.
  - Review `git status` to verify the expected file changes match the plan.
- **Test scenarios:**
  - Verify `make test` passes with no regressions.
  - Verify `make build` produces a valid binary.
  - Verify no stale references to old prompts remain outside `docs/implementations/archive/`.
- **Verification:** All tests pass, build succeeds, repo is clean of old workflow artifacts.

---

## Scope Boundaries

### Deferred to Follow-Up Work

- CI/CD pipeline changes (`.github/workflows/`) — no changes needed for this migration; CI is workflow-agnostic.
- Home Assistant add-on docs (`home-assistant/addons/sbam/DOCS.md`) — these are end-user docs, not contributor docs; out of scope for this migration.

### Outside This Migration

- Adding new features or fixing bugs in Go code.
- Modifying `STRATEGY.md` (already written during `ce-strategy`).
- Changing the project Go module structure, build system, or Dockerfiles.

---

## Risks & Dependencies

- **Risk:** Internal doc cross-references may break. **Mitigation:** U5 validates with grep; any broken references surface before commit.
- **Risk:** The `.claude/commands/` directory may contain CE-managed files in the future. **Mitigation:** Only the four old shim files are removed; the directory is left in place if it contains other files (currently it does not).
- **Dependency:** None. This migration is self-contained and does not depend on any external system or upstream change.
