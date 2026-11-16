import type { Device, Overview } from "./api/types";
import { capText, dayKey, dmy, exitName, hm } from "./fmt";

// How the console talks about a client (wording from concepts5).

/** The upstream router (.1) is listed by the API but is not a client. */
export const isRouter = (d: Device) => /\.1$/.test(d.ip);
export const eff = (d: Device) => d.effective;
export const effVpn = (d: Device) => (d.effective ? d.effective.vpn : !!d.vpn);
export const onVpn = (d: Device) => !!d.controllable && effVpn(d);
export const isPaused = (d: Device) => !!(d.effective?.paused ?? d.paused);
export const capOf = (d: Device) => d.effective?.speedLimitMbps ?? d.speedLimitMbps ?? null;
export const limited = (d: Device) => capOf(d) != null || isPaused(d);
export const totalToday = (d: Device) => d.todayDown + d.todayUp;

export function route(d: Device): { t: string; c: string } {
  if (isRouter(d)) return { t: "Upstream router", c: "t3" };
  if (!d.controllable) return { t: "Bypasses gateway", c: "t3" };
  if (effVpn(d)) return { t: `VPN · ${exitName(d.effective?.exit ?? d.exit)}`, c: "acc" };
  return { t: "Direct", c: "" };
}

export function status(d: Device, rules: { id: number; name: string; then: { pause?: boolean } }[] = [], nowIso?: string): { t: string; c: string } {
  const e = d.effective;
  if (e?.paused && e.pausedBy === "rule") {
    const r = e.rules.find((x) => rules.some((y) => y.id === x.id && y.then.pause)) ?? e.rules[0];
    return { t: `Paused by ${r?.name ?? "a rule"}`, c: "warn" };
  }
  if (isPaused(d)) return { t: d.pausedUntil ? `Paused until ${hm(d.pausedUntil)}` : "Paused", c: "warn" };
  const cap = capOf(d);
  if (cap != null) return { t: `Capped ${capText(cap)}`, c: "warn" };
  if (d.online) return { t: "Online", c: "ok" };
  const ls = Date.parse(d.lastSeen);
  return {
    t: ls > 0 ? `Offline · ${nowIso && dayKey(d.lastSeen) === dayKey(nowIso) ? hm(d.lastSeen) : `${dmy(d.lastSeen)} ${hm(d.lastSeen)}`}` : "Offline",
    c: "",
  };
}

export { gatewayIP, gatewayShort } from "./site";

/** Endpoint (exit) the selector is using right now, or null for direct. */
export function selectedEndpoint(o: Overview) {
  const s = o.selector.selected;
  return s === "direct" ? null : o.exits.find((e) => s.startsWith(e.key)) ?? null;
}

export const defaultProfile = (o: Overview) => o.selector.control?.profile ?? (o.selector.selected.split("-")[1] || "default");

export const NAME_SOURCE: Record<string, string> = {
  user: "you named it",
  dhcp: "named by the device (DHCP)",
  router: "named by your router",
  upnp: "named by the device (UPnP)",
  mdns: "named by the device (mDNS)",
  vendor: "from the MAC vendor",
  ip: "no name",
};
