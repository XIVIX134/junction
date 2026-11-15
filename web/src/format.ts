// Formatting: bytes in decimal units, rates in Mb/s, all dates in the gateway's local time.

import { SITE_TZ } from "./site";

/** The gateway's time zone (Settings → General). */
export const TZ = SITE_TZ;

function trim(n: number, digits: number) {
  return n.toFixed(digits).replace(/\.0+$/, "");
}

/** 1.9 GB, 340 MB, 4.2 MB, 12 KB. */
export function bytes(n: number | null | undefined): string {
  if (n == null || !isFinite(n)) return "—";
  const a = Math.abs(n);
  if (a >= 1e12) return `${trim(n / 1e12, a >= 1e14 ? 0 : 1)} TB`;
  if (a >= 1e9) return `${trim(n / 1e9, a >= 1e11 ? 0 : 1)} GB`;
  if (a >= 1e6) return `${trim(n / 1e6, a >= 1e7 ? 0 : 1)} MB`;
  if (a >= 1e3) return `${Math.round(n / 1e3)} KB`;
  if (a === 0) return "0 MB";
  return `${Math.round(n)} B`;
}

/** Whole or one-decimal GB without the unit, for "141 of 250 GB". */
export function gbNumber(n: number): string {
  const g = n / 1e9;
  return trim(g, g >= 100 ? 0 : 1);
}

/** bytes/s → "18.4" (Mb/s, one decimal). */
export function mbps(bps: number | null | undefined): string {
  if (bps == null || !isFinite(bps)) return "—";
  const v = (bps * 8) / 1e6;
  if (v === 0) return "0";
  return v.toFixed(1);
}
/** bytes/s → [number, unit]: Mb/s from 1 Mb/s up, kb/s below so idle-ish traffic isn't shown as 0. */
export function rateParts(bps: number | null | undefined): [string, string] {
  if (bps == null || !isFinite(bps)) return ["—", "Mb/s"];
  const v = (bps * 8) / 1e6;
  if (v >= 1 || v === 0) return [mbps(bps), "Mb/s"];
  return [String(Math.max(1, Math.round((bps * 8) / 1e3))), "kb/s"];
}
export const rate = (bps: number | null | undefined) => rateParts(bps).join(" ");

export function ms(n: number | null | undefined): string {
  return n == null ? "—" : `${Math.round(n)} ms`;
}

const fmtParts = new Intl.DateTimeFormat("en-US", {
  timeZone: TZ,
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});
const fmtMonthLong = new Intl.DateTimeFormat("en-US", { timeZone: TZ, month: "long" });
const fmtWeekday = new Intl.DateTimeFormat("en-US", { timeZone: TZ, weekday: "short" });

type Parts = { year: string; month: string; day: string; hour: string; minute: string };
function parts(d: Date): Parts {
  const out: Record<string, string> = {};
  for (const p of fmtParts.formatToParts(d)) out[p.type] = p.value;
  return out as Parts;
}

export function parseDate(s: string | null | undefined): Date | null {
  if (!s) return null;
  const d = new Date(s);
  return isNaN(d.getTime()) ? null : d;
}

/** "Nov 12, 23:22" (or "23:22" when it's today in the gateway's time zone). */
export function dateTime(s: string | null | undefined, now: Date = new Date(), alwaysDate = false): string {
  const d = parseDate(s);
  if (!d) return "—";
  const p = parts(d);
  const n = parts(now);
  const sameDay = p.year === n.year && p.month === n.month && p.day === n.day;
  if (sameDay && !alwaysDate) return `${p.hour}:${p.minute}`;
  return `${p.month} ${p.day}, ${p.hour}:${p.minute}`;
}

/** "Nov 23". */
export function shortDate(s: string | Date | null | undefined): string {
  const d = s instanceof Date ? s : parseDayOrDate(s);
  if (!d) return "—";
  const p = parts(d);
  return `${p.month} ${p.day}`;
}

export function monthName(s: string | null | undefined): string {
  const d = parseDate(s);
  return d ? fmtMonthLong.format(d) : "";
}

export function weekday(s: string | Date): string {
  const d = s instanceof Date ? s : parseDayOrDate(s);
  return d ? fmtWeekday.format(d) : "";
}

/** Gateway-time hour "00".."23" for an instant. */
export function gatewayHour(d: Date): number {
  return Number(parts(d).hour);
}

/**
 * API "day" values: accept a plain local date ("2026-11-13", taken as that calendar day in the gateway's time zone)
 * or a full RFC 3339 instant.
 */
export function parseDayOrDate(s: string | null | undefined): Date | null {
  if (!s) return null;
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  // Noon UTC on that date lands on the same calendar day in the gateway's time zone.
  if (m) return new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], 12));
  return parseDate(s);
}

/**
 * API "hour" values: accept "13", "13:00", or an RFC 3339 instant (formatted in gateway time).
 * Returns the hour of day 0..23, or null.
 */
export function hourOf(s: string): number | null {
  const m = /^(\d{1,2})(?::00)?$/.exec(s);
  if (m) return Number(m[1]);
  const d = parseDate(s);
  return d ? gatewayHour(d) : null;
}

/** "just now", "4 min ago", "3 h ago", "2 days ago". */
export function ago(s: string | null | undefined, now: Date = new Date()): string {
  const d = parseDate(s);
  if (!d || d.getUTCFullYear() < 2000) return "never";
  const sec = Math.max(0, (now.getTime() - d.getTime()) / 1000);
  if (sec < 60) return "just now";
  const min = Math.round(sec / 60);
  if (min < 60) return `${min} min ago`;
  const h = Math.round(min / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.round(h / 24)} days ago`;
}

export function plural(n: number, one: string, many = `${one}s`) {
  return `${n} ${n === 1 ? one : many}`;
}

// ---------- form helpers: gateway wall-clock time <-> RFC 3339 ----------

const fmtNumeric = new Intl.DateTimeFormat("en-CA", {
  timeZone: TZ,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});
function zonedNumeric(d: Date) {
  const p: Record<string, string> = {};
  for (const x of fmtNumeric.formatToParts(d)) p[x.type] = x.value;
  return { y: p.year, m: p.month, d: p.day, h: p.hour, min: p.minute };
}

/** An instant as a datetime-local value in gateway time: "2026-11-13T20:45". */
export function toLocalInput(d: Date = new Date()): string {
  const c = zonedNumeric(d);
  return `${c.y}-${c.m}-${c.d}T${c.h}:${c.min}`;
}

/** A gateway wall-clock "YYYY-MM-DDTHH:MM" (or "YYYY-MM-DD" + time) as an RFC 3339 UTC string. */
export function fromLocalInput(local: string, time = "00:00:00"): string | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2})(?::(\d{2}))?)?$/.exec(local);
  if (!m) return null;
  const [hh, mm, ss] = m[4] ? [m[4], m[5], m[6] ?? "00"] : time.split(":");
  const asUTC = Date.UTC(+m[1], +m[2] - 1, +m[3], +hh, +mm, +ss);
  // The time zone's offset at that moment (DST-aware): compare the wall clock Intl reports for the guess.
  const guess = new Date(asUTC);
  const c = zonedNumeric(guess);
  const wall = Date.UTC(+c.y, +c.m - 1, +c.d, +c.h, +c.min);
  const offset = wall - Math.floor(asUTC / 60000) * 60000;
  return new Date(asUTC - offset).toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** "4.2 GB/day". */
export const perDay = (n: number) => `${bytes(n)}/day`;

/** "in 4m", "in 40s", "any moment now" until an instant. */
export function untilShort(s: string | null | undefined, now: Date = new Date()): string | null {
  const d = parseDate(s);
  if (!d) return null;
  const sec = Math.round((d.getTime() - now.getTime()) / 1000);
  if (sec <= 5) return "any moment now";
  if (sec < 60) return `in ${sec}s`;
  return `in ${Math.round(sec / 60)}m`;
}
