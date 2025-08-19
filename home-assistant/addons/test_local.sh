#!/bin/bash
set -euo pipefail

BUILDCONTAINER_DATA_PATH="/data"
PATHTOBUILD="$BUILDCONTAINER_DATA_PATH"
ARCH=amd64


PROJECTDIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )/sbam
ROOTDIR=${PROJECTDIR}/../../../
cd ${PROJECTDIR}/../../../
make build
cd -


rm -rf $PROJECTDIR/bin
mkdir -p $PROJECTDIR/bin
cp $ROOTDIR/bin/sbam $PROJECTDIR/bin/

echo "project directory is $PROJECTDIR"
echo "build container data path is $BUILDCONTAINER_DATA_PATH"
echo "build container target build path is $PATHTOBUILD"
CMD="podman run --rm -ti --name hassio-builder --privileged -v $PROJECTDIR:$BUILDCONTAINER_DATA_PATH -v /var/run/docker.sock:/var/run/docker.sock:ro homeassistant/amd64-builder --target $PATHTOBUILD --$ARCH --test --docker-hub local"
echo "$CMD"
$CMD
