#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Junction installer:
#   curl -fsSL https://github.com/XIVIX134/junction/releases/latest/download/install.sh | sh -s -- [flags]
#
# Downloads the Junction release for this CPU, verifies its SHA256 against the
# release's SHA256SUMS, unpacks it to /opt/junction/<version> and runs
# gateway/setup.sh from it. Every flag not listed here goes to setup.sh
# (see setup.sh --help); with none, setup.sh asks its questions.
#
#   --version V        a release tag (default: latest)
#   --base-url URL     where releases live (default below, or $JUNCTION_BASE_URL);
#                      files are fetched from URL/latest/download/<file> or
#                      URL/download/<version>/<file>, GitHub's layout
#   --file TARBALL     install from a local tarball (SHA256SUMS next to it is checked)
#   --download-only    fetch and verify, print the directory, don't run setup

set -eu

BASE_URL="${JUNCTION_BASE_URL:-https://github.com/XIVIX134/junction/releases}"
VERSION=latest
FILE=
DOWNLOAD_ONLY=0
DEST_ROOT=${JUNCTION_DEST:-/opt/junction}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
say() { printf '%s\n' "$*"; }
have() { command -v "$1" >/dev/null 2>&1; }

# Split our own flags from setup.sh's, keeping setup's in order.
n=$#
while [ "$n" -gt 0 ]; do
    a=$1
    shift
    n=$((n - 1))
    case "$a" in
    --version | --base-url | --file)
        [ "$n" -gt 0 ] || die "$a needs a value"
        v=$1
        shift
        n=$((n - 1))
        ;;
    --version=* | --base-url=* | --file=*) v=${a#*=} a=${a%%=*} ;;
    --download-only) DOWNLOAD_ONLY=1; continue ;;
    *) set -- "$@" "$a"; continue ;;
    esac
    case "$a" in
    --version) VERSION=$v ;;
    --base-url) BASE_URL=${v%/} ;;
    --file) FILE=$v ;;
    esac
done

[ "$(id -u)" = 0 ] || die "run as root, e.g.: curl -fsSL <url>/install.sh | sudo sh -s --"
[ "$(uname -s)" = Linux ] || die "Junction runs on Linux"
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
armv7* | armv8l) ARCH=armv7 ;;
*) die "unsupported CPU $(uname -m) (amd64, arm64 and armv7 are supported)" ;;
esac

fetch() {
    if have curl; then curl -fsSL --retry 2 -o "$2" "$1"
    elif have wget; then wget -q -O "$2" "$1"
    else die "need curl or wget"; fi
}
sha_of() {
    if have sha256sum; then sha256sum "$1" | awk '{print $1}'
    elif have shasum; then shasum -a 256 "$1" | awk '{print $1}'
    else die "need sha256sum"; fi
}

WORK=$(mktemp -d "${TMPDIR:-/tmp}/junction-install.XXXXXX")
trap 'rm -rf "$WORK"' EXIT

if [ -n "$FILE" ]; then
    [ -r "$FILE" ] || die "can't read $FILE"
    NAME=$(basename "$FILE")
    TARBALL=$FILE
    SUMS="$(dirname "$FILE")/SHA256SUMS"
    [ -r "$SUMS" ] || die "no SHA256SUMS next to $FILE: can't verify it"
else
    if [ "$VERSION" = latest ]; then DL="$BASE_URL/latest/download"
    else DL="$BASE_URL/download/$VERSION"; fi
    say "Fetching $DL/SHA256SUMS"
    fetch "$DL/SHA256SUMS" "$WORK/SHA256SUMS" || die "can't download $DL/SHA256SUMS"
    SUMS="$WORK/SHA256SUMS"
    NAME=$(awk '{ f = $2; sub(/^\*/, "", f); print f }' "$SUMS" | grep -E "^junction-.*-linux-$ARCH\.tar\.gz\$" | head -n 1)
    [ -n "$NAME" ] || die "this release has no build for linux-$ARCH"
    say "Fetching $DL/$NAME"
    fetch "$DL/$NAME" "$WORK/$NAME"
    TARBALL="$WORK/$NAME"
fi

WANT=$(awk -v f="$NAME" '{ g = $2; sub(/^\*/, "", g); if (g == f) print $1 }' "$SUMS")
[ -n "$WANT" ] || die "$NAME is not listed in SHA256SUMS"
[ "$(sha_of "$TARBALL")" = "$WANT" ] || die "$NAME: SHA256 does not match. Not installing."
say "SHA256 ok"

VER=${NAME#junction-}
VER=${VER%-linux-*}
DEST="$DEST_ROOT/$VER"
rm -rf "$DEST.new"
mkdir -p "$DEST.new"
tar -xzf "$TARBALL" -C "$DEST.new"
TOP=$(find "$DEST.new" -mindepth 1 -maxdepth 1 -type d | head -n 1)
[ -f "$TOP/gateway/setup.sh" ] && [ -f "$TOP/junctiond" ] || die "$NAME doesn't look like a Junction release"
rm -rf "$DEST"
mv "$TOP" "$DEST"
rm -rf "$DEST.new"
chmod 755 "$DEST/junctiond"
say "Unpacked to $DEST"

if [ "$DOWNLOAD_ONLY" = 1 ]; then
    say "Run: sh $DEST/gateway/setup.sh"
    exit 0
fi
# setup.sh reads answers from /dev/tty, so this works under `curl | sh` too.
rm -rf "$WORK"
trap - EXIT
exec sh "$DEST/gateway/setup.sh" "$@"
