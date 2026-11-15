#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Junction gateway setup: turns a fresh Linux box (Debian, Ubuntu, Raspberry Pi
# OS, Alpine) into a Junction gateway. Safe to run again: it only changes what
# differs, and keeps the Clash API secret, keepalived password and VPN exits it
# made last time. Run with --help for the flags; without flags it asks.
#
# Safety:
#   - nothing here changes this box's own internet path (only forwarded traffic);
#   - the sing-box config is checked with `sing-box check` before it is used;
#   - fail-open: junction-tproxy hooks TPROXY in only while sing-box is healthy,
#     otherwise the box is a plain NAT router, so devices never lose internet;
#   - --dry-run renders everything into a temp dir and prints what it would do.

set -eu

SINGBOX_VERSION=1.12.14
# sha256 of the official release tarballs (GitHub release asset digests).
SB_SHA_amd64=da47b82d032048f313f6448843a50162e14ed28f7d0f3bd69266ec0e5a2f458b
SB_SHA_arm64=429fb80e211dc5ed09ceeff3608e41f90263961bc96bf4072ce716c1690fac6f
SB_SHA_armv7=2eb62c6d0d75977d67e9be69df9118102a6c2ee798ad128803a59eeb610738b2

HERE=$(cd "$(dirname "$0")" && pwd)
TEMPLATES="$HERE/templates"
SERVICES=$(cd "$HERE/../services" 2>/dev/null && pwd || echo "$HERE/../services")
SHARE=/usr/local/share/junction
ETC=/etc/junction
STATE="$ETC/setup.env"
SAVED_EXITS="$ETC/exits.json"
SB_DIR=/etc/sing-box
SB_CONFIG="$SB_DIR/config.json"
LEASES=/var/lib/misc/dnsmasq.leases
TS=$(date +%Y%m%d-%H%M%S)

usage() {
    cat <<'EOF'
Usage: setup.sh [flags]            (asks for anything not given, unless --yes)

General
  -y, --yes                  don't ask; use detected values and defaults
  -n, --dry-run              print what would be done; render files to a temp dir
      --force                skip safety refusals (address in use, foreign configs)
      --skip-packages        don't install packages (you did it yourself)
Network
      --lan-if IF            interface devices reach this box on (default: route to internet)
      --wan-if IF            interface towards the router (default: same as --lan-if)
      --lan-cidr CIDR        the LAN, e.g. 192.168.1.0/24 (default: detected)
      --gateway-ip IP        address devices will use as their gateway
                             (default: this box's address, or the --vip address)
      --dns LIST             DNS servers handed out / used by sing-box (default 1.1.1.1,8.8.8.8)
      --timezone TZ          e.g. Europe/Berlin (default: the system's)
VPN exits (repeatable; names: lowercase letters/digits, e.g. home, wg1)
      --no-vpn                     direct only (VPN can be added later by re-running)
      --exit-wireguard NAME=FILE   a WireGuard .conf (wg-quick format)
      --exit-hysteria2 NAME=URI    a hysteria2://password@host:port/?sni=...&insecure=1 link
      --exit-outbound NAME=FILE    a raw sing-box outbound (VLESS, Trojan, ...) or
                                   wireguard endpoint, as a JSON object
      --profile NAME               VPN profile name (default: default)
DHCP (dnsmasq; off by default)
      --dhcp START-END[,LEASE]     serve DHCP, e.g. --dhcp 192.168.1.100-192.168.1.199,12h
      --dhcp-now                   your router's DHCP is off: start serving right away
      --no-dhcp
Floating gateway address (keepalived; off by default)
      --vip IP/PREFIX              e.g. 192.168.1.4/24; devices use it as their gateway
      --vip-peer IP                the backup box's address (unicast VRRP; omit = multicast)
      --vip-priority N             default 150 (the backup uses 100)
      --vrid N                     VRRP router id, 1-255 (default 52)
      --no-vip
Junction
      --junctiond FILE             junctiond binary to install
      --release FILE|URL           a junction-*.tar.gz release to take junctiond from
                                   (default: the junctiond next to this script's folder)
      --listen ADDR                Junction's web address (default :80)
      --brand NAME                 name shown in the UI (default Junction)
sing-box
      --singbox-bin FILE           use this sing-box binary instead of downloading 1.12.14
      --replace-singbox-config     overwrite an /etc/sing-box/config.json not made by this script
EOF
}

# ---------------------------------------------------------------- output
say() { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

DRY=0 YES=0 FORCE=0 SKIP_PKGS=0
run() {
    if [ "$DRY" = 1 ]; then
        printf '  + %s\n' "$*"
    else
        "$@"
    fi
}

# ---------------------------------------------------------------- prompts
TTY=0
if (: </dev/tty) 2>/dev/null; then TTY=1; fi

# ask VAR "question" DEFAULT
ask() {
    _v=$1 _q=$2 _d=${3-}
    if [ "$YES" = 1 ] || [ "$TTY" = 0 ]; then
        eval "$_v=\$_d"
        return 0
    fi
    if [ -n "$_d" ]; then printf '%s [%s]: ' "$_q" "$_d" >/dev/tty
    else printf '%s: ' "$_q" >/dev/tty; fi
    IFS= read -r _a </dev/tty || _a=
    [ -n "$_a" ] || _a=$_d
    eval "$_v=\$_a"
}
# yesno "question" y|n  -> 0 for yes
yesno() {
    _d=$2
    if [ "$YES" = 1 ] || [ "$TTY" = 0 ]; then [ "$_d" = y ]; return; fi
    if [ "$_d" = y ]; then printf '%s [Y/n]: ' "$1" >/dev/tty; else printf '%s [y/N]: ' "$1" >/dev/tty; fi
    IFS= read -r _a </dev/tty || _a=
    case "${_a:-$_d}" in y | Y | yes | YES) return 0 ;; *) return 1 ;; esac
}

# ---------------------------------------------------------------- small helpers
have() { command -v "$1" >/dev/null 2>&1; }

valid_ip() {
    case "$1" in *[!0-9.]* | *..* | .* | *.) return 1 ;; esac
    _o=$IFS
    IFS=.
    # shellcheck disable=SC2086
    set -- $1
    IFS=$_o
    [ $# -eq 4 ] || return 1
    for _x in "$@"; do [ -n "$_x" ] && [ "$_x" -le 255 ] || return 1; done
}
ip2int() {
    _o=$IFS
    IFS=.
    # shellcheck disable=SC2086
    set -- $1
    IFS=$_o
    echo $((($1 << 24) + ($2 << 16) + ($3 << 8) + $4))
}
int2ip() { echo "$((($1 >> 24) & 255)).$((($1 >> 16) & 255)).$((($1 >> 8) & 255)).$(($1 & 255))"; }
prefix_mask() { echo $(((0xffffffff << (32 - $1)) & 0xffffffff)); }
# network_of ADDR/PREFIX -> NET/PREFIX
network_of() {
    _a=${1%/*} _p=${1#*/}
    echo "$(int2ip $(($(ip2int "$_a") & $(prefix_mask "$_p"))))/$_p"
}
in_cidr() { # in_cidr IP NET/PREFIX
    _m=$(prefix_mask "${2#*/}")
    [ $(($(ip2int "$1") & _m)) -eq $(($(ip2int "${2%/*}") & _m)) ]
}
valid_cidr() {
    case "$1" in */*) ;; *) return 1 ;; esac
    valid_ip "${1%/*}" || return 1
    case "${1#*/}" in '' | *[!0-9]*) return 1 ;; esac
    [ "${1#*/}" -ge 8 ] && [ "${1#*/}" -le 30 ]
}
randhex() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }
randalnum() { LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c "$1"; }
# sh-quote a value for the state file
shq() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

fetch() { # fetch URL OUT
    if have curl; then curl -fsSL --retry 2 -o "$2" "$1"
    elif have wget; then wget -q -O "$2" "$1"
    else die "need curl or wget to download $1"; fi
}
sha_of() { sha256sum "$1" | awk '{print $1}'; }

# render TEMPLATE OUT: @NAME@ -> $R_NAME; lines starting "@?NAME@" are kept
# (prefix removed) only when $R_NAME is non-empty and not 0.
render() {
    awk '
    {
        line = $0
        if (match(line, /^@\?[A-Z_]+@/)) {
            k = substr(line, 3, RLENGTH - 3)
            v = ENVIRON["R_" k]
            if (v == "" || v == "0") next
            line = substr(line, RLENGTH + 1)
        }
        out = ""
        while (match(line, /@[A-Z_]+@/)) {
            k = substr(line, RSTART + 1, RLENGTH - 2)
            if (("R_" k) in ENVIRON) val = ENVIRON["R_" k]
            else { printf "render: %s: no value for @%s@\n", FILENAME, k > "/dev/stderr"; bad = 1; val = "" }
            out = out substr(line, 1, RSTART - 1) val
            line = substr(line, RSTART + RLENGTH)
        }
        print out line
    }
    END { exit bad }' "$1" >"$2"
}

# put SRC DEST MODE: install SRC as DEST if it differs. Sets CHANGED=1 when it did.
# Existing config files are kept as DEST.bak-<time>.
CHANGED=0
put() {
    _src=$1 _dst=$2 _mode=$3
    if [ -f "$_dst" ] && cmp -s "$_src" "$_dst"; then
        return 0
    fi
    CHANGED=1
    if [ "$DRY" = 1 ]; then
        if [ -f "$_dst" ]; then say "  would update $_dst"; else say "  would write  $_dst"; fi
        return 0
    fi
    mkdir -p "$(dirname "$_dst")"
    # Keep the old version of configs (not of service files: init.d and
    # logrotate.d would pick the copies up).
    case "$_dst" in "$ETC"/* | "$SB_DIR"/* | /etc/keepalived/*) [ -f "$_dst" ] && cp -p "$_dst" "$_dst.bak-$TS" ;; esac
    cp "$_src" "$_dst.new.$$"
    chmod "$_mode" "$_dst.new.$$"
    mv -f "$_dst.new.$$" "$_dst"
    say "  wrote $_dst"
}

# ---------------------------------------------------------------- arguments
LAN_IF='' WAN_IF='' LAN_CIDR='' GATEWAY_IP='' DNS='' TZ_NAME='' PROFILE=''
EXIT_SPECS='' NO_VPN=0
DHCP='' DHCP_NOW=0 NO_DHCP=0
VIP='' VIP_PEER='' VIP_PRIO='' VRID='' NO_VIP=0
JUNCTIOND='' RELEASE='' LISTEN='' BRAND=''
SINGBOX_BIN='' REPLACE_SB=0

add_exit() { # add_exit TYPE NAME=VALUE
    case "$2" in *=*) ;; *) die "--exit-$1 wants NAME=VALUE, got '$2'" ;; esac
    EXIT_SPECS="${EXIT_SPECS}$1 $2
"
}

while [ $# -gt 0 ]; do
    case "$1" in
    --*=*)
        _k=${1%%=*} _val=${1#*=}
        shift
        set -- "$_k" "$_val" "$@"
        continue
        ;;
    esac
    _a=${2-}
    case "$1" in
    -h | --help) usage; exit 0 ;;
    -y | --yes) YES=1; shift; continue ;;
    -n | --dry-run) DRY=1; shift; continue ;;
    --force) FORCE=1; shift; continue ;;
    --skip-packages) SKIP_PKGS=1; shift; continue ;;
    --no-vpn) NO_VPN=1; shift; continue ;;
    --no-dhcp) NO_DHCP=1; shift; continue ;;
    --dhcp-now) DHCP_NOW=1; shift; continue ;;
    --no-vip) NO_VIP=1; shift; continue ;;
    --replace-singbox-config) REPLACE_SB=1; shift; continue ;;
    esac
    case "$1" in
    --lan-if | --wan-if | --lan-cidr | --gateway-ip | --dns | --timezone | --profile | \
        --exit-wireguard | --exit-hysteria2 | --exit-outbound | --dhcp | --vip | --vip-peer | \
        --vip-priority | --vrid | --junctiond | --release | --listen | --brand | --singbox-bin)
        [ $# -ge 2 ] || die "$1 needs a value (see --help)" ;;
    *) die "unknown flag: $1 (see --help)" ;;
    esac
    case "$1" in
    --lan-if) LAN_IF=$_a ;;
    --wan-if) WAN_IF=$_a ;;
    --lan-cidr) LAN_CIDR=$_a ;;
    --gateway-ip) GATEWAY_IP=$_a ;;
    --dns) DNS=$_a ;;
    --timezone) TZ_NAME=$_a ;;
    --profile) PROFILE=$_a ;;
    --exit-wireguard) add_exit wireguard "$_a" ;;
    --exit-hysteria2) add_exit hysteria2 "$_a" ;;
    --exit-outbound) add_exit outbound "$_a" ;;
    --dhcp) DHCP=$_a ;;
    --vip) VIP=$_a ;;
    --vip-peer) VIP_PEER=$_a ;;
    --vip-priority) VIP_PRIO=$_a ;;
    --vrid) VRID=$_a ;;
    --junctiond) JUNCTIOND=$_a ;;
    --release) RELEASE=$_a ;;
    --listen) LISTEN=$_a ;;
    --brand) BRAND=$_a ;;
    --singbox-bin) SINGBOX_BIN=$_a ;;
    *) die "unknown flag: $1 (see --help)" ;;
    esac
    shift 2
done

WORK=$(mktemp -d "${TMPDIR:-/tmp}/junction-setup.XXXXXX")
if [ "$DRY" = 1 ]; then
    trap 'say ""; say "Dry run: nothing was changed. Rendered files are in $WORK"' EXIT
else
    trap 'rm -rf "$WORK"' EXIT
fi

# ---------------------------------------------------------------- preflight
step "Checking this box"
if [ "$(id -u)" != 0 ]; then
    if [ "$DRY" = 1 ]; then warn "not root: fine for a dry run"; else die "run as root (sudo sh $0 ...)"; fi
fi

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
armv7* | armv8l) ARCH=armv7 ;;
*) die "unsupported CPU $(uname -m): Junction builds exist for amd64, arm64 and armv7 (Raspberry Pi 2 or newer)" ;;
esac

OS_ID='' OS_LIKE=''
if [ -r /etc/os-release ]; then
    OS_ID=$(. /etc/os-release && echo "${ID:-}")
    OS_LIKE=$(. /etc/os-release && echo "${ID_LIKE:-}")
fi
case "$OS_ID $OS_LIKE" in
alpine*) PKG=apk INIT=openrc ;;
debian* | ubuntu* | raspbian* | *debian* | *ubuntu*) PKG=apt INIT=systemd ;;
*)
    PKG=none
    if [ -d /run/systemd/system ]; then INIT=systemd
    elif have openrc-run || [ -x /sbin/openrc-run ]; then INIT=openrc
    else die "unsupported system '$OS_ID': use Debian, Ubuntu, Raspberry Pi OS or Alpine"; fi
    [ "$SKIP_PKGS" = 1 ] || die "don't know how to install packages on '$OS_ID': install iptables iproute2 jq curl (+dnsmasq, keepalived) and re-run with --skip-packages"
    ;;
esac
if [ "$INIT" = systemd ] && [ ! -d /run/systemd/system ]; then
    die "this $OS_ID system isn't running systemd (a container without it?); not supported"
fi
say "System: ${OS_ID:-unknown}, $ARCH, $INIT"

# Leftovers of a hand-made setup would defeat fail-open (they re-add TPROXY at boot).
for f in /etc/local.d/tproxy.start /etc/sing-box/tproxy-setup.sh; do
    [ -e "$f" ] && warn "$f exists: a hand-made TPROXY setup. Remove it, or it will hook TPROXY in even while sing-box is down."
done

# ---------------------------------------------------------------- detection
default_if() {
    if have ip; then
        ip -4 route show default 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "dev") { print $(i + 1); exit }}'
    else
        awk '$2 == "00000000" { print $1; exit }' /proc/net/route 2>/dev/null
    fi
}
if_addr() { # first primary IPv4 address/prefix of IF
    ip -4 addr show dev "$1" 2>/dev/null | awk '/inet / && !/secondary/ { print $2; exit }'
}
detect_tz() {
    _t=
    have timedatectl && _t=$(timedatectl show -p Timezone --value 2>/dev/null || true)
    [ -n "$_t" ] || { [ -r /etc/timezone ] && _t=$(head -n 1 /etc/timezone); }
    if [ -z "$_t" ] && [ -L /etc/localtime ]; then
        _t=$(readlink /etc/localtime | sed -n 's|.*/zoneinfo/||p')
    fi
    [ -n "$_t" ] || _t=${TZ:-UTC}
    echo "$_t"
}
if_dynamic() { # address of IF came from DHCP?
    ip -4 addr show dev "$1" 2>/dev/null | grep -q dynamic && return 0
    [ -r /etc/network/interfaces ] && grep -Eq "^[[:space:]]*iface[[:space:]]+$1[[:space:]]+inet[[:space:]]+dhcp" /etc/network/interfaces && return 0
    return 1
}

# Values from the last run are the defaults this time.
if [ -r "$STATE" ]; then
    # shellcheck disable=SC1090
    . "$STATE"
fi
[ -n "$LAN_IF" ] || LAN_IF=${S_LAN_IF:-}
[ -n "$LAN_IF" ] || LAN_IF=$(default_if)
ask LAN_IF "LAN interface (devices reach this box on it)" "$LAN_IF"
[ -n "$LAN_IF" ] || die "no LAN interface found: pass --lan-if"
[ -d "/sys/class/net/$LAN_IF" ] || [ "$DRY" = 1 ] || die "no interface '$LAN_IF'"
[ -n "$WAN_IF" ] || WAN_IF=${S_WAN_IF:-$LAN_IF}

SELF_CIDR=$(if_addr "$LAN_IF")
SELF_IP=${SELF_CIDR%/*}
if [ -z "$SELF_IP" ]; then
    [ "$DRY" = 1 ] || die "$LAN_IF has no IPv4 address"
    warn "$LAN_IF has no IPv4 address; using placeholders for the dry run"
    SELF_CIDR=192.168.1.2/24 SELF_IP=192.168.1.2
fi
[ -n "$LAN_CIDR" ] || LAN_CIDR=${S_LAN_CIDR:-$(network_of "$SELF_CIDR")}
ask LAN_CIDR "LAN (address range of your network)" "$LAN_CIDR"
valid_cidr "$LAN_CIDR" || die "bad LAN '$LAN_CIDR' (want e.g. 192.168.1.0/24)"
LAN_CIDR=$(network_of "$LAN_CIDR")
in_cidr "$SELF_IP" "$LAN_CIDR" || die "this box ($SELF_IP) is not inside $LAN_CIDR"
ROUTER_IP=$(ip -4 route show default 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "via") { print $(i + 1); exit }}')
say "This box: $SELF_IP on $LAN_IF; LAN $LAN_CIDR; router ${ROUTER_IP:-unknown}"
if if_dynamic "$LAN_IF"; then
    warn "this box got its address from DHCP. A gateway needs a fixed address: give it a static IP or a DHCP reservation on your router."
fi

[ -n "$TZ_NAME" ] || TZ_NAME=${S_TZ:-$(detect_tz)}
ask TZ_NAME "Time zone" "$TZ_NAME"
[ -n "$DNS" ] || DNS=${S_DNS:-1.1.1.1,8.8.8.8}
DNS=$(echo "$DNS" | tr -d ' ')
for d in $(echo "$DNS" | tr ',' ' '); do valid_ip "$d" || die "bad DNS server '$d'"; done
DNS1=${DNS%%,*}
[ -n "$PROFILE" ] || PROFILE=${S_PROFILE:-default}
case "$PROFILE" in '' | *[!a-z0-9]*) die "profile names are lowercase letters and digits" ;; esac
[ -n "$LISTEN" ] || LISTEN=${S_LISTEN:-:80}
[ -n "$BRAND" ] || BRAND=${S_BRAND:-Junction}

# ---------------------------------------------------------------- floating IP
[ "$NO_VIP" = 1 ] && VIP= || { [ -n "$VIP" ] || VIP=${S_VIP:-}; }
if [ -z "$VIP" ] && [ "$NO_VIP" = 0 ] && [ "$YES" = 0 ] && [ "$TTY" = 1 ]; then
    if yesno "Use a floating gateway address (keepalived) so a backup box can take over?" n; then
        ask VIP "Floating address with prefix, e.g. $(int2ip $(($(ip2int "${LAN_CIDR%/*}") + 4)))/${LAN_CIDR#*/}" ""
    fi
fi
VIP_ADDR=
if [ -n "$VIP" ]; then
    case "$VIP" in */*) ;; *) VIP="$VIP/${LAN_CIDR#*/}" ;; esac
    valid_cidr "$VIP" || die "bad --vip '$VIP'"
    VIP_ADDR=${VIP%/*}
    in_cidr "$VIP_ADDR" "$LAN_CIDR" || die "floating address $VIP_ADDR is outside $LAN_CIDR"
    [ "$VIP_ADDR" != "$SELF_IP" ] || die "the floating address must differ from this box's own ($SELF_IP)"
    [ -n "$VIP_PEER" ] || VIP_PEER=${S_VIP_PEER:-}
    if [ -z "$VIP_PEER" ] && [ "$YES" = 0 ] && [ "$TTY" = 1 ]; then
        ask VIP_PEER "Backup box's address (empty = none yet, multicast)" ""
    fi
    [ -z "$VIP_PEER" ] || valid_ip "$VIP_PEER" || die "bad --vip-peer '$VIP_PEER'"
    [ -n "$VIP_PRIO" ] || VIP_PRIO=${S_VIP_PRIO:-150}
    [ -n "$VRID" ] || VRID=${S_VRID:-52}
    case "$VRID" in '' | *[!0-9]*) die "bad --vrid" ;; esac
    [ "$VRID" -ge 1 ] && [ "$VRID" -le 255 ] || die "--vrid must be 1-255"
    VIP_AUTH=${S_VIP_AUTH:-$(randalnum 8)}
    # Is somebody already using that address?
    if ! ip -4 addr show 2>/dev/null | grep -q "inet $VIP_ADDR/" && have ping; then
        if ping -c 2 -W 1 "$VIP_ADDR" >/dev/null 2>&1; then
            [ "$FORCE" = 1 ] || die "$VIP_ADDR answers ping: something on your network already uses it (pick another, or --force)"
            warn "$VIP_ADDR answers ping (continuing: --force)"
        fi
    fi
fi

[ -n "$GATEWAY_IP" ] || GATEWAY_IP=${VIP_ADDR:-$SELF_IP}
ask GATEWAY_IP "Address devices will use as their gateway" "$GATEWAY_IP"
valid_ip "$GATEWAY_IP" || die "bad gateway address '$GATEWAY_IP'"

# ---------------------------------------------------------------- DHCP
[ "$NO_DHCP" = 1 ] && DHCP= || { [ -n "$DHCP" ] || DHCP=${S_DHCP:-}; }
if [ -z "$DHCP" ] && [ "$NO_DHCP" = 0 ] && [ "$YES" = 0 ] && [ "$TTY" = 1 ]; then
    if yesno "Run a DHCP server here (dnsmasq)? Only if you will turn your router's DHCP off" n; then
        _n=$(ip2int "${LAN_CIDR%/*}")
        ask DHCP "Address pool START-END[,LEASE]" "$(int2ip $((_n + 100)))-$(int2ip $((_n + 199))),12h"
    fi
fi
POOL_START='' POOL_END='' LEASE=12h
if [ -n "$DHCP" ]; then
    _pool=${DHCP%%,*}
    case "$DHCP" in *,*) LEASE=${DHCP#*,} ;; esac
    POOL_START=${_pool%-*} POOL_END=${_pool#*-}
    valid_ip "$POOL_START" && valid_ip "$POOL_END" || die "bad --dhcp '$DHCP' (want START-END[,LEASE])"
    in_cidr "$POOL_START" "$LAN_CIDR" && in_cidr "$POOL_END" "$LAN_CIDR" || die "DHCP pool is outside $LAN_CIDR"
    [ "$(ip2int "$POOL_START")" -le "$(ip2int "$POOL_END")" ] || die "DHCP pool start is after its end"
    for a in "$SELF_IP" "$GATEWAY_IP" ${ROUTER_IP:+"$ROUTER_IP"}; do
        if [ "$(ip2int "$a")" -ge "$(ip2int "$POOL_START")" ] && [ "$(ip2int "$a")" -le "$(ip2int "$POOL_END")" ]; then
            die "DHCP pool $POOL_START-$POOL_END contains $a"
        fi
    done
    case "$LEASE" in *[!0-9smhdw]* | '') die "bad lease time '$LEASE' (e.g. 12h)" ;; esac
    if [ "$DHCP_NOW" = 0 ] && [ "$YES" = 0 ] && [ "$TTY" = 1 ]; then
        yesno "Is your router's DHCP server already OFF? (dnsmasq starts only then)" n && DHCP_NOW=1
    fi
fi

# ---------------------------------------------------------------- VPN exits
EXITS_DIR="$WORK/exits"
mkdir -p "$EXITS_DIR"
NEXIT=0

check_name() {
    case "$1" in '' | *[!a-z0-9]*) die "exit name '$1': use lowercase letters and digits only" ;; esac
    case "$1" in direct | vpn | probe | block | dns) die "exit name '$1' is reserved" ;; esac
    [ ${#1} -le 16 ] || die "exit name '$1' is too long"
    [ ! -e "$EXITS_DIR/$1" ] || die "two exits are called '$1'"
    : >"$EXITS_DIR/$1"
    next_exit
}
# The exits keep their order (the first is the default): 01.json, 02.json, ...
next_exit() { NEXIT=$((NEXIT + 1)); EXIT_OUT="$EXITS_DIR/$(printf "%02d" "$NEXIT").json"; }

pdecode() { # percent-decoding
    printf '%s' "$1" | LC_ALL=C awk 'BEGIN { h = "0123456789ABCDEF" }
    {
        s = $0; out = ""
        while ((i = index(s, "%")) > 0) {
            x = toupper(substr(s, i + 1, 2))
            if (x ~ /^[0-9A-F][0-9A-F]$/) {
                out = out substr(s, 1, i - 1) sprintf("%c", (index(h, substr(x, 1, 1)) - 1) * 16 + index(h, substr(x, 2, 1)) - 1)
                s = substr(s, i + 3)
            } else { out = out substr(s, 1, i); s = substr(s, i + 1) }
        }
        printf "%s", out s
    }'
}

exit_wireguard() { # NAME FILE
    check_name "$1"
    [ -r "$2" ] || die "can't read WireGuard file $2"
    _kv=$(awk '
        /^[ \t]*(#|;|$)/ { next }
        /^[ \t]*\[/ { s = tolower($0); gsub(/[][ \t\r]/, "", s); if (s == "peer") np++; next }
        { i = index($0, "="); if (!i) next
          k = tolower(substr($0, 1, i - 1)); gsub(/[ \t]/, "", k)
          v = substr($0, i + 1); gsub(/^[ \t]+|[ \t\r]+$/, "", v)
          if (s == "peer" && np > 1) next
          print s "." k "=" v }
        END { if (np > 1) print "multi=1" }' "$2")
    _pk='' _addr='' _mtu='' _pub='' _psk='' _ep='' _allowed='' _ka=''
    while IFS= read -r l; do
        case "$l" in
        interface.privatekey=*) _pk=${l#*=} ;;
        interface.address=*) _addr="${_addr:+$_addr,}${l#*=}" ;;
        interface.mtu=*) _mtu=${l#*=} ;;
        peer.publickey=*) _pub=${l#*=} ;;
        peer.presharedkey=*) _psk=${l#*=} ;;
        peer.endpoint=*) _ep=${l#*=} ;;
        peer.allowedips=*) _allowed="${_allowed:+$_allowed,}${l#*=}" ;;
        peer.persistentkeepalive=*) _ka=${l#*=} ;;
        multi=1) warn "$2 has more than one [Peer]; only the first is used" ;;
        esac
    done <<EOF
$_kv
EOF
    [ -n "$_pk" ] && [ -n "$_pub" ] && [ -n "$_ep" ] && [ -n "$_addr" ] ||
        die "$2: need Interface PrivateKey and Address, Peer PublicKey and Endpoint"
    _port=${_ep##*:} _host=${_ep%:*}
    _host=${_host#\[}
    _host=${_host%\]}
    case "$_port" in '' | *[!0-9]*) die "$2: Endpoint must be host:port" ;; esac
    jq -n --arg name "$1" --arg pk "$_pk" --arg addr "$_addr" --arg mtu "$_mtu" --arg pub "$_pub" \
        --arg psk "$_psk" --arg host "$_host" --arg port "$_port" --arg allowed "${_allowed:-0.0.0.0/0}" --arg ka "$_ka" '
        def list: split(",") | map(gsub("^\\s+|\\s+$"; "")) | map(select(length > 0));
        {name: $name, kind: "endpoint", server: $host,
         obj: ({type: "wireguard", system: false,
                address: ($addr | list), private_key: $pk,
                peers: [({address: $host, port: ($port | tonumber), public_key: $pub,
                          allowed_ips: ($allowed | list)}
                         + (if $psk != "" then {pre_shared_key: $psk} else {} end)
                         + (if $ka != "" then {persistent_keepalive_interval: ($ka | tonumber)} else {} end))]}
               + (if $mtu != "" then {mtu: ($mtu | tonumber)} else {} end))}' >"$EXIT_OUT"
    say "  VPN exit '$1': WireGuard via $_host:$_port"
}

exit_hysteria2() { # NAME URI
    check_name "$1"
    _u=$2
    case "$_u" in hysteria2://* | hy2://*) _u=${_u#*://} ;; *) die "'$2' is not a hysteria2:// link" ;; esac
    _u=${_u%%#*}
    _q=
    case "$_u" in *\?*) _q=${_u#*\?} _u=${_u%%\?*} ;; esac
    _u=${_u%/}
    _auth=
    case "$_u" in *@*) _auth=$(pdecode "${_u%@*}") _u=${_u##*@} ;; esac
    case "$_u" in
    \[*\]:*) _host=${_u%%\]*} _host=${_host#\[} _port=${_u##*\]:} ;;
    \[*\]) _host=${_u#\[} _host=${_host%\]} _port=443 ;;
    *:*) _host=${_u%:*} _port=${_u##*:} ;;
    *) _host=$_u _port=443 ;;
    esac
    case "$_port" in *[,-]*)
        warn "port range '$_port' in the link: using the first port only"
        _port=${_port%%[,-]*} ;;
    esac
    case "$_port" in '' | *[!0-9]*) die "bad port in hysteria2 link" ;; esac
    [ -n "$_host" ] && [ -n "$_auth" ] || die "hysteria2 link needs password@host"
    _sni='' _ins=0 _obfs='' _obfspw=''
    _o=$IFS
    IFS='&'
    for kv in $_q; do
        case "$kv" in
        sni=*) _sni=$(pdecode "${kv#*=}") ;;
        insecure=*) _ins=${kv#*=} ;;
        obfs=*) _obfs=$(pdecode "${kv#*=}") ;;
        obfs-password=*) _obfspw=$(pdecode "${kv#*=}") ;;
        esac
    done
    IFS=$_o
    jq -n --arg name "$1" --arg host "$_host" --arg port "$_port" --arg pw "$_auth" --arg sni "$_sni" \
        --arg ins "$_ins" --arg obfs "$_obfs" --arg obfspw "$_obfspw" '
        {name: $name, kind: "outbound", server: $host,
         obj: ({type: "hysteria2", server: $host, server_port: ($port | tonumber), password: $pw,
                tls: ({enabled: true, server_name: (if $sni != "" then $sni else $host end)}
                      + (if $ins == "1" or $ins == "true" then {insecure: true} else {} end))}
               + (if $obfs != "" then {obfs: {type: $obfs, password: $obfspw}} else {} end))}' >"$EXIT_OUT"
    say "  VPN exit '$1': Hysteria2 via $_host:$_port"
}

exit_outbound() { # NAME FILE
    check_name "$1"
    [ -r "$2" ] || die "can't read $2"
    jq -e 'type == "object" and (.type | type == "string")' "$2" >/dev/null 2>&1 ||
        die "$2 must hold one sing-box outbound as a JSON object, with a \"type\""
    jq --arg name "$1" '
        {name: $name,
         kind: (if .type == "wireguard" then "endpoint" else "outbound" end),
         server: (.server // .peers[0].address // ""),
         obj: (del(.tag))}' "$2" >"$EXIT_OUT"
    say "  VPN exit '$1': $(jq -r .type "$2") outbound from $2"
}

step "VPN exits"
if [ -n "$EXIT_SPECS" ] || [ "$NO_VPN" = 1 ]; then
    : # given on the command line
elif [ -s "$SAVED_EXITS" ] && have jq && yesno "Keep the VPN exit(s) set up last time ($(jq -r 'map(.name) | join(", ")' "$SAVED_EXITS"))?" y; then
    USE_SAVED=1
elif [ "$YES" = 0 ] && [ "$TTY" = 1 ]; then
    say "Devices you switch to VPN in Junction go out through these exits; everything else goes direct."
    _t='' _name='' _f=''
    while :; do
        ask _t "Add a VPN exit: [n]one/done, [w]ireguard .conf, [h]ysteria2 link, [r]aw sing-box outbound JSON" n
        case "$_t" in
        w* | h* | r*)
            ask _name "Name for this exit (lowercase letters/digits)" "vpn$((NEXIT + 1))"
            case "$_t" in
            w*) ask _f "Path to the WireGuard .conf file" ""; add_exit wireguard "$_name=$_f" ;;
            h*) ask _f "hysteria2:// link" ""; add_exit hysteria2 "$_name=$_f" ;;
            r*) ask _f "Path to the outbound JSON file" ""; add_exit outbound "$_name=$_f" ;;
            esac
            NEXIT=$((NEXIT + 1))
            ;;
        *) break ;;
        esac
    done
    NEXIT=0
fi

# ---------------------------------------------------------------- packages
step "Packages"
PKGS="iptables iproute2 jq curl ca-certificates logrotate"
if [ "$PKG" = apt ]; then
    [ -n "$DHCP" ] && PKGS="$PKGS dnsmasq-base"
    [ -n "$VIP" ] && PKGS="$PKGS keepalived iputils-ping"
else
    [ -n "$DHCP" ] && PKGS="$PKGS dnsmasq"
    [ -n "$VIP" ] && PKGS="$PKGS keepalived"
fi
if [ "$SKIP_PKGS" = 1 ]; then
    say "skipped (--skip-packages): need $PKGS"
elif [ "$PKG" = apk ]; then
    # shellcheck disable=SC2086
    run apk add --no-cache $PKGS
else
    missing=
    for p in $PKGS; do dpkg -s "$p" >/dev/null 2>&1 || missing="$missing $p"; done
    if [ -n "$missing" ]; then
        run apt-get update -qq
        # shellcheck disable=SC2086
        run env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends $missing
    else
        say "all present"
    fi
fi
have jq || [ "$DRY" = 1 ] || die "jq is missing"
have jq || die "the dry run needs jq to render the configs (install jq)"

# Now that jq is there: build the exits.
if [ -n "$EXIT_SPECS" ]; then
    while IFS= read -r spec; do
        [ -n "$spec" ] || continue
        _type=${spec%% *} _nv=${spec#* }
        _name=${_nv%%=*} _val=${_nv#*=}
        case "$_type" in
        wireguard) exit_wireguard "$_name" "$_val" ;;
        hysteria2) exit_hysteria2 "$_name" "$_val" ;;
        outbound) exit_outbound "$_name" "$_val" ;;
        esac
    done <<EOF
$EXIT_SPECS
EOF
fi
if [ "${USE_SAVED:-0}" = 1 ]; then
    cp "$SAVED_EXITS" "$WORK/exits.json"
elif ls "$EXITS_DIR"/*.json >/dev/null 2>&1; then
    jq -s . "$EXITS_DIR"/*.json >"$WORK/exits.json"
else
    echo '[]' >"$WORK/exits.json"
fi
NEXITS=$(jq length "$WORK/exits.json")
if [ "$NEXITS" -eq 0 ]; then say "no VPN exit: everything goes direct (add one later by re-running setup.sh)"; fi
FAILOVER=false
[ "$NEXITS" -gt 1 ] && FAILOVER=true

# ---------------------------------------------------------------- sing-box binary
step "sing-box $SINGBOX_VERSION"
SB=/usr/local/bin/sing-box
case "$ARCH" in amd64) SB_SHA=$SB_SHA_amd64 ;; arm64) SB_SHA=$SB_SHA_arm64 ;; armv7) SB_SHA=$SB_SHA_armv7 ;; esac
SB_NEW=
if [ -n "$SINGBOX_BIN" ]; then
    [ -x "$SINGBOX_BIN" ] || die "--singbox-bin $SINGBOX_BIN is not executable"
    SB_NEW=$SINGBOX_BIN
elif [ -x "$SB" ] && "$SB" version 2>/dev/null | head -n 1 | grep -q "version $SINGBOX_VERSION\$"; then
    say "already installed"
elif [ "$DRY" = 1 ]; then
    say "  would download sing-box-$SINGBOX_VERSION-linux-$ARCH.tar.gz, check sha256 $SB_SHA, install $SB"
else
    _url="https://github.com/SagerNet/sing-box/releases/download/v$SINGBOX_VERSION/sing-box-$SINGBOX_VERSION-linux-$ARCH.tar.gz"
    say "downloading $_url"
    fetch "$_url" "$WORK/sb.tgz"
    [ "$(sha_of "$WORK/sb.tgz")" = "$SB_SHA" ] || die "sing-box download: sha256 mismatch"
    tar -xzf "$WORK/sb.tgz" -C "$WORK"
    SB_NEW="$WORK/sing-box-$SINGBOX_VERSION-linux-$ARCH/sing-box"
fi
SB_CHANGED=0
if [ -n "$SB_NEW" ]; then
    CHANGED=0
    put "$SB_NEW" "$SB" 755
    SB_CHANGED=$CHANGED
fi
SB_CHECK=$SB
[ -x "$SB_CHECK" ] || SB_CHECK=$(command -v sing-box 2>/dev/null || true)

# ---------------------------------------------------------------- render configs
step "Configuration"
SB_MANAGED=1
if [ -f "$SB_CONFIG" ] && [ "${S_MANAGED:-0}" != 1 ] && [ "$REPLACE_SB" = 0 ]; then
    [ "$FORCE" = 1 ] || die "$SB_CONFIG exists and wasn't made by this script. Re-run with --replace-singbox-config (it is backed up first)."
    warn "keeping your own $SB_CONFIG (--force): make sure it has the TPROXY inbound on 7893, the Clash API on 127.0.0.1:9090 and the rule-sets"
    SB_MANAGED=0
fi

SECRET=
[ -r "$SB_CONFIG" ] && SECRET=$(jq -r '.experimental.clash_api.secret // empty' "$SB_CONFIG" 2>/dev/null || true)
[ -n "$SECRET" ] || SECRET=$(randhex 32)
SELFIPS=$(jq -cn --arg a "$SELF_IP" --arg b "$VIP_ADDR" '[$a, $b] | map(select(. != ""))')

jq --arg secret "$SECRET" --arg dns "$DNS1" --arg profile "$PROFILE" --argjson selfips "$SELFIPS" \
    --argjson failover "$FAILOVER" --argjson probe_base 22101 --slurpfile exits "$WORK/exits.json" \
    -f "$TEMPLATES/sing-box.jq" "$TEMPLATES/sing-box.json.in" >"$WORK/sing-box.json"

if [ "$SB_MANAGED" = 1 ]; then
    if [ -n "$SB_CHECK" ] && [ -x "$SB_CHECK" ]; then
        # The rule-set files must exist for the check; use temp copies.
        mkdir -p "$WORK/rs"
        for f in vpn-devices paused-devices "profile-$PROFILE"; do
            if [ -r "$SB_DIR/$f.json" ]; then cp "$SB_DIR/$f.json" "$WORK/rs/"
            else printf '{"version":2,"rules":[{"source_ip_cidr":["192.0.2.255/32"]}]}\n' >"$WORK/rs/$f.json"; fi
        done
        sed -e "s#$SB_DIR/#$WORK/rs/#g" -e "s#/var/lib/sing-box/#$WORK/rs/#g" "$WORK/sing-box.json" >"$WORK/sing-box-check.json"
        if ! _err=$("$SB_CHECK" check -c "$WORK/sing-box-check.json" 2>&1); then
            printf '%s\n' "$_err" >&2
            die "the generated sing-box config does not pass 'sing-box check' (nothing was changed). Rendered: $WORK/sing-box.json"
        fi
        say "sing-box config: passes sing-box check"
    else
        say "sing-box config: not checked (sing-box is not installed yet in this dry run)"
    fi
fi

# Junction's config; keys added by hand to an existing file are kept.
DHCP_ON=false
[ -n "$DHCP" ] && DHCP_ON=true
jq -n --arg listen "$LISTEN" --arg lan "$LAN_CIDR" --arg ifc "$LAN_IF" --arg tz "$TZ_NAME" \
    --arg gw "$GATEWAY_IP" --arg profile "$PROFILE" --arg brand "$BRAND" --arg leases "$LEASES" \
    --argjson dhcp "$DHCP_ON" --argjson fo "$FAILOVER" '
    {listen: $listen, db: "/var/lib/junction/junction.db",
     singbox_config: "/etc/sing-box/config.json", clash_api: "http://127.0.0.1:9090",
     failover_api: "", lan: $lan, interface: $ifc, timezone: $tz, gateway_ip: $gw,
     default_profile: $profile, brand_name: $brand,
     dhcp_listen: ($dhcp | not), dnsmasq_leases: $leases, failover_enabled: $fo}
    + (if $fo then {failover_config: "/etc/junction/failover.json"} else {} end)' >"$WORK/junction-new.json"
if [ -r "$ETC/config.json" ] && jq -e 'type == "object"' "$ETC/config.json" >/dev/null 2>&1; then
    jq -s '.[0] * .[1]' "$ETC/config.json" "$WORK/junction-new.json" >"$WORK/junction.json"
else
    cp "$WORK/junction-new.json" "$WORK/junction.json"
fi

if [ "$FAILOVER" = true ]; then
    jq -n --arg p "$PROFILE" --argjson base 22101 --slurpfile exits "$WORK/exits.json" '
        {probe_url: "http://cp.cloudflare.com", default_profile: $p, profiles: {($p): ""},
         state_path: "/var/lib/junction/failover-state.json",
         endpoints: [$exits[0] | to_entries[] |
                     {key: .value.name, name: .value.name, ip: .value.server, probe_ports: {($p): ($base + .key)}}]}' \
        >"$WORK/failover.json"
fi

export R_MODE=tproxy R_LAN_IF="$LAN_IF" R_WAN_IF="$WAN_IF" R_LAN_CIDR="$LAN_CIDR" R_GATEWAY_IP="$GATEWAY_IP"
render "$TEMPLATES/tproxy.conf.in" "$WORK/tproxy.conf"
render "$TEMPLATES/sysctl.conf.in" "$WORK/sysctl.conf"
if [ -n "$DHCP" ]; then
    _auth=
    [ "$DHCP_NOW" = 1 ] && _auth=1
    R_NETMASK=$(int2ip "$(prefix_mask "${LAN_CIDR#*/}")")
    export R_AUTHORITATIVE="$_auth" R_POOL_START="$POOL_START" R_POOL_END="$POOL_END" R_LEASE="$LEASE" \
        R_NETMASK R_DNS_LIST="$DNS" R_LEASES="$LEASES"
    render "$TEMPLATES/dnsmasq.conf.in" "$WORK/dnsmasq.conf"
fi
if [ -n "$VIP" ]; then
    _track=
    [ -n "$VIP_PEER" ] && _track=1
    export R_VIP="$VIP" R_VIP_ADDR="$VIP_ADDR" R_VRID="$VRID" R_PRIORITY="$VIP_PRIO" R_BACKUP_PRIORITY=100 \
        R_SELF_IP="$SELF_IP" R_PEER="$VIP_PEER" R_AUTH_PASS="$VIP_AUTH" R_TRACK="$_track"
    render "$TEMPLATES/keepalived.conf.in" "$WORK/keepalived.conf"
    render "$TEMPLATES/keepalived-backup.conf.in" "$WORK/keepalived-backup.conf"
    render "$TEMPLATES/junction-backup-notify.in" "$WORK/junction-backup-notify"
fi

{
    echo "# Written by Junction's setup.sh: the answers from the last run."
    echo "S_MANAGED=$SB_MANAGED"
    for kv in "S_LAN_IF=$LAN_IF" "S_WAN_IF=$WAN_IF" "S_LAN_CIDR=$LAN_CIDR" "S_TZ=$TZ_NAME" "S_DNS=$DNS" \
        "S_PROFILE=$PROFILE" "S_LISTEN=$LISTEN" "S_BRAND=$BRAND" "S_VIP=$VIP" "S_VIP_PEER=$VIP_PEER" \
        "S_VIP_PRIO=$VIP_PRIO" "S_VRID=$VRID" "S_VIP_AUTH=${VIP_AUTH:-}" "S_DHCP=$DHCP"; do
        echo "${kv%%=*}=$(shq "${kv#*=}")"
    done
} >"$WORK/setup.env"

# ---------------------------------------------------------------- junctiond binary
step "junctiond"
JD_SRC=
if [ -n "$JUNCTIOND" ]; then
    JD_SRC=$JUNCTIOND
elif [ -n "$RELEASE" ]; then
    case "$RELEASE" in
    http://* | https://*)
        say "downloading $RELEASE"
        fetch "$RELEASE" "$WORK/release.tgz"
        _sums="${RELEASE%/*}/SHA256SUMS"
        if fetch "$_sums" "$WORK/SHA256SUMS" 2>/dev/null; then
            _want=$(awk -v f="${RELEASE##*/}" '$2 == f || $2 == "*" f { print $1 }' "$WORK/SHA256SUMS")
            [ -n "$_want" ] && [ "$_want" = "$(sha_of "$WORK/release.tgz")" ] || die "release sha256 does not match SHA256SUMS"
            say "sha256 ok"
        else
            warn "no SHA256SUMS next to the release: not verified"
        fi
        _tgz="$WORK/release.tgz"
        ;;
    *)
        _tgz=$RELEASE
        _sums="$(dirname "$RELEASE")/SHA256SUMS"
        if [ -r "$_sums" ]; then
            _want=$(awk -v f="${RELEASE##*/}" '$2 == f || $2 == "*" f { print $1 }' "$_sums")
            [ -z "$_want" ] || [ "$_want" = "$(sha_of "$RELEASE")" ] || die "$RELEASE: sha256 does not match $_sums"
        fi
        ;;
    esac
    mkdir -p "$WORK/release"
    tar -xzf "$_tgz" -C "$WORK/release"
    JD_SRC=$(find "$WORK/release" -name junctiond -type f | head -n 1)
    [ -n "$JD_SRC" ] || die "no junctiond in $RELEASE"
elif [ -f "$HERE/../junctiond" ]; then
    JD_SRC="$HERE/../junctiond"
elif [ -x /usr/local/bin/junctiond ]; then
    say "keeping the installed /usr/local/bin/junctiond"
else
    [ "$DRY" = 1 ] || die "no junctiond binary: pass --junctiond FILE or --release FILE|URL (or run from an unpacked release)"
    warn "no junctiond binary given"
fi
JD_CHANGED=0
if [ -n "$JD_SRC" ]; then
    CHANGED=0
    put "$JD_SRC" /usr/local/bin/junctiond 755
    JD_CHANGED=$CHANGED
fi
# Updates from the Health page go through this: it keeps the previous junctiond
# and switches back if the new one isn't healthy within 60 s.
put "$HERE/safe-update.sh" /usr/local/lib/junction/safe-update.sh 755

if [ "$DRY" = 1 ]; then
    step "Files this run would install (rendered in $WORK)"
fi

# ---------------------------------------------------------------- services
svc_file() { # NAME -> install the unit / init script
    if [ "$INIT" = systemd ]; then put "$SERVICES/systemd/$1.service" "/etc/systemd/system/$1.service" 644
    else put "$SERVICES/openrc/$1" "/etc/init.d/$1" 755; fi
}
svc_enable() {
    if [ "$INIT" = systemd ]; then run systemctl enable -q "$1"
    else run rc-update add "$1" default; fi
}
svc_restart() {
    if [ "$INIT" = systemd ]; then run systemctl restart "$1"
    else run rc-service "$1" restart; fi
}
svc_running() {
    if [ "$INIT" = systemd ]; then systemctl is-active -q "$1" 2>/dev/null
    else rc-service "$1" status >/dev/null 2>&1; fi
}
svc_up() { # NAME CHANGED: (re)start when changed or not running
    if [ "$2" = 1 ] || ! svc_running "$1"; then svc_restart "$1"; fi
}
reload_units() { [ "$INIT" = systemd ] && run systemctl daemon-reload; return 0; }

step "Installing"
# Our own scripts, and a copy of this kit for uninstall.sh and re-runs.
CHANGED=0
put "$HERE/junction-tproxy" /usr/local/sbin/junction-tproxy 755
put "$WORK/tproxy.conf" "$ETC/tproxy.conf" 644
TP_CHANGED=$CHANGED
put "$HERE/junction-gateway-check" /usr/local/sbin/junction-gateway-check 755
put "$HERE/junction-gateway-notify" /usr/local/sbin/junction-gateway-notify 755
if [ "$HERE" != "$SHARE/gateway" ]; then
    if [ "$DRY" = 1 ]; then say "  would copy this kit to $SHARE (for uninstall.sh)"
    else
        rm -rf "$SHARE.new" && mkdir -p "$SHARE.new"
        cp -R "$HERE" "$SHARE.new/gateway"
        cp -R "$SERVICES" "$SHARE.new/services"
        rm -rf "$SHARE" && mv "$SHARE.new" "$SHARE"
    fi
fi
put "$WORK/sysctl.conf" /etc/sysctl.d/90-junction.conf 644
[ "$CHANGED" = 1 ] && run sysctl -q -p /etc/sysctl.d/90-junction.conf >/dev/null 2>&1 || true
put "$SERVICES/logrotate.conf" /etc/logrotate.d/junction 644

# sing-box config and the rule-set files (Junction owns those once it runs).
CHANGED=$SB_CHANGED
if [ "$SB_MANAGED" = 1 ]; then
    [ "$DRY" = 1 ] || mkdir -p "$SB_DIR" /var/lib/sing-box
    put "$WORK/sing-box.json" "$SB_CONFIG" 600
fi
for f in vpn-devices paused-devices "profile-$PROFILE"; do
    if [ ! -f "$SB_DIR/$f.json" ]; then
        printf '{\n  "version": 2,\n  "rules": [\n    {\n      "source_ip_cidr": [\n        "192.0.2.255/32"\n      ]\n    }\n  ]\n}\n' >"$WORK/empty-rs.json"
        put "$WORK/empty-rs.json" "$SB_DIR/$f.json" 644
    fi
done
SB_CHANGED=$CHANGED
_c=0
CHANGED=0
svc_file sing-box
svc_file junction-tproxy
svc_file junctiond
[ -n "$DHCP" ] && svc_file junction-dhcp
[ "$CHANGED" = 1 ] && reload_units
_c=$CHANGED

step "Starting sing-box"
svc_enable sing-box
svc_up sing-box "$( [ "$SB_CHANGED" = 1 ] || [ "$_c" = 1 ] && echo 1 || echo 0)"
if [ "$DRY" = 0 ]; then
    i=0
    until curl -fsS -m 2 -o /dev/null -H "Authorization: Bearer $SECRET" http://127.0.0.1:9090/version 2>/dev/null; do
        i=$((i + 1))
        [ $i -lt 20 ] || die "sing-box did not come up. Logs: $( [ "$INIT" = systemd ] && echo 'journalctl -u sing-box' || echo /var/log/sing-box.log). Devices are not affected: TPROXY is only hooked in while sing-box answers."
        sleep 1
    done
    say "sing-box is answering"
fi

step "Starting junction-tproxy (fail-open TPROXY + NAT)"
svc_enable junction-tproxy
svc_up junction-tproxy "$( [ "$TP_CHANGED" = 1 ] || [ "$_c" = 1 ] && echo 1 || echo 0)"

step "Starting Junction"
CHANGED=0
[ "$DRY" = 1 ] || mkdir -p "$ETC" /var/lib/junction
put "$WORK/junction.json" "$ETC/config.json" 600
[ "$FAILOVER" = true ] && put "$WORK/failover.json" "$ETC/failover.json" 600
put "$WORK/exits.json" "$SAVED_EXITS" 600
put "$WORK/setup.env" "$STATE" 600
svc_enable junctiond
svc_up junctiond "$( [ "$CHANGED" = 1 ] || [ "$JD_CHANGED" = 1 ] || [ "$_c" = 1 ] && echo 1 || echo 0)"

if [ -n "$DHCP" ]; then
    step "DHCP (dnsmasq)"
    if [ "$INIT" = systemd ] && systemctl is-active -q dnsmasq 2>/dev/null; then
        warn "the distribution's dnsmasq service is running; it may also answer DHCP. Consider: systemctl disable --now dnsmasq"
    fi
    CHANGED=0
    [ -f "$ETC/reservations.conf" ] || { printf '# dhcp-host=aa:bb:cc:dd:ee:ff,192.168.1.50,name\n' >"$WORK/res.conf"; put "$WORK/res.conf" "$ETC/reservations.conf" 644; }
    put "$WORK/dnsmasq.conf" "$ETC/dnsmasq.conf" 644
    if [ "$DHCP_NOW" = 1 ]; then
        svc_enable junction-dhcp
        svc_up junction-dhcp "$CHANGED"
    else
        say "Installed but NOT started: two DHCP servers on one network hand out conflicting answers."
        say "Turn your router's DHCP off, then:"
        if [ "$INIT" = systemd ]; then say "  systemctl enable --now junction-dhcp"
        else say "  rc-update add junction-dhcp default && rc-service junction-dhcp start"; fi
    fi
fi

if [ -n "$VIP" ]; then
    step "Floating gateway address $VIP (keepalived)"
    if [ -f /etc/keepalived/keepalived.conf ] && ! grep -q "Junction's setup.sh" /etc/keepalived/keepalived.conf; then
        [ "$FORCE" = 1 ] || die "/etc/keepalived/keepalived.conf exists and isn't Junction's; move it away or use --force (it is backed up)"
    fi
    CHANGED=0
    put "$WORK/keepalived.conf" /etc/keepalived/keepalived.conf 644
    put "$WORK/keepalived-backup.conf" "$ETC/backup/keepalived.conf" 600
    put "$WORK/junction-backup-notify" "$ETC/backup/junction-backup-notify" 755
    svc_enable keepalived
    svc_up keepalived "$CHANGED"
    say "For the backup box: copy $ETC/backup/ (see the README, 'Floating gateway')."
fi

# ---------------------------------------------------------------- final check
step "Checking"
if [ "$DRY" = 0 ]; then
    sleep 5
    if curl -fsS -m 8 -o /dev/null -x http://127.0.0.1:2080 http://cp.cloudflare.com/ 2>/dev/null; then
        say "traffic through sing-box: ok"
    else
        warn "a test request through sing-box failed. junction-tproxy keeps devices on plain routing until it works."
    fi
    /usr/local/sbin/junction-tproxy status || true
fi

_port=${LISTEN##*:}
_url="http://$GATEWAY_IP"
[ "$_port" = 80 ] || _url="$_url:$_port"
cat <<EOF

Done. Junction: $_url

Point devices at the gateway $GATEWAY_IP (pick one; see README.md):
  1. your router's DHCP settings: gateway/router = $GATEWAY_IP (if it has that field);
  2. Junction's DHCP server: re-run with --dhcp START-END and turn the router's off;
  3. per device: set the gateway (router) to $GATEWAY_IP by hand.
Devices you switch to VPN in Junction use the exits; the rest go direct.
Undo everything: sh $SHARE/gateway/uninstall.sh
EOF
