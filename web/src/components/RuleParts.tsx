import { useState } from "react";
import { api, ApiError } from "../api/client";
import type { Device, Profile, Rule, RuleDraft, RuleMatch, RuleSchedule, RuleThen, RuleUsage } from "../api/types";
import { groupsOf } from "../model";
import { useProfiles } from "../prefs";
import { DAY_LETTER, DAY_SHORT, hasAction, ruleSummary } from "../rules";

// Small, restylable editors for the parts of a rule. Used by the rule form, the node editor's side panel,
// and (schedule) the device page.

export function WhoEditor({ m, devices, onChange }: { m: RuleMatch; devices: Device[]; onChange: (m: RuleMatch) => void }) {
  const groups = groupsOf(devices).map((g) => g.name);
  // Which picker is open follows which list the rule has (an empty list still counts).
  const kind = m.all ? "all" : m.devices !== undefined ? "devices" : m.groups !== undefined ? "groups" : m.newDevices ? "new" : "groups";
  const toggle = (list: string[] | undefined, v: string) => {
    const s = new Set(list ?? []);
    if (s.has(v)) s.delete(v);
    else s.add(v);
    return [...s];
  };
  return (
    <fieldset className="part">
      <legend>Who</legend>
      <div className="seg wrap" role="radiogroup" aria-label="Who">
        {(
          [
            ["all", "Everyone"],
            ["groups", "Groups"],
            ["devices", "Devices"],
            ["new", "New devices"],
          ] as const
        ).map(([k, label]) => (
          <button
            key={k}
            type="button"
            role="radio"
            aria-checked={kind === k}
            aria-pressed={kind === k}
            onClick={() =>
              onChange(
                k === "all"
                  ? { all: true }
                  : k === "new"
                    ? { newDevices: true }
                    : k === "groups"
                      ? { groups: m.groups ?? [] }
                      : { devices: m.devices ?? [] },
              )
            }
          >
            {label}
          </button>
        ))}
      </div>
      {kind === "groups" && (
        <div className="checks">
          {groups.length === 0 && <p className="note">No groups yet. Give devices a group on the Devices page.</p>}
          {groups.map((g) => (
            <label key={g} className="check">
              <input type="checkbox" checked={!!m.groups?.includes(g)} onChange={() => onChange({ ...m, groups: toggle(m.groups, g) })} />
              {g}
            </label>
          ))}
        </div>
      )}
      {kind === "devices" && (
        <div className="checks scroll">
          {[...devices]
            .sort((a, b) => a.name.localeCompare(b.name))
            .map((d) => (
              <label key={d.id} className="check">
                <input type="checkbox" checked={!!m.devices?.includes(d.id)} onChange={() => onChange({ ...m, devices: toggle(m.devices, d.id) })} />
                {d.name}
              </label>
            ))}
        </div>
      )}
      {(kind === "groups" || kind === "devices") && (
        <label className="check">
          <input type="checkbox" checked={!!m.newDevices} onChange={(e) => onChange({ ...m, newDevices: e.target.checked || undefined })} />
          Also new devices
        </label>
      )}
      {kind === "new" && <p className="note">Devices first seen in the last 24 h that nobody has named.</p>}
    </fieldset>
  );
}

export function DaysPicker({ days, onChange }: { days: number[]; onChange: (d: number[]) => void }) {
  return (
    <div className="days" role="group" aria-label="Days">
      {DAY_LETTER.map((l, i) => (
        <button
          key={i}
          type="button"
          aria-pressed={days.includes(i)}
          aria-label={DAY_SHORT[i]}
          title={DAY_SHORT[i]}
          onClick={() => onChange(days.includes(i) ? days.filter((x) => x !== i) : [...days, i].sort())}
        >
          {l}
        </button>
      ))}
    </div>
  );
}

export function ScheduleEditor({ s, onChange }: { s: RuleSchedule; onChange: (s: RuleSchedule) => void }) {
  return (
    <div className="sched">
      <DaysPicker days={s.days} onChange={(days) => onChange({ ...s, days })} />
      <div className="fieldrow">
        <label className="field">
          From
          <input className="inp" type="time" value={s.start} onChange={(e) => onChange({ ...s, start: e.target.value })} />
        </label>
        <label className="field">
          To
          <input className="inp" type="time" value={s.end} onChange={(e) => onChange({ ...s, end: e.target.value })} />
        </label>
      </div>
      {s.end <= s.start && s.end !== s.start && <p className="note">Ends the next morning.</p>}
    </div>
  );
}

/** Week preview: each bar is exactly as long as the window (overnight windows spill into the next day). */
export function WeekTracks({ s }: { s: RuleSchedule }) {
  const toPct = (t: string) => {
    const [h, m] = t.split(":").map(Number);
    return ((h * 60 + (m || 0)) / 1440) * 100;
  };
  const a = toPct(s.start);
  const b = toPct(s.end);
  const bars: Record<number, [number, number][]> = {};
  for (const d of s.days) {
    if (b > a) (bars[d] ??= []).push([a, b - a]);
    else {
      (bars[d] ??= []).push([a, 100 - a]);
      if (b > 0) (bars[(d + 1) % 7] ??= []).push([0, b]);
    }
  }
  return (
    <div className="week" aria-hidden="true">
      {DAY_SHORT.map((name, d) => (
        <WeekRow key={d} name={name} bars={bars[d] ?? []} />
      ))}
      <div className="hours">
        <span>00</span>
        <span>06</span>
        <span>12</span>
        <span>18</span>
        <span>24</span>
      </div>
    </div>
  );
}
function WeekRow({ name, bars }: { name: string; bars: [number, number][] }) {
  return (
    <>
      <span>{name}</span>
      <div className="track">
        {bars.map(([l, w], i) => (
          <i key={i} style={{ left: `${l}%`, width: `${w}%` }} />
        ))}
      </div>
    </>
  );
}

export function UsageEditor({ u, onChange }: { u: RuleUsage; onChange: (u: RuleUsage) => void }) {
  return (
    <div className="fieldrow">
      <label className="field">
        Over (GB)
        <input
          className="inp"
          type="number"
          min={0.1}
          step="any"
          value={u.overGB}
          onChange={(e) => onChange({ ...u, overGB: Number(e.target.value) })}
        />
      </label>
      <label className="field">
        Per
        <select className="inp" value={u.period} onChange={(e) => onChange({ ...u, period: e.target.value as RuleUsage["period"] })}>
          <option value="day">day</option>
          <option value="week">week (Sun–Sat)</option>
          <option value="month">month</option>
        </select>
      </label>
    </div>
  );
}

export function ThenEditor({ t, onChange }: { t: RuleThen; onChange: (t: RuleThen) => void }) {
  const profiles = useProfiles();
  const firstProfile = profiles.includes("gaming") ? "gaming" : profiles[0];
  const set = (patch: Partial<RuleThen>) => {
    const next: RuleThen = { ...t, ...patch };
    (Object.keys(next) as (keyof RuleThen)[]).forEach((k) => next[k] === undefined && delete next[k]);
    onChange(next);
  };
  return (
    <fieldset className="part">
      <legend>Then</legend>
      <label className="check">
        <input type="checkbox" checked={!!t.pause} onChange={(e) => set({ pause: e.target.checked || undefined })} />
        Pause (block the internet)
      </label>
      <label className="check">
        <input type="checkbox" checked={t.speedMbps != null} onChange={(e) => set({ speedMbps: e.target.checked ? 2 : undefined })} />
        Slow down to
        <input
          className="inp"
          type="number"
          min={0.5}
          max={1000}
          step={0.5}
          value={t.speedMbps ?? 2}
          disabled={t.speedMbps == null}
          aria-label="Speed limit in Mbit/s"
          onChange={(e) => set({ speedMbps: Number(e.target.value) })}
        />
        Mbit/s
      </label>
      <label className="check">
        <input type="checkbox" checked={t.vpn != null} onChange={(e) => set({ vpn: e.target.checked ? true : undefined })} />
        VPN
        <select className="inp" value={t.vpn === false ? "off" : "on"} disabled={t.vpn == null} aria-label="VPN on or off" onChange={(e) => set({ vpn: e.target.value === "on" })}>
          <option value="on">on</option>
          <option value="off">off</option>
        </select>
      </label>
      <label className="check">
        <input type="checkbox" checked={!!t.profile} onChange={(e) => set({ profile: e.target.checked ? firstProfile : undefined })} />
        Profile
        <select className="inp" value={t.profile ?? firstProfile} disabled={!t.profile} aria-label="VPN profile" onChange={(e) => set({ profile: e.target.value as Profile })}>
          {profiles.map((p) => (
            <option key={p} value={p}>
              {p}
            </option>
          ))}
        </select>
      </label>
      {t.profile && t.vpn == null && <p className="note">The profile only applies while the device is on VPN.</p>}
    </fieldset>
  );
}

export function validateRule(r: RuleDraft): string | null {
  if (!r.name.trim()) return "Give the rule a name.";
  if (!r.match.all && !r.match.newDevices && !r.match.groups?.length && !r.match.devices?.length) return "Pick who the rule is for.";
  if (r.when.schedule && r.when.schedule.days.length === 0) return "Pick at least one day.";
  if (r.when.usage && !(r.when.usage.overGB > 0)) return "The data amount must be more than 0 GB.";
  if (!hasAction(r.then)) return "Pick at least one thing to do.";
  if (r.then.speedMbps != null && (r.then.speedMbps < 0.5 || r.then.speedMbps > 1000)) return "Speed must be between 0.5 and 1000 Mbit/s.";
  return null;
}

/** The full "rule" form: name, Who, When (schedule and/or usage), Then. */
export function RuleForm({
  initial,
  devices,
  onSaved,
  onCancel,
}: {
  initial: RuleDraft;
  devices: Device[];
  onSaved: (r: Rule) => void;
  onCancel: () => void;
}) {
  const [r, setR] = useState<RuleDraft>(initial);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function save() {
    const v = validateRule(r);
    if (v) return setErr(v);
    setBusy(true);
    setErr(null);
    try {
      const saved = r.id != null ? await api.updateRule(r as Rule) : await api.createRule(r);
      onSaved(saved);
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Couldn't save the rule.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="form"
      onSubmit={(e) => {
        e.preventDefault();
        save();
      }}
    >
      <label className="field">
        Name
        <input className="inp" value={r.name} maxLength={60} placeholder="Kids' bedtime" onChange={(e) => setR({ ...r, name: e.target.value })} autoFocus />
      </label>
      <WhoEditor m={r.match} devices={devices} onChange={(match) => setR({ ...r, match })} />
      <fieldset className="part">
        <legend>When</legend>
        <label className="check">
          <input
            type="checkbox"
            checked={!!r.when.schedule}
            onChange={(e) => setR({ ...r, when: { ...r.when, schedule: e.target.checked ? { days: [0, 1, 2, 3, 4], start: "22:00", end: "07:00" } : null } })}
          />
          On a schedule
        </label>
        {r.when.schedule && <ScheduleEditor s={r.when.schedule} onChange={(schedule) => setR({ ...r, when: { ...r.when, schedule } })} />}
        <label className="check">
          <input
            type="checkbox"
            checked={!!r.when.usage}
            onChange={(e) => setR({ ...r, when: { ...r.when, usage: e.target.checked ? { period: "day", overGB: 4 } : null } })}
          />
          After using a set amount of data
        </label>
        {r.when.usage && <UsageEditor u={r.when.usage} onChange={(usage) => setR({ ...r, when: { ...r.when, usage } })} />}
        {!r.when.schedule && !r.when.usage && <p className="note">No condition: the rule always applies.</p>}
      </fieldset>
      <ThenEditor t={r.then} onChange={(then) => setR({ ...r, then })} />
      <p className="note">
        <b>Summary:</b> {ruleSummary(r, devices)}
      </p>
      {err && (
        <p className="err" role="alert">
          {err}
        </p>
      )}
      <div className="fieldrow">
        <button type="submit" className="btn pri" disabled={busy}>
          {busy ? "Saving…" : r.id != null ? "Save rule" : "Add rule"}
        </button>
        <button type="button" className="btn" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}
