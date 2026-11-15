#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Run scripts/check-private.sh before every push from this clone.
set -eu
cd "$(dirname "$0")/.."
hook=.git/hooks/pre-push
cat >"$hook" <<'HOOK'
#!/bin/sh
# installed by scripts/install-hooks.sh
while read -r _local local_sha _remote remote_sha; do
	[ "$local_sha" = 0000000000000000000000000000000000000000 ] && continue
	from=$remote_sha
	[ "$from" = 0000000000000000000000000000000000000000 ] && from=origin/main
	sh scripts/check-private.sh "$from" "$local_sha" || exit 1
done
HOOK
chmod +x "$hook"
echo "pre-push check installed ($hook)"
