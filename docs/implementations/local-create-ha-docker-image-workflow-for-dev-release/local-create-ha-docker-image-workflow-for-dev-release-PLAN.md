# Plan: Create HA Docker Image Workflow for Dev Release

> Date: 2026-05-28 · Issue: [#155](https://github.com/atbore-phx/sbam/issues/155) · TASK: [TASK](local-create-ha-docker-image-workflow-for-dev-release-TASK.md)

## 1. Task Analysis

**Goal**: Create a CI workflow that builds and pushes the Home Assistant add-on Docker image (multi-arch) to ghcr.io for development/testing, and rename the pre-release tag in the existing release workflow.

**Non-goals**: No Go code changes, no standalone Dockerfile builds, no Docker Hub publishing.

**Acceptance criteria**:
- Push to `release/*` auto-builds HA Docker images
- Push to any other branch with commit message starting with `[build-ha]` triggers the build
- Push without `[build-ha]` does not trigger
- `workflow_dispatch` works from GitHub UI
- Images tagged `<normalized-branch>-<sha>` and `<normalized-branch>-latest`
- Both `amd64` and `aarch64` built and pushed
- Release workflow tags pre-releases as `pre-release` instead of `dev`

## 2. Current State

- `.github/workflows/release.yml` — builds and pushes HA Docker images only when a git tag is pushed. Pre-releases get tagged `dev`.
- `home-assistant/addons/sbam/Dockerfile` — HA add-on Dockerfile (multi-stage, just copies `run.sh` and `bin/sbam`)
- `home-assistant/addons/sbam/config.json` — add-on config pointing image to `ghcr.io/atbore-phx/ha-{arch}-sbam`
- No workflow exists for dev/pre-release Docker image builds from branches

## 3. Target Architecture

One new workflow file and one tiny modification to the existing release workflow:

```
.github/workflows/
  dev-image.yml     (NEW)   — build & push HA Docker image for dev branches
  release.yml       (MOD)   — dev → pre-release tag rename
  test.yml                   (unchanged)
```

**Data flow**:
```
Branch push or workflow_dispatch
  → trigger dev-image.yml
  → build Go binary via GoReleaser (per arch)
  → build HA Docker image via home-assistant/builder
  → push to ghcr.io/atbore-phx/ha-{arch}-sbam with branch-name tags
```

## 4. Dependency Choices

All actions already used elsewhere in the repo — no new dependencies:
- `actions/checkout@v6`
- `actions/setup-go@v6`
- `goreleaser/goreleaser-action@v7`
- `home-assistant/builder/actions/build-image@2026.03.2`

## 5. Configuration Changes

| File | Change |
|---|---|
| `.github/workflows/dev-image.yml` | New — full workflow definition |
| `.github/workflows/release.yml` | Line 104: `dev` → `pre-release` |

No new config keys, env vars, CLI flags, or HA add-on schema changes.

## 6. Implementation Blueprint

### Step 1 — Create `.github/workflows/dev-image.yml`

```yaml
name: Dev Image

on:
  push:
  workflow_dispatch:

permissions:
  packages: write

jobs:
  build-ha:
    if: |
      (github.event_name == 'workflow_dispatch') ||
      (startsWith(github.ref_name, 'release/')) ||
      (github.event.head_commit != null &&
       startsWith(github.event.head_commit.message, '[build-ha]'))
    runs-on: ${{ matrix.runs-on }}
    strategy:
      matrix:
        goarch:
          - amd64
          - arm64
        goos:
          - linux
        include:
          - goarch: amd64
            haarch: amd64
            platform: linux/amd64
            runs-on: ubuntu-24.04
          - goarch: arm64
            haarch: aarch64
            platform: linux/arm64
            runs-on: ubuntu-24.04-arm

    steps:
      - name: Checkout
        uses: actions/checkout@v6
        with:
          fetch-depth: 0

      - name: Normalize branch name for Docker tags
        id: branch
        run: |
          BRANCH_NAME="${GITHUB_REF_NAME}"
          SANITIZED=$(echo "${BRANCH_NAME}" | sed 's/[^a-zA-Z0-9._-]/-/g' | tr '[:upper:]' '[:lower:]')
          SHORT_SHA="${GITHUB_SHA:0:7}"
          echo "name=${SANITIZED}" >> "$GITHUB_OUTPUT"
          echo "short-sha=${SHORT_SHA}" >> "$GITHUB_OUTPUT"

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version: '1.26'

      - name: GoReleaser Install
        uses: goreleaser/goreleaser-action@v7
        with:
          install-only: true

      - name: GoReleaser Build ${{matrix.goos}}, ${{matrix.goarch}}
        run: |
          mkdir -p home-assistant/addons/sbam/bin
          goreleaser build --clean --single-target --output home-assistant/addons/sbam/bin/sbam
        env:
          GOOS: ${{matrix.goos}}
          GOARCH: ${{matrix.goarch}}
          CGO_ENABLED: 0

      - name: Build add-on image ${{ matrix.haarch }}
        uses: home-assistant/builder/actions/build-image@2026.03.2
        with:
          arch: ${{ matrix.goarch }}
          image: ghcr.io/${{ github.repository_owner }}/ha-${{ matrix.haarch }}-sbam
          image-tags: |
            ${{ steps.branch.outputs.name }}-${{ steps.branch.outputs.short-sha }}
            ${{ steps.branch.outputs.name }}-latest
          version: ${{ steps.branch.outputs.name }}-${{ steps.branch.outputs.short-sha }}
          context: home-assistant/addons/sbam
          cosign: "false"
          container-registry-password: ${{ secrets.GITHUB_TOKEN }}
          push: "true"
          build-args: |
            BUILD_FROM=ghcr.io/home-assistant/${{ matrix.haarch }}-base
            PLATFORM=${{ matrix.platform }}
```

### Step 2 — Modify `.github/workflows/release.yml`

Change line 104 from `dev` to `pre-release`:

```yaml
# Before:
            ${{ needs.determine-release.outputs.release == 'latest' && 'latest' || 'dev' }}
# After:
            ${{ needs.determine-release.outputs.release == 'latest' && 'latest' || 'pre-release' }}
```

## 7. Test Plan

Testing is manual since this is a CI workflow change:

| Scenario | Action | Expected result |
|---|---|---|
| Push without `[build-ha]` on `feat/foo` | Push a regular commit | Workflow does not run |
| Push with `[build-ha]` on `feat/foo` | Push commit starting with `[build-ha]` | Workflow runs, images appear in ghcr.io with `feat-foo-<sha>` and `feat-foo-latest` tags |
| Push to `release/test` | Push any commit | Workflow runs automatically |
| Manual trigger | `workflow_dispatch` via GitHub UI | Workflow runs on selected branch |
| Release tag push | Push a pre-release tag | `release.yml` tags image with `pre-release` (not `dev`) |

## 8. Validation Gates

- `make test` — ensure no Go code regressions (not expected, but gate)
- `make build` — ensure Go build still works
- Verify YAML syntax: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/dev-image.yml'))"` or VS Code YAML linting

## 9. Rollout / Backward Compatibility

- New workflow has no backward compatibility impact
- `release.yml` change: pre-release tag changes from `dev` to `pre-release`. Any consumer referencing the `dev` tag must update to `pre-release`. Document in the next release CHANGELOG.

## 10. Security Considerations

- Registry auth uses `secrets.GITHUB_TOKEN` (auto-generated, scoped to the workflow run) — same pattern as `release.yml`
- No user-facing secrets handled in the new workflow
- Branch name sanitization prevents Docker tag injection (replaces special chars)

## 11. Gotchas

- `github.event.head_commit` is null for `workflow_dispatch` events and branch deletions — the `if` condition guards against this with the `github.event_name == 'workflow_dispatch'` short-circuit
- The `home-assistant/builder/actions/build-image` action expects the Go binary at `home-assistant/addons/sbam/bin/sbam` before it runs — the GoReleaser step must come first
- `mkdir -p` (not bare `mkdir`) in the GoReleaser build step to avoid failures if the directory already exists (cf. `release.yml` uses bare `mkdir`)
- Branch names containing `/` (e.g. `feat/foo`) will be sanitized to `feat-foo` for Docker tag compliance

## 12. Open Questions / Risks

| Question/Risk | Status |
|---|---|
| Very long branch names could exceed Docker tag length limit (128 chars) | ACCEPTED — branch names in practice are <50 chars |
| `home-assistant/builder` action version `2026.03.2` may have different behavior in the future | DEFERRED — pinned to current version like `release.yml` |

## 13. Confidence Score

**9/10** — The pattern closely mirrors the existing `release.yml` release-ha job. The only novel parts are the trigger conditions and tag naming, both straightforward. Risk area is the `if` condition handling edge cases (null `head_commit`), which is explicitly guarded.
