// SAMPLE fixtures for `npm run dev:mock`, shaped exactly like junction/API.md.
// Devices, exits and latencies follow the real network (gathered 2026-11-13); every usage number is synthetic.

import type { Bundle, BundleEvent, BundleState, Device, DeviceDetail, Effective, EventKind, ExitHealth, JEvent, NameSource, Overview, Profile, Rule, UsageDay } from "../src/api/types.ts";

const TZ = "Europe/London";
const GB = 1e9;

// ---------- time helpers (local local days) ----------

const ymdFmt = new Intl.DateTimeFormat("en-CA", {
  timeZone: TZ,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});
function local(d: Date) {
  const p: Record<string, string> = {};
  for (const x of ymdFmt.formatToParts(d)) p[x.type] = x.value;
  return { y: +p.year, m: +p.month, d: +p.day, h: +p.hour, min: +p.minute };
}
function offsetMs(d: Date) {
  const c = local(d);
  const asUTC = Date.UTC(c.y, c.m - 1, c.d, c.h, c.min);
  return asUTC - Math.floor(d.getTime() / 60000) * 60000;
}
/** The instant of local midnight (plus `hour`) on a local calendar date. */
function localInstant(y: number, m: number, d: number, hour = 0) {
  const guess = new Date(Date.UTC(y, m - 1, d, hour));
  return new Date(guess.getTime() - offsetMs(guess));
}
const iso = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");
const dayStr = (y: number, m: number, d: number) => `${y}-${String(m).padStart(2, "0")}-${String(d).padStart(2, "0")}`;
function addDays(y: number, m: number, d: number, n: number) {
  const t = new Date(Date.UTC(y, m - 1, d + n, 12));
  return { y: t.getUTCFullYear(), m: t.getUTCMonth() + 1, d: t.getUTCDate() };
}

// ---------- deterministic noise ----------

function hash(s: string) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  // final avalanche so similar strings (consecutive dates) don't get similar values
  h ^= h >>> 16;
  h = Math.imul(h, 0x85ebca6b);
  h ^= h >>> 13;
  h = Math.imul(h, 0xc2b2ae35);
  h ^= h >>> 16;
  return (h >>> 0) / 4294967296;
}
const wobble = (t: number, seed: number) => 0.5 + 0.3 * Math.sin(t / 7 + seed * 11) + 0.2 * Math.sin(t / 2.3 + seed * 5);

// ---------- the network ----------

type Spec = {
  short: string;
  name: string | null; // the user's label (null = never renamed)
  autoName: string | null; // what the device announces about itself
  autoSource: NameSource;
  model: string | null;
  ip: string;
  mac: string | null;
  vendor: string | null;
  group: string | null;
  exit: string | null;
  randomMac?: boolean;
  usesGateway: boolean;
  online?: boolean; // default true
  lastSeen?: string; // when offline
  liveMbps: number; // typical live download rate
  dailyGB: number; // typical daily usage
  firstSeen: string;
  hosts: string[];
};

// The network as captured for the approved console concept (concepts5/data), with synthetic usage.
const D = (o: Partial<Spec> & Pick<Spec, "short" | "ip">): Spec => ({
  name: null, autoName: null, autoSource: "ip", model: null, mac: null, vendor: null, group: null,
  exit: null, usesGateway: false, liveMbps: 0, dailyGB: 0, firstSeen: "2026-11-13T18:48:59Z", hosts: [], ...o,
});
const SPECS: Spec[] = [
  D({ short: "D12", ip: "192.168.50.12", mac: "02:4a:00:00:00:01", name: "Office PC", vendor: "Dell", group: "Work", exit: "direct", usesGateway: true, liveMbps: 6.1, dailyGB: 3.2,
      hosts: ["googlevideo.com", "discord.com", "steamcontent.com", "github.com", "claude.ai"] }),
  D({ short: "D06", ip: "192.168.50.39", mac: "02:4a:00:00:00:02", autoName: "Galaxy A15", autoSource: "dhcp", randomMac: true, group: "Kids", exit: "direct", usesGateway: true, liveMbps: 1.4, dailyGB: 1.2,
      hosts: ["whatsapp.net", "fbcdn.net", "googlevideo.com", "tiktokcdn.com"] }),
  D({ short: "D11", ip: "192.168.50.234", mac: "02:4a:00:00:00:03", autoName: "sam pixel 8", autoSource: "dhcp", randomMac: true, group: "Family", exit: "direct", usesGateway: true, liveMbps: 2.2, dailyGB: 1.5,
      hosts: ["instagram.com", "googlevideo.com", "whatsapp.net", "spotify.com"] }),
  D({ short: "D07", ip: "192.168.50.6", mac: "02:4a:00:00:00:04", name: "NAS", vendor: "Synology", group: "Media", exit: "direct", usesGateway: true, liveMbps: 0.3, dailyGB: 0.4,
      hosts: ["api.anthropic.com", "pypi.org", "github.com"] }),
  D({ short: "D04", ip: "192.168.50.3", mac: "02:4a:00:00:00:05", name: "Home server", autoName: "homeserver", autoSource: "dhcp", group: "Media", exit: "direct", usesGateway: true, liveMbps: 0.5, dailyGB: 0.3,
      hosts: ["updates.example.com", "deb.debian.org", "ntp.org"] }),
  D({ short: "D09", ip: "192.168.50.10", mac: "02:4a:00:00:00:06", name: "Printer", vendor: "Brother", group: "Work", exit: "direct", usesGateway: true, liveMbps: 0.2, dailyGB: 0.3,
      hosts: ["registry-1.docker.io", "github.com"] }),
  D({ short: "D05", ip: "192.168.50.5", mac: "02:4a:00:00:00:07", name: "Media player", vendor: "Raspberry Pi", group: "Media", exit: "direct", usesGateway: true, liveMbps: 0.1, dailyGB: 0.2,
      hosts: ["image.tmdb.org", "api.themoviedb.org"] }),
  D({ short: "D14", ip: "192.168.50.32", mac: "02:4a:00:00:00:08", autoName: "mia laptop", autoSource: "dhcp", randomMac: true, group: "Family", exit: "direct", usesGateway: true, liveMbps: 3.5, dailyGB: 3.4,
      hosts: ["googlevideo.com", "github.com", "vdocipher.com", "zoom.us"] }),
  D({ short: "D01", ip: "192.168.50.1", mac: "02:4a:00:00:00:09", name: "Router", vendor: "TP-Link", group: "Network", autoName: "Archer AX55", autoSource: "router" }),
  D({ short: "D02", ip: "192.168.50.221", mac: "02:4a:00:00:00:0a", autoName: "Kitchen tablet", autoSource: "router", randomMac: true, group: "Family" }),
  D({ short: "D03", ip: "192.168.50.2", mac: "02:4a:00:00:00:0b", name: "Mesh point", vendor: "TP-Link", group: "Network" }),
  D({ short: "D10", ip: "192.168.50.26", mac: "02:4a:00:00:00:0c", autoName: "Guest iPhone", autoSource: "router", randomMac: true, group: "Guests" }),
  D({ short: "D13", ip: "192.168.50.238", mac: "02:4a:00:00:00:0d", autoName: "Living room TV", autoSource: "upnp", group: "Family" }),
  D({ short: "D08", ip: "192.168.50.56", mac: "02:4a:00:00:00:0e", name: "PlayStation 5", group: "Kids", online: false, lastSeen: "2026-11-13T21:16:00Z" }),
  D({ short: "D18", ip: "192.168.50.53", online: false, firstSeen: "" }),
];

// Evening-heavy day shape (local hours 0..23).
const HOUR_W = [3, 2, 1, 1, 1, 1, 1, 2, 3, 3, 4, 5, 5, 5, 6, 7, 8, 9, 11, 12, 11, 9, 7, 5];
const HOUR_SUM = HOUR_W.reduce((a, b) => a + b, 0);

// ---------- mutable sample state ----------

const startedAt = Date.now();
// name: undefined = untouched, null = cleared back to the auto name
const labels = new Map<string, { name?: string | null; group?: string | null }>();

const idOf = (s: Spec) => (s.mac ? s.mac.toLowerCase() : `ip:${s.ip}`);
// Device .53 joined 20 min before the mock started and is unnamed (drives the "New device" alert).
const NEW_FIRST_SEEN = () => iso(new Date(startedAt - 20 * 60 * 1000));

// ---------- per-device usage ----------

function dayGB(s: Spec, day: string) {
  return s.dailyGB * (0.6 + 0.8 * hash(`${s.short}:${day}`));
}
function hourBytes(s: Spec, day: string, h: number) {
  const g = dayGB(s, day) * GB * (HOUR_W[h] / HOUR_SUM) * (0.7 + 0.6 * hash(`${s.short}:${day}:${h}`));
  return g;
}
const UP_SHARE = (s: Spec) => (s.short === "D01" || s.short === "D04" ? 0.35 : 0.08);

function todayParts(now: Date) {
  const c = local(now);
  const day = dayStr(c.y, c.m, c.d);
  const frac = c.min / 60;
  return { c, day, frac };
}

/** Today's bytes per hour (local), the current hour partly filled. */
function hoursToday(s: Spec, now: Date) {
  const { c, day, frac } = todayParts(now);
  return HOUR_W.map((_, h) => {
    if (!s.usesGateway || h > c.h) return 0;
    const full = hourBytes(s, day, h);
    return h === c.h ? full * frac : full;
  });
}

function naming(s: Spec, label: string | null | undefined): Pick<Device, "name" | "nameSource" | "autoName" | "model"> {
  const user = label !== undefined ? label : s.name;
  const name = user ?? s.autoName ?? `Device .${s.ip.split(".").pop()}`;
  const nameSource: NameSource = user ? "user" : s.autoName ? s.autoSource : "ip";
  return { name, nameSource, autoName: s.autoName, model: s.model };
}

function device(s: Spec, now: Date): Device {
  const t = now.getTime() / 1000;
  const seed = hash(s.short);
  const l = labels.get(idOf(s)) ?? {};
  const hours = hoursToday(s, now);
  const total = hours.reduce((a, b) => a + b, 0);
  const up = UP_SHARE(s);
  const id = idOf(s);
  const vpn = vpnOf(s);
  const pause = pauseOf(id, now);
  const raw = s.usesGateway ? ((s.liveMbps * 1e6) / 8) * (0.55 + 0.9 * wobble(t, seed)) : 0;
  const first = s.firstSeen || NEW_FIRST_SEEN();
  const week = periodBytes(s, now, "week");
  const month = periodBytes(s, now, "month");
  const base: Device = {
    id: idOf(s),
    short: s.short,
    ip: s.ip,
    mac: s.mac,
    ...naming(s, l.name),
    group: l.group !== undefined ? l.group : s.group,
    vendor: s.vendor,
    randomMac: !!s.randomMac,
    online: s.online !== false,
    usesGateway: s.usesGateway,
    firstSeen: first,
    lastSeen: s.online === false ? s.lastSeen ?? iso(new Date(startedAt - 25 * 60000)) : iso(new Date(now.getTime() - (s.usesGateway ? 1000 : 40000))),
    rateDown: 0,
    rateUp: 0,
    todayDown: Math.round(total * (1 - up)),
    todayUp: Math.round(total * up),
    exit: null,
    vpn,
    paused: !!pause,
    pausedUntil: pause && pause.until ? iso(new Date(pause.until)) : null,
    controllable: s.usesGateway,
    connections: 0,
    profile: profileState.has(id) ? profileState.get(id)! : null,
    speedLimitMbps: speedState.get(id) ?? null,
    weekDown: Math.round(week * (1 - up)),
    weekUp: Math.round(week * up),
    monthDown: Math.round(month * (1 - up)),
    monthUp: Math.round(month * up),
  };
  base.links = linksOf(id);
  const eff = effectiveOf(s, base, now);
  base.effective = eff;
  const blocked = eff.paused;
  const cap = eff.speedLimitMbps != null ? (eff.speedLimitMbps * 1e6) / 8 : Infinity;
  const live = blocked ? 0 : Math.min(raw, cap);
  base.rateDown = Math.round(live);
  base.rateUp = Math.round(Math.min(live * up * 1.4, cap));
  base.exit = s.usesGateway && s.exit ? eff.exit : null;
  base.connections = s.usesGateway && !blocked ? Math.round(4 + 40 * wobble(t / 3, seed) * (s.liveMbps / 6 + 0.2)) : 0;
  return base;
}

/** Bytes this calendar week (Sun–Sat) or month, local days, including today so far. */
function periodBytes(s: Spec, now: Date, period: "week" | "month") {
  if (!s.usesGateway) return 0;
  const { c } = todayParts(now);
  const dow = new Date(Date.UTC(c.y, c.m - 1, c.d)).getUTCDay();
  const back = period === "week" ? dow : c.d - 1;
  let total = hoursToday(s, now).reduce((a, b) => a + b, 0);
  for (let i = 1; i <= back; i++) {
    const x = addDays(c.y, c.m, c.d, -i);
    total += dayGB(s, dayStr(x.y, x.m, x.d)) * GB;
  }
  return total;
}

function devices(now: Date) {
  return SPECS.map((s) => device(s, now));
}

// ---------- exits ----------

// ---------- VPN selector (Junction does failover itself) ----------

type Control = { mode: "auto" | "manual" | "direct"; profile: string; manualEndpoint: string };
let control: Control = { mode: "direct", profile: "default", manualEndpoint: "home" };
let lastSwitch = "2026-11-12T23:22:05Z";
let reason = "travel-default failed 3 health checks in a row";

/** The sing-box outbound in use. Automatic picks the best healthy exit: Home VPN (Travel VPN is down). */
function selectedOutbound() {
  if (control.mode === "direct") return "direct";
  if (control.mode === "manual") return `${control.manualEndpoint}-${control.profile}`;
  return `home-${control.profile}`;
}

// ---------- per-device VPN and pause ----------

const vpnState = new Map<string, boolean>();
// until: epoch ms, or null = until resumed. media starts paused for 75 min to show the state.
const pauses = new Map<string, { until: number | null }>([["02:4a:00:00:00:0f", { until: startedAt + 75 * 60 * 1000 }]]);

/** Devices whose usual route is a tunnel start with the VPN on; the rest go direct. */
function vpnOf(s: Spec): boolean {
  const v = vpnState.get(idOf(s));
  return v ?? (!!s.exit && s.exit !== "direct");
}
function pauseOf(id: string, now: Date) {
  const p = pauses.get(id);
  if (p && p.until != null && p.until <= now.getTime()) {
    pauses.delete(id); // a timed pause ends on its own
    return null;
  }
  return p ?? null;
}

type Fail = { status: number; error: string };
const specById = (id: string) => SPECS.find((x) => idOf(x) === id);

export function setDeviceVpn(id: string, body: unknown): Device | Fail {
  const s = specById(id);
  if (!s) return { status: 404, error: "No device with that id." };
  const on = (body as { on?: unknown } | undefined)?.on;
  if (typeof on !== "boolean") return { status: 400, error: "Send { on: true } or { on: false }." };
  if (!s.usesGateway) return { status: 409, error: "This device isn't using the gateway yet, so its VPN can't be changed." };
  vpnState.set(id, on);
  const d = device(s, new Date());
  log("control", `You turned the VPN ${on ? "on" : "off"} for ${d.name}.`, id);
  return d;
}

export function pauseDevice(id: string, body: unknown): Device | Fail {
  const s = specById(id);
  if (!s) return { status: 404, error: "No device with that id." };
  if (!s.usesGateway) return { status: 409, error: "This device isn't using the gateway yet, so it can't be paused." };
  const minutes = (body as { minutes?: unknown } | undefined)?.minutes;
  if (minutes !== undefined && (typeof minutes !== "number" || !Number.isInteger(minutes) || minutes < 1 || minutes > 10080))
    return { status: 400, error: "Minutes must be a whole number from 1 to 10080." };
  pauses.set(id, { until: minutes === undefined ? null : Date.now() + minutes * 60 * 1000 });
  const d = device(s, new Date());
  log("control", `You paused ${d.name}${minutes === undefined ? " until you resume it" : ` for ${minutes} min`}.`, id);
  return d;
}

export function resumeDevice(id: string): Device | Fail {
  const s = specById(id);
  if (!s) return { status: 404, error: "No device with that id." };
  pauses.delete(id);
  const d = device(s, new Date());
  log("control", `You resumed ${d.name}.`, id);
  return d;
}

export function setGroupVpn(name: string, body: unknown): { group: string; on: boolean; devices: Device[] } | Fail {
  const on = (body as { on?: unknown } | undefined)?.on;
  if (typeof on !== "boolean") return { status: 400, error: "Send { on: true } or { on: false }." };
  const now = new Date();
  const members = SPECS.filter((s) => device(s, now).group === name);
  if (!members.length) return { status: 404, error: `There's no group called ${name}.` };
  for (const s of members) if (s.usesGateway) vpnState.set(idOf(s), on);
  return { group: name, on, devices: members.map((s) => device(s, now)) };
}

export function putSelector(body: unknown): { exits: ExitHealth[]; selector: Overview["selector"] } | { status: number; error: string } {
  if (!body || typeof body !== "object") return { status: 400, error: "Send a JSON object with mode." };
  const b = body as { mode?: unknown; endpoint?: unknown; profile?: unknown };
  if (b.mode !== "auto" && b.mode !== "manual" && b.mode !== "direct") return { status: 400, error: "Mode must be auto, manual or direct." };
  if (b.profile !== undefined && !["default", "streaming", "gaming"].includes(b.profile as string))
    return { status: 400, error: "Profile must be default, streaming or gaming." };
  if (b.endpoint !== undefined && b.endpoint !== "home" && b.endpoint !== "travel") return { status: 400, error: "Endpoint must be home or travel." };
  const endpoint = (b.endpoint as string | undefined) ?? control.manualEndpoint;
  if (b.mode === "manual" && endpoint === "travel")
    return { status: 409, error: "Travel VPN is down right now, so Junction won't switch to it. Pick a healthy exit or Automatic." };
  const next: Control = {
    mode: b.mode,
    profile: (b.profile as string | undefined) ?? control.profile,
    manualEndpoint: b.mode === "manual" ? endpoint : control.manualEndpoint,
  };
  const changed = JSON.stringify(next) !== JSON.stringify(control);
  control = next;
  if (changed) {
    log(
      "control",
      next.mode === "direct"
        ? "You sent everything direct."
        : next.mode === "manual"
          ? `You chose ${next.manualEndpoint === "travel" ? "Travel VPN" : "Home VPN"} (${next.profile}) for VPN devices.`
          : `You set the VPN to Automatic (${next.profile}).`,
    );
    lastSwitch = iso(new Date());
    reason =
      next.mode === "direct"
        ? "you chose Direct"
        : next.mode === "manual"
          ? `you chose ${next.manualEndpoint}-${next.profile}`
          : `you chose Automatic (${next.profile} profile)`;
  }
  const o = overview();
  return { exits: o.exits, selector: o.selector };
}

function exits(now: Date, ds: Device[]): ExitHealth[] {
  const t = now.getTime() / 1000;
  // Travel VPN is re-checked with backoff: first 4 min after the mock starts, then every 5 min.
  const first = startedAt + 4 * 60 * 1000;
  const k = Math.max(0, Math.ceil((now.getTime() - first) / (5 * 60 * 1000)));
  const travelNext = iso(new Date(first + k * 5 * 60 * 1000));
  const soon = iso(new Date(now.getTime() + 30 * 1000));
  const homeLat = (base: number, ph: number) => Math.round(base + 8 * Math.sin(t / 5 + ph));
  const homeToday = ds.filter((d) => d.exit?.startsWith("home")).reduce((a, d) => a + d.todayDown + d.todayUp, 0);
  return [
    {
      key: "home",
      name: "Home VPN",
      healthy: true,
      probed: true,
      latencyMs: Math.round(318 + 9 * Math.sin(t / 5)),
      lastSuccess: iso(new Date(now.getTime() - 2000)),
      lastFailure: "2026-11-10T03:41:07Z",
      lastError: "timeout: no recent network activity",
      bytesToday: homeToday,
      profiles: [
        { profile: "default", known: true, healthy: true, latencyMs: homeLat(318, 0), nextCheck: soon },
        { profile: "streaming", known: true, healthy: true, latencyMs: homeLat(322, 1), nextCheck: soon },
        { profile: "gaming", known: true, healthy: true, latencyMs: homeLat(325, 2), nextCheck: soon },
      ],
      nextCheck: null,
    },
    {
      key: "travel",
      name: "Travel VPN",
      healthy: false,
      probed: true,
      latencyMs: null,
      lastSuccess: "2026-11-12T23:21:48Z",
      lastFailure: "2026-11-12T23:22:00Z",
      lastError: "timeout: no recent network activity",
      bytesToday: 0,
      profiles: ["default", "streaming", "gaming"].map((profile) => ({ profile, known: true, healthy: false, latencyMs: null, nextCheck: travelNext })),
      nextCheck: travelNext,
    },
  ];
}

// ---------- usage history ----------

function usageDays(now: Date, n: number): UsageDay[] {
  const { c } = todayParts(now);
  const out: UsageDay[] = [];
  for (let i = n - 1; i >= 0; i--) {
    const d = addDays(c.y, c.m, c.d, -i);
    const day = dayStr(d.y, d.m, d.d);
    const byExit: Record<string, number> = {};
    let tunnel = 0;
    let direct = 0;
    for (const s of SPECS) {
      if (!s.usesGateway || !s.exit) continue;
      const b = i === 0 ? hoursToday(s, now).reduce((a, x) => a + x, 0) : dayGB(s, day) * GB;
      byExit[s.exit] = (byExit[s.exit] ?? 0) + Math.round(b);
      if (s.exit === "direct") direct += b;
      else tunnel += b;
    }
    out.push({ day, tunnel: Math.round(tunnel), direct: Math.round(direct), byExit, billed: Math.round((tunnel + direct) * 1.061) });
  }
  return out;
}

export function usage(now: Date, n: number) {
  return usageDays(now, Math.max(1, Math.min(n, 90)));
}

// ---------- bundle ----------

const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;

/** Traffic Junction would have counted between two instants (gateway devices only). */
function counted(t0: number, t1: number) {
  let tunnel = 0;
  let direct = 0;
  for (let hs = Math.floor(t0 / HOUR) * HOUR; hs < t1; hs += HOUR) {
    const frac = (Math.min(t1, hs + HOUR) - Math.max(t0, hs)) / HOUR;
    if (frac <= 0) continue;
    const c = local(new Date(hs));
    const day = dayStr(c.y, c.m, c.d);
    for (const s of SPECS) {
      if (!s.usesGateway || !s.exit) continue;
      const b = hourBytes(s, day, c.h) * frac;
      if (s.exit === "direct") direct += b;
      else tunnel += b;
    }
  }
  return { tunnel: Math.round(tunnel), direct: Math.round(direct), total: Math.round(tunnel + direct) };
}

let nextId = 1;
let events: BundleEvent[] = [];

// Seed: a 140 GB renewal two days ago and a "set left" correction yesterday (MOCK_NO_BUNDLE=1 starts empty).
if (process.env.MOCK_NO_BUNDLE !== "1") {
  const renewedAt = Math.floor((startedAt - 2 * DAY) / HOUR) * HOUR;
  const checkedAt = Math.floor((startedAt - DAY) / HOUR) * HOUR;
  // What the ISP's app said: a bit less than the gateway counted, as phones bypass it.
  const setLeft = Math.round((140 * GB - counted(renewedAt, checkedAt).total * 1.12) / 1e8) * 1e8;
  events = [
    { id: nextId++, kind: "renewal", at: iso(new Date(renewedAt)), amount: 140 * GB, expiresAt: iso(new Date(renewedAt + 30 * DAY)) },
    { id: nextId++, kind: "check", at: iso(new Date(checkedAt)), amount: setLeft, expiresAt: null },
  ];
}

function bundle(now: Date): Bundle | null {
  const t = now.getTime();
  const past = events.filter((e) => new Date(e.at).getTime() <= t);
  const renewal = past.filter((e) => e.kind === "renewal").sort((a, b) => b.at.localeCompare(a.at))[0];
  if (!renewal) return null;
  const rAt = new Date(renewal.at).getTime();
  const check = past
    .filter((e) => e.kind === "check" && new Date(e.at).getTime() >= rAt)
    .sort((a, b) => b.at.localeCompare(a.at))[0];
  const anchor = check ?? renewal;
  const since = counted(rAt, t);
  const left = anchor.amount - counted(new Date(anchor.at).getTime(), t).total;
  const elapsed = t - rAt;
  const pacePerDay = elapsed >= 6 * HOUR ? Math.round(since.total / (elapsed / DAY)) : 0;
  const runsOut = pacePerDay > 0 && left > 0 ? iso(new Date(t + (left / pacePerDay) * DAY)) : null;
  return {
    renewedAt: renewal.at,
    size: renewal.amount,
    expiresAt: renewal.expiresAt,
    anchor: { kind: anchor.kind, at: anchor.at, amount: anchor.amount },
    usedSinceRenewal: since.total,
    tunnel: since.tunnel,
    direct: since.direct,
    left,
    pacePerDay,
    runsOut,
    overheadPct: 6.1,
  };
}

export function bundleState(now = new Date()): BundleState {
  const list = [...events].sort((a, b) => b.at.localeCompare(a.at) || b.id - a.id).slice(0, 50);
  return { current: bundle(now), events: list };
}

function when(v: unknown, label: string): number | string | undefined {
  if (v === undefined || v === null || v === "") return undefined;
  const t = typeof v === "string" ? new Date(v).getTime() : NaN;
  if (!isFinite(t)) return `${label} must be an RFC 3339 time.`;
  return t;
}

export function addRenewal(body: unknown): BundleState | string {
  if (!body || typeof body !== "object") return "Send a JSON object with gb.";
  const b = body as { gb?: unknown; at?: unknown; expiresAt?: unknown };
  if (typeof b.gb !== "number" || !isFinite(b.gb) || b.gb <= 0 || b.gb > 100000) return "The bundle size must be a positive number of GB.";
  const at = when(b.at, "The renewal time");
  if (typeof at === "string") return at;
  const t = at ?? Date.now();
  if (t > Date.now() + 5 * 60 * 1000) return "The renewal can't be in the future.";
  const exp = when(b.expiresAt, "The expiry");
  if (typeof exp === "string") return exp;
  if (exp !== undefined && exp <= t) return "The expiry must be after the renewal.";
  events.push({
    id: nextId++,
    kind: "renewal",
    at: iso(new Date(t)),
    amount: Math.round(b.gb * GB),
    expiresAt: exp === undefined ? null : iso(new Date(exp)),
  });
  return bundleState();
}

export function addCheck(body: unknown): BundleState | string {
  if (!body || typeof body !== "object") return "Send a JSON object with gbLeft.";
  const b = body as { gbLeft?: unknown; at?: unknown };
  if (typeof b.gbLeft !== "number" || !isFinite(b.gbLeft) || b.gbLeft < 0) return "What's left must be a number of GB, zero or more.";
  const at = when(b.at, "The check time");
  if (typeof at === "string") return at;
  const t = at ?? Date.now();
  if (t > Date.now() + 5 * 60 * 1000) return "The check can't be in the future.";
  if (!events.some((e) => e.kind === "renewal")) return "Log a renewal first.";
  events.push({ id: nextId++, kind: "check", at: iso(new Date(t)), amount: Math.round(b.gbLeft * GB), expiresAt: null });
  return bundleState();
}

export function deleteEvent(id: number): BundleState | null {
  const i = events.findIndex((e) => e.id === id);
  if (i < 0) return null;
  events.splice(i, 1);
  return bundleState();
}

// ---------- linked devices (v1.1) ----------

// Galaxy S23's second Wi-Fi MAC, folded into it (hostnames match).
const links = new Map<string, { id: string; ip: string; mac: string; name: string; online: boolean; to: string | null }>([
  ["02:4a:00:00:00:10", { id: "02:4a:00:00:00:10", ip: "192.168.50.134", mac: "02:4a:00:00:00:10", name: "Sam's Pixel 8", online: true, to: "02:4a:00:00:00:03" }],
]);
function linksOf(id: string) {
  return [...links.values()].filter((l) => l.to === id).map(({ to: _to, ...l }) => l);
}
export function linkDevice(id: string, body: unknown): { ok: string } | Fail {
  const l = links.get(id);
  if (!l) return { status: 404, error: "No linked connection with that id." };
  const to = (body as { to?: unknown } | undefined)?.to;
  if (to !== null && (typeof to !== "string" || !specById(to))) return { status: 400, error: "`to` must be a device id or null." };
  l.to = to as string | null;
  return { ok: to ? `Linked ${l.name}.` : `Unlinked ${l.name}; it shows on its own again.` };
}

// ---------- rules engine (v1) ----------
// Effective state = manual settings, then every enabled matching rule whose `when` holds, in order.
// Later rules override field by field, except: any pause wins and the lowest speed cap wins.
// Device-level exemptions ("give 1 h more", "skip tonight") ignore a rule until they end.

const profileState = new Map<string, Profile | null>();
const speedState = new Map<string, number | null>();
const exemptions = new Map<string, number>(); // `${ruleId}|${deviceId}` -> until (epoch ms)

let rules: Rule[] = [
  { id: 1, name: "Daily cap", enabled: true, order: 1, match: { groups: ["Guests"] }, when: { usage: { period: "day", overGB: 4 } }, then: { speedMbps: 2 } },
  { id: 2, name: "Kids bedtime", enabled: true, order: 2, match: { groups: ["Kids"] }, when: { schedule: { days: [0, 1, 2, 3, 4, 5, 6], start: "23:30", end: "07:00" } }, then: { pause: true } },
];

const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
function daysText(days: number[]) {
  const ds = [...new Set(days)].sort((a, b) => a - b);
  if (ds.length === 7) return "Every day";
  const contiguous = ds.every((d, i) => i === 0 || d === ds[i - 1] + 1);
  return contiguous && ds.length >= 3 ? `${DAYS[ds[0]]}–${DAYS[ds[ds.length - 1]]}` : ds.map((d) => DAYS[d]).join(", ");
}
const hm = (t: string) => {
  const [h, m] = t.split(":").map(Number);
  return h * 60 + (m || 0);
};
const gbText = (b: number) => `${(b / GB).toFixed(1).replace(/\.0$/, "")} GB`;
const hhmm = new Intl.DateTimeFormat("en-GB", { timeZone: TZ, hour: "2-digit", minute: "2-digit", hourCycle: "h23" });

function localDowMin(at: Date) {
  const c = local(at);
  return { dow: new Date(Date.UTC(c.y, c.m - 1, c.d)).getUTCDay(), min: c.h * 60 + c.min };
}

function scheduleActive(sc: { days: number[]; start: string; end: string }, at: Date) {
  const { dow, min } = localDowMin(at);
  const a = hm(sc.start);
  const b = hm(sc.end);
  if (b > a) return sc.days.includes(dow) && min >= a && min < b;
  return (sc.days.includes(dow) && min >= a) || (sc.days.includes((dow + 6) % 7) && min < b);
}

function matches(m: Rule["match"], d: Device, at: Date) {
  if (m.all) return true;
  if (m.devices?.includes(d.id)) return true;
  if (d.group && m.groups?.includes(d.group)) return true;
  if (m.newDevices && d.nameSource !== "user" && at.getTime() - new Date(d.firstSeen).getTime() < DAY) return true;
  return false;
}

/** Why the rule applies now, or null when its `when` doesn't hold. Usage is the device's current usage. */
function whyNow(r: Rule, d: Device, at: Date): string | null {
  const parts: string[] = [];
  if (r.when.schedule) {
    if (!scheduleActive(r.when.schedule, at)) return null;
    parts.push(`${daysText(r.when.schedule.days)} ${r.when.schedule.start}–${r.when.schedule.end}, it's ${hhmm.format(at)}`);
  }
  if (r.when.usage) {
    const u = r.when.usage;
    const used =
      u.period === "day" ? d.todayDown + d.todayUp : u.period === "week" ? (d.weekDown ?? 0) + (d.weekUp ?? 0) : (d.monthDown ?? 0) + (d.monthUp ?? 0);
    if (used <= u.overGB * GB) return null;
    parts.push(`Used ${gbText(used)} ${u.period === "day" ? "today" : `this ${u.period}`}, over ${u.overGB} GB`);
  }
  return parts.length ? parts.join("; ") : "Always applies";
}

function effectiveOf(s: Spec, d: Device, at: Date): Effective {
  let vpn = !!d.vpn;
  let profile: Profile = d.profile ?? (control.profile as Profile);
  let paused = !!d.paused;
  let pausedBy: Effective["pausedBy"] = paused ? "manual" : null;
  let speed: number | null = d.speedLimitMbps ?? null;
  const active: Effective["rules"] = [];
  const ex: Effective["exemptions"] = [];
  for (const [k, until] of exemptions) {
    const [rid, did] = k.split("|");
    if (did === d.id && until > at.getTime()) ex.push({ ruleId: Number(rid), until: iso(new Date(until)) });
  }
  if (s.usesGateway) {
    for (const r of [...rules].sort((a, b) => a.order - b.order)) {
      if (!r.enabled || !matches(r.match, d, at) || ex.some((e) => e.ruleId === r.id)) continue;
      const why = whyNow(r, d, at);
      if (why == null) continue;
      if (r.then.pause) {
        paused = true;
        pausedBy = pausedBy ?? "rule";
      }
      if (r.then.speedMbps != null) speed = speed == null ? r.then.speedMbps : Math.min(speed, r.then.speedMbps);
      if (r.then.vpn != null) vpn = r.then.vpn;
      if (r.then.profile) profile = r.then.profile;
      active.push({ id: r.id, name: r.name, why });
    }
  } else vpn = false;
  const sel = selectedOutbound();
  const exit = !vpn || sel === "direct" ? "direct" : `${sel.split("-")[0]}-${profile}`;
  return { vpn, profile, exit, paused, pausedBy, speedLimitMbps: speed, rules: active, exemptions: ex };
}

// ---------- events timeline ----------

let nextEventId = 1;
let timeline: JEvent[] = [];
function log(kind: EventKind, text: string, deviceId: string | null = null, ruleId: number | null = null, at = new Date()) {
  timeline.push({ id: nextEventId++, at: iso(at), kind, deviceId, ruleId, text });
}

(function seedEvents() {
  const ago = (min: number) => new Date(startedAt - min * 60 * 1000);
  const LAPTOP = "02:4a:00:00:00:08";
  const S26 = "02:4a:00:00:00:03";
  const SKY = "02:4a:00:00:00:06";
  const NEWDEV = "ip:192.168.50.53";
  const seeds: [EventKind, string, string | null, number | null, Date][] = [
    ["exit", "Travel VPN stopped answering (probe to cp.cloudflare.com timed out). VPN traffic moved to Home VPN.", null, null, ago(26 * 60)],
    ["rule", 'Rule "Daily cap" created', null, 1, ago(20 * 60)],
    ["rule", 'Rule "Kids bedtime" created', null, 2, ago(19 * 60)],
    ["device", "Sam's Pixel 8 (192.168.50.134) is another connection of sam pixel 8: linked", S26, null, ago(140)],
    ["control", "Home server: VPN turned on", SKY, null, ago(70)],
    ["control", "Home server: VPN profile set to gaming", SKY, null, ago(69)],
    ["control", "Home server: VPN turned off", SKY, null, ago(55)],
    ["dhcp", "Device .53 got 192.168.50.53 from DHCP", NEWDEV, null, ago(20)],
    ["device", "New device on the network: 192.168.50.53", NEWDEV, null, ago(20)],
    ["control", "mia laptop paused until resumed", LAPTOP, null, ago(12)],
    ["control", "mia laptop resumed", LAPTOP, null, ago(11)],
  ];
  seeds.sort((a, b) => a[4].getTime() - b[4].getTime()).forEach(([k, t, d, r, at]) => log(k, t, d, r, at));
})();

// ---------- console v1.2: traffic and exit history ----------

export function traffic(q: URLSearchParams) {
  const n = Math.max(1, Math.min(168, Number(q.get("hours") ?? 24) || 24));
  const now = Date.now();
  const cur = Math.floor(now / HOUR) * HOUR;
  const hours = [];
  for (let i = n - 1; i >= 0; i--) {
    const h0 = cur - i * HOUR;
    const t = counted(h0, Math.min(now, h0 + HOUR));
    // counted() splits tunnel/direct; down/up use a typical 88/12 split
    hours.push({ hour: iso(new Date(h0)), down: Math.round(t.total * 0.88), up: Math.round(t.total * 0.12) });
  }
  return { hours };
}

export function exitHistory(q: URLSearchParams) {
  const n = Math.max(1, Math.min(1440, Number(q.get("minutes") ?? 60) || 60));
  // history is in memory on the gateway: here it starts 40 minutes before the mock did
  const first = Math.floor((startedAt - 40 * 60000) / 60000) * 60000;
  const end = Math.floor(Date.now() / 60000) * 60000;
  const samples = [];
  for (let t = Math.max(first, end - (n - 1) * 60000); t <= end; t += 60000) {
    const k = t / 60000;
    const exits: Record<string, number | null> = {
      home: Math.round(312 + 9 * Math.sin(k / 3) + (k % 17 === 0 ? 60 : 0)),
      direct: Math.round(14 + 2 * Math.sin(k / 5)),
    };
    if (t - first > 5 * 60000) exits.travel = null; // Travel VPN: no reply (missing in the first samples)
    samples.push({ at: iso(new Date(t)), exits });
  }
  return { samples };
}

export function eventList(q: URLSearchParams): JEvent[] {
  const limit = Math.max(1, Math.min(500, Number(q.get("limit") ?? 100) || 100));
  const dev = q.get("device");
  const before = q.get("before") ? Number(q.get("before")) : null;
  return timeline
    .filter((e) => (!dev || e.deviceId === dev) && (before == null || e.id < before))
    .sort((a, b) => b.id - a.id)
    .slice(0, limit);
}

// ---------- rules endpoints ----------

const PROFILE_SET = ["default", "streaming", "gaming"];
const ruleSummary = (r: Rule) => {
  const who = r.match.all ? "Everyone" : [r.match.groups?.join(", "), r.match.devices?.length ? `${r.match.devices.length} device(s)` : "", r.match.newDevices ? "New devices" : ""].filter(Boolean).join(" + ");
  const when = [r.when.schedule ? `${daysText(r.when.schedule.days)} ${r.when.schedule.start}–${r.when.schedule.end}` : "", r.when.usage ? `over ${r.when.usage.overGB} GB a ${r.when.usage.period}` : ""].filter(Boolean).join(", ");
  const then = [r.then.pause ? "Pause" : "", r.then.speedMbps != null ? `Slow to ${r.then.speedMbps} Mbit/s` : "", r.then.vpn != null ? `VPN ${r.then.vpn ? "on" : "off"}` : "", r.then.profile ? `${r.then.profile} profile` : ""].filter(Boolean).join(", ");
  return `${who}${when ? ` · ${when}` : ""} → ${then}`;
};

function cleanRule(body: unknown): Omit<Rule, "id" | "order"> | string {
  if (!body || typeof body !== "object") return "Send the rule as a JSON object.";
  const b = body as Partial<Rule>;
  if (typeof b.name !== "string" || !b.name.trim()) return "A rule needs a name.";
  if (b.name.trim().length > 60) return "Rule names can be at most 60 characters.";
  const m = b.match ?? {};
  if (!m.all && !m.newDevices && !m.groups?.length && !m.devices?.length) return "Say who the rule is for.";
  const w = b.when ?? {};
  if (w.schedule) {
    const sc = w.schedule;
    if (!Array.isArray(sc.days) || !sc.days.length || sc.days.some((d) => !Number.isInteger(d) || d < 0 || d > 6)) return "Schedule days must be 0 (Sunday) to 6.";
    if (!/^\d{2}:\d{2}$/.test(sc.start) || !/^\d{2}:\d{2}$/.test(sc.end)) return "Schedule times must look like 21:30.";
  }
  if (w.usage && (!["day", "week", "month"].includes(w.usage.period) || !(w.usage.overGB > 0))) return "A data condition needs a period and an amount over 0 GB.";
  const t = b.then ?? {};
  if (!t.pause && t.speedMbps == null && t.vpn == null && !t.profile) return "A rule needs at least one thing to do.";
  if (t.speedMbps != null && !(t.speedMbps >= 0.5 && t.speedMbps <= 1000)) return "Speed caps must be 0.5 to 1000 Mbit/s.";
  if (t.profile && !PROFILE_SET.includes(t.profile)) return "Profile must be default, streaming or gaming.";
  return {
    name: b.name.trim(),
    enabled: b.enabled !== false,
    match: { ...(m.all ? { all: true } : {}), ...(m.devices?.length ? { devices: m.devices } : {}), ...(m.groups?.length ? { groups: m.groups } : {}), ...(m.newDevices ? { newDevices: true } : {}) },
    when: { schedule: w.schedule ?? null, usage: w.usage ?? null },
    then: { ...(t.pause ? { pause: true } : {}), ...(t.speedMbps != null ? { speedMbps: t.speedMbps } : {}), ...(t.vpn != null ? { vpn: t.vpn } : {}), ...(t.profile ? { profile: t.profile } : {}) },
    ...(b.graph !== undefined ? { graph: b.graph } : {}),
  };
}

export const listRules = () => [...rules].sort((a, b) => a.order - b.order);

export function createRule(body: unknown): Rule | Fail {
  const r = cleanRule(body);
  if (typeof r === "string") return { status: 400, error: r };
  const rule: Rule = { ...r, id: Math.max(0, ...rules.map((x) => x.id)) + 1, order: Math.max(0, ...rules.map((x) => x.order)) + 1 };
  rules.push(rule);
  log("rule", `You added the rule “${rule.name}”: ${ruleSummary(rule)}.`, null, rule.id);
  return rule;
}

export function updateRule(id: number, body: unknown): Rule | Fail {
  const old = rules.find((x) => x.id === id);
  if (!old) return { status: 404, error: "No rule with that id." };
  const r = cleanRule(body);
  if (typeof r === "string") return { status: 400, error: r };
  const rule: Rule = { ...r, id, order: old.order, ...(r.graph === undefined && old.graph !== undefined ? { graph: old.graph } : {}) };
  rules = rules.map((x) => (x.id === id ? rule : x));
  if (old.enabled !== rule.enabled && JSON.stringify({ ...old, enabled: rule.enabled, graph: null }) === JSON.stringify({ ...rule, graph: null }))
    log("rule", `You turned ${rule.enabled ? "on" : "off"} “${rule.name}”.`, null, id);
  else log("rule", `You changed “${rule.name}”: ${ruleSummary(rule)}.`, null, id);
  return rule;
}

export function deleteRule(id: number): { deleted: number } | Fail {
  const r = rules.find((x) => x.id === id);
  if (!r) return { status: 404, error: "No rule with that id." };
  rules = rules.filter((x) => x.id !== id);
  for (const k of [...exemptions.keys()]) if (k.startsWith(`${id}|`)) exemptions.delete(k);
  log("rule", `You deleted the rule “${r.name}”.`, null, id);
  return { deleted: id };
}

export function orderRules(body: unknown): Rule[] | Fail {
  const ids = (body as { ids?: unknown } | undefined)?.ids;
  if (!Array.isArray(ids) || ids.length !== rules.length || new Set(ids).size !== ids.length || !ids.every((i) => rules.some((r) => r.id === i)))
    return { status: 400, error: "Send every rule id exactly once, in the new order." };
  rules = rules.map((r) => ({ ...r, order: ids.indexOf(r.id) + 1 }));
  log("rule", "You changed the order of the rules.");
  return listRules();
}

function effectiveFor(deviceId: string, at = new Date()): Effective | null {
  const s = specById(deviceId);
  if (!s) return null;
  return device(s, at).effective!;
}

export function exempt(ruleId: number, body: unknown): Effective | Fail {
  const r = rules.find((x) => x.id === ruleId);
  if (!r) return { status: 404, error: "No rule with that id." };
  const b = (body ?? {}) as { deviceId?: unknown; minutes?: unknown };
  if (typeof b.deviceId !== "string" || !specById(b.deviceId)) return { status: 404, error: "No device with that id." };
  if (typeof b.minutes !== "number" || !Number.isInteger(b.minutes) || b.minutes < 1 || b.minutes > 1440)
    return { status: 400, error: "Minutes must be a whole number from 1 to 1440." };
  const until = Date.now() + b.minutes * 60 * 1000;
  exemptions.set(`${ruleId}|${b.deviceId}`, until);
  const name = device(specById(b.deviceId)!, new Date()).name;
  log("rule", `${name} skips “${r.name}” until ${hhmm.format(new Date(until))}.`, b.deviceId, ruleId);
  return effectiveFor(b.deviceId)!;
}

export function endExempt(ruleId: number, deviceId: string): Effective | Fail {
  if (!specById(deviceId)) return { status: 404, error: "No device with that id." };
  const r = rules.find((x) => x.id === ruleId);
  if (exemptions.delete(`${ruleId}|${deviceId}`) && r)
    log("rule", `“${r.name}” applies to ${device(specById(deviceId)!, new Date()).name} again.`, deviceId, ruleId);
  return effectiveFor(deviceId)!;
}

export function simulate(q: URLSearchParams): { effective: Effective; matched: Effective["rules"] } | Fail {
  const id = q.get("device") ?? "";
  const s = specById(id);
  if (!s) return { status: 404, error: "No device with that id." };
  const at = new Date(q.get("at") ?? Date.now());
  if (isNaN(at.getTime())) return { status: 400, error: "`at` must be an RFC 3339 time." };
  const base = device(s, new Date());
  // Rules are evaluated at `at`; data usage is today's so far (the future isn't known).
  const eff = effectiveOf(s, { ...base, paused: base.paused && (!base.pausedUntil || new Date(base.pausedUntil) > at) }, at);
  return { effective: eff, matched: eff.rules };
}

export function setDeviceProfile(id: string, body: unknown): Device | Fail {
  const s = specById(id);
  if (!s) return { status: 404, error: "No device with that id." };
  const p = (body as { profile?: unknown } | undefined)?.profile;
  if (p !== null && !PROFILE_SET.includes(p as string)) return { status: 400, error: "Profile must be default, streaming, gaming or null." };
  profileState.set(id, p as Profile | null);
  const d = device(s, new Date());
  log("control", p ? `You set ${d.name} to the ${p} profile.` : `${d.name} follows the default profile again.`, id);
  return d;
}

export function setDeviceSpeed(id: string, body: unknown): Device | Fail {
  const s = specById(id);
  if (!s) return { status: 404, error: "No device with that id." };
  const v = (body as { mbps?: unknown } | undefined)?.mbps;
  if (v !== null && !(typeof v === "number" && v >= 0.5 && v <= 1000)) return { status: 400, error: "Speed must be 0.5 to 1000 Mbit/s, or null for no cap." };
  speedState.set(id, v as number | null);
  const d = device(s, new Date());
  log("control", v == null ? `You removed the speed cap on ${d.name}.` : `You capped ${d.name} at ${v} Mbit/s.`, id);
  return d;
}

// ---------- floating gateway ----------

// Master since 1 h ago, after a short fault. MOCK_GATEWAY=backup (or fault/stopped) shows the covering state.
function gateway(): NonNullable<Overview["gateway"]> {
  const at = (minAgo: number) => iso(new Date(startedAt - minAgo * 60 * 1000));
  const role = process.env.MOCK_GATEWAY;
  if (role === "backup" || role === "fault") {
    return {
      enabled: true,
      role,
      since: at(12),
      events: [
        { at: at(12), state: role === "fault" ? "FAULT" : "BACKUP" },
        { at: at(60), state: "MASTER" },
        { at: at(62), state: "FAULT" },
        { at: at(180), state: "MASTER" },
      ],
    };
  }
  if (role === "stopped") {
    return { enabled: true, role: "stopped", since: at(5), events: [{ at: at(5), state: "STOP" }, { at: at(60), state: "MASTER" }] };
  }
  return {
    enabled: true,
    role: "master",
    since: at(60),
    events: [
      { at: at(60), state: "MASTER" },
      { at: at(62), state: "FAULT" },
      { at: at(180), state: "MASTER" },
    ],
  };
}

// ---------- endpoints ----------

export function overview(now = new Date()): Overview {
  const ds = devices(now).sort((a, b) => b.rateDown - a.rateDown || b.todayDown - a.todayDown);
  const ex = exits(now, ds);
  const top = ds.find((d) => d.usesGateway && d.rateDown > 0) ?? null;
  const directToday = ds.filter((d) => d.exit === "direct").reduce((a, d) => a + d.todayDown + d.todayUp, 0);
  return {
    timezone: TZ,
    gatewayIP: "192.168.50.4",
    backupName: "Backup Pi",
    brand: { name: "Junction", color: "#4797ff", letter: "J", image: null },
    now: iso(now),
    live: {
      down: ds.reduce((a, d) => a + d.rateDown, 0),
      up: ds.reduce((a, d) => a + d.rateUp, 0),
    },
    top: top ? { deviceId: top.id, name: top.name, rateDown: top.rateDown } : null,
    selector: {
      selected: selectedOutbound(),
      mode: control.mode,
      lastSwitch,
      reason,
      control: { ...control },
    },
    exits: ex,
    direct: { latencyMs: Math.round(14 + 2 * Math.sin(now.getTime() / 3000)), bytesToday: directToday },
    bundle: bundle(now),
    gateway: gateway(),
    counts: {
      online: ds.filter((d) => d.online).length,
      viaGateway: ds.filter((d) => d.online && d.usesGateway).length,
      waiting: ds.filter((d) => !d.group).length,
    },
    devices: ds,
  };
}

export function deviceList(now = new Date()): Device[] {
  return devices(now).sort((a, b) => Number(b.online) - Number(a.online) || a.short.localeCompare(b.short));
}

export function deviceDetail(id: string, now = new Date()): DeviceDetail | null {
  const s = SPECS.find((x) => idOf(x) === id);
  if (!s) return null;
  const d = device(s, now);
  const { c } = todayParts(now);
  const up = UP_SHARE(s);
  const hours = hoursToday(s, now).map((b, h) => ({
    hour: iso(localInstant(c.y, c.m, c.d, h)),
    down: Math.round(b * (1 - up)),
    up: Math.round(b * up),
    byExit: b > 0 && s.exit ? { [s.exit]: Math.round(b) } : {},
  }));
  const days = [];
  for (let i = 29; i >= 0; i--) {
    const x = addDays(c.y, c.m, c.d, -i);
    const day = dayStr(x.y, x.m, x.d);
    const b = !s.usesGateway ? 0 : i === 0 ? d.todayDown + d.todayUp : dayGB(s, day) * GB;
    days.push({ day, down: Math.round(b * (1 - up)), up: Math.round(b * up) });
  }
  const todayTotal = d.todayDown + d.todayUp;
  const weights = s.hosts.map((h, i) => (1 / (i + 1.3)) * (0.8 + 0.4 * hash(h)));
  const wsum = weights.reduce((a, b) => a + b, 0) || 1;
  const topHosts = s.hosts
    .map((host, i) => ({ host, bytes: Math.round((todayTotal * 0.92 * weights[i]) / wsum) }))
    .sort((a, b) => b.bytes - a.bytes)
    .slice(0, 10);
  return { ...d, hours, days, topHosts };
}

export function patchDevice(id: string, body: unknown): Device | string | null {
  const s = SPECS.find((x) => idOf(x) === id);
  if (!s) return null;
  if (!body || typeof body !== "object") return "Send a JSON object with name and/or group.";
  const b = body as { name?: unknown; group?: unknown };
  const cur = labels.get(id) ?? {};
  if (b.name !== undefined) {
    if (typeof b.name !== "string") return "A name must be text.";
    if (b.name.trim().length > 64) return "Names can be at most 64 characters.";
    // An empty name clears the user's label and falls back to the device's own name.
    cur.name = b.name.trim() || null;
  }
  if (b.group !== undefined) {
    if (b.group !== null && typeof b.group !== "string") return "A group must be text or null.";
    cur.group = typeof b.group === "string" && b.group.trim() ? b.group.trim() : null;
  }
  labels.set(id, cur);
  return device(s, new Date());
}
