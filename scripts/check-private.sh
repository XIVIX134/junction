#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Refuse to push your own network's details. Checks the files changed between
# two commits (default: what's about to be pushed to origin/main) against a
# list of patterns that must never be published.
#
#   scripts/check-private.sh [from] [to]
#   PATTERNS=~/.junction-private   one extended regex per line (# = comment):
#                                  names, MAC addresses, account numbers, home IPs...
#
# scripts/install-hooks.sh runs it before every git push.
set -eu
cd "$(dirname "$0")/.."
pat=${PATTERNS:-$HOME/.junction-private}
from=${1:-origin/main}
to=${2:-HEAD}
[ -f "$pat" ] || { echo "check-private: no patterns file at $pat; nothing checked" >&2; exit 0; }
list=$(mktemp)
trap 'rm -f "$list"' EXIT
grep -v '^[[:space:]]*\(#\|$\)' "$pat" >"$list" || true
[ -s "$list" ] || exit 0
range="$from..$to"
git rev-parse -q --verify "$from" >/dev/null || range=$to # nothing pushed yet: check everything
bad=0
for f in $(git diff --name-only --diff-filter=AM "$range" 2>/dev/null || git ls-tree -r --name-only "$to"); do
	if git show "$to:$f" 2>/dev/null | grep -aniE -f "$list" >/dev/null; then
		echo "check-private: $f contains something from $pat:" >&2
		git show "$to:$f" | grep -aniE -f "$list" | cut -c1-160 | sed 's/^/    /' >&2
		bad=1
	fi
done
# commit messages too
if git log --format=%B "$range" 2>/dev/null | grep -aiE -f "$list" >/dev/null; then
	echo "check-private: a commit message contains something from $pat" >&2
	bad=1
fi
[ "$bad" = 0 ] || { echo "check-private: nothing pushed. Remove those details first." >&2; exit 1; }
