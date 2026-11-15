<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# Routers

**Junction works with any router.** It sits next to your router as the gateway and hands traffic on to it, so the router's make and model don't matter.

**Reading the router is optional, and most people don't need it.** If your router is listed below, Junction can log in to it every few minutes and read its device list, only to give devices better names. Without it, Junction still names devices from:

- **DHCP:** when Junction hands out addresses, every device tells it its own name. This is the best source and works with any router.
- **mDNS and UPnP:** Apple devices, printers, TVs and consoles announce their names.
- **The manufacturer** from the MAC address, as a last resort.

You can also rename any device on its page.

## Supported routers

| Router | Firmware | What Junction reads | Tested by |
|---|---|---|---|
| Huawei HG8145V5 | TE Data / WE (Egypt) | Connected devices: name, IP, MAC, port, online | maintainer, daily |

Set it up with `router_url`, `router_user` and `router_password` in [config.json](configuration.md). Junction only reads: it logs in, fetches the device list and logs out within a second or two (these routers allow one admin session at a time, so it doesn't block you for long).

Other routers aren't supported yet. If yours isn't listed, leave the router settings empty; everything else works the same.

## Adding your router

Router support lives in `router.go`: one function logs in and fetches the device page, another turns that page into a list of devices (IP, MAC, name, online). To add a router:

1. Save its device-list page (or API answer) while logged in, and **remove anything private** from it: your MACs, device names, public IP.
2. Write the parser for it, with a test that reads the saved page (see `TestParseRouterDevices` in `names_test.go`).
3. Write the login and fetch. Log out at the end, and keep the session short: many routers allow only one admin at a time.
4. Add a row to the table above, with the firmware you tested.

Open a pull request even if you only have the saved page: someone else can write the parser from it.
