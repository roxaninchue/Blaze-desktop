#!/usr/bin/env bash
# check-linux-artifacts.sh <dir> <version> <arch>: names match update.go's pickAsset, contents are complete.
set -euo pipefail
DIR="$1"; V="$2"; ARCH="$3"
T="$DIR/Blaze-v${V}-linux-${ARCH}.tar.gz"; DEB="$DIR/blaze-desktop_${V}_${ARCH}.deb"
test -f "$T" && test -f "$DEB"
[ "$(tar tzf "$T" | sort | tr '\n' ' ')" = "blaze-desktop blaze.desktop blaze.png " ] || { echo "tarball layout wrong"; exit 5; }
dpkg-deb -f "$DEB" Package Version Architecture
[ "$(dpkg-deb -f "$DEB" Version)" = "$V" ] || { echo "deb version wrong"; exit 5; }
dpkg-deb -c "$DEB" | grep -q './usr/bin/blaze-desktop$'
if dpkg-deb -f "$DEB" Maintainer | grep -qiE 'kapapa|lackson|luckson'; then echo "private name in deb"; exit 5; fi
echo "linux artifacts ok"
