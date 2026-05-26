# Feature: Fix ARM64/aarch64 Docker Image for v2.0.0

> Slug: `140-issue-fix-arm64-build` · Created: 2026-05-26 · Updated: 2026-05-26
> Source issue: [#140](https://github.com/atbore-phx/sbam/issues/140)
> Fetched: 2026-05-26

## Summary

Users on ARM64/aarch64 (e.g., Home Assistant OS on Raspberry Pi 4/5, generic-aarch64) cannot install or upgrade to sbam v2.0.0. Docker pull fails with "no matching manifest for linux/arm64 in the manifest list entries" for `ghcr.io/atbore-phx/ha-aarch64-sbam:2.0.0`. The root cause is that `build-image@2026.03.2` does not pass `platforms:` to `docker/build-push-action`, so all images are labeled `linux/amd64` regardless of target architecture.

## Motivation / User Story

As an sbam user running Home Assistant on ARM64 hardware (Raspberry Pi, ODROID, etc.), I need to upgrade to v2.0.x to get the latest features and fixes. Currently, the Docker image for aarch64 has the wrong platform label, blocking all ARM64 users from the update.

## Scope

- In scope: Fix the release workflow so `ghcr.io/atbore-phx/ha-aarch64-sbam` images are built with a valid linux/arm64 manifest using a native ARM64 GitHub runner
- In scope: Drop 32-bit architecture support (i386, armv7) — no native GitHub runners exist for these, and the `build-image` action cannot cross-compile without QEMU, which is not worth the complexity
- In scope: Remove `RUN chmod` from Dockerfile (replaced by workflow step) to eliminate QEMU dependency
- In scope: Update `config.json` and `README.md` to reflect amd64 + aarch64 only
- Out of scope: Changes to the sbam Go application code itself
- Out of scope: Multi-arch manifest lists
- Out of scope: Upstream PR to `home-assistant/builder` (deferred)

## Functional Requirements

- The release workflow must build and push valid images for amd64 and aarch64 with correct platform manifests
- The aarch64 image must be pullable by Docker on real ARM64 hardware
- The amd64 build must continue to work unchanged

## Non-functional Requirements

- Backward compatibility: existing amd64 image must remain unaffected
- Safety / defaults: no changes to default build behavior outside the release pipeline
- Performance: build time should not increase — ARM64 runner is similar speed to amd64

## Configuration Impact

- New CLI flags: none
- New config keys (`config.yaml`): none
- New env vars: none
- Home Assistant add-on schema (`config.json`): removed `i386` and `armv7` from `arch` array
- CI/CD: `release.yml` now uses `${{ matrix.runs-on }}` with `ubuntu-24.04-arm` for aarch64
- Dockerfile: removed `RUN chmod` instructions

## External Integrations Touched

- GitHub Container Registry (ghcr.io): image push target
- `home-assistant/builder/actions/build-image@2026.03.2`: retained, but arch naming fixed
- GitHub Actions runners: now uses `ubuntu-24.04-arm` for aarch64 builds

## Acceptance Criteria

- [ ] Release workflow build succeeds for amd64 and aarch64 matrix entries
- [ ] `docker pull ghcr.io/atbore-phx/ha-aarch64-sbam:<version>` succeeds on ARM64 hardware
- [ ] `docker manifest inspect ghcr.io/atbore-phx/ha-aarch64-sbam:<version>` shows `linux/arm64`
- [ ] `docker manifest inspect ghcr.io/atbore-phx/ha-amd64-sbam:<version>` shows `linux/amd64`
- [ ] `config.json` arch field is `["amd64", "aarch64"]`

## Test Strategy

- Unit tests: N/A (CI/CD workflow fix)
- Integration: push a pre-release tag and verify manifests for both architectures
- On-device: pull the aarch64 image on HAOS ARM64 instance
- Edge cases: verify both `latest` and versioned tags are pushed correctly

## Root Cause Analysis

The v2.0.0 release switched from `home-assistant/builder@2024.08.2` to `home-assistant/builder/actions/build-image@2026.03.2`. The old builder ran inside a `--privileged` container that installed QEMU via `multiarch/qemu-user-static --reset` and explicitly passed `--platform` to `docker buildx build`. The new `build-image` action is a composite action that:

1. Calls `docker/build-push-action@v7.2.0` **without a `platforms:` parameter** — buildx defaults to the runner's native arch
2. Uses the `arch:` input only for OCI labels and `BUILD_ARCH` build arg, not the target platform
3. Generates provenance attestations by default (v7 behavior), creating `unknown/unknown` entries

Tested `FROM --platform=$PLATFORM` (with full `linux/arm64` syntax) and verified it does NOT set the output manifest platform — `--platform` on the buildx CLI is required for that.

**Fix**: Use a native ARM64 GitHub runner (`ubuntu-24.04-arm`) for aarch64 builds. Since buildx defaults to the runner's arch, the native ARM64 runner produces correctly-labeled `linux/arm64` images. Drop i386 and armv7 since no native 32-bit runners exist.

## Risks / Open Questions

- ARM64 runner availability: `ubuntu-24.04-arm` is GA on GitHub. If it's ever unavailable, the aarch64 build will be queued.
- 32-bit users: existing i386/armv7 users of v1.6.0 will no longer receive updates. This is acceptable — the hardware overlap between 32-bit and "running sbam" is negligible.

## Clarifications

- 2026-05-26: User confirmed v1.6.0 was working on all architectures. Issue arrived with v2.0.0 when `home-assistant/builder` action was upgraded from `2024.08.2` to `2026.03.2`.
- 2026-05-26: User confirmed they have HAOS on ARM64 to test fixes.
- 2026-05-26: User confirmed keeping `build-image` action is a requirement.
- 2026-05-26: `FROM --platform=linux/arm64` alone does not set output manifest platform (tested with pre-release `v2.0.1-alpha-issue-140.0`).
- 2026-05-26: Decision to drop 32-bit architectures (i386, armv7) and use native ARM64 runner.

## References

- Issue [#140](https://github.com/atbore-phx/sbam/issues/140)
- Release workflow: [.github/workflows/release.yml](../../.github/workflows/release.yml)
- Add-on config: [home-assistant/addons/sbam/config.json](../../home-assistant/addons/sbam/config.json)
- Dockerfile: [home-assistant/addons/sbam/Dockerfile](../../home-assistant/addons/sbam/Dockerfile)
- `home-assistant/builder` old version: https://github.com/home-assistant/builder/tree/2024.08.2
- `home-assistant/builder` new action: https://github.com/home-assistant/builder/tree/2026.03.2
