# PLAN: Fix ARM64/aarch64 Docker Image

> Slug: `140-issue-fix-arm64-build` · Date: 2026-05-26 · Updated: 2026-05-26
> TASK: [140-issue-fix-arm64-build-TASK.md](./140-issue-fix-arm64-build-TASK.md)
> Issue: [#140](https://github.com/atbore-phx/sbam/issues/140)

## Task Analysis

**Goal**: Fix the release workflow so the aarch64 Docker image is built with a correct `linux/arm64` manifest and is pullable on ARM64 hardware.

**Non-goals**: Changes to Go application code, multi-arch manifest lists, switching away from `home-assistant/builder`, supporting 32-bit architectures.

**Acceptance criteria**:
- [ ] Release workflow succeeds for amd64 and aarch64 matrix entries
- [ ] `docker pull ghcr.io/atbore-phx/ha-aarch64-sbam:<version>` succeeds on ARM64
- [ ] `docker manifest inspect` shows `linux/arm64` for aarch64, `linux/amd64` for amd64
- [ ] amd64 image continues to work

## Current State

The v2.0.0 release upgraded from `home-assistant/builder@2024.08.2` to `home-assistant/builder/actions/build-image@2026.03.2`. The old builder worked because it:

1. Ran inside a `--privileged` container with Docker socket access
2. Installed QEMU on the host via `multiarch/qemu-user-static --reset` (for `RUN` instructions)
3. Explicitly passed `--platform` to `docker buildx build` (for correct manifest labels)

The new `build-image@2026.03.2` composite action does neither: no QEMU setup, no `platforms:` passthrough to `docker/build-push-action@v7.2.0`. Buildx defaults to the runner's native architecture for the output manifest.

**Tested and ruled out**: `FROM --platform=linux/arm64` in the Dockerfile does NOT set the output manifest platform. Tested with pre-release `v2.0.1-alpha-issue-140.0` — the aarch64 image still showed `linux/amd64`.

## Decision: Native ARM64 Runner + Drop 32-bit

Since `build-image@2026.03.2` lacks a `platforms:` input and `FROM --platform` does not control the output manifest, the only way to get a correct `linux/arm64` manifest while keeping `build-image` is to run the aarch64 build on a native ARM64 runner. Buildx defaults to the host arch, so on `ubuntu-24.04-arm` it produces `linux/arm64`.

32-bit architectures (i386, armv7) are dropped because GitHub has no native 32-bit runners, and adding QEMU + `--platform` requires bypassing `build-image`.

## Target Architecture

```
release.yml matrix
  ┌──────────┬──────────┬──────────────────┬────────────────────┐
  │ goarch   │ haarch   │ platform         │ runs-on            │
  ├──────────┼──────────┼──────────────────┼────────────────────┤
  │ amd64    │ amd64    │ linux/amd64      │ ubuntu-latest      │
  │ arm64    │ aarch64  │ linux/arm64      │ ubuntu-24.04-arm   │
  └──────────┴──────────┴──────────────────┴────────────────────┘

goarch  → arch: parameter to build-image, Go cross-compile target
haarch  → image name suffix (ha-{haarch}-sbam), BUILD_FROM base image
platform → Dockerfile FROM --platform= value (controls base image pull)
runs-on → GitHub Actions runner label (controls output manifest arch)
```

## Files Modified

| File | Change |
|------|--------|
| `.github/workflows/release.yml` | Matrix reduced to amd64 + arm64, `runs-on` from matrix, removed GOARM, removed 32-bit entries |
| `home-assistant/addons/sbam/Dockerfile` | Removed two `RUN chmod` lines |
| `home-assistant/addons/sbam/config.json` | `arch` reduced to `["amd64", "aarch64"]` |
| `home-assistant/addons/sbam/README.md` | Removed i386 and armv7 badges |

## Implementation Blueprint

### Step 1 — Remove RUN instructions from Dockerfile ✓

**File**: `home-assistant/addons/sbam/Dockerfile`

Removed `RUN chmod a+x /run.sh` and `RUN chmod a+x /usr/bin/sbam`. The base image is foreign-architecture on the amd64 runner, so executing `/bin/chmod` would require QEMU. Replaced by a workflow `chmod` step before COPY.

### Step 2 — Add chmod workflow step ✓

**File**: `.github/workflows/release.yml`

```yaml
- name: Make scripts and binary executable
  run: chmod -R a+x home-assistant/addons/sbam/
```

### Step 3 — Matrix: add runs-on, remove 32-bit ✓

**File**: `.github/workflows/release.yml`

```yaml
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
        runs-on: ubuntu-latest
      - goarch: arm64
        haarch: aarch64
        platform: linux/arm64
        runs-on: ubuntu-24.04-arm
```

Removed `386`, `arm` from goarch list. Removed corresponding include entries. Removed `GOARM: 7` env var (only needed for armv7).

### Step 4 — Fix arch and platform parameters ✓

```yaml
arch: ${{ matrix.goarch }}        # was: haarch
PLATFORM=${{ matrix.platform }}   # was: goarch (bare)
```

### Step 5 — Update config.json ✓

```json
"arch": ["amd64", "aarch64"]
```

### Step 6 — Update README.md ✓

Removed i386 and armv7 shield badges and links.

## Test Plan

### Pre-release smoke test

1. Push a pre-release tag: `v2.0.1-alpha-issue-140.1`
2. Wait for the release workflow to complete on both runners
3. Inspect manifests:

```bash
docker manifest inspect ghcr.io/atbore-phx/ha-amd64-sbam:v2.0.1-alpha-issue-140.1 | \
  jq '.manifests[] | select(.platform.architecture != "unknown") | .platform'
docker manifest inspect ghcr.io/atbore-phx/ha-aarch64-sbam:v2.0.1-alpha-issue-140.1 | \
  jq '.manifests[] | select(.platform.architecture != "unknown") | .platform'
```

4. On ARM64 HAOS: `docker pull ghcr.io/atbore-phx/ha-aarch64-sbam:v2.0.1-alpha-issue-140.1`

### Expected results

| Image | Expected platform |
|-------|------------------|
| ha-amd64-sbam | `{"architecture":"amd64","os":"linux"}` |
| ha-aarch64-sbam | `{"architecture":"arm64","os":"linux"}` |

### Failure case

If the ARM64 runner is unavailable (queued), the job will wait. GitHub's `ubuntu-24.04-arm` is GA and broadly available, so this is unlikely.

## Rollout / Backward Compatibility

- **Dockerfile**: Removal of `RUN chmod` is safe — files are made executable by workflow step before `COPY`. Identical result.
- **release.yml**: Only the release pipeline changes. amd64 build unchanged (still `ubuntu-latest`).
- **config.json**: 32-bit architectures removed. Existing 32-bit v1.6.0 users will not see updates.
- **README**: Updated architecture badges.
- **CHANGELOG**: Entry needed noting ARM64 fix and 32-bit deprecation.

## Security Considerations

None. Build pipeline fix only.

## Confidence Score

**9/10** — Native ARM64 runner guarantees `linux/arm64` manifest (buildx defaults to host arch). The approach is deterministic: there's no guessing about BuildKit behavior. The only risk is ARM64 runner availability, which is minimal for a GA feature.

## Open Questions

1. Should we open a PR to `home-assistant/builder` to add a `platforms` input passthrough? (Deferred — evaluate later)
2. Should we add an ARM64 runner to the test workflow as well? (Deferred — release fix is the priority)
