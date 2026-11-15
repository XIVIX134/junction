import type { Bundle, Device, ExitHealth, NameSource, Overview } from "./api/types";
import { dateTime, ms, plural, shortDate } from "./format";
import { exitColorVar } from "./site";

/** Filter value for devices without a group. */
export const NO_GROUP = "_none";

export const cssVars = (v: Record<string, string>) => v as import("react").CSSProperties;

/** "vpn1-gaming" → "vpn1"; "direct" → "direct". */
export function endpointOf(exit: string | null | undefined): string | null {
  if (!exit) return null;
  return exit.split("-")[0];
}
/** "vpn1-gaming" → "gaming"; "direct" → null. */
export function profileOf(exit: string | null | undefined): string | null {
  if (!exit || !exit.includes("-")) return null;
  return exit.slice(exit.indexOf("-") + 1);
}
export const isTunnel = (exit: string | null | undefined) => !!exit && endpointOf(exit) !== "direct";

/** Colour token for an endpoint key. All actual colours live in theme.css. */
export function endpointColor(key: string | null | undefined): string {
  if (!key) return "var(--other)";
  return key === "direct" ? "var(--direct)" : exitColorVar(key);
}

export function endpointName(key: string | null | undefined, exits: ExitHealth[] = []): string {
  if (!key) return "—";
  if (key === "direct") return "Direct";
  return exits.find((e) => e.key === key)?.name ?? key;
}

const KNOWN_GROUPS: Record<string, string> = {
  gaming: "var(--g-gaming)",
  servers: "var(--g-servers)",
  personal: "var(--g-personal)",
};
const EXTRA = ["var(--g-extra-1)", "var(--g-extra-2)", "var(--g-extra-3)", "var(--g-extra-4)"];

/** Colour follows the group name (stable), never its position in a list. */
export function groupColor(group: string | null | undefined): string {
  if (!group) return "var(--g-none)";
  const k = group.trim().toLowerCase();
  if (KNOWN_GROUPS[k]) return KNOWN_GROUPS[k];
  let h = 0;
  for (const c of k) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return EXTRA[h % EXTRA.length];
}

export type GroupInfo = { name: string; devices: Device[] };

/** Groups derived from device.group, in a stable order (known ones first, then alphabetical). */
export function groupsOf(devices: Device[]): GroupInfo[] {
  const map = new Map<string, Device[]>();
  for (const d of devices) {
    if (!d.group) continue;
    const list = map.get(d.group) ?? [];
    list.push(d);
    map.set(d.group, list);
  }
  const order = ["gaming", "servers", "personal"];
  return [...map.entries()]
    .map(([name, devices]) => ({ name, devices }))
    .sort((a, b) => {
      const ia = order.indexOf(a.name.toLowerCase());
      const ib = order.indexOf(b.name.toLowerCase());
      if (ia !== ib) return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
      return a.name.localeCompare(b.name);
    });
}

export function deviceTotalToday(d: Device) {
  return d.todayDown + d.todayUp;
}

/** Turn a raw probe error into a plain sentence; keep the raw text available separately. */
export function plainError(err: string | null | undefined): string | null {
  if (!err) return null;
  const e = err.toLowerCase();
  if (e.includes("timeout") || e.includes("deadline exceeded") || e.includes("timed out"))
    return "The server stopped answering (timed out).";
  if (e.includes("connection refused")) return "The server refused the connection.";
  if (e.includes("no route to host") || e.includes("network is unreachable")) return "There is no network path to the server.";
  if (e.includes("no such host") || e.includes("lookup")) return "The server's name could not be looked up.";
  if (e.includes("certificate") || e.includes("tls") || e.includes("x509")) return "The secure handshake with the server failed.";
  if (e.includes("auth")) return "The server rejected Junction's credentials.";
  return err + (/[.!?]$/.test(err) ? "" : ".");
}

/** When an exit went bad, as precisely as the API lets us say. */
export function downSince(e: ExitHealth): string | null {
  // The honest anchor is the last time it worked. lastFailure is either still moving (probed)
  // or frozen at the moment checks were switched off (not probed) - neither is when it went down.
  return e.lastSuccess;
}

export function exitStatusText(e: ExitHealth): string {
  return e.healthy ? `healthy · ${ms(e.latencyMs)}` : "unreachable";
}

/** Devices currently carried by an endpoint: online, through the gateway, last exit on that endpoint. */
export function carriedBy(devices: Device[], endpoint: string) {
  return devices.filter((d) => d.online && d.usesGateway && endpointOf(d.exit) === endpoint);
}

export type Headline = { title: string; sub: string; wash: string };

export function controlHeadline(o: Overview): Headline {
  const now = new Date(o.now);
  const sel = o.selector.selected;
  const ep = endpointOf(sel) ?? "direct";
  const exit = o.exits.find((e) => e.key === ep);
  const notes: string[] = [];
  for (const e of o.exits) {
    if (e.healthy || e.key === ep) continue;
    const since = downSince(e);
    notes.push(
      since
        ? `${e.name} has been unreachable since ${dateTime(since, now, true)}.`
        : `${e.name} is unreachable.`,
    );
  }
  if (ep === "direct") {
    const n = o.devices.filter((d) => d.online && d.usesGateway).length;
    return {
      title: "Everything is going direct",
      sub: [`No tunnel in use; ${plural(n, "device")} straight out through the ISP, ${ms(o.direct.latencyMs)}.`, ...notes].join(" "),
      wash: "var(--direct)",
    };
  }
  const n = carriedBy(o.devices, ep).length;
  const name = exit?.name ?? ep;
  if (exit && !exit.healthy) {
    return {
      title: `${name} is down`,
      sub: [`It is still selected (${sel}). ${plainError(exit.lastError) ?? ""}`.trim(), ...notes].join(" "),
      wash: "var(--crit)",
    };
  }
  const profile = profileOf(sel);
  const first = [profile ? `${profile} profile` : sel, exit ? ms(exit.latencyMs) : null].filter(Boolean).join(", ") + ".";
  return {
    title: n === 0 ? `${name} is carrying no devices right now` : `${name} is carrying ${plural(n, "device")}`,
    sub: [first, ...notes].join(" "),
    wash: endpointColor(ep),
  };
}

export function deviceRouteText(d: Device): string {
  if (d.paused) return "Paused";
  if (!d.usesGateway) return "Bypasses the gateway";
  if (typeof d.vpn === "boolean") return d.vpn ? (d.exit && isTunnel(d.exit) ? `VPN · ${d.exit}` : "VPN on") : "VPN off · direct";
  if (!d.exit) return "No traffic yet";
  return isTunnel(d.exit) ? `VPN · ${d.exit}` : "Direct";
}

// ---------- names ----------

const SOURCE_TEXT: Record<NameSource, string> = {
  user: "You named it",
  router: "Named by your router",
  dhcp: "Named by the device",
  mdns: "Named by the device (announced on the network)",
  upnp: "Named by the device (UPnP)",
  vendor: "Guessed from its maker",
  ip: "No name yet: just its address",
};
export function nameSourceText(src: NameSource | string | null | undefined): string | null {
  if (!src) return null;
  return SOURCE_TEXT[src as NameSource] ?? `Named via ${src}`;
}

/** The device announces a name of its own that differs from the one the user gave it. */
export function hasOwnName(d: Device): boolean {
  return d.nameSource === "user" && !!d.autoName && d.autoName.trim().toLowerCase() !== d.name.trim().toLowerCase();
}

// ---------- bundle ----------

export const BUNDLE_NOTE =
  "Junction only counts traffic that goes through the gateway, so phones that bypass it aren't included. " +
  "Your ISP's number is the truth: when you check it, log it here and the count restarts from that number.";

export type BundleOutlook = { text: string; warn: boolean };

/** What happens next: runs out before it expires, expires first, or already used up. */
export function bundleOutlook(b: Bundle): BundleOutlook {
  if (b.left <= 0) return { text: "used up", warn: true };
  const out = b.runsOut ? new Date(b.runsOut).getTime() : null;
  const exp = b.expiresAt ? new Date(b.expiresAt).getTime() : null;
  if (out != null && (exp == null || out < exp)) return { text: `out ~${shortDate(b.runsOut)}`, warn: exp != null };
  if (exp != null) return { text: `expires ${shortDate(b.expiresAt)}`, warn: false };
  return { text: b.pacePerDay > 0 ? "" : "pace after 6 h of data", warn: false };
}

/** Bar colour token: calm while fine, warning when on pace to run out before expiry, critical when low. */
export function bundleBarColor(b: Bundle): string {
  if (b.left <= 0 || bundleLeftShare(b) < 0.15) return "var(--bundle-low)";
  return bundleOutlook(b).warn ? "var(--bundle-warn)" : "var(--bundle-left)";
}

/** Share of the anchor amount still left, 0..1. */
export function bundleLeftShare(b: Bundle): number {
  if (b.anchor.amount <= 0) return 0;
  return Math.max(0, Math.min(1, b.left / b.anchor.amount));
}
