---
agent: "agent"
description: "Commit all changed files and open a pull request"
---

# Create PR

Commit all changed files and open a pull request for the **sbam** project (see [CLAUDE.md](../../CLAUDE.md)). The user invokes this command as:

```
/create-pr [base-branch] [--ready] [--label <labels>]
```

## Arguments

| Argument | Required | Default | Description |
|---|---|---|---|
| `base-branch` | no | `main` | The branch to merge into |
| `--ready` | no | draft | Create a PR ready for review instead of a draft |
| `--label <labels>` | no | auto-detect | Comma-separated list of GitHub labels |

## Workflow

### Step 1 — Audit current state

Run these commands in parallel:

- `git status` — list modified/untracked files (never use `-uall`)
- `git diff --stat` — size of unstaged changes
- `git log <base-branch>..HEAD --oneline` — commits on this branch not yet on base
- `git branch --show-current` — confirm branch name

**If there are no changes to commit and no commits ahead of base, stop and tell the user.**

### Step 2 — Draft a commit message

Run `git diff` and `git diff --cached` to see the full set of changes. Also list any new untracked files.

Draft a commit message following the repo convention visible in recent commits:

```
<type>(<scope>): <description>
```

Where `<type>` is one of: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `ci`, `build`.
Where `<scope>` is the affected package or area (e.g. `fronius`, `mqtt`, `cmd`, `ci`, `ha`, `make`).

### Step 3 — Stage and commit

Stage all relevant changed files explicitly by name (not `git add -A` or `git add .`):

```bash
git add <file1> <file2> ...
```

Never stage files that contain secrets (`.env`, `credentials.json`, etc.).

Commit using a heredoc:

```bash
git commit -m "$(cat <<'EOF'
<commit-message>
EOF
)"
```

If the commit hook (pre-commit) fails, fix the reported issue, re-stage, and commit again as a **new** commit — never use `--amend` after a hook failure.

### Step 4 — Determine PR parameters

Parse these from the user's `/create-pr` arguments:

- **Base branch**: if the user supplied a branch name, use it. Otherwise `main`.
- **Labels**: if the user passed `--label`, use those labels. Otherwise auto-detect:
  - Branch starts with `feat/` → `enhancement`
  - Branch starts with `fix/` → `bug`
  - Branch starts with `docs/` → `documentation`
  - Branch starts with `chore/` → `chore`
  - If no pattern matches, omit labels (leave the PR unlabeled).
- **Draft**: `--draft` by default; use `--ready` (omit `--draft`) only if the user explicitly passed `--ready`.

### Step 5 — Push

Push the branch to origin if it hasn't been pushed yet:

```bash
git push -u origin <branch-name>
```

If the branch already has an upstream, just `git push`.

### Step 6 — Create the PR

Use `gh pr create --repo atbore-phx/sbam` with the parameters from step 4.

Draft a PR body with:

- A **Summary** section (1-3 bullet points capturing the key changes)
- A **Test plan** section (checklist of manual or automated steps)

Format:

```bash
gh pr create --repo atbore-phx/sbam \
  --title "<PR title>" \
  --base "<base-branch>" \
  --head "<current-branch>" \
  [--draft] \
  [--label "<labels>"] \
  --body "$(cat <<'EOF'
## Summary
<1-3 bullet points>

## Test plan
<checklist of steps to verify the change>
EOF
)"
```

Omit `--draft` if the user passed `--ready`. Omit `--label` if no labels were determined.

### Step 7 — Report

Output the PR URL. If `gh` command fails, print the rendered PR body as a markdown block so the user can create it manually.

## Operating Rules

- Never commit secrets. Never stage `.env`, `credentials.json`, or similar files.
- Never skip hooks (`--no-verify`, `--no-gpg-sign`).
- Never amend commits unless the user explicitly asked.
- Never force-push.
- Follow the project CLAUDE.md rules.
