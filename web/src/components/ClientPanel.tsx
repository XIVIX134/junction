import { useEffect, useState, type ReactNode } from "react";
import { DeviceCategories, DeviceWeek } from "../screens/Analysis";
import { Columns } from "../charts";
import { api, ApiError } from "../api/client";
import type { Device, DeviceDetail, Overview, Profile, Rule, RuleSchedule } from "../api/types";
import { defaultProfile, effVpn, NAME_SOURCE, route, status } from "../clients";
import { useDeviceActions, useDeviceControlState, withPending } from "../deviceControl";
import { bytes, DAY3, dmy, exitName, hm, rate, whenShort } from "../fmt";
import { useFetch, useLive } from "../live";
import { groupsOf } from "../model";
import { gatewayClock, dailyLimitRule, minutesUntilWindowEnd, pausingRule, scheduleRule } from "../rules";
import { bumpEvents } from "../events";
import { useProfiles } from "../prefs";
import { HourChart, Icon, kindOf, MenuButton, Sw } from "../ui";

export type Tab = "overview" | "insights" | "settings";
const TABS: Tab[] = ["overview", "insights", "settings"];

/** Right-hand client panel (Clients and Topology). Tabs: Overview, Insights, Settings. */
export function ClientPanel({ id, tab, o, onTab, onClose }: { id: string; tab: Tab; o: Overview; onTab: (t: Tab) => void; onClose: () => void }) {
  const ctl = useDeviceControlState();
  const det = useFetch(`device:${id}`, () => api.device(id), 15000);
  const rules = useFetch("rules", api.rules, 30000);
  const live = o.devices.find((d) => d.id === id);

  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && !(e.target as HTMLElement).closest(".menu") && onClose();
    document.addEventListener("keydown", k);
    return () => document.removeEventListener("keydown", k);
  }, [onClose]);

  if (!live && !det.data) {
    return (
      <aside className="side" aria-label="Client">
        <div className="sh">
          <div>
            <h3>{det.error ? "Client not found" : "Loading…"}</h3>
            {det.error && <div className="sub">{det.error}</div>}
          </div>
          <button className="x" type="button" onClick={onClose} aria-label="Close">
            <Icon n="x" />
          </button>
        </div>
      </aside>
    );
  }
  // Live fields from the overview stream win over the slower detail fetch; in-flight changes win over both.
  const d: Device = withPending(det.data ? { ...det.data, ...(live ?? {}) } : live!, ctl);
  const s = status(d, rules.data ?? [], o.now);
  const reload = () => {
    det.reload();
    rules.reload();
  };
  return (
    <aside className="side" aria-label={`Client ${d.name}`}>
      <div className="sh">
        <div className="dic">
          <Icon n={kindOf(d)} s={20} />
        </div>
        <div style={{ minWidth: 0 }}>
          <h3>{d.name}</h3>
          <div className="sub">
            <span className={`dot ${s.c}`} />
            {s.t}
            <span className="t3">·</span>
            {d.ip}
            {d.group && (
              <>
                <span className="t3">·</span>
                {d.group}
              </>
            )}
          </div>
        </div>
        <button className="x" type="button" onClick={onClose} aria-label="Close panel">
          <Icon n="x" />
        </button>
      </div>
      <div className="tabs" role="tablist" aria-label="Client views">
        {TABS.map((t) => (
          <button key={t} type="button" role="tab" aria-selected={tab === t} className={tab === t ? "on" : ""} onClick={() => onTab(t)}>
            {t[0].toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>
      <div className="sb" role="tabpanel" aria-label={tab}>
        {tab === "overview" && <OverviewTab d={d} o={o} rules={rules.data ?? []} reload={reload} />}
        {tab === "insights" && <InsightsTab d={d} det={det.data} o={o} err={det.error} />}
        {tab === "settings" && <SettingsTab d={d} o={o} rules={rules.data ?? []} reload={reload} />}
      </div>
    </aside>
  );
}

// ---------- shared action helper ----------

function useAct(after: () => void) {
  const { refresh } = useLive();
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; t: string } | null>(null);
  async function run(fn: () => Promise<unknown>, ok: string | null, fail: string) {
    setBusy(true);
    setMsg(null);
    try {
      await fn();
      if (ok) setMsg({ ok: true, t: ok });
      after();
      refresh();
      bumpEvents();
    } catch (e) {
      setMsg({ ok: false, t: e instanceof ApiError ? e.message : fail });
    } finally {
      setBusy(false);
    }
  }
  const view: ReactNode = msg && (
    <div className={msg.ok ? "okmsg" : "err"} role={msg.ok ? "status" : "alert"} style={{ marginTop: 8 }}>
      {msg.t}
    </div>
  );
  return { busy, run, view, setMsg };
}

const linkList = (d: Device, unlink: (id: string) => void, busy: boolean) =>
  d.links && d.links.length ? (
    d.links.map((l) => (
      <div className="lr" style={{ minHeight: 40 }} key={l.id}>
        <Icon n="phone" s={15} />
        <div style={{ minWidth: 0 }}>
          <div>
            {l.name} <span className="t3">· {l.ip}</span>
          </div>
          <div className="t3 mono" style={{ fontSize: 11.5 }}>
            {l.mac}
          </div>
        </div>
        <span className="v" style={{ display: "flex", gap: 10, alignItems: "center" }}>
          <span className={`dot ${l.online ? "ok" : ""}`} role="img" aria-label={l.online ? "online" : "offline"} />
          <button type="button" className="btn sm" onClick={() => unlink(l.id)} disabled={busy} aria-label={`Unlink ${l.name}`}>
            Unlink
          </button>
        </span>
      </div>
    ))
  ) : (
    <div className="note">None.</div>
  );

// ---------- Overview ----------

function OverviewTab({ d, o, rules, reload }: { d: Device; o: Overview; rules: Rule[]; reload: () => void }) {
  const a = useAct(reload);
  const e = d.effective;
  const r = route(d);
  return (
    <>
      <div className="sec">
        <div className="st">Now</div>
        <dl className="kv" style={{ marginTop: 6 }}>
          <dt>Download</dt>
          <dd>{d.usesGateway ? rate(d.rateDown) : <span className="t3">Not counted</span>}</dd>
          <dt>Upload</dt>
          <dd>{d.usesGateway ? rate(d.rateUp) : <span className="t3">Not counted</span>}</dd>
          <dt>Connections</dt>
          <dd>{d.connections}</dd>
          <dt>Route</dt>
          <dd className={r.c}>
            {r.t}
            {d.controllable && !effVpn(d) && <span className="t3"> · VPN off</span>}
          </dd>
          <dt>Rules</dt>
          <dd className="t3">{e && e.rules.length ? e.rules.map((x) => x.name).join(", ") : "None apply"}</dd>
        </dl>
        {e && (e.rules.length > 0 || e.exemptions.length > 0) && (
          <div style={{ marginTop: 6 }}>
            {e.rules.map((x) => (
              <div className="lr" key={x.id}>
                <span>{x.name}</span>
                <span className="t3" style={{ fontSize: 12, overflow: "hidden", textOverflow: "ellipsis" }}>
                  {x.why}
                </span>
              </div>
            ))}
            {e.exemptions.map((x) => (
              <div className="lr" key={`ex${x.ruleId}`}>
                <span>
                  {rules.find((y) => y.id === x.ruleId)?.name ?? `Rule ${x.ruleId}`} <span className="t3">skipped until {hm(x.until)}</span>
                </span>
                <span className="v">
                  <button type="button" className="btn sm" disabled={a.busy} onClick={() => a.run(() => api.endExempt(x.ruleId, d.id), null, "Couldn't end it.")}>
                    End now
                  </button>
                </span>
              </div>
            ))}
          </div>
        )}
        {a.view}
      </div>
      <div className="sec">
        <div className="st">
          Usage<span className="r t3">through the gateway</span>
        </div>
        {d.usesGateway ? (
          <dl className="kv" style={{ marginTop: 6 }}>
            <dt>Today</dt>
            <dd>
              {bytes(d.todayDown)} down · {bytes(d.todayUp)} up
            </dd>
            <dt>This week</dt>
            <dd>{bytes((d.weekDown ?? 0) + (d.weekUp ?? 0))} total</dd>
            <dt>This month</dt>
            <dd>{bytes((d.monthDown ?? 0) + (d.monthUp ?? 0))} total</dd>
          </dl>
        ) : (
          <div className="sd">This device bypasses the gateway, so its usage is not counted (it isn't zero).</div>
        )}
      </div>
      <div className="sec">
        <div className="st">
          Linked connections<span className="r t3">{d.links?.length ?? 0}</span>
        </div>
        <div className="sd">Same device on a second MAC. Usage and settings are combined.</div>
        <div style={{ marginTop: 6 }}>{linkList(d, (id) => a.run(() => api.linkDevice(id, null), "Unlinked.", "Couldn't unlink it."), a.busy)}</div>
      </div>
      <div className="sec">
        <div className="st">Seen</div>
        <dl className="kv" style={{ marginTop: 6 }}>
          <dt>First seen</dt>
          <dd>{whenShort(d.firstSeen, o.now)}</dd>
          <dt>Last seen</dt>
          <dd>{whenShort(d.lastSeen, o.now)}</dd>
        </dl>
      </div>
    </>
  );
}

// ---------- Insights ----------

function InsightsTab({ d, det, o, err }: { d: Device; det: DeviceDetail | null; o: Overview; err: string | null }) {
  if (!det) return <div className="sec">{err ? <span className="err">{err}</span> : <span className="t3">Loading…</span>}</div>;
  const th = det.topHosts ?? [];
  const mx = th.length ? th[0].bytes : 1;
  const ex: Record<string, number> = {};
  (det.hours ?? []).forEach((h) => Object.entries(h.byExit ?? {}).forEach(([k, v]) => (ex[k] = (ex[k] ?? 0) + v)));
  const days = det.days ?? [];
  return (
    <>
      <div className="sec">
        <div className="st">
          Traffic today<span className="r t3">hourly average</span>
        </div>
        {d.usesGateway ? (
          <>
            <div style={{ marginTop: 8 }}>
              <HourChart hours={det.hours ?? []} now={Date.parse(o.now)} h={150} label={`Traffic today for ${d.name}`} />
            </div>
            <div className="legend" style={{ marginTop: 4 }}>
              <span><i className="ln" style={{ background: "var(--series-down)" }} />Download</span>
              <span><i className="ln" style={{ background: "var(--series-up)" }} />Upload</span>
              <span style={{ marginLeft: "auto" }}>By exit: {Object.entries(ex).map(([k, v]) => `${exitName(k)} ${bytes(v)}`).join(" · ") || "—"}</span>
            </div>
          </>
        ) : (
          <div className="sd">Not visible: this device bypasses the gateway.</div>
        )}
      </div>
      <div className="sec">
        <div className="st">
          Top sites today<span className="r t3">{th.length} hosts</span>
        </div>
        <div style={{ marginTop: 4 }}>
          {th.length ? (
            th.map((h) => (
              <div className="lr" style={{ minHeight: 30 }} key={h.host}>
                <span style={{ width: 200, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={h.host}>
                  {h.host}
                </span>
                <span className="bar" style={{ flex: 1, height: 4 }}>
                  <i style={{ width: `${(h.bytes / mx) * 100}%`, background: "var(--accent)" }} />
                </span>
                <span className="v" style={{ width: 58, textAlign: "right" }}>
                  {bytes(h.bytes)}
                </span>
              </div>
            ))
          ) : (
            <div className="note">No traffic through the gateway today.</div>
          )}
        </div>
      </div>
      {d.usesGateway && <DeviceCategories id={d.id} />}
      {d.usesGateway && <DeviceWeek id={d.id} />}
      {days.length > 0 && d.usesGateway && (
        <div className="sec">
          <div className="st">
            Last {days.length} days<span className="r t3">{bytes(days.reduce((s, x) => s + x.down + x.up, 0))}</span>
          </div>
          <div style={{ marginTop: 8 }}>
            <Columns
              h={120}
              padL={46}
              label={`Daily usage for the last ${days.length} days`}
              yFmt={(x) => (x === 0 ? "0" : bytes(x, 1))}
              xLabel={(i) => (i === days.length - 1 ? "today" : i % 7 === 0 && i < days.length - 3 ? dmy(`${days[i].day}T12:00:00Z`) : null)}
              cols={days.map((x, i) => ({
                key: x.day,
                value: x.down + x.up,
                accent: i === days.length - 1,
                tip: i === days.length - 1 ? "Today · so far" : dmy(`${x.day}T12:00:00Z`),
                rows: [
                  { label: "in total", value: bytes(x.down + x.up) },
                  { label: "download", value: bytes(x.down), color: "var(--series-down)" },
                  { label: "upload", value: bytes(x.up), color: "var(--series-up)" },
                ],
              }))}
            />
          </div>
        </div>
      )}
    </>
  );
}

// ---------- Settings ----------

function minutesUntil(hour: number, now: Date) {
  const m = gatewayClock(now).minutes;
  const d = (hour * 60 - m + 1440) % 1440;
  return d === 0 ? 1440 : d;
}

function SettingsTab({ d, o, rules, reload }: { d: Device; o: Overview; rules: Rule[]; reload: () => void }) {
  const profiles = useProfiles();
  const act = useDeviceActions();
  const ctl = useDeviceControlState();
  const a = useAct(reload);
  const e = d.effective;
  const def = defaultProfile(o);
  const busy = ctl.busy.has(d.id);
  const now = new Date();
  const pr = pausingRule(e, rules);
  const err = ctl.errors.get(d.id);
  const can = !!d.controllable;
  const why = can ? undefined : "Not controllable: bypasses the gateway";

  return (
    <>
      {/* Internet access */}
      <div className="sec">
        <div className="st">
          Internet access
          <span className="r">
            {e?.paused ? (
              <span className="warnc">{pr ? `Paused by ${pr.name}` : d.pausedUntil ? `Paused until ${hm(d.pausedUntil)}` : "Paused"}</span>
            ) : (
              <span className="okc">Allowed</span>
            )}
          </span>
        </div>
        <div className="ctl">
          {pr ? (
            <button
              type="button"
              className="btn"
              disabled={a.busy}
              onClick={() => {
                const m = pr.when.schedule ? minutesUntilWindowEnd(pr, now) : 60;
                a.run(() => api.exempt(pr.id, d.id, m), `${pr.name} skipped for ${m} min.`, "Couldn't skip it.");
              }}
            >
              {pr.when.schedule ? "Skip this time" : "Give 1 h more"}
            </button>
          ) : d.paused ? (
            <button type="button" className="btn" disabled={busy} onClick={() => act.resume(d)}>
              {busy ? "Resuming…" : "Resume"}
            </button>
          ) : (
            <MenuButton
              disabled={!can || busy}
              label={
                <>
                  Pause <Icon n="chev" s={12} />
                </>
              }
              items={[
                { label: "15 minutes", onPick: () => act.pause(d, 15) },
                { label: "1 hour", onPick: () => act.pause(d, 60) },
                { label: "Until 07:00", onPick: () => act.pause(d, minutesUntil(7, now)) },
                { label: "Until resumed", onPick: () => act.pause(d) },
              ]}
            />
          )}
          <span className="note">{can ? "Blocks all traffic from this device." : "Not controllable: this device bypasses the gateway."}</span>
        </div>
        {err && (
          <div className="err" role="alert" style={{ marginTop: 8 }}>
            {err}
          </div>
        )}
      </div>

      {/* VPN */}
      <div className="sec">
        <div className="st">
          VPN
          <span className="r">
            <Sw on={!!d.vpn} label={`VPN for ${d.name}`} disabled={!can} reason={why} busy={busy} onChange={(on) => act.setVpn(d, on)} />
          </span>
        </div>
        <div className="sd">
          {e ? (e.vpn ? `On · exits via ${exitName(e.exit)}` : "Off · traffic exits Direct through the ISP") : d.vpn ? "On" : "Off"}
          {e && e.vpn !== !!d.vpn ? " · set by a rule" : ""}
        </div>
        <div className="ctl">
          <span className="lab" aria-hidden="true">
            Profile
          </span>
          <select
            className="inp"
            style={{ width: 190 }}
            aria-label="VPN profile"
            value={d.profile ?? ""}
            disabled={!can || a.busy}
            onChange={(ev) => a.run(() => api.setDeviceProfile(d.id, (ev.target.value || null) as Profile | null), null, "Couldn't change the profile.")}
          >
            <option value="">Default ({def})</option>
            {profiles.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </div>
        {a.view}
      </div>

      <SpeedSection d={d} reload={reload} />
      <LimitSection d={d} rules={rules} reload={reload} />
      <ScheduleSection d={d} rules={rules} reload={reload} />

      {/* Linked connections */}
      <div className="sec">
        <div className="st">Linked connection{(d.links?.length ?? 0) === 1 ? "" : "s"}</div>
        <div style={{ marginTop: 6 }}>{linkList(d, (id) => a.run(() => api.linkDevice(id, null), "Unlinked.", "Couldn't unlink it."), a.busy)}</div>
      </div>

      <DetailsSection d={d} o={o} reload={reload} />
    </>
  );
}

function SpeedSection({ d, reload }: { d: Device; reload: () => void }) {
  const a = useAct(reload);
  const [v, setV] = useState(d.speedLimitMbps != null ? String(d.speedLimitMbps) : "");
  useEffect(() => setV(d.speedLimitMbps != null ? String(d.speedLimitMbps) : ""), [d.speedLimitMbps]);
  const cap = d.effective?.speedLimitMbps ?? d.speedLimitMbps ?? null;
  const can = !!d.controllable;
  return (
    <div className="sec">
      <div className="st">
        Speed limit<span className="r t3">{cap != null ? `${cap} Mbit/s${cap !== d.speedLimitMbps ? " (rule)" : ""}` : "No limit"}</span>
      </div>
      <form
        className="ctl"
        onSubmit={(ev) => {
          ev.preventDefault();
          const n = Number(v);
          if (!(n >= 0.5 && n <= 1000)) return a.setMsg({ ok: false, t: "Use 0.5 to 1000 Mbit/s." });
          a.run(() => api.setDeviceSpeed(d.id, n), null, "Couldn't set the speed limit.");
        }}
      >
        <input
          className="inp num"
          style={{ width: 84 }}
          placeholder="—"
          inputMode="decimal"
          aria-label="Speed limit (Mbit/s, both directions)"
          value={v}
          disabled={!can || a.busy}
          onChange={(ev) => setV(ev.target.value)}
        />
        <span className="t2">Mbit/s, both directions</span>
        <span style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
          {d.speedLimitMbps != null && (
            <button type="button" className="btn ghost" disabled={a.busy} onClick={() => a.run(() => api.setDeviceSpeed(d.id, null), null, "Couldn't remove the limit.")}>
              Off
            </button>
          )}
          <button type="submit" className="btn" disabled={!can || a.busy || !v}>
            Set
          </button>
        </span>
      </form>
      {a.view}
    </div>
  );
}

function LimitSection({ d, rules, reload }: { d: Device; rules: Rule[]; reload: () => void }) {
  const rule = dailyLimitRule(rules, d.id);
  const a = useAct(reload);
  const [gbv, setGb] = useState(rule?.when.usage ? String(rule.when.usage.overGB) : "");
  const [then, setThen] = useState<"speed" | "pause">(rule?.then.pause ? "pause" : "speed");
  const [mbps, setMbps] = useState(String(rule?.then.speedMbps ?? 2));
  useEffect(() => {
    if (!rule) return;
    setGb(String(rule.when.usage?.overGB ?? ""));
    setThen(rule.then.pause ? "pause" : "speed");
    setMbps(String(rule.then.speedMbps ?? 2));
  }, [rule?.id]); // eslint-disable-line react-hooks/exhaustive-deps

  const used = d.todayDown + d.todayUp;
  const limit = (rule?.when.usage?.overGB ?? 0) * 1e9;
  const over = !!rule && limit > 0 && used >= limit;
  const active = !!rule && !!d.effective?.rules.some((x) => x.id === rule.id);
  const ex = rule && d.effective?.exemptions.find((x) => x.ruleId === rule.id);
  const can = !!d.controllable;

  function save() {
    const g = Number(gbv);
    const m = Number(mbps);
    if (!(g > 0)) return a.setMsg({ ok: false, t: "Enter the daily amount in GB." });
    if (then === "speed" && !(m >= 0.5 && m <= 1000)) return a.setMsg({ ok: false, t: "Use 0.5 to 1000 Mbit/s." });
    const body = {
      name: rule?.name ?? `${d.name}: daily limit`,
      enabled: rule?.enabled ?? true,
      match: { devices: [d.id] },
      when: { usage: { period: "day" as const, overGB: g } },
      then: then === "pause" ? { pause: true } : { speedMbps: m },
    };
    a.run(() => (rule ? api.updateRule({ ...rule, ...body }) : api.createRule(body)), rule ? "Daily limit saved." : "Daily limit set.", "Couldn't save the daily limit.");
  }

  return (
    <div className="sec">
      <div className="st">
        Daily data limit
        <span className="r t3">
          {rule ? `${rule.when.usage?.overGB} GB a day${rule.enabled ? "" : " · off"}` : "Not set"}
        </span>
      </div>
      {rule && (
        <>
          <div className="ctl" style={{ justifyContent: "space-between" }}>
            <span className="t2">
              {bytes(used)} of {rule.when.usage?.overGB} GB today
            </span>
            {active && <span className="warnc">{rule.then.pause ? "Paused" : `Slowed to ${rule.then.speedMbps} Mbit/s`}</span>}
          </div>
          <div className="bar" style={{ marginTop: 6 }} role="img" aria-label={`${bytes(used)} of ${rule.when.usage?.overGB} GB used today`}>
            <i style={{ width: `${Math.min(100, (used / Math.max(1, limit)) * 100)}%`, background: over ? "var(--warn)" : "var(--accent)" }} />
          </div>
          {ex && (
            <div className="ctl">
              <span className="t2">Extra time until {hm(ex.until)}</span>
              <button type="button" className="btn sm" disabled={a.busy} onClick={() => a.run(() => api.endExempt(rule.id, d.id), null, "Couldn't end it.")}>
                End now
              </button>
            </div>
          )}
        </>
      )}
      <form
        onSubmit={(ev) => {
          ev.preventDefault();
          save();
        }}
      >
        <div className="ctl">
          <span className="lab" aria-hidden="true">
            After
          </span>
          <input
            className="inp num"
            style={{ width: 64 }}
            placeholder="GB"
            inputMode="decimal"
            aria-label="Limit (GB a day)"
            value={gbv}
            disabled={!can || a.busy}
            onChange={(ev) => setGb(ev.target.value)}
          />
          <span className="t2">GB today</span>
        </div>
        <div className="ctl">
          <span className="lab" aria-hidden="true">
            Then
          </span>
          <select className="inp" aria-label="Then" style={{ width: 92 }} value={then} disabled={!can || a.busy} onChange={(ev) => setThen(ev.target.value as "speed" | "pause")}>
            <option value="speed">Slow to</option>
            <option value="pause">Pause</option>
          </select>
          {then === "speed" && (
            <>
              <input
                className="inp num"
                style={{ width: 52 }}
                inputMode="decimal"
                aria-label="To (Mbit/s)"
                value={mbps}
                disabled={!can || a.busy}
                onChange={(ev) => setMbps(ev.target.value)}
              />
              <span className="t2">Mbit/s</span>
            </>
          )}
          <span style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
            {rule && (active || over) && !ex && (
              <button type="button" className="btn" disabled={a.busy} onClick={() => a.run(() => api.exempt(rule.id, d.id, 60), "1 more hour granted.", "Couldn't give more time.")}>
                Give 1 h more
              </button>
            )}
            {rule && (
              <button type="button" className="btn ghost" disabled={a.busy} onClick={() => a.run(() => api.deleteRule(rule.id), "Daily limit removed.", "Couldn't remove it.")}>
                Remove
              </button>
            )}
            <button type="submit" className="btn" disabled={!can || a.busy}>
              {rule ? "Save" : "Set limit"}
            </button>
          </span>
        </div>
      </form>
      {a.view}
    </div>
  );
}

const TIME = /^([01]\d|2[0-3]):[0-5]\d$/;

function ScheduleSection({ d, rules, reload }: { d: Device; rules: Rule[]; reload: () => void }) {
  const rule = scheduleRule(rules, d.id);
  const a = useAct(reload);
  const [s, setS] = useState<RuleSchedule>(rule?.when.schedule ?? { days: [0, 1, 2, 3, 4], start: "23:00", end: "07:00" });
  useEffect(() => {
    if (rule?.when.schedule) setS(rule.when.schedule);
  }, [rule?.id]); // eslint-disable-line react-hooks/exhaustive-deps
  const active = !!rule && !!d.effective?.rules.some((x) => x.id === rule.id);
  const ex = rule && d.effective?.exemptions.find((x) => x.ruleId === rule.id);
  const can = !!d.controllable;
  const on = !!rule && rule.enabled;

  function body(enabled: boolean) {
    return { name: rule?.name ?? `${d.name}: schedule`, enabled, match: { devices: [d.id] }, when: { schedule: s }, then: { pause: true } };
  }
  function save() {
    if (!s.days.length) return a.setMsg({ ok: false, t: "Pick at least one day." });
    if (!TIME.test(s.start) || !TIME.test(s.end)) return a.setMsg({ ok: false, t: "Times look like 23:00." });
    a.run(() => (rule ? api.updateRule({ ...rule, ...body(rule.enabled) }) : api.createRule(body(true))), rule ? "Schedule saved." : "Schedule set.", "Couldn't save the schedule.");
  }
  return (
    <div className="sec">
      <div className="st">
        Schedule
        <span className="r">
          <Sw
            on={on}
            label="Schedule on"
            disabled={!can || (!rule && (!s.days.length || !TIME.test(s.start) || !TIME.test(s.end)))}
            busy={a.busy}
            onChange={(next) => (rule ? a.run(() => api.updateRule({ ...rule, enabled: next }), null, "Couldn't change it.") : save())}
          />
        </span>
      </div>
      <div className="sd">
        Block internet on these days and hours.
        {active && !ex && <span className="warnc"> Paused by it now.</span>}
        {ex && <span className="t2"> Skipped until {hm(ex.until)}.</span>}
      </div>
      <form
        className={on || !rule ? "" : "dimmed"}
        onSubmit={(ev) => {
          ev.preventDefault();
          save();
        }}
      >
        <div className="ctl">
          <div className="days" role="group" aria-label="Days">
            {DAY3.map((name, i) => (
              <button
                key={name}
                type="button"
                aria-label={name}
                aria-pressed={s.days.includes(i)}
                title={name}
                disabled={!can}
                onClick={() => setS({ ...s, days: s.days.includes(i) ? s.days.filter((x) => x !== i) : [...s.days, i].sort() })}
              >
                {name[0]}
              </button>
            ))}
          </div>
        </div>
        <div className="ctl">
          <span className="t2" aria-hidden="true">
            From
          </span>
          <input className="inp num" style={{ width: 64 }} aria-label="From" placeholder="23:00" value={s.start} disabled={!can} onChange={(ev) => setS({ ...s, start: ev.target.value })} />
          <span className="t2" aria-hidden="true">
            to
          </span>
          <input className="inp num" style={{ width: 64 }} aria-label="To" placeholder="07:00" value={s.end} disabled={!can} onChange={(ev) => setS({ ...s, end: ev.target.value })} />
          <span style={{ marginLeft: "auto", display: "flex", gap: 6, flexWrap: "wrap" }}>
            {rule && active && !ex && (
              <button
                type="button"
                className="btn ghost"
                disabled={a.busy}
                onClick={() => {
                  const m = minutesUntilWindowEnd(rule, new Date());
                  a.run(() => api.exempt(rule.id, d.id, m), `Skipped until ${hm(Date.now() + m * 60000)}.`, "Couldn't skip it.");
                }}
              >
                Skip this time
              </button>
            )}
            {rule && (
              <button type="button" className="btn ghost" disabled={a.busy} onClick={() => a.run(() => api.deleteRule(rule.id), "Schedule removed.", "Couldn't remove it.")}>
                Remove
              </button>
            )}
            <button type="submit" className="btn" disabled={!can || a.busy}>
              {rule ? "Save" : "Set schedule"}
            </button>
          </span>
        </div>
      </form>
      {a.view}
    </div>
  );
}

function DetailsSection({ d, o, reload }: { d: Device; o: Overview; reload: () => void }) {
  const a = useAct(reload);
  const [edit, setEdit] = useState<"name" | "group" | null>(null);
  const [val, setVal] = useState("");
  const groups = groupsOf(o.devices).map((g) => g.name);
  const own = d.nameSource === "user" && d.autoName && d.autoName.toLowerCase() !== d.name.toLowerCase();
  const saveEdit = () => {
    if (edit === "name") {
      if (!val.trim()) return a.setMsg({ ok: false, t: "A name can't be empty." });
      a.run(() => api.patchDevice(d.id, { name: val.trim() }), "Renamed.", "Couldn't rename it.").then(() => setEdit(null));
    } else if (edit === "group") {
      a.run(() => api.patchDevice(d.id, { group: val.trim() || null }), "Group saved.", "Couldn't save the group.").then(() => setEdit(null));
    }
  };
  const editor = (label: string) => (
    <form
      style={{ display: "flex", gap: 6 }}
      onSubmit={(ev) => {
        ev.preventDefault();
        saveEdit();
      }}
    >
      <input className="inp" style={{ flex: 1 }} aria-label={label} value={val} list={edit === "group" ? "jgroups" : undefined} autoFocus maxLength={64} onChange={(ev) => setVal(ev.target.value)} onKeyDown={(ev) => ev.key === "Escape" && setEdit(null)} />
      {edit === "group" && (
        <datalist id="jgroups">
          {groups.map((g) => (
            <option key={g} value={g} />
          ))}
        </datalist>
      )}
      <button type="submit" className="btn sm pri" disabled={a.busy}>
        Save
      </button>
      <button type="button" className="btn sm ghost" onClick={() => setEdit(null)}>
        Cancel
      </button>
    </form>
  );
  return (
    <div className="sec">
      <div className="st">Details</div>
      <dl className="kv" style={{ marginTop: 6 }}>
        <dt>Name</dt>
        <dd style={{ whiteSpace: "normal" }}>
          {edit === "name" ? (
            editor("Device name")
          ) : (
            <>
              {d.name} <span className="t3">· {NAME_SOURCE[d.nameSource] ?? d.nameSource}</span>{" "}
              <button type="button" className="linkbtn" onClick={() => (setVal(d.name), setEdit("name"))}>
                Rename
              </button>
              {own && (
                <>
                  {" · "}
                  <button type="button" className="linkbtn" disabled={a.busy} onClick={() => a.run(() => api.patchDevice(d.id, { name: "" }), `Back to “${d.autoName}”.`, "Couldn't change the name.")}>
                    Use the device's own name ({d.autoName})
                  </button>
                </>
              )}
            </>
          )}
        </dd>
        <dt>Group</dt>
        <dd>
          {edit === "group" ? (
            editor("Group (empty for none)")
          ) : (
            <>
              {d.group ?? "Ungrouped"}{" "}
              <button type="button" className="linkbtn" onClick={() => (setVal(d.group ?? ""), setEdit("group"))}>
                Change
              </button>
            </>
          )}
        </dd>
        <dt>MAC</dt>
        <dd className="mono">
          {d.mac ?? "—"}
          {d.randomMac && <span className="t3" style={{ fontFamily: "Lato, system-ui" }}> · private</span>}
        </dd>
        <dt>IP</dt>
        <dd>{d.ip}</dd>
        {d.vendor && (
          <>
            <dt>Vendor</dt>
            <dd>{d.vendor}</dd>
          </>
        )}
        {d.model && (
          <>
            <dt>Model</dt>
            <dd>{d.model}</dd>
          </>
        )}
        <dt>ID</dt>
        <dd>{d.short}</dd>
        <dt>First seen</dt>
        <dd>
          {dmy(d.firstSeen)} {hm(d.firstSeen)}
        </dd>
      </dl>
      {a.view}
    </div>
  );
}
