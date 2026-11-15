<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# FAQ

**Will it slow down my internet?**
A Raspberry Pi 4 or any small PC forwards a typical home connection at full speed. All traffic does pass through the box, so it should be on wired Ethernet, not Wi-Fi.

**Does it work without a VPN?**
Yes. Usage per device, limits, schedules, pausing and the Health page all work with everything going direct. VPN exits are optional.

**What does it know about my traffic?**
Which device talked to which site (from DNS and TLS names), how much, and when. Never the content. Everything stays on the box: there's no cloud service and no telemetry.

**How does it know what traffic is video, social media and so on?**
From the site names: every name is matched against a built-in list of domains (YouTube's video servers count as video, WhatsApp's as messaging, and so on). Traffic to a bare IP address shows as "No site name" rather than a guess. If a site lands in the wrong category, open an issue or a pull request against `categories.go`.

**Does it replace my router?**
No. It sits next to it. Devices use Junction as their gateway (it can hand out addresses itself, or you change one setting on your router), and Junction sends traffic on to your router.

**What happens if the box breaks?**
If Junction crashes or updates, nothing happens to traffic: sing-box carries it. If sing-box stops, traffic goes direct without the VPN. If the whole box dies, devices need another way out: your router's DHCP, or a backup gateway that takes over the address within seconds (`setup.sh --vip`).

**Why is its count higher than the per-device numbers?**
The bundle is counted the way ISPs bill, whole packets including headers (about 6% more than the content). Per-device numbers are content only, since packets can't be split by device.

**IPv6?**
Not yet. IPv6 traffic doesn't go through the gateway, so it's neither counted nor steered.

**Which VPNs work?**
Anything sing-box supports as an outbound: WireGuard, Hysteria2, VLESS, Trojan, Shadowsocks and more. `setup.sh` takes a WireGuard config or the outbound's JSON.

**Can several people manage it?**
Anyone on your network can open the page unless you turn on a password (Settings → Access). There's one password, no separate accounts.

**Can it read my ISP's own counter?**
Not built in. Use **Set what's left…** on the Internet page to line Junction up with your ISP's number now and then.

**Does it run on…?**
Linux on amd64, arm64 or armv7: Raspberry Pi 3 and newer, any Debian/Ubuntu box, Alpine, a VM, or a Proxmox container (`proxmox-lxc.sh`). Not on Windows or macOS.
