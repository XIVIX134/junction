import { BusyHours, StatsRow } from "./DashboardViz";
import { useEffect, useState } from "react";
import { TZ } from "../format";
import { backupLabel } from "../site";
import { api } from "../api/client";
import type { Device, Overview } from "../api/types";
import { gatewayShort, isRouter, onVpn, selectedEndpoint, totalToday } from "../clients";
import { NeedOverview } from "../components/Shell";
import { feed, KIND_LABEL, useEventsVersion } from "../events";
import { bytes, dmy, hm, hms } from "../fmt";
import { useFetch } from "../live";
import { plainError } from "../model";
import { deviceHref, Link, navigate } from "../router";
import { HourChart, Icon, kindOf, StateStrip } from "../ui";
import { bundleView, UsedBar } from "../components/BundlePanel";

export function Dashboard() {
  return <NeedOverview>{(o) => <DashboardView o={o} />}</NeedOverview>;
}

const openClient = (id: string, tab = "overview") => navigate(`${deviceHref(id)}/${tab}`);
const DAY = 864e5;

type Alert = { key: string; dot: string; title: string; detail: string; go: () => void };

function alertsOf(o: Overview): Alert[] {
  const now = Date.parse(o.now);
  const out: Alert[] = [];
  // The bundle running low (Junction's own count, whole packets).
  const b = o.bundle;
  if (b && b.size > 0 && b.left / b.size <= 0.1)
    out.push({
      key: "bundle-low",
      dot: b.left <= 0 ? "bad" : "warn",
      title: b.left <= 0 ? "Bundle used up" : `Bundle ${Math.max(0, Math.round((b.left / b.size) * 100))}% left`,
      detail: `${(Math.max(0, b.left) / 1e9).toFixed(1)} GB left${b.runsOut ? ` · runs out ~${dmy(b.runsOut)} at this pace` : ""}`,
      go: () => navigate("/internet#renew"),
    });
  for (const e of o.exits)
    if (!e.healthy)
      out.push({
        key: `exit-${e.key}`,
        dot: "bad",
        title: `${e.name} exit down`,
        detail: `${e.lastSuccess ? `Last worked ${hm(e.lastSuccess)}` : "No successful probe yet"}${e.nextCheck ? ` · next check ${hm(e.nextCheck)}` : ""}`,
        go: () => navigate("/internet"),
      });
  const g = o.gateway;
  if (g && g.enabled) {
    if (g.role !== "master")
      out.push({
        key: "gw-now",
        dot: "bad",
        title: g.role === "stopped" ? "Failover isn't running" : "Backup is covering",
        detail: g.role === "stopped" ? `since ${hm(g.since)}` : `Internet works direct, VPN paused · since ${hm(g.since)}`,
        go: () => navigate("/internet"),
      });
    const recent = g.events.filter((e) => (e.state === "FAULT" || e.state === "BACKUP") && now - Date.parse(e.at) < DAY);
    const faults = recent.filter((e) => e.state === "FAULT");
    const n = faults.length || recent.filter((e) => e.state === "BACKUP").length;
    if (n > 0) {
      const ts = recent.map((e) => Date.parse(e.at)).sort((a, b) => a - b);
      out.push({
        key: "gw-24h",
        dot: "warn",
        title: `Gateway ${gatewayShort()} failed over ${n}×`,
        detail: `${hm(ts[0])}–${hm(ts[ts.length - 1])}${g.role === "master" && g.since ? ` · master since ${hm(g.since)}` : ""}`,
        go: () => navigate("/internet"),
      });
    }
  }
  for (const d of o.devices)
    if (d.nameSource === "ip" && !isRouter(d) && now - Date.parse(d.firstSeen) < DAY)
      out.push({ key: `new-${d.id}`, dot: "warn", title: `New device ${d.ip}`, detail: `Unnamed · first seen ${hm(d.firstSeen)}`, go: () => openClient(d.id, "settings") });
  for (const d of o.devices) {
    const e = d.effective;
    if (!e || !e.rules.length) continue;
    if (e.paused && e.pausedBy === "rule") out.push({ key: `rp-${d.id}`, dot: "warn", title: `${d.name} paused`, detail: `by ${e.rules[0].name}`, go: () => openClient(d.id, "settings") });
    else if (e.speedLimitMbps != null && e.speedLimitMbps !== d.speedLimitMbps)
      out.push({ key: `rl-${d.id}`, dot: "warn", title: `${d.name} slowed to ${e.speedLimitMbps} Mbit/s`, detail: e.rules.map((r) => r.name).join(", "), go: () => openClient(d.id, "settings") });
  }
  return out;
}

function DashboardView({ o }: { o: Overview }) {
  const now = Date.parse(o.now);
  const traffic = useFetch("traffic168", () => api.traffic(168), 60000); // a week: the heatmap, and today against yesterday
  const v = useEventsVersion();
  const ev = useFetch("dash-events", () => api.events({ limit: 8 }), 20000);
  const { reload } = ev;
  useEffect(() => {
    if (v) reload();
  }, [v, reload]);

  const devs = o.devices.filter((d) => !isRouter(d));
  const online = devs.filter((d) => d.online);
  const vpnN = online.filter(onVpn).length;
  const directN = online.filter((d) => d.controllable && !onVpn(d)).length;
  const ncN = online.filter((d) => !d.controllable).length;
  const top = devs.filter((d) => d.usesGateway).sort((a, b) => totalToday(b) - totalToday(a));
  const topMax = Math.max(1, top.length ? totalToday(top[0]) : 1);
  const week = traffic.data?.hours ?? [];
  const hours = week.slice(-24);
  const tDown = hours.reduce((s, h) => s + h.down, 0);
  const tUp = hours.reduce((s, h) => s + h.up, 0);
  const alerts = alertsOf(o);
  const ep = selectedEndpoint(o);
  const sel = o.selector;
  const bypass = devs.filter((d) => !d.usesGateway).length;
  const recent = feed(ev.data ?? [], o).slice(0, 6);
  const g = o.gateway;

  return (
    <div className="page">
      <div className="ph">
        <h1>Dashboard</h1>
        <span className="meta">Home network · {o.devices.find(isRouter)?.ip.replace(/\.1$/, ".0/24") ?? "192.168.1.0/24"}</span>
        <div className="right">
          <span className="t3" style={{ fontSize: 12 }}>
            Times in {TZ.split("/").pop()!.replace(/_/g, " ")}
          </span>
        </div>
      </div>

      <div className="alerts" role="region" aria-label="Alerts">
        {alerts.length ? (
          alerts.map((a) => (
            <button key={a.key} type="button" className="al" onClick={a.go}>
              <span className={`dot ${a.dot}`} />
              <b>{a.title}</b>
              <span className="t2">{a.detail}</span>
            </button>
          ))
        ) : (
          <div className="calm">
            <span className="dot ok" />
            Nothing needs attention.
          </div>
        )}
      </div>

      <StatsRow o={o} hours={week} devices={devs} />

      <div className="grid">
        <section className="pn c4" aria-labelledby="w-inet">
          <div className="hd">
            <h2 id="w-inet">Internet</h2>
            <div className="r">
              <Link href="/internet">Exits</Link>
            </div>
          </div>
          <div className="bd">
            <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
              <span className="big">{ep ? ep.name : "Direct"}</span>
              <span className="t2">
                {sel.selected.split("-")[1] ? `${sel.selected.split("-")[1]} · ` : ""}
                {sel.mode === "auto" ? "Automatic" : sel.mode === "manual" ? "Manual" : "All direct"}
              </span>
            </div>
            <div className="note" style={{ margin: "2px 0 8px" }}>
              {sel.lastSwitch ? `In use since ${hm(sel.lastSwitch)}` : "In use"}
              {sel.reason ? ` · ${sel.reason}` : ""}
            </div>
            {o.exits.map((e) => (
              <div className="lr" key={e.key}>
                <span className={`dot ${e.healthy ? "ok" : "bad"}`} />
                {e.name}
                <span className="t3">VPN</span>
                <span className={`v ${e.healthy ? "" : "badc"}`}>{e.healthy ? `${Math.round(e.latencyMs ?? 0)} ms` : "Down"}</span>
              </div>
            ))}
            <div className="lr">
              <span className="dot ok" />
              Direct<span className="t3">ISP</span>
              <span className="v">{o.direct.latencyMs != null ? `${o.direct.latencyMs.toFixed(0)} ms` : "—"}</span>
            </div>
            {o.exits
              .filter((e) => !e.healthy && e.lastError)
              .map((e) => (
                <div className="line-alert" key={e.key}>
                  <span className="dot bad" />
                  <span>
                    {e.name}: {plainError(e.lastError)}
                  </span>
                </div>
              ))}
          </div>
        </section>

        <section className="pn c8" aria-labelledby="w-traffic">
          <div className="hd">
            <h2 id="w-traffic">Traffic</h2>
            <span className="t3 hide-m" style={{ fontSize: 12 }}>
              Last 24 h · hourly average · through the gateway
            </span>
            <div className="r">
              <span style={{ display: "flex", gap: 6, alignItems: "center" }}>
                <i className="ln" style={{ width: 12, height: 2, background: "var(--series-down)", display: "inline-block" }} />
                Download <span className="t2 num">{bytes(tDown)}</span>
              </span>
              <span style={{ display: "flex", gap: 6, alignItems: "center" }}>
                <i className="ln" style={{ width: 12, height: 2, background: "var(--series-up)", display: "inline-block" }} />
                Upload <span className="t2 num">{bytes(tUp)}</span>
              </span>
            </div>
          </div>
          <div className="bd" style={{ padding: "10px 10px 6px" }}>
            <HourChart hours={hours} now={now} h={196} label="Whole-network traffic per hour, today" />
          </div>
        </section>

        <section className="pn c3" aria-labelledby="w-clients">
          <div className="hd">
            <h2 id="w-clients">Clients</h2>
            <div className="r">
              <Link href="/clients">All</Link>
            </div>
          </div>
          <div className="bd">
            <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
              <span className="big">{online.length}</span>
              <span className="t2">online</span>
              <span className="t3" style={{ marginLeft: "auto" }}>
                {devs.length} known
              </span>
            </div>
            <div className="bar" style={{ margin: "10px 0 6px" }} role="img" aria-label={`${vpnN} on VPN, ${directN} direct, ${ncN} not controllable`}>
              <i style={{ width: `${(vpnN / Math.max(1, online.length)) * 100}%`, background: "var(--accent)" }} />
              <i style={{ width: `${(directN / Math.max(1, online.length)) * 100}%`, background: "var(--series-direct)" }} />
              <i style={{ width: `${(ncN / Math.max(1, online.length)) * 100}%`, background: "var(--series-nc)" }} />
            </div>
            <div className="lr"><i className="dot acc" />On VPN<span className="v">{vpnN}</span></div>
            <div className="lr"><i className="dot direct" />Direct<span className="v">{directN}</span></div>
            <div className="lr"><i className="dot nc" />Not controllable<span className="v">{ncN}</span></div>
            <div className="note" style={{ marginTop: 6 }}>
              Not controllable: traffic goes to router .1, not through the gateway.
            </div>
          </div>
        </section>

        <TopWidget top={top} topMax={topMax} bypass={bypass} />

        <BundleWidget o={o} />

        <BusyHours hours={week} now={now} />

        {g && (
          <section className="pn c4" aria-labelledby="w-gw">
            <div className="hd">
              <h2 id="w-gw">Gateway</h2>
              <div className="r">Floating IP {gatewayShort()}</div>
            </div>
            <div className="bd">
              <div className="lr">
                <span className={`dot ${g.role === "master" ? "ok" : g.role === "stopped" ? "stop" : "bad"}`} />
                Main · Junction {gatewayShort()}
                <span className={`v ${g.role === "master" ? "okc" : "badc"}`}>{cap(g.role)}</span>
              </div>
              <div className="lr">
                <span className={`dot ${g.role === "master" ? "" : "warn"}`} />
                Backup · {backupLabel()}
                <span className={`v ${g.role === "master" ? "t2" : "warnc"}`}>{g.role === "master" ? "Standby" : g.role === "stopped" ? "—" : "Covering"}</span>
              </div>
              <div style={{ marginTop: 12 }}>
                <StateStrip events={g.events} now={now} />
              </div>
              <GatewayNote o={o} />
            </div>
          </section>
        )}

        <section className={`pn ${g ? "c12" : "c4"}`} aria-labelledby="w-ev">
          <div className="hd">
            <h2 id="w-ev">Recent events</h2>
            <div className="r">
              <Link href="/activity">Activity</Link>
            </div>
          </div>
          <div className="scroll-x">
            <table className="tb compact">
              <tbody>
                {recent.map((e) => (
                  <tr key={e.id}>
                    <td className="t3 num" style={{ width: 76 }}>
                      {hms(e.at)}
                    </td>
                    <td style={{ width: 84 }} className={e.kind === "limit" ? "warnc" : "t2"}>
                      {KIND_LABEL[e.kind] ?? e.kind}
                    </td>
                    <td className="wrap">{e.text}</td>
                  </tr>
                ))}
                {!recent.length && (
                  <tr>
                    <td className="t3">{ev.error ?? "Nothing has happened yet."}</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
  );
}

const cap = (s: string) => s[0].toUpperCase() + s.slice(1);

type Period = "today" | "week" | "month";
const PERIODS: [Period, string][] = [
  ["today", "Today"],
  ["week", "7 days"],
  ["month", "30 days"],
];
const usedIn = (d: Device, p: Period) =>
  p === "today" ? totalToday(d) : p === "week" ? (d.weekDown ?? 0) + (d.weekUp ?? 0) : (d.monthDown ?? 0) + (d.monthUp ?? 0);

// Top usage, by client or by group. Groups add up every device in them that
// uses the gateway; devices that bypass it aren't counted anywhere.
function TopWidget({ top, topMax, bypass }: { top: Device[]; topMax: number; bypass: number }) {
  const [by, setBy] = useState<"clients" | "groups">(() => (localStorage.getItem("dash-top") === "groups" ? "groups" : "clients"));
  const [period, setPeriod] = useState<Period>("today");
  const pick = (v: "clients" | "groups") => {
    setBy(v);
    localStorage.setItem("dash-top", v);
  };
  const groups = [...top.reduce((m, d) => {
    const k = d.group || "";
    const g = m.get(k) ?? { name: k, n: 0, bytes: 0 };
    g.n++;
    g.bytes += usedIn(d, period);
    return m.set(k, g);
  }, new Map<string, { name: string; n: number; bytes: number }>()).values()]
    .filter((g) => g.bytes > 0)
    .sort((a, b) => b.bytes - a.bytes);
  const gMax = Math.max(1, groups[0]?.bytes ?? 1);
  const gSum = groups.reduce((s, g) => s + g.bytes, 0);
  const tSum = top.reduce((s, d) => s + totalToday(d), 0);
  const share = (b: number, of: number) => `${Math.round((b / Math.max(1, of)) * 100)}% of all`;
  return (
    <section className="pn c5" aria-labelledby="w-top">
      <div className="hd">
        <h2 id="w-top">{by === "clients" ? "Top clients today" : "Usage by group"}</h2>
        <div className="r">
          <div className="seg sm" role="group" aria-label="Show usage by">
            {(["clients", "groups"] as const).map((v) => (
              <button key={v} type="button" aria-pressed={by === v} className={by === v ? "on" : ""} onClick={() => pick(v)}>
                {cap(v)}
              </button>
            ))}
          </div>
        </div>
      </div>
      <div className="bd" style={{ padding: "4px 14px 8px" }}>
        {by === "clients" ? (
          <>
            {top.slice(0, 6).map((d) => (
              <button type="button" className="lr" key={d.id} onClick={() => openClient(d.id)}>
                <span className="t2">
                  <Icon n={kindOf(d)} s={15} />
                </span>
                <span style={{ width: 150, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{d.name}</span>
                <span className="bar" style={{ flex: 1, height: 4 }}>
                  <i style={{ width: `${(totalToday(d) / topMax) * 100}%`, background: "var(--accent)" }} />
                </span>
                <span className="v" style={{ width: 74, textAlign: "right" }}>
                  <span className="lv-a">{bytes(totalToday(d))}</span>
                  <span className="lv-b">{share(totalToday(d), tSum)}</span>
                </span>
              </button>
            ))}
            {!top.length && <div className="note" style={{ padding: "10px 0" }}>No traffic through the gateway yet today.</div>}
          </>
        ) : (
          <>
            <div className="seg sm" role="group" aria-label="Period" style={{ margin: "6px 0 4px" }}>
              {PERIODS.map(([k, l]) => (
                <button key={k} type="button" aria-pressed={period === k} className={period === k ? "on" : ""} onClick={() => setPeriod(k)}>
                  {l}
                </button>
              ))}
            </div>
            {groups.map((g) => (
              <button
                type="button"
                className="lr"
                key={g.name || "-"}
                onClick={() => navigate(`/clients?group=${encodeURIComponent(g.name || "Ungrouped")}`)}
              >
                <span style={{ width: 150, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} className={g.name ? "" : "t2"}>
                  {g.name || "No group"}
                  <span className="t3"> · {g.n}</span>
                </span>
                <span className="bar" style={{ flex: 1, height: 4 }}>
                  <i style={{ width: `${(g.bytes / gMax) * 100}%`, background: "var(--accent)" }} />
                </span>
                <span className="v" style={{ width: 74, textAlign: "right" }}>
                  <span className="lv-a">{bytes(g.bytes)}</span>
                  <span className="lv-b">{share(g.bytes, gSum)}</span>
                </span>
              </button>
            ))}
            {!groups.length && <div className="note" style={{ padding: "10px 0" }}>No traffic through the gateway in this period.</div>}
          </>
        )}
        <div className="note" style={{ marginTop: 4 }}>
          {bypass} device{bypass === 1 ? "" : "s"} bypass the gateway; their usage is not counted.
        </div>
      </div>
    </section>
  );
}

function GatewayNote({ o }: { o: Overview }) {
  const g = o.gateway!;
  const now = Date.parse(o.now);
  const faults = g.events.filter((e) => e.state === "FAULT" && now - Date.parse(e.at) < DAY);
  if (g.role !== "master")
    return (
      <div className="line-alert">
        <span className="dot bad" />
        <span>The backup on the Proxmox server is covering: internet works, VPN is paused until this gateway is healthy again.</span>
      </div>
    );
  if (!faults.length)
    return (
      <div className="line-alert">
        <span className="dot ok" />
        <span>No failovers in the last 24 h.</span>
      </div>
    );
  const last = faults.map((e) => Date.parse(e.at)).sort((a, b) => b - a)[0];
  return (
    <div className="line-alert">
      <span className="dot warn" />
      <span>
        {faults.length} fault{faults.length === 1 ? "" : "s"} in the last 24 h, last {hm(last)}. Backup carried traffic direct, no VPN.
      </span>
    </div>
  );
}

function BundleWidget({ o }: { o: Overview }) {
  const v = bundleView(o.bundle, Date.parse(o.now));
  return (
    <section className="pn c4 bcard" aria-labelledby="w-bundle">
      <div className="hd">
        <h2 id="w-bundle">Monthly bundle</h2>
        <div className="r">
          {v?.renewInDays != null && (
            <span>
              renews in {v.renewInDays} day{v.renewInDays === 1 ? "" : "s"}
            </span>
          )}
          <Link href="/internet#renew">Details</Link>
        </div>
      </div>
      <div className="bd">
        {!v ? (
          <>
            <div className="big" style={{ fontSize: 18 }}>
              Log your bundle
            </div>
            <div className="note" style={{ margin: "6px 0 12px" }}>
              Tell Junction when you renewed and how big the bundle is; it will show what's left and when it would run out.
            </div>
            <Link className="btn pri" href="/internet#renew">
              I renewed
            </Link>
          </>
        ) : (
          <>
            <div className={`big ${v.left <= 0 ? "badc" : ""}`}>{gbLeft(v.left)}</div>
            <div className="note">left of {(v.size / 1e9).toFixed(0)} GB</div>
            <UsedBar v={v} compact />
            <div className="lr">
              Used<span className="v t2">{bytes(v.used)}</span>
            </div>
            <div className="lr">
              Pace<span className="v t2">{v.pace ? `${bytes(v.pace)}/day` : "Needs 6 h of data"}</span>
            </div>
            <div className="lr">
              Runs out<span className={`v ${v.earlyDays ? "warnc" : "t2"}`}>{v.runsOut ? `~${dmy(v.runsOut)}` : "—"}</span>
            </div>
          </>
        )}
      </div>
    </section>
  );
}

const gbLeft = (b: number) => `${(b / 1e9).toFixed(1)} GB`;

