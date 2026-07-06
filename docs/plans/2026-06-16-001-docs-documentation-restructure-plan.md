---
title: "docs: Restructure documentation into MkDocs site on GitHub Pages"
type: docs
date: 2026-06-16
origin: docs/brainstorms/2026-06-16-documentation-restructure-requirements.md
---

## Summary

Replace the current split-documentation setup with a single MkDocs Material site deployed to GitHub Pages. README.md shrinks to a project pitch. `home-assistant/addons/sbam/DOCS.md` becomes a thin HA entry page. `docs/mqtt.md` and `docs/prereq.md` content migrates into the site and the originals are removed. A new GitHub Actions workflow deploys on push to `main`.

## Problem Frame

sbam's documentation spans four files with overlapping content. README.md (~250 lines) doubles as CLI reference. DOCS.md (~147 lines) duplicates the same config option descriptions. Every new feature requires updating at least two files with slightly different framing. New users cannot tell at a glance whether to follow the CLI or HA path. The current structure has no navigation, no search, and no visual hierarchy beyond markdown headings. The HA add-on store requires DOCS.md and CHANGELOG.md to exist, but they do not need to carry the full technical reference.

## Requirements

Requirements carried forward from the origin document (`docs/brainstorms/2026-06-16-documentation-restructure-requirements.md`).

### Content migration

- R1. README.md rewritten to project pitch only: title, one-line description, caution/important callouts, introduction story, contributions section, Compound Engineering section, and links to the MkDocs site. Current Prerequisites, Home Assistant, MQTT Feed, and Stand Alone sections removed.
- R2. `home-assistant/addons/sbam/DOCS.md` rewritten to HA-specific content only: installation screenshots, repository URL, watchdog/start-on-boot instructions, and a prominent link to the MkDocs site. All config option descriptions removed.
- R3. `home-assistant/addons/sbam/CHANGELOG.md` updated with v2.1.0 release notes. Existing v2.0.x entries preserved. No structural changes.
- R4. `docs/prereq.md` content migrated to MkDocs site; original file removed.
- R5. `docs/mqtt.md` content migrated to MkDocs site; original file removed.

### MkDocs site structure

- R6. MkDocs source lives in `docs/site/` (resolves naming conflict with existing `docs/brainstorms/`, `docs/plans/`, and `docs/implementations/`). `mkdocs.yml` at repo root. Material theme with navigation, search, and responsive layout.
- R7. Site includes top-level pages: Prerequisites, Installation (HA + Docker), Configuration, MQTT Guide, CLI Reference, Changelog, plus a landing page (`index.md`).
- R8. Configuration page documents each option once. Deployment annotations note where CLI flag name, env var, or HA add-on YAML key differ. Behavior differences between deployment modes noted inline.
- R9. Site documents v2.1.0 features: forecast and consumption horizons, multi-window charging schedule with per-window configuration, and `scheduler_mode` selector with crontab deprecation notes.

### Deployment and linking

- R10. GitHub Actions workflow builds MkDocs on push to `main` and deploys to GitHub Pages at `https://atbore-phx.github.io/sbam/`.
- R11. README.md and DOCS.md link to the live GitHub Pages URL.

### Redirects and legacy cleanup

- R12. `docs/mqtt.md` and `docs/prereq.md` removed after migration.
- R13. Internal repo links that pointed to `docs/mqtt.md` or `docs/prereq.md` updated to corresponding MkDocs page URLs.

## Key Technical Decisions

- **MkDocs source in `docs/site/` instead of `docs/` root.** The `docs/` directory already holds brainstorms, plans, implementations, and vibe files that are internal development artifacts, not user-facing documentation. Using a subdirectory keeps the two categories separated without relying on `exclude_docs` lists that need ongoing maintenance.
- **`mkdocs.yml` at repo root with `docs_dir: docs/site`.** Standard MkDocs convention keeps the config file at repo root where CI can find it without path arguments. The custom `docs_dir` points it at the subdirectory.
- **Material theme with default color palette.** No custom branding needed beyond the existing project identity. The Material theme's built-in navigation, search, and responsive layout satisfy the visual polish and usability goals from the brainstorm.
- **Single Configuration page with deployment annotations.** Each config option documented once. Where CLI and HA forms differ (flag name vs. YAML key, different defaults), the difference is noted in an inline annotation rather than splitting into separate pages. This avoids recreating the duplication problem inside MkDocs.
- **`actions/upload-pages-artifact` + `actions/deploy-pages` for deployment.** Native GitHub Actions deployment compatible with the repository's existing `build_type: workflow` Pages configuration. On `main`, the site is built, uploaded as a Pages artifact, and deployed to the production URL. On other branches, the site is built and uploaded as an artifact for preview — no deployment step runs.
- **MkDocs pages written as standalone markdown files, not generated.** Content is authored directly in markdown. No template generation or auto-extraction from code. This keeps the authoring workflow simple and the content portable.

## High-Level Technical Design

```
Repo root
├── mkdocs.yml                  # MkDocs config, docs_dir: docs/site
├── README.md                   # Project pitch only (rewritten)
├── docs/
│   ├── site/                   # MkDocs source (new)
│   │   ├── index.md            # Landing page
│   │   ├── prerequisites.md    # ← docs/prereq.md (migrated)
│   │   ├── installation.md     # HA + Docker + standalone
│   │   ├── configuration.md    # Single config reference
│   │   ├── mqtt.md             # ← docs/mqtt.md (migrated)
│   │   ├── cli.md              # ← README.md Stand Alone (migrated)
│   │   └── changelog.md        # Release history
│   ├── brainstorms/            # Unchanged (internal)
│   ├── plans/                  # Unchanged (internal)
│   ├── implementations/        # Unchanged (internal)
│   └── vibe/                   # Unchanged (internal)
├── home-assistant/addons/sbam/
│   ├── DOCS.md                 # Thin entry page (rewritten)
│   └── CHANGELOG.md            # HA add-on store changelog (updated)
└── .github/workflows/
    └── docs-deploy.yml         # New: MkDocs → GitHub Pages
```

Site navigation (MkDocs nav):

```
Home          → index.md
Prerequisites → prerequisites.md
Installation  → installation.md
Configuration → configuration.md
MQTT Guide    → mqtt.md
CLI Reference → cli.md
Changelog     → changelog.md
```

## Implementation Units

### U1. MkDocs project scaffolding

**Goal:** Create `mkdocs.yml` at repo root with Material theme, establish `docs/site/` directory, and add the landing page.

**Requirements:** R6, R7

**Dependencies:** none

**Files:**
- `mkdocs.yml` (create)
- `docs/site/index.md` (create)
- `.gitignore` (modify — add `site/` under Python/MkDocs ignores if relevant, though `docs/site/` is committed content so likely no change needed)

**Approach:** Write `mkdocs.yml` with `site_name: sbam`, `docs_dir: docs/site`, Material theme, and the navigation structure from the High-Level Technical Design. Create `docs/site/index.md` as the landing page with a brief project description and links into each section. Enable search and the standard Material features (no custom overrides).

**Patterns to follow:** Standard MkDocs Material project layout. No existing MkDocs patterns in this repo.

**Test scenarios:**
- Run `mkdocs build --strict` from repo root — must exit zero with no warnings.
- Run `mkdocs serve` and verify the landing page renders with navigation sidebar and all seven nav entries (even if target pages are stubs).
- Verify search index is generated in the built site.

**Verification:** `mkdocs build --strict` succeeds. `mkdocs serve` shows a navigable site with the correct nav structure and Material theme styling.

---

### U2. Migrate all documentation content

**Goal:** Write all MkDocs content pages migrating material from the existing documentation files.

**Requirements:** R1, R2 (content removal side), R4, R5, R7, R8, R9

**Dependencies:** U1 (needs `mkdocs.yml` nav and `docs/site/` directory to know page structure)

**Files:**
- `docs/site/prerequisites.md` (create — migrate from `docs/prereq.md`)
- `docs/site/installation.md` (create — migrate HA install from DOCS.md, Docker/standalone from README.md)
- `docs/site/configuration.md` (create — single config reference, merge from README.md flags and DOCS.md options)
- `docs/site/mqtt.md` (create — migrate from `docs/mqtt.md`)
- `docs/site/cli.md` (create — migrate from README.md Stand Alone section)
- `docs/site/changelog.md` (create — consolidate release history)

**Approach:** Each page is a direct content migration, not a rewrite. Preserve all technical details (payload schemas, command examples, flag listings, topic tables). For `configuration.md`, build a single table with columns: Option name, Description, CLI flag, Env var, HA add-on YAML key, Default. Where CLI and HA forms differ, add an inline annotation. Document v2.1.0 features (horizons, multi-window, scheduler_mode) inline in the relevant config options and in the changelog page. Keep all existing images and screenshots; copy them to `docs/site/assets/` and update image paths.

**Patterns to follow:** Existing `docs/mqtt.md` structure (quick start → reference → examples) for the MQTT page. DOCS.md prose style for the installation page.

**Test scenarios:**
- Each page renders in `mkdocs serve` without broken images or formatting issues.
- The Configuration page lists every config option from the HA add-on's `config.yaml` schema and every CLI flag from the current README.md `schedule` command output. No option is documented in two different ways on the same page.
- v2.1.0 features appear in the Configuration page (horizons, windows, scheduler_mode) and the Changelog page.
- All migrated images display correctly when the site is served locally.
- MQTT payload examples, topic map table, and command examples render correctly in the MkDocs page.

**Verification:** `mkdocs build --strict` succeeds with no warnings. Manual visual review of each page confirms content fidelity against the source files.

---

### U3. GitHub Actions Pages deployment workflow

**Goal:** Create a workflow that builds the MkDocs site. On push to `main`, deploys to GitHub Pages production. On push to other branches, deploys an ephemeral preview to Surge.sh.

**Requirements:** R10

**Dependencies:** none (can run in parallel with U1 and U2)

**Files:**
- `.github/workflows/docs-deploy.yml` (create)
- `docs/site/requirements.txt` (create — pin `mkdocs-material` version)

**Approach:** Workflow triggers on `push` with `paths:` filter for `docs/site/**` and `mkdocs.yml`. Two jobs: `build` (always) and `deploy` (main only, protected `github-pages` environment). Build steps: checkout → `actions/setup-python@v5` (Python 3.12) → `pip install -r docs/site/requirements.txt` → `mkdocs build --strict` → Surge.sh deploy (non-main only, uses `npx surge` with branch-slug subdomain) → `actions/upload-pages-artifact@v3` (always). Deploy job: `actions/deploy-pages@v4` (main only). Branch names are sanitized to subdomain-safe slugs. Requires two GitHub secrets: `SURGE_TOKEN` (from `npx surge token`) and `SURGE_DOMAIN` (base domain, e.g. `sbam-docs.surge.sh`). Pin `mkdocs-material` in `docs/site/requirements.txt` for reproducible builds.

**Patterns to follow:** Existing `.github/workflows/test.yml` for checkout and tool-setup patterns. Existing `.github/workflows/release.yml` for the `permissions:` block pattern.

**Test scenarios:**
- Push to a non-`main` branch with changes to `docs/site/**` — workflow triggers, builds, deploys preview to `<branch-slug>.<SURGE_DOMAIN>`, uploads artifact. The deploy job is skipped (environment protection).
- Push to `main` with only Go code changes — workflow does not trigger (paths filter).
- Push to `main` with a change to `docs/site/index.md` — workflow triggers, builds, deploys preview (then overwritten by Pages deploy), uploads artifact, deploys to Pages.
- `mkdocs build --strict` failure blocks all downstream steps.
- Missing `SURGE_TOKEN` or `SURGE_DOMAIN` secret causes Surge step to fail on non-main branches (expected — secrets must be configured first).

**Verification:** After merge to `main`, the site is reachable at `https://atbore-phx.github.io/sbam/`. Branch previews are reachable at `https://<branch-slug>.<SURGE_DOMAIN>/`.

---

### U4. Rewrite README.md

**Goal:** Slim README.md to a project pitch that links to the MkDocs site.

**Requirements:** R1, R11

**Dependencies:** U2 (needs to know the MkDocs page URLs for linking; predictable from nav structure), U3 (needs the confirmed GitHub Pages base URL — `https://atbore-phx.github.io/sbam/` is predictable and can be used speculatively)

**Files:**
- `README.md` (modify)

**Approach:** Remove the following sections and their content: Prerequisites, Home Assistant, MQTT Feed, Stand Alone (including all CLI flag listings for `configure`, `estimate`, `schedule`), Debug Logs, Log Format, Config file and env vars, the charge windows YAML examples, and the crontab/forecast/consumption horizon documentation. Replace each removed section with a brief one-line link to the corresponding MkDocs page. Keep: title, badges, caution/important callouts, introduction story, contributions section, sponsor section, Compound Engineering section. The result should be ~60-80 lines, fitting on one screen.

**Patterns to follow:** Current README.md prose style for the kept sections. Link format: `[section name](https://atbore-phx.github.io/sbam/page-slug/)`.

**Test scenarios:**
- README.md renders on GitHub without broken links or formatting issues.
- All removed content exists in equivalent form on the MkDocs site (cross-reference with U2 pages).
- The README fits on one screen at 1080p resolution (~80 lines).
- All MkDocs links use the correct GitHub Pages URL.

**Verification:** Visual review on GitHub. Click-through of each MkDocs link confirms the target page exists.

---

### U5. Rewrite DOCS.md and update CHANGELOG.md

**Goal:** Rewrite `home-assistant/addons/sbam/DOCS.md` to a thin HA entry page. Add v2.1.0 release notes to `home-assistant/addons/sbam/CHANGELOG.md`.

**Requirements:** R2, R3, R11

**Dependencies:** U2 (needs MkDocs page URLs for linking)

**Files:**
- `home-assistant/addons/sbam/DOCS.md` (modify)
- `home-assistant/addons/sbam/CHANGELOG.md` (modify)

**Approach:** DOCS.md: Keep installation screenshots, repository URL, and watchdog/start-on-boot instructions. Replace the full configuration options listing with a single link to the MkDocs Configuration page. Replace the MQTT integration section with a link to the MkDocs MQTT Guide page. Result ~30-40 lines. CHANGELOG.md: Move the current Unreleased section content into a new `## What's New in v2.1.0` section (release date TBD). Preserve existing v2.0.x entries. Update the MQTT docs link to point to the MkDocs page.

**Patterns to follow:** Current DOCS.md prose style for the kept sections. Current CHANGELOG.md formatting conventions (`## What's New in vX.Y.Z`, `### Feature name`).

**Test scenarios:**
- DOCS.md renders on GitHub without broken links or formatting issues.
- All installation screenshots still display correctly.
- CHANGELOG.md shows v2.1.0 section with multi-window, horizons, scheduler_mode, and crontab validation fix entries — grouped under appropriate sub-headings following the existing format.
- Links in both files point to correct MkDocs page URLs.

**Verification:** Visual review of both files on GitHub. DOCS.md is under 50 lines. CHANGELOG.md follows the existing format.

---

### U6. Remove legacy files and update cross-links

**Goal:** Remove the migrated standalone docs and update any remaining internal links to point to the MkDocs site.

**Requirements:** R4, R5, R12, R13

**Dependencies:** U2 (content migrated), U4 (README.md rewritten), U5 (DOCS.md and CHANGELOG.md updated)

**Files:**
- `docs/mqtt.md` (delete)
- `docs/prereq.md` (delete)
- `CLAUDE.md` (modify — update project structure tree to reflect removed files and new `docs/site/` directory)

**Approach:** Delete `docs/mqtt.md` and `docs/prereq.md`. Search the repo for any remaining references to these files (grep for `docs/mqtt.md` and `docs/prereq.md`) and update them to the corresponding MkDocs page URLs. Archived implementation plans under `docs/implementations/archive/` that reference these files should also be updated to preserve link integrity. Update `CLAUDE.md` project structure to add `docs/site/` entries and remove the standalone files.

**Patterns to follow:** `CLAUDE.md` project structure conventions.

**Test scenarios:**
- `grep -r "docs/mqtt.md"` across the repo returns no results.
- `grep -r "docs/prereq.md"` across the repo returns no results.
- `CLAUDE.md` project structure reflects the current state: `docs/site/` listed, `docs/mqtt.md` and `docs/prereq.md` removed.
- `mkdocs build --strict` still succeeds (no broken internal links from deleted content).

**Verification:** No references to deleted files remain in the repo. `mkdocs build --strict` passes. `make test` and `make build` still pass (confirms no unintended side effects).

---

## Scope Boundaries

### Deferred to Follow-Up Work

- Adding `mike` for versioned documentation (v2.0, v2.1, v2.2 as separate doc sets).
- Auto-generating DOCS.md from MkDocs content.
- i18n / translations of the documentation.
- Adding a link checker to CI to catch broken cross-page links.

### Deferred for Later (from origin)

- Versioned documentation via `mike`.
- Auto-generating DOCS.md from MkDocs content.
- i18n / translations of the documentation.

### Outside This Product's Identity (from origin)

- A full marketing website separate from the documentation site. The MkDocs site serves both purposes for now.

## Risks & Dependencies

- **GitHub Pages is already configured with `build_type: workflow`** on this repository. No settings change is required — the U3 workflow uses the native `actions/deploy-pages` path which is compatible with the current configuration.
- **`docs/site/` adds a nesting level.** Existing `docs/` directory already has 4 subdirectories. Adding `docs/site/` means maintainers need to know which files are internal vs. public. The separation by directory (public in `site/`, internal at `docs/` root) makes this explicit, but it is a new convention.
- **GitHub Pages URL may differ from the predicted `atbore-phx.github.io/sbam`.** If a custom domain is configured, all hard-coded links in README.md and DOCS.md need updating. The predicted URL is the standard for `<org>.github.io/<repo>` and will be correct if no custom domain is set.
- **`mkdocs-material` version pin.** A pinned version in the requirements file prevents surprise CI breakage from upstream changes but requires periodic bumping. Dependabot can handle this if configured for pip.

## Open Questions

### Deferred to Implementation

- Whether to add Dependabot configuration for pip/mkdocs-material updates.
- Whether the MkDocs site needs a favicon or custom logo beyond the Material theme defaults.
- Exact wording of Deployment annotations on the Configuration page — depends on which options actually differ between CLI and HA.

## Sources / Research

- Origin requirements: `docs/brainstorms/2026-06-16-documentation-restructure-requirements.md`
- Current README.md, `home-assistant/addons/sbam/DOCS.md`, `docs/mqtt.md`, `docs/prereq.md` — content sources for migration
- `home-assistant/addons/sbam/config.yaml` — authoritative list of HA add-on config keys and defaults
- Existing GitHub Actions workflows: `.github/workflows/test.yml`, `.github/workflows/release.yml` — patterns for checkout, tool setup, and branch filtering
- `CLAUDE.md` — project structure conventions for documentation
- Repository research (ce-repo-research-analyst) confirms no existing MkDocs or Pages deployment infrastructure
- Learnings research (ce-learnings-researcher) surfaced the `docs/` naming conflict and the issue #91 documentation drift risk
