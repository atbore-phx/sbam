#!/bin/bash
set -euo pipefail

# Architectures to test. Override with ARCHS="amd64" for native-only.
ARCHS=${ARCHS:-amd64 arm64}
IMAGE_TAG_PREFIX=${IMAGE_TAG_PREFIX:-local/ha-sbam}

PROJECTDIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )/sbam
ROOTDIR=${PROJECTDIR}/../../../
cd "${ROOTDIR}"

NATIVE_ARCH=$(uname -m)
case "$NATIVE_ARCH" in
  x86_64)  NATIVE=amd64  ;;
  aarch64) NATIVE=arm64  ;;
  *)       NATIVE=amd64  ;;  # fallback
esac

for ARC in ${ARCHS}; do
  case "$ARC" in
    amd64)  HA_ARC=amd64   ;;
    arm64)  HA_ARC=aarch64 ;;
    *)      echo "Unknown architecture: ${ARC} (expected amd64 or arm64)" >&2; exit 1 ;;
  esac

  BUILD_FROM="ghcr.io/home-assistant/${HA_ARC}-base:latest"
  IMAGE_TAG="${IMAGE_TAG_PREFIX}-${HA_ARC}:test"

  echo "=== Building Go binary for ${ARC} (HA: ${HA_ARC}) ==="
  rm -rf "${PROJECTDIR}/bin"
  mkdir -p "${PROJECTDIR}/bin"
  make "build-${ARC}"
  cp "${ROOTDIR}/bin/sbam" "${PROJECTDIR}/bin/"

  echo "=== Building Docker image for ${ARC} ==="
  if [ "${ARC}" = "${NATIVE}" ]; then
    docker buildx build \
      --load \
      --build-arg "BUILD_FROM=${BUILD_FROM}" \
      -t "${IMAGE_TAG}" \
      "${PROJECTDIR}"
  else
    echo "Skipping Docker build for non-native arch ${ARC}"
  fi
done

echo "=== Done ==="
