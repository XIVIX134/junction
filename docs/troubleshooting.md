<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# Troubleshooting

Start with the **Health** page (the heart icon). Every part of the gateway is listed there with a plain-language status. The answers below cover what it can't.

Logs: `/var/log/junctiond.log` (or `journalctl -u junctiond` on systemd). sing-box: `/var/log/sing-box.log`.

## The internet is down for everyone

1. Is your router's own internet up? Plug a laptop straight into the router to check.
2. Is the gateway box on? If it's off, devices that use it as their gateway lose the internet. Either turn it back on, or turn your router's DHCP back on and reconnect devices (they'll go straight to the router), or set up a backup gateway (`setup.sh --vip`, see [packaging/README.md](../packaging/README.md)).
3. If the box is on: Junction itself can't cut the internet (traffic is carried by sing-box, and the routing rules are only hooked in while sing-box is healthy). Check `rc-service sing-box status` / `systemctl status sing-box`.

To remove the gateway completely: `sh /opt/junction/<version>/gateway/uninstall.sh` leaves a plain router path.

## A device doesn't show up, or shows as "Not controllable"

- **Not in the list at all:** it hasn't sent anything through the gateway yet. It shows up once it gets an address from Junction's DHCP (if you use it) or sends traffic.
- **"Not controllable":** it's on your network but sends its traffic straight to the router, so Junction can't steer it. Usually it has a fixed gateway set by hand, or got its address from the router's DHCP before you switched. Reconnect its Wi-Fi, or set its gateway to Junction's address.
- **Shows twice:** phones use a different random MAC per network or after a reset. Junction links devices that share a name; you can link or unlink them on the device page.

## The VPN shows "Down"

The exit's server isn't answering its health checks. Junction keeps checking with a back-off and switches VPN devices to another healthy exit, or sends them direct if none is up. Check the VPN server itself (is the VM running, has its IP changed, has its subscription run out?). Settings → VPN & profiles lists the exits Junction knows.

## Junction's "left" differs from my ISP's number

Some difference is normal:

- **Your ISP's counter lags.** Most ISP apps and websites update every few minutes to half an hour. When the house is busy, that delay alone can be a gigabyte or more. Compare after a quiet stretch.
- **Packet overhead.** Junction counts whole packets (headers, acknowledgements, retransmissions), the way ISPs bill. If you're comparing against a per-device figure, those are content only, about 6% less.
- **Traffic that skips the gateway:** devices set to use the router directly, the routers' own traffic, and anything over IPv6.

If they drift apart steadily (more than 2-3% over a day), something is going around the gateway. To line them up, use **Set what's left…** on the Internet page with your ISP's number.

## An update was rolled back

The Health page shows it under "Recent updates". The new version didn't answer for 20 seconds in a row within a minute, so the previous one came back by itself. Nothing on your network changed. Please open an issue with `/var/log/junctiond.log` from around that time.

## I forgot the password

On the gateway: `junctiond -reset-password`, then restart Junction (`rc-service junctiond restart` or `systemctl restart junctiond`). Sign-in is turned off; set a new password in Settings → Access.

## The page says "Can't reach the gateway"

The browser can't reach Junction (the internet itself may be fine). Check the box is on and that you're using its address. Junction's own address and the gateway address can differ when a backup gateway holds a floating address: both work.
