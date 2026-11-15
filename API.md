# Junction v0 API (watch-only)

junctiond serves the React app and this JSON API on the same origin (`http://192.168.1.4/`).
v0 is **watch-only for traffic**: nothing here changes routing. The only writes are Junction's own
labels (device name and group) and the bundle log (renewals and "set what's left" corrections).

All sizes are **bytes**, rates are **bytes per second**, times are **RFC 3339 UTC strings**.
`exit` values are sing-box outbound tags: `<exit>-<profile>` (e.g. `vpn1-default`, `vpn2-gaming`) or `direct`. The exit is the part before the dash (`vpn1`, `vpn2`): the endpoint names in your failover config.

## Types

```ts
type Device = {
  id: string;            // stable: MAC address lowercased ("68:6c:e6:1f:b7:d6"), or "ip:192.168.1.10" when MAC unknown
  short: string;         // "D01", "D02"… assigned in first-seen order, never reused
  ip: string;
  mac: string | null;
  name: string;          // user label, else the auto name, else "Device .56"
  nameSource: "user" | "router" | "dhcp" | "mdns" | "upnp" | "vendor" | "ip"; // router = the ISP router's device list // where `name` came from
  autoName: string | null; // best name the device announced about itself (kept even when the user renamed it)
  model: string | null;    // e.g. "LG webOS TV", "Android", "iPhone", "Windows" when known
  group: string | null;  // user label: "Gaming", "Servers", "Personal" or any string; null = ungrouped
  vendor: string | null; // from MAC prefix when known
  randomMac: boolean;    // locally-administered (private/randomized) MAC
  online: boolean;       // seen in ARP or traffic in the last 5 minutes
  usesGateway: boolean;  // has sent traffic through the gateway (sing-box) in the last 24 h
  firstSeen: string;
  lastSeen: string;
  rateDown: number;      // live, bytes/s (smoothed over ~3 s)
  rateUp: number;
  todayDown: number;     // since local midnight (the gateway's time zone)
  todayUp: number;
  exit: string | null;   // exit used by its most recent connection
  connections: number;   // open connections right now
  vpn: boolean;          // follows the VPN setting (selector); false = always direct
  paused: boolean;       // internet blocked
  pausedUntil: string | null; // when a timed pause ends; null while paused = until resumed
  controllable: boolean; // the gateway carries its traffic, so vpn/pause take effect
};

type ExitHealth = {
  key: string;           // an exit name from the failover config, e.g. "vpn1"
  name: string;          // its display name, e.g. "Home VPN"
  healthy: boolean;
  probed: boolean;       // false when Junction/failoverd is not currently probing it (an exit that has been down a while)
  latencyMs: number | null;
  lastSuccess: string | null;
  lastFailure: string | null;
  lastError: string | null;
  bytesToday: number;    // through any profile of this endpoint
  profiles: { profile: string; known: boolean; healthy: boolean; latencyMs: number | null; nextCheck: string }[]; // moe/psn/gaming
  nextCheck: string | null; // when a down exit is next checked (backs off up to 5 min)
};

type Overview = {
  now: string;
  live: { down: number; up: number };
  top: { deviceId: string; name: string; rateDown: number } | null;
  selector: {
    selected: string;      // sing-box outbound in use: "vpn1-default",[key: exit name | "direct", ...
    mode: string;          // "auto" (failover between exits) | "manual" (fixed exit) |[key: exit name | "direct"
    lastSwitch: string | null; reason: string | null;
    control: { mode: string; profile: string; manualEndpoint: string } | null; // non-null = Junction can change it
  };
  exits: ExitHealth[];
  direct: { latencyMs: number | null; bytesToday: number };
  bundle: Bundle | null;                   // null until the user logs a renewal
  gateway: {                               // floating gateway address (keepalived); null if not set up
    enabled: boolean;
    role: "master" | "backup" | "fault" | "stopped"; // master = this gateway carries traffic (VPN works);
                                           // backup/fault = the backup box covers: internet direct, no VPN
    since: string | null;
    events: { at: string; state: "MASTER" | "BACKUP" | "FAULT" | "STOP" }[]; // newest first, up to 20
  } | null;
  counts: { online: number; viaGateway: number; waiting: number };
  devices: Device[];                       // sorted: rateDown desc, then todayDown desc
};

type DeviceDetail = Device & {
  hours: { hour: string; down: number; up: number; byExit: Record<string, number> }[]; // today, 24 entries
  days: { day: string; down: number; up: number }[];   // last 30 days, oldest first
  topHosts: { host: string; bytes: number }[];          // today, up to 10
};

type UsageDay = { day: string; tunnel: number; direct: number; byExit: Record<string, number> };

// Bundles vary in size and renewal date, so there is no fixed cycle. The user logs each renewal
// ("I renewed: 140 GB today") and can log what the ISP says is left. The latest of the two is the
// anchor: left = anchor amount - traffic Junction counted since then.
type Bundle = {
  renewedAt: string;       // latest renewal
  size: number;            // bytes, size of the latest renewal
  expiresAt: string | null;
  anchor: { kind: "renewal" | "check"; at: string; amount: number }; // amount = size, or the "left" you set
  usedSinceRenewal: number; tunnel: number; direct: number;          // since renewedAt
  left: number;            // anchor.amount - counted since anchor.at (can go negative)
  pacePerDay: number;      // average bytes/day since renewedAt (needs >= 6 h of data, else 0)
  runsOut: string | null;  // now + left / pace, null when pace is 0 or left <= 0
};
type BundleEvent = { id: number; kind: "renewal" | "check"; at: string; amount: number; expiresAt: string | null };
```

## Endpoints

| Method | Path | Returns |
|---|---|---|
| GET | `/api/overview` | `Overview` |
| GET | `/api/devices` | `Device[]` (all known, online first) |
| GET | `/api/devices/{id}` | `DeviceDetail` (id URL-encoded) |
| PATCH | `/api/devices/{id}` | body `{ name?: string, group?: string \| null }` → `Device` |
| GET | `/api/exits` | `{ exits: ExitHealth[], selector: Overview["selector"] }` |
| GET | `/api/usage?days=30` | `UsageDay[]` oldest first |
| PUT | `/api/selector` | body `{ mode: "auto" \| "manual" \| "direct", endpoint?: string /* exit name */, profile?: string }` → same as GET /api/exits. Changes the VPN for every device that uses the gateway. |
| PUT | `/api/devices/{id}/vpn` | body `{ on: boolean }` → `Device`. Takes effect within ~1 s, no restart. |
| PUT | `/api/devices/{id}/pause` | body `{ minutes?: number }` (1..10080; absent = until resumed) → `Device` |
| DELETE | `/api/devices/{id}/pause` | resume → `Device` |
| PUT | `/api/groups/{name}/vpn` | body `{ on: boolean }` → `{ group, on, devices }` (every device in that group) |
| GET | `/api/bundle` | `{ current: Bundle \| null, events: BundleEvent[] }` (newest first, up to 50) |
| POST | `/api/bundle/renewals` | body `{ gb: number, at?: string, expiresAt?: string \| null }` → same as GET |
| POST | `/api/bundle/checks` | body `{ gbLeft: number, at?: string }` → same as GET ("my ISP says X GB left") |
| DELETE | `/api/bundle/events/{id}` | → same as GET (undo a mistaken entry) |
| GET | `/api/stream` | Server-Sent Events: event `overview`, data = `Overview` JSON, every 2 s |

Errors: non-2xx with `{ "error": "plain sentence" }`.

## Notes for the UI

- Usage only covers traffic that passes through the gateway. Devices that bypass it (most phones today)
  show `usesGateway: false`; say so plainly rather than showing 0 as if they used nothing.
- `bundle` null → prompt "Log your bundle": an "I renewed" form (GB, date defaulting to now, optional expiry).
- Always offer "My ISP says … GB left" next to the bundle: Junction only counts traffic that passes through
  the gateway, so the ISP's number is the truth and resets the count.
- Names: show `nameSource` subtly (e.g. "named by the device" vs "you named it"); rename sets nameSource "user";
  PATCH with `name: ""` clears the user name and falls back to the auto name.
- The global VPN controls are live when `selector.control` is non-null: exit buttons (manual on that exit),
  an "Automatic" choice (auto failover), and "All direct" (mode direct; switching it off returns to auto).
  Switching drops open connections for a moment; say so near the controls. Per-device VPN and pause are live (see the device endpoints).
- Dev: `npm run dev` proxies `/api` to `http://192.168.1.4` (Vite `server.proxy`).

---

## Rules, VPN profiles, speed limits, events (v1)

One rules engine drives data limits, schedules, speed caps and per-device VPN profiles.
The device page's simple controls create/edit rules; the node editor edits the same rules.
Every 15 s the gateway works out each device's **effective** state:

1. start from the device's manual settings (`vpn`, `profile`, `speedLimitMbps`, manual pause);
2. apply every enabled rule in `order` whose `match` covers the device and whose `when` is true now;
   later rules override earlier ones field by field, except: a pause from any source wins,
   and speed limits take the lowest value;
3. a device-level **exemption** for a rule ("give 1 h more", "skip tonight") ignores that rule until it ends.

### Types

```ts
type Profile = "psn" | "moe" | "gaming";      // SNI profiles; each has its own exit + failover

type Rule = {
  id: number;
  name: string;                // "Kids' bedtime"
  enabled: boolean;
  order: number;               // lower runs first
  match: {
    all?: boolean;             // every device
    devices?: string[];        // device ids
    groups?: string[];         // group names
    newDevices?: boolean;      // devices first seen in the last 24 h that nobody named
  };
  when: {                      // all given conditions must hold; empty = always
    schedule?: { days: number[]; start: string; end: string } | null; // days 0=Sun..6=Sat, "HH:MM" gateway time; end < start = past midnight
    usage?: { period: "day" | "week" | "month"; overGB: number } | null; // the device's own usage this period
  };
  then: {                      // at least one
    pause?: boolean;
    speedMbps?: number;        // cap, both directions
    vpn?: boolean;             // force VPN on/off
    profile?: Profile;         // VPN profile to use while on VPN
  };
  graph?: unknown;             // node-editor layout (positions etc.), stored as-is, ignored by the gateway
};

type Effective = {             // added to Device as `effective`
  vpn: boolean;
  profile: Profile;            // profile actually used (device's own, a rule's, or the global one)
  exit: string;                // outbound it should be using now: "vpn1-gaming" |[key: exit name | "direct" | …
  paused: boolean;
  pausedBy: "manual" | "rule" | null;
  speedLimitMbps: number | null;
  rules: { id: number; name: string; why: string }[];   // rules affecting it right now
  exemptions: { ruleId: number; until: string }[];
};

type DeviceAdditions = {       // new fields on Device
  profile: Profile | null;     // manual choice; null = follow the global profile
  speedLimitMbps: number | null; // manual cap
  monthDown: number; monthUp: number; weekDown: number; weekUp: number; // calendar month / week (Sun–Sat), gateway time.
                               // NOTE: weekDown/monthDown currently hold down+up TOTALS; weekUp/monthUp are 0.
  effective: Effective;
};

type Event = {
  id: number;
  at: string;                  // RFC 3339
  kind: "exit" | "gateway" | "device" | "rule" | "control" | "limit" | "dhcp" | "isp"; // "isp": only in old events, from the WE counter removed in v1.7
  deviceId: string | null;
  ruleId: number | null;
  text: string;                // plain sentence: "Xbox Series X reached 4 GB today: slowed to 2 Mbit/s (Daily cap)"
};
```

### Endpoints

| Method | Path | Body → returns |
|---|---|---|
| PUT | `/api/devices/{id}/profile` | `{ profile: Profile \| null }` → `Device` |
| PUT | `/api/devices/{id}/speed` | `{ mbps: number \| null }` (0.5–1000) → `Device` |
| GET | `/api/rules` | → `Rule[]` in order |
| POST | `/api/rules` | `Rule` without id/order → `Rule` (appended last) |
| PUT | `/api/rules/{id}` | `Rule` → `Rule` |
| DELETE | `/api/rules/{id}` | → `{ deleted: id }` |
| PUT | `/api/rules/order` | `{ ids: number[] }` → `Rule[]` |
| POST | `/api/rules/{id}/exempt` | `{ deviceId: string, minutes: number }` (1–1440) → `Effective` of that device |
| DELETE | `/api/rules/{id}/exempt/{deviceId}` | end an exemption → `Effective` |
| GET | `/api/simulate?device={id}&at={RFC3339}` | → `{ effective: Effective, matched: { id, name, why }[] }` ("what happens to X at 23:00 Thursday") |
| GET | `/api/events?limit=100&device={id}&before={eventId}` | → `Event[]` newest first |

Notes:
- Usage counts only traffic through the gateway (same as everywhere else).
- `speedMbps`/`speedLimitMbps` cap download and upload of that device.
- A rule with `then.profile` but no `then.vpn` only chooses the profile used *if* the device is on VPN.
- Errors: plain sentences, as everywhere.

## Linked devices (v1.1)

One phone can show up as two connections (two MACs, e.g. dual Wi-Fi). Junction links
them automatically when their DHCP hostnames match ("Alex-s-S26-Ultra" = "alex-s26-ultra").

- `Device.links: { id, ip, mac, name, online }[]` lists the extra connections folded into
  this device (always present, usually `[]`). Linked connections are **not** returned as
  separate devices. Rates, today/week/month usage and connection counts are summed into the
  primary; online/controllable is true if any connection is.
- Settings (VPN, profile, speed, pause, group, rules) live on the primary and apply to every
  linked connection.

| Method | Path | Body → returns |
|---|---|---|
| PUT | `/api/devices/{id}/link` | `{ to: "<primary id>" }` link this connection under another device; `{ to: null }` unlink it (shown on its own again, never auto-linked) → `{ ok: text }` |

UI: on the device page, list `links` as "Also connected as: name · ip" with an **Unlink**
button (`PUT /api/devices/{linkId}/link {to:null}`). Optionally a "Same device as…" picker
on a device page (`PUT /api/devices/{thisId}/link {to: otherId}`).

## Console (v1.2)

| Method | Path | Returns |
|---|---|---|
| GET | `/api/traffic?hours=24` (1–168) | `{ hours: { hour: RFC3339, down: bytes, up: bytes }[] }`: whole network, through the gateway, oldest first, the current (partial) hour last |
| GET | `/api/exits/history?minutes=60` (1–1440) | `{ samples: { at: RFC3339, exits: { [key: exit name | "direct"\|endpoint?: string (exit name) \| "direct" \| …]: number \| null } }[] }`: one sample a minute, oldest first; `null` = down/unknown; a key may be missing in a sample (treat as no data). In memory only: history restarts empty when Junction restarts. |

## v1.5: counting on the wire

The bundle (`used`, `left`, pace) counts whole packets between the gateway and the internet, the way the ISP bills, instead of the content sing-box reports (about 6% less: headers, acknowledgements, retransmissions). Two counting-only firewall chains do it, `filter INPUT -> JUNCTION_WIRE_IN` and `filter OUTPUT -> JUNCTION_WIRE_OUT`; private and local addresses return before the counting rule, so traffic between devices and the gateway isn't counted twice. Every rule is a RETURN: nothing is dropped or changed. Junction creates the chains at start if they're missing and reads them every 30 s; the counters keep running while it restarts.

- `Bundle.overheadPct`: the overhead added to content for hours from before wire counting started (measured over the last 7 days once there's 1 GB of it, else 6.1%).
- `UsageDay.billed`: bytes on the wire that day (content + overhead for hours before wire counting).
- Per-device numbers (`todayDown`, `/api/devices/{id}`, hosts) stay content: the wire count isn't split by device.

## v1.6: settings, health, setup, sign-in

Types are in `web/src/api/types.ts` (section "v1.6"): `Settings`, `SettingsPatch`, `Health`, `SetupInfo`, `SetupBody`, `Session`.

| Method | Path | |
|---|---|---|
| GET | `/api/settings` | `Settings` |
| PUT | `/api/settings` | body `SettingsPatch` (any subset; missing = unchanged) → `Settings`. `brand.image`: a `data:` URL (PNG, JPEG, WebP or SVG, at most 300 KB), `null` or `""` removes it. `access.required: true` needs a password first (400). Changing `timezone`, `network.gatewayIP` or `network.lan` restarts Junction about 1 s after the answer (the service supervisor starts it again; traffic isn't affected): the app should expect the API to be away for a few seconds. |
| POST | `/api/settings/password` | body `{ password }` (8+ characters) → `Settings`. Stored as PBKDF2-SHA256 (600k rounds); signs out every other browser. |
| GET | `/api/session` | `Session` `{ required, signedIn }` (always public) |
| POST | `/api/login` | body `{ password }` → `Session`, sets the `junction_session` cookie (HttpOnly, SameSite=Strict, lasts `access.sessionDays`, 0 = until signed out). 401 wrong password, 409 no password set, 429 after 5 wrong passwords in a minute from one address. |
| POST | `/api/logout` | → `Session`; ends this browser's session |
| GET | `/api/health` | `Health`: checks `routing`, `rules`, `counting`, `exits`, `backup`, `dhcp`, `clock`, `storage` (run at most every 30 s; `since` = when the status last changed while Junction was running). `version.available` is the newest release at config `update_url` (checked every 6 h), null when unset or not newer. `version.history` comes from `/var/lib/junction/updates.log` (lines `RFC3339 version result [note]`, newest first, up to 10). |
| POST | `/api/update` | body `{ version }` → `Health` with a `pending` history entry. 501 unless config `update_url` is set; 409 while an install runs; 502 when the release can't be fetched or fails its SHA256SUMS check. Downloads `junction-<version>-linux-<arch>.tar.gz`, unpacks it (without its top folder) to `/var/lib/junction/update/<version>/` and runs `/usr/local/lib/junction/safe-update.sh <that folder>` in the background. |
| GET | `/api/setup` | `SetupInfo`. `needed` is true only on an install that started with an empty database and hasn't finished setup; an install with devices or bundle entries is marked set up at start. |
| POST | `/api/setup` | body `SetupBody` → `Settings`; 409 once setup is done. Saves brand, timezone, gateway address and LAN (restarting as PUT does), turns VPN on for `devicesOnVPN`, and writes `exits` and `dhcp` to `/etc/junction/exits-pending.json` (mode 600) for the setup script: Junction never edits sing-box's config. |

When a password is set and `access.required` is on, every `/api/*` call except session, login and logout (the SSE stream too) answers 401 `{error}` without a valid session; the web app's own files stay public. `junctiond -reset-password` removes the password and turns sign-in off.

`Overview` gains `brand` (as `Settings.brand`), `gatewayIP` and `timezone`, so the app needn't hardcode them.

New config keys (all optional): `gateway_ip` (default: keepalived's floating address, else the interface's first IPv4), `default_profile`, `brand_name` ("Junction"), `backup_name` ("Backup"), `backup_ip` (default: keepalived's `unicast_peer`), `update_url`. `failover_api` defaults to `http://<gateway_ip>:7070/api/status`. VPN profiles are sing-box's `vpn-<name>` selectors (psn, moe, gaming until sing-box has been read or when there are none); exit names come from the failover config's endpoints.

## v1.7

- The ISP counter (WE, read from my.te.eg) was removed: `/api/isp`, `/api/isp/refresh`, `/api/settings/isp-headers`, `Settings.isp`, `SetupBody.isp`, the `isp` health check, `Bundle.renewedBy` and `BundleEvent.source` are gone; Junction's on-the-wire count is the only counter, and renewals and "set what's left" are logged by hand.
