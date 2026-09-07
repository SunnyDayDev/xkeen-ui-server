#!/bin/sh
set -eu
arch=$1
xray_version=$2
xkeen_revision=$3
case "$arch" in
  arm64)
    feed=aarch64-k3.10
    asset=Xray-linux-arm64-v8a.zip
    digest=4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c
    ;;
  amd64)
    feed=x64-k3.2
    asset=Xray-linux-64.zip
    digest=23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae
    ;;
  *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
esac
[ "$xray_version" = 26.3.27 ] || { echo 'Update the pinned Xray checksums with the version' >&2; exit 1; }
mkdir -p /opt/bin /opt/etc /opt/lib/opkg /opt/tmp /opt/var/lock /usr/local/share/router-lab
curl -fSL --retry 3 "https://bin.entware.net/$feed/installer/opkg" -o /opt/bin/opkg
curl -fSL --retry 3 "https://bin.entware.net/$feed/installer/opkg.conf" -o /opt/etc/opkg.conf
chmod +x /opt/bin/opkg
opkg update
cp /bin/busybox /opt/bin/busybox
opkg install entware-opt busybox curl jq ip-full iptables ipset ca-bundle
for name in passwd group shells shadow gshadow; do
  if [ -f "/etc/$name" ]; then ln -sf "/etc/$name" "/opt/etc/$name"; fi
done
chmod 1777 /opt/tmp
opkg list-installed > /usr/local/share/router-lab/entware-packages.txt
curl -fSL --retry 3 "https://github.com/XTLS/Xray-core/releases/download/v$xray_version/$asset" -o /tmp/xray.zip
printf '%s  /tmp/xray.zip\n' "$digest" | sha256sum -c -
mkdir -p /tmp/xray /opt/sbin /opt/etc/xray/configs /opt/etc/xray/dat /opt/etc/xkeen /opt/var/log/xray /opt/var/run
unzip -q /tmp/xray.zip -d /tmp/xray
install -m 755 /tmp/xray/xray /opt/sbin/xray
curl -fSL --retry 3 "https://codeload.github.com/jameszeroX/XKeen/tar.gz/$xkeen_revision" -o /tmp/xkeen.tar.gz
printf '%s  /tmp/xkeen.tar.gz\n' 6336a66eefb6de51c07d7c56d279ece8d6e2e4f5ee5eb319dc242845b2cd9aba | sha256sum -c -
mkdir -p /tmp/xkeen
/usr/bin/tar -xzf /tmp/xkeen.tar.gz -C /tmp/xkeen --strip-components=1
cp /tmp/xkeen/scripts/xkeen /opt/sbin/xkeen
cp -R /tmp/xkeen/scripts/_xkeen /opt/sbin/.xkeen
cp /opt/sbin/.xkeen/02_install/07_install_register/04_register_init.sh /opt/etc/init.d/S05xkeen
chmod +x /opt/sbin/xkeen /opt/etc/init.d/S05xkeen
printf 'Xray=%s\nXKeen=%s\nEntware=%s\nArchitecture=%s\n' "$xray_version" "$xkeen_revision" "$feed" "$arch" > /usr/local/share/router-lab/versions.txt
sha256sum /opt/bin/opkg /opt/sbin/xray /tmp/xkeen.tar.gz > /usr/local/share/router-lab/artifacts.sha256
rm -rf /tmp/xray /tmp/xray.zip /tmp/xkeen /tmp/xkeen.tar.gz
