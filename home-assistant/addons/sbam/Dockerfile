ARG BUILD_FROM="ghcr.io/home-assistant/amd64-base:latest"
ARG PLATFORM="amd64"
FROM --platform=$PLATFORM $BUILD_FROM

COPY run.sh /
COPY bin/sbam /usr/bin/

CMD [ "/run.sh" ]