# Feature: Create HA Docker Image Workflow for Dev Release

> Slug: `local-create-ha-docker-image-workflow-for-dev-release` · Created: 2026-05-28

## Summary

A new GitHub Actions workflow that builds and pushes the Home Assistant add-on Docker image (multi-arch: amd64, arm64) to ghcr.io for development and testing on non-release branches. Also modify the existing release workflow to tag pre-releases as `pre-release` instead of `dev`.

## Motivation / User Story

As a developer, I need to test HA add-on Docker image builds on feature/fix branches before cutting a release. Currently the only way to build the HA Docker image is through the release workflow which requires a git tag. A dedicated dev-image workflow enables rapid iteration and testing of Docker image changes without polluting the release pipeline.

## Scope

- In scope:
  - New `.github/workflows/dev-image.yml` workflow
  - Multi-arch build (amd64, aarch64) using `home-assistant/builder/actions/build-image`
  - Build the Go binary via GoReleaser (same pattern as `release.yml`)
  - Push images to ghcr.io with normalized branch-name tags
  - Modify `release.yml` pre-release tag from `dev` to `pre-release`
- Out of scope:
  - Building the standalone root `Dockerfile`
  - Publishing to Docker Hub
  - Modifying any Go source code
  - Changes to the HA add-on `config.json` or `run.sh`

## Functional Requirements

1. **Trigger rules**:
   - Push to `release/*` branches: automatic on every push
   - Any other branch (feat/*, fix/*, chore/*, etc.): only when commit message starts with `[build-ha]`
   - Manual trigger via `workflow_dispatch` (with branch selector)
2. **Build**: multi-arch build for both `amd64` and `arm64` (aarch64), matching the existing `release.yml` matrix pattern
3. **Push**: to `ghcr.io/atbore-phx/ha-{arch}-sbam` (same image name as release)
4. **Tags**: `<normalized-branch-name>-<short-sha>` and `<normalized-branch-name>-latest`
5. **Branch name normalization**: replace non-allowed Docker tag characters (slashes, underscores) with hyphens, lowercase

## Non-functional Requirements

- Backward compatibility: new workflow only; existing release workflow unchanged except for the tag rename
- Safety: must not interfere with the release workflow; no changes to Go code
- Idempotent: each push overwrites the `*-latest` tag for that branch

## Configuration Impact

- New file: `.github/workflows/dev-image.yml`
- Modified file: `.github/workflows/release.yml` — change `dev` → `pre-release` in the pre-release tag logic

## External Integrations Touched

- GitHub Container Registry (ghcr.io) — push target
- `home-assistant/builder/actions/build-image@2026.03.2` — HA add-on image builder action

## Acceptance Criteria

- [ ] A push to a `release/*` branch automatically builds and pushes HA Docker images
- [ ] A push to `feat/foo` with commit message `[build-ha] add stuff` triggers the workflow
- [ ] A push to `fix/bar` without `[build-ha]` in commit message does NOT trigger the workflow
- [ ] `workflow_dispatch` via GitHub UI triggers the workflow on the selected branch
- [ ] Images are pushed with tags: `<normalized-branch>-<sha>` and `<normalized-branch>-latest`
- [ ] Both `amd64` and `aarch64` architectures are built and pushed
- [ ] Release workflow now tags pre-releases as `pre-release` instead of `dev`

## Test Strategy

- Manual verification on a test branch (no automated GH Actions workflow testing):
  - Push to a `feat/*` branch without `[build-ha]` prefix — verify workflow does not run
  - Push with `[build-ha]` prefix — verify workflow runs and images appear in ghcr.io
  - Run `workflow_dispatch` from GitHub UI — verify it triggers
  - Inspect ghcr.io package tags for correct naming

## Risks / Open Questions

- The `home-assistant/builder/actions/build-image` action requires a Go binary present in `home-assistant/addons/sbam/bin/sbam` before building — must mirror the GoReleaser build step from `release.yml`
- Branch name sanitization edge cases: very long branch names, branch names with special characters

## References

- `.github/workflows/release.yml` — existing release workflow to mirror pattern
- `home-assistant/addons/sbam/Dockerfile` — HA add-on Dockerfile
- `home-assistant/addons/sbam/config.json` — add-on config with image reference
- `home-assistant/builder/actions/build-image@2026.03.2` — HA builder action
- Issue [#155](https://github.com/atbore-phx/sbam/issues/155) — GitHub feature request
