#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Remove Junction from this gateway without cutting anyone off.
#
#   uninstall.sh            remove junctiond, sing-box and the TPROXY rules/routes,
#                           and leave a plain router behind (forwarding + NAT, kept
#                           across reboots), so devices still pointed at this box
#                           keep their internet. DHCP and keepalived keep running.
#   uninstall.sh --purge    also stop routing, DHCP and keepalived, and delete
#                           /etc/junction, /etc/sing-box and /var/lib/junction.
#                           First point your devices back at the router (and turn
#                           the router's DHCP back on if this box served DHCP).
#   -n, --dry-run           print what would be done
#   -y, --yes               don't ask

set -eu
DRY=0 YES=0 PURGE=0
for a in "$@"; do
    case "$a" in
    -n | --dry-run) DRY=1 ;;
    -y | --yes) YES=1 ;;
    --purge) PURGE=1 ;;
    -h | --help) sed -n '4,17p' "$0"; exit 0 ;;
    *) echo "unknown flag $a" >&2; exit 2 ;;
    esac
done

say() { printf '%s\n' "$*"; }
run() { if [ "$DRY" = 1 ]; then printf '  + %s\n' "$*"; else "$@"; fi; }
quiet() { if [ "$DRY" = 1 ]; then printf '  + %s\n' "$*"; else "$@" >/dev/null 2>&1 || true; fi; }

[ "$(id -u)" = 0 ] || [ "$DRY" = 1 ] || { echo "run as root" >&2; exit 1; }
if [ -d /run/systemd/system ]; then INIT=systemd; else INIT=openrc; fi

svc_remove() { # stop, disable and delete a service we installed
    if [ "$INIT" = systemd ]; then
        quiet systemctl disable --now "$1"
        [ -f "/etc/systemd/system/$1.service" ] && run rm -f "/etc/systemd/system/$1.service"
    else
        quiet rc-service "$1" stop
        quiet rc-update del "$1" default
        [ -f "/etc/init.d/$1" ] && run rm -f "/etc/init.d/$1"
    fi
    return 0
}
svc_stop() {
    if [ "$INIT" = systemd ]; then quiet systemctl disable --now "$1"
    else quiet rc-service "$1" stop; quiet rc-update del "$1" default; fi
}
svc_restart() {
    if [ "$INIT" = systemd ]; then quiet systemctl restart "$1"
    else quiet rc-service "$1" restart; fi
}

TP=/usr/local/sbin/junction-tproxy
CONF=/etc/junction/tproxy.conf

if [ "$PURGE" = 1 ]; then
    say "PURGE: this box stops routing. Devices that still use it as their gateway lose internet"
    say "until they are pointed back at the router (and the router's DHCP is on again)."
else
    say "This removes Junction and sing-box, and leaves a plain router (forwarding + NAT)."
fi
if [ "$YES" = 0 ] && [ "$DRY" = 0 ]; then
    printf 'Continue? [y/N]: '
    read -r ans </dev/tty || ans=
    case "$ans" in y | Y | yes) ;; *) say "Nothing changed."; exit 0 ;; esac
fi

say "== Switching to plain routing first (TPROXY off)"
[ -x "$TP" ] && run "$TP" disable

say "== Removing Junction and sing-box"
svc_remove junctiond
svc_remove sing-box
run rm -f /usr/local/bin/junctiond /usr/local/bin/sing-box

if [ "$PURGE" = 0 ]; then
    say "== Keeping a plain router: junction-tproxy in router mode"
    if [ -f "$CONF" ]; then
        if [ "$DRY" = 1 ]; then say "  + set MODE=router in $CONF"
        else sed -i 's/^MODE=.*/MODE=router/' "$CONF"; fi
    fi
    # Router mode removes the TPROXY chain, guard, ip rule and table 100.
    svc_restart junction-tproxy
    [ "$DRY" = 1 ] || { sleep 1; "$TP" status || true; }
    say ""
    say "Left in place: junction-tproxy (router mode), /etc/junction, /etc/sing-box (has your VPN keys),"
    say "/var/lib/junction (Junction's data), and junction-dhcp / keepalived if you used them."
    say "Remove all of it later with: sh $0 --purge"
    exit 0
fi

say "== Removing routing, DHCP and keepalived"
svc_remove junction-dhcp
if [ -f /etc/keepalived/keepalived.conf ] && grep -q "Junction's setup.sh" /etc/keepalived/keepalived.conf; then
    svc_stop keepalived
    run rm -f /etc/keepalived/keepalived.conf
fi
svc_remove junction-tproxy
[ -x "$TP" ] && run "$TP" teardown
run rm -f /etc/sysctl.d/90-junction.conf
quiet sysctl -w net.ipv4.ip_forward=0
run rm -f "$TP" /usr/local/sbin/junction-gateway-check /usr/local/sbin/junction-gateway-notify
run rm -f /etc/logrotate.d/junction
run rm -rf /etc/junction /etc/sing-box /var/lib/junction /var/lib/sing-box /usr/local/share/junction
[ "$INIT" = systemd ] && quiet systemctl daemon-reload
say "Done: Junction is gone and this box no longer routes."
