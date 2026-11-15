<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# Configuration

Junction reads `/etc/junction/config.json` at start (`junctiond -config <path>` to use another file). `setup.sh` writes it for you; every key is optional. **Settings** in the app changes all of them: it edits this file (keeping a copy as `config.json.prev`) and restarts Junction to apply it, which doesn't affect traffic. Timezone, gateway address and network range set in the app are stored in Junction's database and take precedence over the file.

## Keys

| Key | Default | What it does |
|---|---|---|
| `listen` | `":80"` | Address the web page and API listen on. |
| `db` | `/var/lib/junction/junction.db` | The SQLite database (usage history, devices, rules, settings). |
| `interface` | `"eth0"` | The network card facing your devices. |
| `lan` | detected from `interface` | Your network's range, e.g. `192.168.1.0/24`. Scanned to find devices. |
| `gateway_ip` | keepalived's floating address, else the interface's | The address devices use as their gateway. |
| `timezone` | the system's, else UTC | Days, schedules and times on every page use it, e.g. `Europe/London`. |
| `singbox_config` | `/etc/sing-box/config.json` | Read for the Clash API secret and the profile selectors. |
| `clash_api` | `http://127.0.0.1:9090` | sing-box's API, where Junction reads connections. |
| `default_profile` | the failover's, else the first profile | VPN profile for devices that don't have their own. |
| `failover_enabled` | `false` | Switch VPN devices between exits automatically when one stops answering. |
| `failover` | none | The exits and how to check them (below). |
| `failover_config` | `/etc/vpn-failover/config.json` | Older installs: a separate file in the same format as `failover`. Used only when `failover` is absent. |
| `dhcp_listen` | `true` | Learn device names by listening to DHCP. Set `false` when Junction's dnsmasq hands out addresses (it needs the port); names then come from the lease file. |
| `dnsmasq_leases` | `/var/lib/misc/dnsmasq.leases` | The lease file, when dnsmasq runs. |
| `router_url`, `router_user`, `router_password` | none | Optional, not needed for Junction to work: read your router's device list for better names. Supported routers: [routers.md](routers.md). |
| `brand_name` | `"Junction"` | The name in the top bar (also in Settings → General). |
| `backup_name`, `backup_ip` | `"Backup"`, keepalived's peer | How the backup gateway box is shown. |
| `update_url` | the official releases (`https://api.github.com/repos/XIVIX134/junction/releases/latest`) | Where to look for new versions: a GitHub releases API address, e.g. a fork's. Looking is off until you turn on Settings → Updates (it's on by default only where `update_url` is set by hand); the Health page then offers new versions and installs them with automatic rollback. |
| `seed_names`, `seed_groups` | none | `{ "<mac>": "name" }`: names and groups to start with, before you rename devices in the app. |

## `failover`

```json
"failover_enabled": true,
"failover": {
  "probe_url": "http://cp.cloudflare.com",
  "probe_interval_seconds": 10,
  "probe_timeout_seconds": 3,
  "failure_threshold": 3,
  "success_threshold": 6,
  "recovery_cooldown_seconds": 600,
  "default_profile": "default",
  "profiles": { "default": "", "gaming": "www.example-game.com" },
  "endpoints": [
    { "key": "home", "name": "Home VPN", "ip": "203.0.113.10", "probe_ports": { "default": 22101, "gaming": 22102 } },
    { "key": "travel", "name": "Travel VPN", "ip": "198.51.100.20", "probe_ports": { "default": 22103, "gaming": 22104 } }
  ]
}
```

- **endpoints**: one per VPN exit. `key` matches the sing-box outbound tags `<key>-<profile>`; `name` is what the app shows.
- **probe_ports**: a local port per profile that sing-box forwards through that exit, used to check it (`setup.sh` creates them).
- An exit is marked down after `failure_threshold` failed checks in a row, and trusted again after `success_threshold` good ones and `recovery_cooldown_seconds`.
- **profiles**: the sing-box selectors `vpn-<profile>`. The value is the profile's optional site name: the TLS name (SNI) its connections to the exits use, for VPN servers or ISPs that treat traffic by name (Settings → VPN changes it in sing-box's config too).
