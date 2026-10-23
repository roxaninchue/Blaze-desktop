#!/usr/bin/env bash
# package-linux.sh <binary> <version> <outdir> <arch>
# Makes Blaze-v<V>-linux-<arch>.tar.gz and blaze-desktop_<V>_<arch>.deb in the layout update.go installs.
set -euo pipefail
BIN="$1"; V="$2"; OUT="$3"; ARCH="$4"; SRC="${SRC:-$PWD}"
mkdir -p "$OUT"; OUT="$(cd "$OUT" && pwd)"
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(date +%s)}"
W="$(mktemp -d)"
# tar.gz: flat binary + desktop entry + icon
mkdir -p "$W/tar"
install -m755 "$BIN" "$W/tar/blaze-desktop"
install -m644 "$SRC/packaging/linux/blaze.desktop" "$W/tar/blaze.desktop"
install -m644 "$SRC/build/appicon.png" "$W/tar/blaze.png"
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$SOURCE_DATE_EPOCH" -C "$W/tar" -czf "$OUT/Blaze-v${V}-linux-${ARCH}.tar.gz" blaze-desktop blaze.desktop blaze.png
# .deb
D="$W/deb"
install -Dm755 "$BIN" "$D/usr/bin/blaze-desktop"
install -Dm644 "$SRC/packaging/linux/blaze.desktop" "$D/usr/share/applications/blaze.desktop"
install -Dm644 "$SRC/build/appicon.png" "$D/usr/share/icons/hicolor/512x512/apps/blaze.png"
mkdir -p "$D/usr/share/icons/hicolor/256x256/apps"
convert "$SRC/build/appicon.png" -resize 256x256 "$D/usr/share/icons/hicolor/256x256/apps/blaze.png"
mkdir -p "$D/usr/share/doc/blaze-desktop"
cat > "$D/usr/share/doc/blaze-desktop/copyright" <<C
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: blaze-desktop
Source: https://blazeinchue.pages.dev

Files: *
Copyright: 2026 Blazeinchue
License: custom
 Copyright (c) 2026 Blazeinchue. All rights reserved.
 .
 This source code is published for viewing only. No permission is granted to
 copy, modify, distribute, sublicense, sell, or use this software or any part
 of it, in source or binary form, except with the prior written permission of
 the copyright holder. Official builds may be used under the terms published
 with each release.
C
printf 'blaze-desktop (%s) stable; urgency=medium\n\n  * Release %s. See https://blazeinchue.pages.dev\n\n -- Blaze <blazecoderhue@gmail.com>  %s\n' \
  "$V" "$V" "$(date -u -R -d "@$SOURCE_DATE_EPOCH")" | gzip -9n > "$D/usr/share/doc/blaze-desktop/changelog.gz"
SIZE=$(du -sk --exclude=DEBIAN "$D" | cut -f1)
mkdir -p "$D/DEBIAN"
cat > "$D/DEBIAN/control" <<C
Package: blaze-desktop
Version: ${V}
Section: utils
Priority: optional
Architecture: ${ARCH}
Maintainer: Blaze <blazecoderhue@gmail.com>
Installed-Size: ${SIZE}
Depends: libgtk-3-0, libwebkit2gtk-4.1-0, libx11-6, libc6 (>= 2.34)
Homepage: https://blazeinchue.pages.dev
Description: Blaze assistant for your computer
 Blaze is a desktop assistant: chat, agents, memory and tools in one window.
 It updates itself from the Blaze downloads site.
C
(cd "$D" && find usr -type f -print0 | sort -z | xargs -0 md5sum > DEBIAN/md5sums)
find "$D" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
dpkg-deb --root-owner-group -Zxz --build "$D" "$OUT/blaze-desktop_${V}_${ARCH}.deb"
rm -rf "$W"
ls -l "$OUT"
