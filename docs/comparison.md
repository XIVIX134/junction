<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# How Junction compares

Junction is a **gateway that sits next to your router**: devices send their traffic through it, and you see and steer each device from one page. It doesn't replace your router, block ads, or need new hardware. Here's how it relates to tools people often ask about.

| | Junction | Pi-hole / AdGuard Home | OpenWrt | Firewalla | pfSense / OPNsense |
|---|---|---|---|---|---|
| What it is | Gateway beside your router | DNS server | Router firmware | Gateway appliance | Router / firewall OS |
| Replaces your router | No | No | Yes (flash it) | No (it can) | Usually |
| Hardware | Any Linux box, Raspberry Pi, VM, Proxmox container | Same | A supported router | Their box | A PC with 2+ network cards |
| Usage per device (live, per day, per site) | Yes | Queries only, not bytes | With add-ons | Yes | With add-ons |
| Counted the way your ISP bills | Yes (whole packets) | No | Partly | Partly | Partly |
| VPN for chosen devices only | Yes, per device or group, with failover between exits | No | Yes, by hand (policy routing) | Yes | Yes, by hand |
| Pause / speed cap / daily limit / schedule per device | Yes | Block lists per device | With add-ons | Yes | With rules |
| Ad and tracker blocking | No (use it alongside Pi-hole) | Yes | With add-ons | Yes | With add-ons |
| Setup effort | One script, then a web page | Low | Medium | Low | High |
| If it breaks | Traffic goes direct (fails open) | DNS fails unless you have a second one | The router is down | Depends on mode | The router is down |
| Price / license | Free, GPL-3.0 | Free | Free | Paid hardware | Free / paid |

## When to pick what

- **You want to see what each device uses and send some of them through a VPN, without touching your router:** Junction.
- **You want ads blocked on every device:** Pi-hole or AdGuard Home. They work well together with Junction: point Junction's DHCP (or your router) at Pi-hole for DNS.
- **You want to replace your ISP's router with something open:** OpenWrt, or pfSense / OPNsense on a small PC.
- **You want a box that does it all and you don't want to run anything yourself:** Firewalla.

## What Junction doesn't do (yet)

- **IPv6.** Traffic over IPv6 doesn't go through the gateway, so it isn't counted or steered. If your ISP gives devices IPv6, turn it off on the router or on devices you want to control.
- **Content.** Junction sees which site a connection goes to (from DNS and TLS names), never what's inside it.
- **Blocking sites.** Pausing and limits apply to a whole device.
