import type { Device, Effective, Profile, Rule, RuleDraft, RuleMatch, RuleSchedule, RuleThen, RuleWhen } from "./api/types";
import { fromLocalInput, TZ } from "./format";

// Plain-language helpers for rules: summaries, day ranges, when a rule's window ends.

export const DAY_SHORT = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
export const DAY_LETTER = ["S", "M", "T", "W", "T", "F", "S"];

/** [0,1,2,3,4] → "Sun–Thu"; [1,3,5] → "Mon, Wed, Fri"; all → "Every day". */
export function daysText(days: number[]): string {
  const ds = [...new Set(days)].filter((d) => d >= 0 && d <= 6).sort((a, b) => a - b);
  if (ds.length === 7) return "Every day";
  if (ds.length === 0) return "No days";
  if (ds.join() === "1,2,3,4,5") return "Weekdays";
  if (ds.join() === "0,6") return "Weekends";
  const runs: number[][] = [];
  for (const d of ds) {
    const last = runs[runs.length - 1];
    if (last && d === last[last.length - 1] + 1) last.push(d);
    else runs.push([d]);
  }
  return runs
    .map((r) => (r.length >= 3 ? `${DAY_SHORT[r[0]]}–${DAY_SHORT[r[r.length - 1]]}` : r.map((d) => DAY_SHORT[d]).join(", ")))
    .join(", ");
}

export function scheduleText(s: RuleSchedule): string {
  return `${daysText(s.days)} ${s.start}–${s.end}`;
}

const PERIOD: Record<string, string> = { day: "today", week: "this week", month: "this month" };
export function usageText(u: NonNullable<RuleWhen["usage"]>): string {
  return `Usage ${PERIOD[u.period] ?? u.period} over ${u.overGB} GB`;
}

export function whoText(m: RuleMatch, devices: Device[] = []): string {
  if (m.all) return "Everyone";
  const parts: string[] = [];
  if (m.groups?.length) parts.push(m.groups.join(", "));
  if (m.devices?.length) {
    const names = m.devices.map((id) => devices.find((d) => d.id === id)?.name ?? id);
    parts.push(names.length > 2 ? `${names.slice(0, 2).join(", ")} +${names.length - 2}` : names.join(", "));
  }
  if (m.newDevices) parts.push("New devices");
  return parts.length ? parts.join(" + ") : "Nobody yet";
}

export function whenText(w: RuleWhen): string | null {
  const parts = [w.schedule ? scheduleText(w.schedule) : null, w.usage ? usageText(w.usage) : null].filter(Boolean);
  return parts.length ? parts.join(", ") : null;
}

export function thenText(t: RuleThen): string {
  const parts: string[] = [];
  if (t.pause) parts.push("Pause");
  if (t.speedMbps != null) parts.push(`Slow to ${t.speedMbps} Mbit/s`);
  if (t.vpn != null) parts.push(t.vpn ? "VPN on" : "VPN off");
  if (t.profile) parts.push(`${t.profile} profile`);
  return parts.length ? parts.join(", ") : "Nothing yet";
}

/** "Kids · Sun–Thu 00:00–07:00 → Pause". */
export function ruleSummary(r: Pick<Rule, "match" | "when" | "then">, devices: Device[] = []): string {
  const when = whenText(r.when);
  return `${whoText(r.match, devices)}${when ? ` · ${when}` : ""} → ${thenText(r.then)}`;
}

export const hasAction = (t: RuleThen) => !!t.pause || t.speedMbps != null || t.vpn != null || !!t.profile;

// ---------- gateway wall clock ----------

const fmt = new Intl.DateTimeFormat("en-US", {
  timeZone: TZ,
  weekday: "short",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
});
export function gatewayClock(d: Date) {
  const p: Record<string, string> = {};
  for (const x of fmt.formatToParts(d)) p[x.type] = x.value;
  return { dow: DAY_SHORT.indexOf(p.weekday), ymd: `${p.year}-${p.month}-${p.day}`, minutes: +p.hour * 60 + +p.minute };
}
const toMin = (hhmm: string) => {
  const [h, m] = hhmm.split(":").map(Number);
  return h * 60 + (m || 0);
};
function addDaysYmd(ymd: string, n: number) {
  const [y, m, d] = ymd.split("-").map(Number);
  const t = new Date(Date.UTC(y, m - 1, d + n, 12));
  return t.toISOString().slice(0, 10);
}

/** End of a schedule's window that contains `now`, or null if the window isn't active. */
export function scheduleWindowEnd(s: RuleSchedule, now: Date): Date | null {
  const c = gatewayClock(now);
  const start = toMin(s.start);
  const end = toMin(s.end);
  const prev = (c.dow + 6) % 7;
  if (end > start) {
    if (s.days.includes(c.dow) && c.minutes >= start && c.minutes < end) return new Date(fromLocalInput(`${c.ymd}T${s.end}`)!);
    return null;
  }
  // overnight window (or 24 h when start == end)
  if (s.days.includes(c.dow) && c.minutes >= start) return new Date(fromLocalInput(`${addDaysYmd(c.ymd, 1)}T${s.end}`)!);
  if (s.days.includes(prev) && c.minutes < end) return new Date(fromLocalInput(`${c.ymd}T${s.end}`)!);
  return null;
}

/** When a rule stops applying on its own: end of its schedule window, else end of its usage period. */
export function ruleWindowEnd(r: Rule, now: Date): Date | null {
  if (r.when.schedule) return scheduleWindowEnd(r.when.schedule, now);
  if (r.when.usage) {
    const c = gatewayClock(now);
    if (r.when.usage.period === "day") return new Date(fromLocalInput(`${addDaysYmd(c.ymd, 1)}T00:00`)!);
    if (r.when.usage.period === "week") return new Date(fromLocalInput(`${addDaysYmd(c.ymd, 7 - c.dow)}T00:00`)!);
    const [y, m] = c.ymd.split("-").map(Number);
    const next = m === 12 ? `${y + 1}-01-01` : `${y}-${String(m + 1).padStart(2, "0")}-01`;
    return new Date(fromLocalInput(`${next}T00:00`)!);
  }
  return null;
}

/** Minutes from now until the rule's window ends, clamped to the exemption range 1..1440. */
export function minutesUntilWindowEnd(r: Rule, now: Date): number {
  const end = ruleWindowEnd(r, now);
  const m = end ? Math.ceil((end.getTime() - now.getTime()) / 60000) : 60;
  return Math.max(1, Math.min(1440, m));
}

// ---------- a device's own rules (made by the device page's simple controls) ----------

const onlyDevice = (r: Rule, id: string) =>
  !r.match.all && !r.match.newDevices && !(r.match.groups?.length) && r.match.devices?.length === 1 && r.match.devices[0] === id;

export const dailyLimitRule = (rules: Rule[], id: string) =>
  rules.find((r) => onlyDevice(r, id) && r.when.usage?.period === "day" && !r.when.schedule && (r.then.pause || r.then.speedMbps != null));

export const scheduleRule = (rules: Rule[], id: string) =>
  rules.find((r) => onlyDevice(r, id) && !!r.when.schedule && !r.when.usage && !!r.then.pause);

/** The rule currently pausing a device, if a rule (not a manual pause) is doing it. */
export function pausingRule(eff: Effective | undefined, rules: Rule[]): Rule | null {
  if (!eff || !eff.paused || eff.pausedBy !== "rule") return null;
  for (const a of eff.rules) {
    const r = rules.find((x) => x.id === a.id);
    if (r?.then.pause) return r;
  }
  return null;
}

export function blankRule(): RuleDraft {
  return { name: "", enabled: true, match: { all: true }, when: {}, then: { pause: true } };
}

export const profileLabel = (p: Profile | null | undefined, fallback: string) => (p ? p : `Follow default (${fallback})`);
