# Build with a locally trusted, pinned source image containing Xray and profile assets.
ARG XRAY_SOURCE=xkeen-router-lab:dev
FROM ${XRAY_SOURCE} AS source
FROM scratch
COPY --from=source /opt/sbin/xray /opt/sbin/xray
COPY --from=source /opt/etc/xray/dat/ /opt/etc/xray/dat/
COPY check /check
ENV XRAY_LOCATION_ASSET=/opt/etc/xray/dat
USER 65534:65534
ENTRYPOINT ["/check"]
