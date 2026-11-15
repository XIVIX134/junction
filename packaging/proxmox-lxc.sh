#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Run on a Proxmox VE host: creates an Alpine LXC container for the Junction
# gateway and installs Junction in it.
#
#   sh proxmox-lxc.sh --ip 192.168.1.5/24 --gw 192.168.1.1 [flags] [-- setup.sh flags]
#
#   --ctid N            container id (default: next free)
#   --hostname NAME     default junction
#   --ip CIDR|dhcp      the container's own address. Use a fixed one: it's a gateway.
#   --gw IP             your router (required with a fixed --ip)
#   --bridge BR         default vmbr0
#   --storage NAME      for the root disk (default: first storage that holds containers)
#   --disk GB           default 2
#   --memory MB         default 512
#   --cores N           default 1
#   --base-url URL      where Junction releases live (passed to install.sh)
#   --version V         release tag (default latest)
#   --release FILE      install from a local release tarball on this host (SHA256SUMS next to it)
#   --no-install        only create and start the container
# Everything after `--` goes to setup.sh inside the container, which runs with
# --yes (no questions); e.g. -- --exit-wireguard home=/root/wg0.conf --dhcp ...
# A file you reference must be inside the container: pct push <ctid> <file> /root/<file>.
#
# What the container needs (and gets), unprivileged is enough:
#   - TPROXY, NAT and tc run inside the container's own network namespace, where
#     the container's root has CAP_NET_ADMIN; no privileged container needed.
#   - The kernel modules must be loaded on the host (a container can't load
#     modules): this script loads them now and lists them in
#     /etc/modules-load.d/junction.conf so they load at every boot.
#   - Its NIC must not use the Proxmox firewall's IP/MAC filter: TPROXY answers
#     devices from internet addresses and keepalived adds a floating address.
#     The container is created with firewall=0 on net0.
#   - keepalived (optional) works unprivileged too (VRRP is plain IP protocol 112).

set -eu

CTID='' HOSTNAME=junction IP='' GW='' BRIDGE=vmbr0 STORAGE='' DISK=2 MEMORY=512 CORES=1
BASE_URL='' VERSION='' RELEASE='' INSTALL=1
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
say() { printf '%s\n' "$*"; }

while [ $# -gt 0 ]; do
    case "$1" in
    --) shift; break ;;
    --no-install) INSTALL=0; shift; continue ;;
    -h | --help) sed -n '4,33p' "$0"; exit 0 ;;
    esac
    [ $# -ge 2 ] || die "$1 needs a value"
    case "$1" in
    --ctid) CTID=$2 ;;
    --hostname) HOSTNAME=$2 ;;
    --ip) IP=$2 ;;
    --gw) GW=$2 ;;
    --bridge) BRIDGE=$2 ;;
    --storage) STORAGE=$2 ;;
    --disk) DISK=$2 ;;
    --memory) MEMORY=$2 ;;
    --cores) CORES=$2 ;;
    --base-url) BASE_URL=$2 ;;
    --version) VERSION=$2 ;;
    --release) RELEASE=$2 ;;
    *) die "unknown flag $1 (see --help)" ;;
    esac
    shift 2
done

[ "$(id -u)" = 0 ] || die "run as root on the Proxmox host"
command -v pct >/dev/null 2>&1 || die "pct not found: run this on a Proxmox VE host"
[ -n "$IP" ] || die "--ip is required (e.g. --ip 192.168.1.5/24 --gw 192.168.1.1, or --ip dhcp)"
if [ "$IP" = dhcp ]; then
    say "WARNING: a DHCP address can change; give the container a reservation on your router."
else
    case "$IP" in */*) ;; *) die "--ip wants CIDR, e.g. 192.168.1.5/24" ;; esac
    [ -n "$GW" ] || die "--gw (your router's address) is required with a fixed --ip"
    if ping -c 1 -W 1 "${IP%/*}" >/dev/null 2>&1; then die "${IP%/*} is already in use on your network"; fi
fi
[ -n "$RELEASE" ] && { [ -r "$RELEASE" ] || die "can't read $RELEASE"; }

# 1. Kernel modules for TPROXY, NAT, the counting chains and speed caps.
MODULES="nf_tproxy_ipv4 xt_TPROXY xt_mark xt_comment xt_addrtype xt_conntrack nf_nat xt_MASQUERADE iptable_mangle iptable_nat iptable_filter nft_compat sch_htb sch_fq_codel sch_ingress cls_u32 act_police"
say "== Loading kernel modules on the host"
for m in $MODULES; do modprobe "$m" 2>/dev/null || say "   (could not load $m; may be built in)"; done
{
    echo "# Junction gateway container: TPROXY, NAT, counting chains, speed caps"
    for m in $MODULES; do echo "$m"; done
} >/etc/modules-load.d/junction.conf
say "   listed in /etc/modules-load.d/junction.conf"

# 2. Alpine template.
say "== Alpine template"
pveam update >/dev/null 2>&1 || say "   (pveam update failed; using the cached list)"
ARCH=$(dpkg --print-architecture 2>/dev/null || echo amd64)
TEMPLATE=$(pveam available --section system 2>/dev/null | awk '{print $2}' |
    grep -E "^alpine-3\.[0-9]+-default_[0-9]+_$ARCH\.tar\.xz\$" | sort -V | tail -n 1)
[ -n "$TEMPLATE" ] || die "no Alpine template for $ARCH in 'pveam available'"
TSTORE=$(pvesm status -content vztmpl 2>/dev/null | awk 'NR > 1 && $3 == "active" { print $1; exit }')
[ -n "$TSTORE" ] || TSTORE=local
if ! pveam list "$TSTORE" 2>/dev/null | grep -q "$TEMPLATE"; then
    pveam download "$TSTORE" "$TEMPLATE"
fi
say "   $TSTORE:vztmpl/$TEMPLATE"

# 3. The container.
[ -n "$CTID" ] || CTID=$(pvesh get /cluster/nextid)
pct status "$CTID" >/dev/null 2>&1 && die "container $CTID already exists"
if [ -z "$STORAGE" ]; then
    STORAGE=$(pvesm status -content rootdir 2>/dev/null | awk 'NR > 1 && $3 == "active" { print $1; exit }')
fi
[ -n "$STORAGE" ] || die "no storage for containers found: pass --storage"
NET="name=eth0,bridge=$BRIDGE,firewall=0,ip=$IP"
[ "$IP" = dhcp ] || NET="$NET,gw=$GW"

say "== Creating container $CTID ($HOSTNAME) on $STORAGE"
pct create "$CTID" "$TSTORE:vztmpl/$TEMPLATE" \
    --hostname "$HOSTNAME" --ostype alpine --unprivileged 1 --features nesting=1 \
    --cores "$CORES" --memory "$MEMORY" --swap 256 --rootfs "$STORAGE:$DISK" \
    --net0 "$NET" --onboot 1 --description "Junction gateway"
pct start "$CTID"

say "== Waiting for the container's network"
i=0
until pct exec "$CTID" -- sh -c 'wget -q -T 3 -O /dev/null http://dl-cdn.alpinelinux.org/ 2>/dev/null'; do
    i=$((i + 1))
    [ $i -lt 30 ] || die "container $CTID has no internet (check --ip/--gw/--bridge)"
    sleep 2
done
pct exec "$CTID" -- apk add --no-cache curl ca-certificates >/dev/null

if [ "$INSTALL" = 0 ]; then
    say "Container $CTID is ready. Install with: pct enter $CTID, then run install.sh"
    exit 0
fi

# 4. Junction. Quote every argument for the container's shell.
q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
ARGS=
for a in "$@"; do ARGS="$ARGS $(q "$a")"; done
IARGS=
[ -n "$BASE_URL" ] && IARGS="$IARGS --base-url $(q "$BASE_URL")"
[ -n "$VERSION" ] && IARGS="$IARGS --version $(q "$VERSION")"

say "== Installing Junction in container $CTID"
if [ -n "$RELEASE" ]; then
    pct exec "$CTID" -- mkdir -p /root/junction-release
    pct push "$CTID" "$RELEASE" "/root/junction-release/$(basename "$RELEASE")"
    SUMS="$(dirname "$RELEASE")/SHA256SUMS"
    [ -r "$SUMS" ] || die "no SHA256SUMS next to $RELEASE"
    pct push "$CTID" "$SUMS" /root/junction-release/SHA256SUMS
    INSTALLER="$(dirname "$RELEASE")/install.sh"
    [ -r "$INSTALLER" ] || INSTALLER="$(dirname "$0")/install.sh"
    pct push "$CTID" "$INSTALLER" /root/junction-release/install.sh
    IARGS="$IARGS --file $(q "/root/junction-release/$(basename "$RELEASE")")"
    pct exec "$CTID" -- sh -c "sh /root/junction-release/install.sh $IARGS --yes $ARGS"
else
    URL=${BASE_URL:-https://github.com/XIVIX134/junction/releases}
    if [ -n "$VERSION" ]; then URL="$URL/download/$VERSION/install.sh"; else URL="$URL/latest/download/install.sh"; fi
    pct exec "$CTID" -- sh -c "curl -fsSL $(q "$URL") | sh -s -- $IARGS --yes $ARGS"
fi

CT_IP=${IP%/*}
[ "$IP" = dhcp ] && CT_IP="<the container's address>"
say ""
say "Junction is running in container $CTID: http://$CT_IP"
say "Shell inside it: pct enter $CTID. Re-run setup interactively there: sh /usr/local/share/junction/gateway/setup.sh"
