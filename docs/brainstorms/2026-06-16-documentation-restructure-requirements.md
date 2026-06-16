---
date: 2026-06-16
topic: documentation-restructure
---

## Summary

Replace the current split-documentation setup (README as CLI reference, DOCS.md as HA reference, standalone deep-dive files) with a single MkDocs Material site deployed to GitHub Pages, served from a `docs/` source directory. README.md shrinks to a project pitch that links to the site. DOCS.md shrinks to HA installation steps that link to the site. CHANGELOG.md stays as-is for the HA add-on store.

## Problem Frame

sbam's documentation is spread across four files with overlapping content: README.md (~250 lines, half of which is a CLI flag reference), `home-assistant/addons/sbam/DOCS.md` (HA-specific but duplicates many config option descriptions), `docs/mqtt.md` (deep MQTT reference), and `docs/prereq.md` (prerequisites). Every new config option requires updating descriptions in at least two places — README.md and DOCS.md — with slightly different framing. New users landing on the README cannot tell at a glance whether to follow the CLI path, the HA path, or both. The README's length buries the project story under reference material. The current structure has no navigation, no search, and no visual hierarchy beyond markdown headings.

## Key Decisions

- **MkDocs with Material theme** over Docusaurus or Hugo — simpler than Docusaurus (no Node.js dependency), more polished out of the box than Hugo's doc themes, and markdown-native so content stays portable.
- **DOCS.md as thin entry page** over keeping it substantial — the HA add-on store requires DOCS.md to exist, but it only needs installation screenshots, the repo URL, and a prominent link to the MkDocs site. Technical reference lives in one place.
- **Single config reference with deployment annotations** over separate CLI and HA config pages — every config option is documented once, with inline notes where CLI flag names, HA add-on YAML keys, or behavior differ.
- **GitHub Actions deployment to GitHub Pages** — MkDocs build runs on push to `main`, deploys to `gh-pages` branch. The stable URL (`atbore-phx.github.io/sbam`) becomes the link target from README.md and DOCS.md.

## Requirements

### Content migration

- R1. README.md must be rewritten to contain only the project pitch: title, one-line description, caution/important callouts, introduction story, contributions section, compound engineering section, and links to the MkDocs site. The current "Prerequisites," "Home Assistant," "MQTT Feed," and "Stand Alone" sections must be removed and their content migrated to the MkDocs site.
- R2. `home-assistant/addons/sbam/DOCS.md` must be rewritten to contain only HA-specific content: installation screenshots, repository URL, watchdog/start-on-boot instructions, and a prominent link to the MkDocs site. All configuration option descriptions must be removed and migrated to the MkDocs site.
- R3. `home-assistant/addons/sbam/CHANGELOG.md` must be updated with the v2.1.0 release notes. Existing entries for v2.0.x must be preserved. No structural changes to this file.
- R4. `docs/prereq.md` content must be migrated to the MkDocs site and the original file removed.
- R5. `docs/mqtt.md` content must be migrated to the MkDocs site and the original file removed.

### MkDocs site structure

- R6. The MkDocs source must live in a `docs/` directory at the repo root, using the Material theme with navigation, search, and a responsive layout.
- R7. The site must include the following top-level sections, each as a separate page or logical group:
  - **Prerequisites** — migrated from `docs/prereq.md` (inverter settings, Solcast setup)
  - **Installation** — HA add-on installation (migrated from DOCS.md installation steps) and Docker/standalone installation
  - **Configuration** — single config reference covering all options, with deployment annotations where CLI and HA differ
  - **MQTT Guide** — migrated from `docs/mqtt.md` (quick start, topic map, payload schemas, command examples, migration notes)
  - **CLI Reference** — migrated command usage and flags from README.md "Stand Alone" section
  - **Changelog** — release history, initially covering v2.0.0 through v2.1.0
- R8. The Configuration page must document each option once. Where the CLI flag name, environment variable, or HA add-on YAML key differ, the page must show all forms. Where behavior differs between deployment modes, the difference must be noted inline.
- R9. The site must include documentation for v2.1.0 features: forecast and consumption horizons, multi-window charging schedule with per-window configuration, and the `scheduler_mode` selector with crontab deprecation notes.

### Deployment and linking

- R10. A GitHub Actions workflow must build the MkDocs site on push to `main` and deploy to GitHub Pages at `atbore-phx.github.io/sbam`.
- R11. README.md and DOCS.md must link to the live GitHub Pages URL once the deployment workflow is active.

### Redirects and legacy file cleanup

- R12. After `docs/mqtt.md` and `docs/prereq.md` are migrated, the original files must be removed from the repo.
- R13. Any internal repo links that pointed to `docs/mqtt.md` or `docs/prereq.md` must be updated to point to the corresponding MkDocs page URLs.

## Success Criteria

- A new HA user, starting from the README, can reach installation instructions and a complete config reference without encountering duplicated or conflicting descriptions.
- Every config option is documented in exactly one place. The only permitted duplication is where CLI and HA forms of the same option carry different default values or behavioral notes.
- The README fits on one screen at typical resolution without scrolling past the project story.
- `make test` and `make build` continue to pass — no Go code changes are part of this work.

## Scope Boundaries

### Deferred for later
- Versioned documentation (v2.0, v2.1, v2.2 as separate doc sets). Single latest version for now.
- Auto-generating DOCS.md from MkDocs content.
- i18n / translations of the documentation.

### Outside this product's identity
- A full marketing website separate from the documentation site. The MkDocs site serves both purposes for now.

## Dependencies / Assumptions

- GitHub Pages must be enabled on the `atbore-phx/sbam` repository.
- The GitHub Actions deployment workflow must succeed before DOCS.md and README.md links can be finalized — link URLs depend on the live site being reachable.
- The `gh-pages` branch is assumed as the deployment target; if the repo uses a different Pages source, the workflow must adapt.

## Outstanding Questions

### Deferred to Planning
- Exact `mkdocs.yml` configuration (theme features, plugins, navigation structure).
- Whether to use `mike` for versioning in the future.
- Whether to add a link checker to CI to catch broken cross-page links.
