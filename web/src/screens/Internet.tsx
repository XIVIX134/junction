import type { ReactNode } from "react";
import { WhereTrafficWent } from "./Analysis";
import { api } from "../api/client";
import type { ExitHealth, Overview, SelectorBody } from "../api/types";
import { gatewayShort, selectedEndpoint } from "../clients";
import { NeedOverview } from "../components/Shell";
import { bumpEvents } from "../events";
import { bytes, dmy, exitName, hm, hms } from "../fmt";
import { useFetch, useLive } from "../live";
import { plainError } from "../model";
import { changeSelector, DOWN_REASON, profileBody, useSelectorState } from "../selector";
import { Icon, Radio, Spark, stateDot, StateStrip, Sw } from "../ui";
import { BundlePanel } from "../components/BundlePanel";
import { LatencyPanel } from "../components/LatencyPanel";
import { useProfiles } from "../prefs";
import { backupLabel } from "../site";

export function Internet() {
  return <NeedOverview>{(o) => <InternetView o={o} />}</NeedOverview>;
}

function InternetView({ o }: { o: Overview }) {
  return (
    <div className="page">
      <div className="ph">
        <h1>Internet</h1>
        <span className="meta">Exits, bundle, gateway failover</span>
      </div>
      <Exits o={o} />
      <LatencyPanel o={o} />
      <WhereTrafficWent o={o} />
      <div className="grid collapse-md">
        <BundlePanel o={o} />
        <Failover o={o} />
      </div>
    </div>
  );
}

// ---------- exits ----------

const stats = (v: (number | null | undefined)[]) => {
  const x = v.filter((n): n is number => n != null);
  return x.length ? { min: Math.min(...x), max: Math.max(...x), avg: x.reduce((a, b) => a + b, 0) / x.length } : null;
};

function Exits({ o }: { o: Overview }) {
  const profileNames = useProfiles();
  const { refresh } = useLive();
  const sel = useSelectorState();
  const hist = useFetch("exit-history", () => api.exitHistory(60), 30000);
  const c = o.selector.control ?? null;
  const ep = selectedEndpoint(o);
  const samples = hist.data?.samples ?? [];
  const series = (k: string) => samples.map((s) => (k in s.exits ? s.exits[k] ?? null : null));
  const span = samples.length > 1 ? Math.round((Date.parse(samples[samples.length - 1].at) - Date.parse(samples[0].at)) / 60e3) : 0;
  const allMax = Math.max(1, ...[...o.exits.map((e) => e.key), "direct"].flatMap((k) => series(k).filter((x): x is number => x != null))) * 1.1;
  const busy = !!sel.busy;
  const locked = !c ? "This gateway can't switch exits from here yet" : null;
  const mode = c?.mode ?? o.selector.mode;
  const go = (body: SelectorBody, label: string) => changeSelector(body, label, refresh).then(bumpEvents);
  const profiles = (e: ExitHealth) =>
    (e.profiles ?? []).map((p, i) => (
      <span key={p.profile}>
        {i > 0 && <span className="t3"> · </span>}
        {p.profile} <span className={p.healthy ? "" : "badc"}>{p.healthy && p.latencyMs != null ? `${p.latencyMs} ms` : p.known ? "✗" : "?"}</span>
      </span>
    ));
  const viaDirect = o.devices.filter((d) => d.controllable && !(d.effective?.vpn ?? d.vpn)).length;

  type Row = { key: string; pick: () => void; on: boolean; disabled: string | null; name: string };
  const rows: Row[] = [
    { key: "auto", name: "Automatic", on: mode === "auto", disabled: locked, pick: () => go({ mode: "auto" }, "Switching to automatic") },
    ...o.exits.map((e) => ({
      key: e.key,
      name: e.name,
      on: mode === "manual" && c?.manualEndpoint === e.key,
      disabled: locked ?? (e.healthy ? null : DOWN_REASON),
      pick: () => go({ mode: "manual", endpoint: e.key }, `Switching to ${e.name}`),
    })),
    { key: "direct", name: "Direct", on: mode === "direct", disabled: locked, pick: () => go({ mode: "direct" }, "Switching to Direct") },
  ];
  const row = (r: Row, cells: ReactNode) => (
    <tr
      key={r.key}
      className={`rw ${r.on ? "sel" : ""}`}
      aria-disabled={r.disabled ? "true" : undefined}
      title={r.disabled ?? undefined}
      onClick={() => !r.on && !r.disabled && !busy && r.pick()}
    >
      <td>
        <button
          type="button"
          role="radio"
          aria-checked={r.on}
          aria-label={`Use ${r.name}`}
          aria-disabled={r.disabled || busy ? "true" : undefined}
          title={r.disabled ?? undefined}
          style={{ background: "none", border: 0, padding: 0, cursor: r.disabled ? "default" : "pointer" }}
          onClick={(ev) => {
            ev.stopPropagation();
            if (!r.on && !r.disabled && !busy) r.pick();
          }}
        >
          <Radio on={r.on} />
        </button>
      </td>
      {cells}
    </tr>
  );

  return (
    <section className="pn" style={{ marginBottom: 12 }} aria-labelledby="ex-h">
      <div className="hd">
        <h2 id="ex-h">Exits</h2>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          VPN for devices with VPN on · switching drops open connections for a moment
        </span>
        <div className="r" style={{ color: "var(--text)" }}>
          {c && (
            <>
              <span className="t2">Profile</span>
              <select
                className="inp"
                style={{ height: 24, width: 96 }}
                aria-label="Exit profile"
                value={c.profile}
                disabled={busy}
                onChange={(ev) => go(profileBody(o, ev.target.value), `Switching to the ${ev.target.value} profile`)}
              >
                {profileNames.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </>
          )}
          <span className="t2">All direct</span>
          <Sw
            on={mode === "direct"}
            label="Send everything direct"
            disabled={!c}
            reason={locked ?? undefined}
            busy={busy}
            onChange={(on) => go(on ? { mode: "direct" } : { mode: "auto" }, on ? "Switching to Direct" : "Switching to automatic")}
          />
        </div>
      </div>
      {(sel.busy || sel.error) && (
        <div className="line-alert" role={sel.error ? "alert" : "status"} style={{ margin: "10px 14px 0" }}>
          <span className={`dot ${sel.error ? "bad" : "acc"}`} />
          {sel.busy ? `${sel.busy}… open connections drop for a moment and reconnect.` : `The switch didn't happen: ${sel.error}`}
        </div>
      )}
      <div className="scroll-x">
        <table className="tb" role="radiogroup" aria-label="How traffic leaves">
          <thead>
            <tr>
              <th style={{ width: 36 }} />
              <th>Exit</th>
              <th>Status</th>
              <th className="rt">Latency</th>
              <th>{span ? `Last ${span} min` : "Last hour"}</th>
              <th>Profiles</th>
              <th className="rt">Today</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {row(
              rows[0],
              <>
                <td>
                  <b style={{ fontWeight: 700 }}>Automatic</b>
                </td>
                <td colSpan={5} className="t2 wrap">
                  Failover between healthy exits · now {exitName(o.selector.selected)}
                  {o.selector.lastSwitch ? ` since ${hm(o.selector.lastSwitch)}` : ""}
                  {o.selector.reason ? ` · ${o.selector.reason}` : ""}
                </td>
                <td className="rt">{rows[0].on && <span className="tag acc">Selected</span>}</td>
              </>,
            )}
            {o.exits.map((e, i) => {
              const r = rows[i + 1];
              return row(
                r,
                <>
                  <td>
                    <span style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
                      <Icon n="tunnel" s={15} />
                      <b style={{ fontWeight: 700 }}>{e.name}</b>
                      <span className="t3">VPN · Hysteria2</span>
                    </span>
                  </td>
                  <td>
                    <span style={{ display: "inline-flex", gap: 7, alignItems: "center" }}>
                      <span className={`dot ${e.healthy ? "ok" : "bad"}`} />
                      <span className={e.healthy ? "" : "badc"}>{e.healthy ? "Healthy" : "Down"}</span>
                    </span>
                  </td>
                  <td className={`rt ${e.healthy ? "" : "t3"}`}>{e.healthy && e.latencyMs != null ? `${Math.round(e.latencyMs)} ms` : "—"}</td>
                  <td>
                    <Spark vals={series(e.key)} max={allMax} label={`${e.name} latency`} color={e.healthy ? "var(--accent)" : "var(--bad)"} />
                  </td>
                  <td className="t2">{profiles(e)}</td>
                  <td className={`rt ${e.bytesToday ? "" : "t3"}`}>{bytes(e.bytesToday)}</td>
                  <td className="rt">
                    {ep?.key === e.key ? (
                      <span className="tag acc">In use · {o.selector.selected.split("-")[1]}</span>
                    ) : r.on ? (
                      <span className="tag acc">Selected</span>
                    ) : !e.healthy && e.nextCheck ? (
                      <span className="t3">Next check {hm(e.nextCheck)}</span>
                    ) : null}
                  </td>
                </>,
              );
            })}
            {row(
              rows[rows.length - 1],
              <>
                <td>
                  <span style={{ display: "inline-flex", gap: 8, alignItems: "center" }}>
                    <Icon n="globe" s={15} />
                    <b style={{ fontWeight: 700 }}>Direct</b>
                    <span className="t3">ISP</span>
                  </span>
                </td>
                <td>
                  <span style={{ display: "inline-flex", gap: 7, alignItems: "center" }}>
                    <span className="dot ok" />
                    OK
                  </span>
                </td>
                <td className="rt">{o.direct.latencyMs != null ? `${o.direct.latencyMs.toFixed(0)} ms` : "—"}</td>
                <td>
                  <Spark vals={series("direct")} max={allMax} color="var(--series-up)" label="Direct latency" />
                </td>
                <td className="t3">ISP, no tunnel</td>
                <td className="rt">{bytes(o.direct.bytesToday)}</td>
                <td className="rt t3">{rows[rows.length - 1].on ? <span className="tag acc">Selected</span> : `${viaDirect} devices`}</td>
              </>,
            )}
          </tbody>
        </table>
      </div>
      <div className="t3" style={{ display: "flex", gap: 24, padding: "10px 14px", borderTop: "1px solid var(--line)", fontSize: 12, flexWrap: "wrap" }}>
        {[...o.exits.filter((e) => e.healthy).map((e) => [e.name, e.key]), ["Direct", "direct"]].map(([n, k]) => {
          const s = stats(series(k));
          return <span key={k}>{n}: {s ? `avg ${s.avg.toFixed(0)} ms · min ${s.min.toFixed(0)} · max ${s.max.toFixed(0)}` : "—"}</span>;
        })}
        {o.exits
          .filter((e) => !e.healthy && e.lastError)
          .map((e) => (
            <span key={e.key} className="badc">
              {e.name} last error: {plainError(e.lastError)}
            </span>
          ))}
        <span style={{ marginLeft: "auto" }}>
          {samples.length ? `${samples.length} probes sampled ${hm(samples[0].at)}–${hm(samples[samples.length - 1].at)}` : hist.error ? "Latency history unavailable" : "No latency history yet"}
        </span>
      </div>
    </section>
  );
}

// ---------- gateway failover ----------

const STATE_TEXT: Record<string, string> = {
  MASTER: "Junction carries traffic",
  BACKUP: "Host covers · direct, no VPN",
  FAULT: "Health check failed",
  STOP: "keepalived stopped",
};
const cap = (s: string) => s[0] + s.slice(1).toLowerCase();

function Failover({ o }: { o: Overview }) {
  const g = o.gateway;
  const now = Date.parse(o.now);
  return (
    <section className="pn c12" aria-labelledby="gf-h">
      <div className="hd">
        <h2 id="gf-h">Gateway failover</h2>
        <div className="r">keepalived · VRRP</div>
      </div>
      <div className="bd">
        {!g ? (
          <div className="note">Failover isn't set up on this gateway.</div>
        ) : (
          <>
            {g.role !== "master" && (
              <div className="line-alert" style={{ marginTop: 0, marginBottom: 10 }} role="status">
                <span className="dot bad" />
                {g.role === "stopped" ? "Failover isn't running." : "The backup on the Proxmox server is covering: internet works, VPN is paused until this gateway is healthy again."}
              </div>
            )}
            <table className="tb compact">
              <thead>
                <tr>
                  <th>Role</th>
                  <th>Host</th>
                  <th>State</th>
                  <th>Since</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>Main</td>
                  <td>Junction {gatewayShort()}</td>
                  <td>
                    <span style={{ display: "inline-flex", gap: 7, alignItems: "center" }}>
                      <span className={`dot ${g.role === "master" ? "ok" : g.role === "stopped" ? "stop" : "bad"}`} />
                      {g.role[0].toUpperCase() + g.role.slice(1)}
                    </span>
                  </td>
                  <td className="t2">{g.since ? `${dmy(g.since)} ${hm(g.since)}` : "—"}</td>
                </tr>
                <tr>
                  <td>Backup</td>
                  <td>{backupLabel()}</td>
                  <td>
                    <span style={{ display: "inline-flex", gap: 7, alignItems: "center" }}>
                      <span className={`dot ${g.role === "master" ? "" : "warn"}`} />
                      {g.role === "master" ? "Standby" : g.role === "stopped" ? "Unknown" : "Covering"}
                    </span>
                  </td>
                  <td className="t3">{g.role === "master" ? "—" : g.since ? `${dmy(g.since)} ${hm(g.since)}` : "—"}</td>
                </tr>
              </tbody>
            </table>
            <div style={{ margin: "14px 0 4px" }}>
              <b style={{ fontSize: 12.5 }}>State of {gatewayShort()}</b>
              <span className="t3" style={{ marginLeft: 8, fontSize: 12 }}>
                last {g.events.length} changes
              </span>
            </div>
            <StateStrip events={g.events} now={now} />
            <table className="tb compact" style={{ marginTop: 10 }}>
              <tbody>
                {g.events.slice(0, 9).map((e, i) => (
                  <tr key={i}>
                    <td className="t2" style={{ width: 120 }}>
                      {dmy(e.at)} {hms(e.at)}
                    </td>
                    <td>
                      <span style={{ display: "inline-flex", gap: 7, alignItems: "center" }}>
                        <span className={`dot ${stateDot(e.state)}`} />
                        {cap(e.state)}
                      </span>
                    </td>
                    <td className="t3">{STATE_TEXT[e.state] ?? ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </div>
    </section>
  );
}
