#!/bin/bash
set -euox pipefail

ARC=${ARC:-amd64}
HA_ARC=${HA_ARC:-amd64}
BUILD_FROM=${BUILD_FROM:-ghcr.io/home-assistant/${HA_ARC}-base:latest}
IMAGE_TAG=${IMAGE_TAG:-local/${HA_ARC}-sbam:test}

PROJECTDIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )/sbam
ROOTDIR=${PROJECTDIR}/../../../

cd "${ROOTDIR}"
make build
cd -

rm -rf "${PROJECTDIR}/bin"
mkdir -p "${PROJECTDIR}/bin"
cp "${ROOTDIR}/bin/sbam" "${PROJECTDIR}/bin/"

echo "project directory : ${PROJECTDIR}"
echo "HA_arc            : ${HA_ARC}"
echo "OS arc            : ${ARC}"
echo "base image        : ${BUILD_FROM}"
echo "output image      : ${IMAGE_TAG}"

docker buildx build \
  --load \
  --build-arg "BUILD_FROM=${BUILD_FROM}" --build-arg "PLATFORM=${ARC}" \
  -t "${IMAGE_TAG}" \
  "${PROJECTDIR}"
