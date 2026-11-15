#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Export a clean copy of Junction for the public repository: the current commit,
# minus the paths in .publishignore, with fresh history (one commit), and only if
# none of your private details appear anywhere in it.
#
#   scripts/publish.sh <out-dir> [private-patterns-file]
#
# The patterns file (default ~/.junction-private) holds one extended regex per
# line: your names, device names, MAC addresses, account numbers, home IPs...
# It is never committed. Lines starting with # are ignored.
set -eu
cd "$(dirname "$0")/.."
out=${1:?usage: scripts/publish.sh <out-dir> [patterns-file]}
pat=${2:-$HOME/.junction-private}
[ -e "$out" ] && { echo "$out exists; pick a new directory" >&2; exit 1; }
[ -f "$pat" ] || { echo "no patterns file at $pat (one regex per line of things that must not be published)" >&2; exit 1; }
git diff --quiet && git diff --cached --quiet || { echo "commit or stash your changes first" >&2; exit 1; }

mkdir -p "$out"
git archive --format=tar HEAD | tar -x -C "$out"
grep -v '^[[:space:]]*\(#\|$\)' .publishignore | while read -r p; do rm -rf "${out:?}/$p"; done

# Refuse if anything private is left.
grep -v '^[[:space:]]*\(#\|$\)' "$pat" > "$out.patterns"
if grep -rniE -f "$out.patterns" "$out" > "$out.hits" 2>/dev/null; then
  echo "Private details found; nothing was published. Fix these first:" >&2
  cut -c1-200 "$out.hits" >&2
  rm -rf "$out" "$out.patterns" "$out.hits"
  exit 1
fi
rm -f "$out.patterns" "$out.hits"

cd "$out"
git init -q -b main
git add -A
git commit -q -m "Junction $(git -C "$OLDPWD" describe --tags --always 2>/dev/null || echo snapshot)"
echo "Clean copy with fresh history in $out. Review it, then add your public remote and push."
