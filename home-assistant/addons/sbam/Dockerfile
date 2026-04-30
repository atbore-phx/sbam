ARG BUILD_FROM="ghcr.io/home-assistant/amd64-base:latest"
ARG PLATFORM="amd64"
FROM --platform=$PLATFORM $BUILD_FROM

# Copy data for add-on
COPY run.sh /
RUN chmod a+x /run.sh

COPY bin/sbam /usr/bin/
RUN chmod a+x /usr/bin/sbam

CMD [ "/run.sh" ]