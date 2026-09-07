FROM debian:trixie-slim

ARG TARGETARCH
ARG XRAY_VERSION=26.3.27
ARG XKEEN_REVISION=e461c4e9964fb8ac78e5fe01aa2e27ab980af712
ENV PATH="/opt/bin:/opt/sbin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
    XRAY_LOCATION_ASSET=/opt/etc/xray/dat \
    XKEEN_FOREGROUND=1 TERM=xterm

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl wget unzip procps iproute2 iptables ipset busybox \
    && rm -rf /var/lib/apt/lists/*
COPY bootstrap.sh /usr/local/lib/router-lab/bootstrap.sh
RUN /bin/sh /usr/local/lib/router-lab/bootstrap.sh "$TARGETARCH" "$XRAY_VERSION" "$XKEEN_REVISION"
COPY configs/ /usr/local/share/router-lab/configs/
COPY www/ /usr/local/share/router-lab/www/
COPY entrypoint.sh verify.sh /usr/local/lib/router-lab/
RUN chmod +x /usr/local/lib/router-lab/*.sh && ln -sf /opt/bin/sh /bin/sh
ENTRYPOINT ["/usr/local/lib/router-lab/entrypoint.sh"]
CMD ["xkeen"]
