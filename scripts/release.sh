#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Build Junction release tarballs:
#   scripts/release.sh            -> out/release/junction-<version>-linux-{amd64,arm64,armv7}.tar.gz
#                                    + install.sh, proxmox-lxc.sh, SHA256SUMS
# Environment:
#   GO=/path/to/go     Go toolchain (default ~/sdk/go/bin/go if present, else go)
#   VERSION=v1.2.3     override `git describe --tags --always --dirty`
#   OUT=dir            output directory (default out/release)
#   SKIP_WEB=1         reuse an existing web/dist
# Works in Git Bash on Windows and on Linux CI (needs GNU tar, gzip, sha256sum, npm).

set -eu
cd "$(dirname "$0")/.."
ROOT=$(pwd)

if [ -z "${GO:-}" ]; then
    if command -v "$HOME/sdk/go/bin/go" >/dev/null 2>&1; then GO="$HOME/sdk/go/bin/go"; else GO=go; fi
fi
command -v "$GO" >/dev/null 2>&1 || { echo "Go not found (set GO=...)" >&2; exit 1; }
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
OUT=${OUT:-out/release}
EPOCH=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || date +%s)}
TARGETS="amd64:amd64: arm64:arm64: armv7:arm:7"

echo "== Junction $VERSION ($("$GO" version))"

if [ "${SKIP_WEB:-0}" != 1 ]; then
    echo "== web"
    (cd web && { [ -d node_modules ] || npm ci; } && npm run build)
fi
[ -f web/dist/index.html ] || { echo "web/dist is missing: build the web UI first" >&2; exit 1; }

rm -rf "$OUT"
mkdir -p "$OUT/stage"
GNU_TAR=0
tar --version 2>/dev/null | grep -q 'GNU tar' && GNU_TAR=1

# pack DIR-NAME: deterministic tarball of $OUT/stage/DIR-NAME with real file modes
# (Windows filesystems don't keep the executable bit, so modes are set explicitly).
pack() {
    (
        set -f # the find patterns below must not be globbed by the shell
        cd "$OUT/stage"
        is_exec='-name junctiond -o -name *.sh -o -name junction-tproxy -o -name junction-gateway-* -o -path */openrc/*'
        # shellcheck disable=SC2086
        { find "$1" -type d; find "$1" -type f \( $is_exec \); } | LC_ALL=C sort >../exec.list
        # shellcheck disable=SC2086
        find "$1" -type f ! \( $is_exec \) | LC_ALL=C sort >../plain.list
        if [ "$GNU_TAR" = 1 ]; then
            opts="--owner=0 --group=0 --numeric-owner --mtime=@$EPOCH --no-recursion"
            # shellcheck disable=SC2086
            tar -cf "../$1.tar" $opts --mode=0755 -T ../exec.list
            # shellcheck disable=SC2086
            tar -rf "../$1.tar" $opts --mode=0644 -T ../plain.list
        else
            xargs chmod 755 <../exec.list
            xargs chmod 644 <../plain.list
            tar -cf "../$1.tar" "$1"
        fi
        gzip -n -9 -f "../$1.tar"
        rm -f ../exec.list ../plain.list
    )
}

for t in $TARGETS; do
    name=${t%%:*} rest=${t#*:}
    goarch=${rest%%:*} goarm=${rest#*:}
    dir="junction-$VERSION-linux-$name"
    stage="$OUT/stage/$dir"
    echo "== $dir"
    mkdir -p "$stage"
    GOOS=linux GOARCH=$goarch GOARM=$goarm CGO_ENABLED=0 \
        "$GO" build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$stage/junctiond" .
    cp -R packaging/gateway "$stage/gateway"
    cp -R packaging/services "$stage/services"
    cp packaging/README.md "$stage/INSTALL.md"
    cp packaging/install.sh packaging/proxmox-lxc.sh "$stage/"
    for f in LICENSE COPYING README.md; do [ -f "$f" ] && cp "$f" "$stage/"; done
    [ -f "$stage/LICENSE" ] || [ -f "$stage/COPYING" ] || echo "   note: no LICENSE file in the repo root" >&2
    echo "$VERSION" >"$stage/VERSION"
    pack "$dir"
done

cp packaging/install.sh packaging/proxmox-lxc.sh "$OUT/"
rm -rf "$OUT/stage"
(cd "$OUT" && sha256sum -- *.tar.gz install.sh proxmox-lxc.sh >SHA256SUMS)

echo "== done: $ROOT/$OUT"
ls -l "$OUT"
