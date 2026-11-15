#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Install a new junctiond and keep it only if it's healthy.
#
#   safe-update.sh <dir>     <dir>/junctiond is the new binary
#
# Keeps the running binary as junctiond.prev, swaps in the new one and restarts
# the service. The new one must answer on the local API for 20 s in a row within
# 60 s; otherwise the previous binary goes back and is restarted. The outcome is
# appended to /var/lib/junction/updates.log ("RFC3339 version result [note]"),
# which the Health page shows. Traffic never depends on junctiond (sing-box
# carries it), so a restart or a rollback doesn't interrupt the internet.
set -u

dir=${1:?usage: safe-update.sh <dir with the new junctiond>}
new="$dir/junctiond"
bin=${JUNCTIOND_BIN:-/usr/local/bin/junctiond}
log=${JUNCTION_UPDATES_LOG:-/var/lib/junction/updates.log}
cfg=${JUNCTION_CONFIG:-/etc/junction/config.json}

# Under systemd, a script started by junctiond lives in junctiond's cgroup and
# would be killed by the restart below: re-run detached in its own unit.
if [ -z "${SAFE_UPDATE_DETACHED:-}" ] && [ -d /run/systemd/system ] && command -v systemd-run >/dev/null 2>&1 &&
	grep -q junctiond /proc/self/cgroup 2>/dev/null; then
	exec systemd-run --quiet --collect --unit "junction-update-$$" --setenv SAFE_UPDATE_DETACHED=1 /bin/sh "$0" "$@"
fi

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
note() { mkdir -p "$(dirname "$log")"; echo "$(now) $1" >>"$log"; }
say() { echo "safe-update: $*"; }

[ -f "$new" ] || { say "no junctiond in $dir"; exit 1; }
chmod 755 "$new" 2>/dev/null || true # archives made on Windows lose the executable bit
ver=$("$new" -version 2>/dev/null | head -n1)
[ -n "$ver" ] || { say "$new doesn't run on this machine (wrong architecture?)"; note "unknown rolled-back the new binary didn't start"; exit 1; }

restart() {
	if command -v rc-service >/dev/null 2>&1; then
		rc-service junctiond restart >/dev/null 2>&1 || rc-service junctiond start >/dev/null 2>&1
	elif command -v systemctl >/dev/null 2>&1; then
		systemctl restart junctiond
	else
		say "no rc-service or systemctl: restart junctiond yourself"
		return 1
	fi
}

# The local API port, from "listen" in the config (":80" by default). The new
# binary reads it (the config has other "listen" keys, like the failover's);
# older ones don't know -print-listen: then a top-level key at two spaces.
listen=$("$new" -print-listen -config "$cfg" 2>/dev/null) || listen=""
[ -n "$listen" ] || listen=$(sed -n 's/^ \{0,2\}"listen"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$cfg" 2>/dev/null | head -n1)
port=${listen##*:}
case "$port" in '' | *[!0-9]*) port=80 ;; esac
url="http://127.0.0.1:${port:-80}/api/session"
answers() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsS -m 3 -o /dev/null "$url" 2>/dev/null
	else
		wget -q -T 3 -O /dev/null "$url" 2>/dev/null
	fi
}

# Healthy = answering for 20 s in a row, within 60 s of the restart.
healthy() {
	start=$(date +%s)
	streak=0
	while [ $(($(date +%s) - start)) -lt 60 ]; do
		if answers; then
			streak=$((streak + 2))
			[ "$streak" -ge 20 ] && return 0
		else
			streak=0
		fi
		sleep 2
	done
	return 1
}

note "$ver pending"
cp -p "$bin" "$bin.prev" 2>/dev/null || true
install -m 755 "$new" "$bin.new" && mv -f "$bin.new" "$bin" || { say "couldn't install $new"; note "$ver rolled-back couldn't write $bin"; exit 1; }
say "installed $ver, restarting"
t0=$(date +%s)
restart
if healthy; then
	say "$ver is healthy"
	note "$ver healthy"
	exit 0
fi

secs=$(($(date +%s) - t0))
say "$ver didn't pass its health check; going back"
if [ -f "$bin.prev" ]; then
	mv -f "$bin.prev" "$bin"
	restart
fi
note "$ver rolled-back rolled back after $secs s"
exit 1
