// Console formatting (matches concepts5): bytes, kbit/s rates, clock times in the gateway's time zone.
import { TZ } from "./format";
import { exitDisplayName } from "./site";

export const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
export const DAYN = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
export const DAY3 = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

const f = new Intl.DateTimeFormat("en-CA", {
  timeZone: TZ,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hourCycle: "h23",
  weekday: "short",
});
type P = { y: number; mo: number; d: number; h: string; mi: string; s: string; dow: number; key: string };
const cache = new Map<number, P>();
/** Wall-clock parts (gateway time zone) of an instant. */
export function cz(v: string | number | Date): P {
  const t = typeof v === "number" ? v : typeof v === "string" ? Date.parse(v) : v.getTime();
  const hit = cache.get(t);
  if (hit) return hit;
  const o: Record<string, string> = {};
  for (const x of f.formatToParts(new Date(t))) o[x.type] = x.value;
  const p: P = {
    y: +o.year,
    mo: +o.month,
    d: +o.day,
    h: o.hour,
    mi: o.minute,
    s: o.second,
    dow: DAY3.indexOf(o.weekday),
    key: `${o.year}-${o.month}-${o.day}`,
  };
  if (cache.size > 5000) cache.clear();
  cache.set(t, p);
  return p;
}
const ok = (v: string | number | Date | null | undefined) => v != null && !(typeof v === "string" && isNaN(Date.parse(v)));

/** "02:16" */
export const hm = (v: string | number | Date | null | undefined) => (ok(v) ? `${cz(v!).h}:${cz(v!).mi}` : "—");
/** "02:16:55" */
export const hms = (v: string | number | Date | null | undefined) => (ok(v) ? `${cz(v!).h}:${cz(v!).mi}:${cz(v!).s}` : "—");
/** "14 Nov" */
export const dmy = (v: string | number | Date | null | undefined) => (ok(v) ? `${cz(v!).d} ${MON[cz(v!).mo - 1]}` : "—");
/** Calendar day key (gateway time zone) "2026-11-14". */
export const dayKey = (v: string | number | Date) => cz(v).key;
/** "14 Nov 02:16", or just "02:16" when it is today. */
export function whenShort(v: string | null | undefined, now: string | number | Date = Date.now()) {
  if (!ok(v)) return "—";
  return dayKey(v!) === dayKey(now) ? hm(v) : `${dmy(v)} ${hm(v)}`;
}

/** 1.81 GB, 204.6 GB, 553 MB, 4.6 MB, 12 KB */
export function bytes(b: number | null | undefined, dec?: number): string {
  if (b == null || !isFinite(b)) return "—";
  const a = Math.abs(b);
  const sign = b < 0 ? "−" : "";
  if (a < 1e3) return `${sign}${Math.round(a)} B`;
  if (a < 1e6) return `${sign}${(a / 1e3).toFixed(0)} KB`;
  if (a < 1e9) return `${sign}${(a / 1e6).toFixed(a < 1e7 ? 1 : 0)} MB`;
  return `${sign}${(a / 1e9).toFixed(dec ?? (a < 1e10 ? 2 : 1))} GB`;
}
/** bytes/s → "9.5 kbit/s", "3.0 Mbit/s" */
export function rate(Bps: number | null | undefined): string {
  if (Bps == null || !isFinite(Bps)) return "—";
  const b = Bps * 8;
  if (b < 1) return "0 kbit/s";
  if (b < 1e6) return `${(b / 1e3).toFixed(b < 1e4 ? 1 : 0)} kbit/s`;
  return `${(b / 1e6).toFixed(1)} Mbit/s`;
}
export const gb = (b: number, d = 1) => `${(b / 1e9).toFixed(d)} GB`;

/** "vpn1-gaming" → "VPN 1 gaming" (the exit's display name); "direct" → "Direct" */
export const exitName = (t: string | null | undefined) =>
  !t ? "—" : t === "direct" ? "Direct" : t.replace(/^[^-]+/, (k) => exitDisplayName(k)).replace("-", " ");

export const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
