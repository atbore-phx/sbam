ARG BUILD_FROM
FROM $BUILD_FROM

# Copy data for add-on
COPY run.sh /
RUN chmod a+x /run.sh

COPY bin/sbam /usr/bin/
RUN chmod a+x /usr/bin/sbam

CMD [ "/run.sh" ]