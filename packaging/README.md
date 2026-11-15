<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# Installing Junction

Junction turns a small Linux box into your home network's gateway: every device
that uses it shows up with its traffic, and you can send single devices through
a VPN, pause them, cap their speed, or put them on schedules. The box sits on your
normal network next to your router; nothing is rewired.

```
devices ──> Junction box (sing-box + junctiond) ──> your router ──> internet
                     └─ VPN exits, for the devices you choose
```

What you need: a box that's always on, on wired Ethernet, with a **fixed
address** (a static IP, or a DHCP reservation on your router). Any of these
works: a Raspberry Pi 3/4/5, an old PC or mini PC, a VM, or a Proxmox container.
Supported systems: Debian 12+, Ubuntu 22.04+, Raspberry Pi OS (32/64-bit), Alpine 3.19+.
CPU: amd64, arm64 or armv7.

## Install

### Any Debian / Ubuntu box, or a Raspberry Pi

```sh
curl -fsSL https://github.com/XIVIX134/junction/releases/latest/download/install.sh | sudo sh
```

The installer picks the build for your CPU, checks its SHA256, unpacks it to
`/opt/junction/<version>` and runs `gateway/setup.sh`, which asks a few questions
(every answer has a detected default):

| Question | Default |
|---|---|
| LAN interface | the one your internet route uses |
| LAN range | from that interface's address, e.g. `192.168.1.0/24` |
| Time zone | the system's |
| Floating gateway address (keepalived) | no |
| Address devices use as their gateway | this box's address (or the floating one) |
| DHCP server (dnsmasq) | no |
| VPN exits | none (everything direct) |

Then open `http://<the box's address>/`.

Raspberry Pi: Raspberry Pi OS Lite is enough. Give it a fixed address first, e.g.
`sudo nmcli con mod "Wired connection 1" ipv4.method manual ipv4.addresses 192.168.1.5/24 ipv4.gateway 192.168.1.1 ipv4.dns 1.1.1.1`
(then reboot), or reserve its address on your router.

### Alpine

Same command, as root (`apk add curl` first if it's missing). setup.sh uses OpenRC
services instead of systemd ones; everything else is the same.

### Proxmox container

On the Proxmox host:

```sh
curl -fsSLO https://github.com/XIVIX134/junction/releases/latest/download/proxmox-lxc.sh
sh proxmox-lxc.sh --ip 192.168.1.5/24 --gw 192.168.1.1
```

It creates an **unprivileged** Alpine container (512 MB, 1 core, 2 GB disk) and
installs Junction in it. Unprivileged is enough: TPROXY, NAT and `tc` all happen
inside the container's own network namespace, where its root has `CAP_NET_ADMIN`.
Two host-side things are needed, and the script does both:

- the kernel modules (`xt_TPROXY`, `nf_tproxy_ipv4`, `xt_MASQUERADE`, `sch_htb`, ...)
  are loaded on the host and listed in `/etc/modules-load.d/junction.conf`, because a
  container can't load modules;
- the container's NIC has `firewall=0`: the Proxmox firewall's IP filter would drop
  the gateway's replies (they carry internet source addresses) and a floating address.

Flags for setup.sh go after `--`, e.g. `sh proxmox-lxc.sh --ip ... --gw ... -- --dhcp 192.168.1.100-192.168.1.199`.
To answer the questions yourself instead: `pct enter <id>` and run
`sh /usr/local/share/junction/gateway/setup.sh`.

### Without the one-liner

Download `junction-<version>-linux-<arch>.tar.gz` and `SHA256SUMS` from the
releases page, check with `sha256sum -c --ignore-missing SHA256SUMS`, unpack, and
run `sudo sh junction-*/gateway/setup.sh`. `setup.sh --help` lists every flag;
`--yes` answers everything with the defaults, `--dry-run` shows what would happen
and leaves the rendered config files in a temp directory.

## VPN exits

Devices you switch to "VPN" in Junction leave through an exit; all others go
direct. Add exits when you run setup (or re-run it later):

| Kind | Flag | What you give it |
|---|---|---|
| WireGuard | `--exit-wireguard home=/root/wg0.conf` | the `.conf` file from your VPN provider (wg-quick format) |
| Hysteria2 | `--exit-hysteria2 hy=hysteria2://pass@host:443/?sni=host` | a hysteria2 link |
| VLESS, Trojan, anything sing-box supports | `--exit-outbound work=/root/out.json` | one sing-box outbound as a JSON object (no `tag` needed) |

Names are lowercase letters and digits. With two or more exits, Junction checks
them all the time and moves VPN devices to the first healthy one (failover).
The first exit is the default.

## Point your devices at the gateway

Nothing changes until devices use the Junction box as their gateway. Three ways:

1. **Your router's DHCP settings.** If the router lets you set the "gateway" or
   "default router" it hands out, set it to the Junction address. Many ISP routers
   don't have that field; then use 2 or 3.
2. **Junction's DHCP server.** Run setup with `--dhcp 192.168.1.100-192.168.1.199`
   (pick a range your router doesn't use for fixed devices). It installs dnsmasq
   but only starts it once you confirm (or pass `--dhcp-now`) that your router's
   DHCP server is **off**: two DHCP servers on one network fight. Fixed addresses go
   in `/etc/junction/reservations.conf` (`dhcp-host=aa:bb:cc:dd:ee:ff,192.168.1.50,tv`).
   Devices pick it up when they renew (toggle Wi-Fi to hurry them).
3. **Per device.** In the device's network settings, set a manual IP and use the
   Junction address as the gateway/router. Good for trying it with one device first.

To undo any of these, set the gateway back to your router's address.
Note: Junction handles IPv4. If your router hands out IPv6, devices may send some
traffic over IPv6 straight to the router; turn IPv6 off on the router's LAN if you
want every connection to go through Junction.

## How fail-open works

The box only *adds* a path; it never becomes the single thing your internet hangs on.

- `junction-tproxy` (a small service) sets up plain routing first: IP forwarding,
  NAT for forwarded traffic, no ICMP redirects. With just that, the box is an
  ordinary router.
- It then checks sing-box every 2 seconds (its API) and every 30 seconds sends a
  real request through it. Only while sing-box is healthy does it hook the TPROXY
  rule into the firewall, which hands devices' traffic to sing-box.
- When sing-box stops, crashes, hangs or stops passing traffic, the hook is removed
  within about 6 seconds (immediately on a clean stop) and devices carry on
  through plain routing — direct, without VPN — until sing-box is back.
- Configs are checked with `sing-box check` before they are used; a config that
  fails leaves the running one alone.
- Nothing touches the box's own internet connection; only forwarded traffic.

`junction-tproxy status` shows the current state; its log is in the journal
(`journalctl -u junction-tproxy`) or `/var/log/junction-tproxy.log` on Alpine.

That covers sing-box failing. If the whole box can fail (power, hardware), use
the **floating gateway address**: `--vip 192.168.1.4/24 --vip-peer <backup box>`.
Devices use the floating address; keepalived holds it on the Junction box and a
second machine (another Pi, your NAS, the Proxmox host) takes it over within a few
seconds when the Junction box goes quiet. setup.sh writes the backup machine's
files to `/etc/junction/backup/`: install `keepalived` there, copy `keepalived.conf`
to `/etc/keepalived/` and `junction-backup-notify` to `/usr/local/sbin/` (set the
interface name in both), and enable keepalived. While covering, the backup just
routes and NATs (no VPN).

## Re-running, upgrading

Re-run `setup.sh` (or the installer for a newer version) any time. It remembers
your answers (`/etc/junction/setup.env`), keeps the Clash API secret, the
keepalived password and the VPN exits, and only restarts what changed. Your
own additions to `/etc/junction/config.json` are kept. `/etc/sing-box/config.json`
is generated: hand edits are replaced (the old file is kept as `.bak-<time>`).

## Uninstall

```sh
sudo sh /usr/local/share/junction/gateway/uninstall.sh
```

This removes Junction, sing-box and the TPROXY rules and routes, and leaves a
**plain router** (forwarding + NAT, kept across reboots), so devices still pointed
at the box keep their internet. DHCP and keepalived keep running if you used them.
Then point your devices back at the router (and turn the router's DHCP back on), and
remove the rest:

```sh
sudo sh /usr/local/share/junction/gateway/uninstall.sh --purge
```

`--purge` stops routing, DHCP and keepalived, and deletes `/etc/junction`,
`/etc/sing-box` and Junction's data. Both accept `--dry-run`.

## Files

| Path | What |
|---|---|
| `/usr/local/bin/junctiond`, `/etc/junction/config.json` | Junction and its settings |
| `/usr/local/bin/sing-box`, `/etc/sing-box/config.json` | sing-box 1.12.14 (official build) and its generated config |
| `/etc/sing-box/{vpn-devices,paused-devices,profile-default}.json` | per-device lists Junction writes; sing-box reloads them by itself |
| `/usr/local/sbin/junction-tproxy`, `/etc/junction/tproxy.conf` | fail-open TPROXY + NAT |
| `/etc/junction/dnsmasq.conf`, `reservations.conf` | DHCP (optional) |
| `/etc/keepalived/keepalived.conf`, `/etc/junction/backup/` | floating address (optional) |
| `/usr/local/share/junction/` | this kit, for re-runs and uninstall |

## Troubleshooting

- **A device has no internet through Junction**: `junction-tproxy status` on the box.
  "tproxy: off" means sing-box isn't healthy and the box is routing plainly; see
  `journalctl -u sing-box` (or `/var/log/sing-box.log`).
- **Devices bypass the gateway**: make sure nothing else on the box sends ICMP
  redirects (`sysctl net.ipv4.conf.all.send_redirects` must be 0) and that the device
  really uses the Junction address as its gateway.
- **firewalld / ufw / Docker on the same box**: they can drop forwarded traffic.
  junction-tproxy inserts its own ACCEPT rules for the LAN and re-adds its rules if
  they get flushed, but firewalld's nftables rules sit outside iptables; disable
  firewalld on a dedicated gateway.
