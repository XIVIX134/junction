// Console formatting (matches concepts5): bytes, kbit/s rates, clock times in the gateway's time zone.
import { TZ } from "./format";
import { clock12, exitDisplayName, speedInBytes } from "./site";

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

const ampm = (h: number) => (h < 12 ? "AM" : "PM");
const h12 = (h: number) => h % 12 || 12;
/** "02:16", or "2:16 AM" with the 12-hour clock (Settings). */
export const hm = (v: string | number | Date | null | undefined) => {
  if (!ok(v)) return "—";
  const p = cz(v!);
  return clock12() ? `${h12(+p.h)}:${p.mi} ${ampm(+p.h)}` : `${p.h}:${p.mi}`;
};
/** "02:16:55", or "2:16:55 AM" */
export const hms = (v: string | number | Date | null | undefined) => {
  if (!ok(v)) return "—";
  const p = cz(v!);
  return clock12() ? `${h12(+p.h)}:${p.mi}:${p.s} ${ampm(+p.h)}` : `${p.h}:${p.mi}:${p.s}`;
};
/** A wall-clock "23:00" (schedules) → "23:00", or "11 PM" / "11:30 PM". */
export function clockText(hhmm: string): string {
  const m = /^(\d{1,2}):(\d{2})$/.exec(hhmm);
  if (!m || !clock12()) return hhmm;
  const h = +m[1];
  return `${h12(h)}${m[2] === "00" ? "" : `:${m[2]}`} ${ampm(h)}`;
}
/** Hour of day 0–23 → "14:00", or "2 PM". */
export const hourLabel = (h: number) => (clock12() ? `${h12(h % 24)} ${ampm(h % 24)}` : `${String(h % 24).padStart(2, "0")}:00`);
/** Short axis tick for an hour: "14", or "2p". */
export const hourTick = (h: number) => (clock12() ? `${h12(h)}${h < 12 ? "a" : "p"}` : String(h).padStart(2, "0"));
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
/** bytes/s → "9.5 kbit/s", "3.0 Mbit/s"; or "1.2 KB/s", "0.4 MB/s" with speeds in MB/s (Settings). */
export function rate(Bps: number | null | undefined): string {
  if (Bps == null || !isFinite(Bps)) return "—";
  if (speedInBytes()) {
    if (Bps < 1) return "0 KB/s";
    if (Bps < 1e6) return `${(Bps / 1e3).toFixed(Bps < 1e4 ? 1 : 0)} KB/s`;
    return `${(Bps / 1e6).toFixed(1)} MB/s`;
  }
  const b = Bps * 8;
  if (b < 1) return "0 kbit/s";
  if (b < 1e6) return `${(b / 1e3).toFixed(b < 1e4 ? 1 : 0)} kbit/s`;
  return `${(b / 1e6).toFixed(1)} Mbit/s`;
}
/** The speed unit every screen uses: "Mbit/s" or "MB/s" (Settings → General). */
export const speedUnit = () => (speedInBytes() ? "MB/s" : "Mbit/s");
/** bytes/s → a number in that unit, for chart axes. */
export const speedValue = (Bps: number) => (speedInBytes() ? Bps / 1e6 : (Bps * 8) / 1e6);
const trimNum = (n: number) =>
  n === 0 ? "0" : n >= 10 ? n.toFixed(0) : n >= 1 ? n.toFixed(1).replace(/\.0$/, "") : n.toFixed(2).replace(/\.?0+$/, "");
/** An axis value already in that unit → "12 MB/s". */
export const speedAxis = (v: number) => `${trimNum(v)} ${speedUnit()}`;

// Speed caps are stored in Mbit/s; these show and read them in the chosen unit.
/** 2 → "2 Mbit/s" or "0.25 MB/s" */
export const capText = (mbps: number) => (speedInBytes() ? `${capInput(mbps)} MB/s` : `${mbps} Mbit/s`);
/** 2 → "2" or "0.25": the value for a cap input box */
export const capInput = (mbps: number) => (speedInBytes() ? String(+(mbps / 8).toFixed(3)) : String(mbps));
/** What was typed in a cap box → Mbit/s, or null when out of range (0.5–1000 Mbit/s, 0.1–125 MB/s). */
export function capFromInput(v: string | number): number | null {
  const n = Number(v);
  if (!isFinite(n)) return null;
  if (speedInBytes()) return n >= 0.1 && n <= 125 ? +(n * 8).toFixed(3) : null;
  return n >= 0.5 && n <= 1000 ? n : null;
}
export const capRange = () => (speedInBytes() ? "Use 0.1 to 125 MB/s." : "Use 0.5 to 1000 Mbit/s.");

export const gb = (b: number, d = 1) => `${(b / 1e9).toFixed(d)} GB`;

/** "vpn1-gaming" → "VPN 1 gaming" (the exit's display name); "direct" → "Direct" */
export const exitName = (t: string | null | undefined) =>
  !t ? "—" : t === "direct" ? "Direct" : t.replace(/^[^-]+/, (k) => exitDisplayName(k)).replace("-", " ");

export const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
