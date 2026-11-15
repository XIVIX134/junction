import { useState, type ReactNode } from "react";
import type { Device, Overview } from "../api/types";
import { effVpn, isRouter, limited, onVpn, route, status, totalToday } from "../clients";
import { ClientPanel, type Tab } from "../components/ClientPanel";
import { NeedOverview } from "../components/Shell";
import { useDeviceActions, useDeviceControlState, withPending } from "../deviceControl";
import { bytes, rate } from "../fmt";
import { deviceHref, navigate, useLocation } from "../router";
import { Icon, kindOf, Sw } from "../ui";

export function Clients({ id, tab }: { id: string | null; tab: Tab }) {
  return <NeedOverview>{(o) => <ClientsView o={o} id={id} tab={tab} />}</NeedOverview>;
}

type F = "all" | "online" | "vpn" | "limited" | "bypass";
const FILTERS: [F, string, (d: Device) => boolean][] = [
  ["all", "All", (d) => !isRouter(d)],
  ["online", "Online", (d) => d.online && !isRouter(d)],
  ["vpn", "On VPN", onVpn],
  ["limited", "Limited", limited],
  ["bypass", "Bypassing", (d) => !d.usesGateway && !isRouter(d)],
];
type ColKey = "name" | "status" | "ip" | "route" | "down" | "up" | "today" | "vpn";
const COLS: [ColKey, string, (d: Device) => string | number | boolean, boolean?][] = [
  ["name", "Name", (d) => d.name.toLowerCase()],
  ["status", "Status", (d) => (d.online ? 0 : 1)],
  ["ip", "IP", (d) => +d.ip.split(".")[3]],
  ["route", "Route", (d) => route(d).t],
  ["down", "Down now", (d) => d.rateDown, true],
  ["up", "Up now", (d) => d.rateUp, true],
  ["today", "Today", (d) => (d.usesGateway ? totalToday(d) : -1), true],
  ["vpn", "VPN", (d) => effVpn(d)],
];

function ClientsView({ o, id, tab }: { o: Overview; id: string | null; tab: Tab }) {
  const { query } = useLocation();
  const q = (query.get("q") ?? "").toLowerCase();
  const [filter, setFilter] = useState<F>("all");
  const [group, setGroup] = useState(() => query.get("group") ?? "");
  const [sort, setSort] = useState<[ColKey, number]>(["today", -1]);
  const ctl = useDeviceControlState();
  const act = useDeviceActions();
  const devs = o.devices.map((d) => withPending(d, ctl));
  const f = FILTERS.find((x) => x[0] === filter)![2];
  const c = COLS.find((x) => x[0] === sort[0])!;
  const rows = devs
    .filter(f)
    .filter((d) => !group || (d.group || "Ungrouped") === group)
    .filter((d) => !q || [d.name, d.ip, d.mac ?? "", d.group ?? "", d.vendor ?? "", d.autoName ?? "", ...(d.links ?? []).flatMap((l) => [l.ip, l.mac, l.name])].join(" ").toLowerCase().includes(q))
    .sort((a, b) => {
      const x = c[2](a);
      const y = c[2](b);
      return (x > y ? 1 : x < y ? -1 : 0) * sort[1] || a.name.localeCompare(b.name);
    });
  const groups = [...new Set(devs.filter((d) => !isRouter(d)).map((d) => d.group || "Ungrouped"))].sort();
  const known = devs.filter((d) => !isRouter(d));
  const qs = query.toString();
  const open = (devId: string, t: Tab = id ? tab : "overview") => navigate(`${deviceHref(devId)}/${t}${qs ? `?${qs}` : ""}`, { replace: !!id });
  const close = () => navigate(`/clients${qs ? `?${qs}` : ""}`, { replace: true });

  // Group-wide VPN, when one real group is picked
  const gDevs = group && group !== "Ungrouped" ? devs.filter((d) => d.group === group && d.controllable) : [];
  const gOn = gDevs.length > 0 && gDevs.every((d) => d.vpn);
  const gBusy = ctl.busy.has(`group:${group}`);
  const gErr = ctl.errors.get(`group:${group}`);

  const vpnSwitch = (d: Device) => (
    <Sw
      on={!!d.controllable && effVpn(d)}
      label={`VPN, ${d.name}`}
      disabled={!d.controllable}
      reason={d.controllable ? undefined : "Not controllable: bypasses the gateway"}
      busy={ctl.busy.has(d.id)}
      onChange={(on) => act.setVpn(d, on)}
    />
  );

  return (
    <div className="page">
      <div className={id ? "with-side" : ""}>
        <div className="ph">
          <h1>Clients</h1>
          <span className="meta">
            {known.filter((d) => d.online).length} online · {known.length} known
          </span>
        </div>
        <div className="toolbar">
          <div className="seg" role="group" aria-label="Filter clients">
            {FILTERS.map(([k, l, ff]) => (
              <button key={k} type="button" aria-pressed={filter === k} className={filter === k ? "on" : ""} onClick={() => setFilter(k)}>
                {l}
                <span className="c">{devs.filter(ff).length}</span>
              </button>
            ))}
          </div>
          <select className="inp" style={{ width: 150 }} aria-label="Group" value={group} onChange={(e) => setGroup(e.target.value)}>
            <option value="">All groups</option>
            {groups.map((g) => (
              <option key={g} value={g}>
                {g}
              </option>
            ))}
          </select>
          {gDevs.length > 0 && (
            <span className="hide-m" style={{ display: "flex", gap: 8, alignItems: "center" }}>
              <span className="t2">VPN for {group}</span>
              <Sw on={gOn} label={`Group VPN, ${group}`} busy={gBusy} onChange={(on) => act.setGroupVpn(group, devs.filter((d) => d.group === group), on)} />
              {gErr && <span className="err">{gErr}</span>}
            </span>
          )}
          {q && (
            <span className="tag acc" style={{ gap: 6 }}>
              “{query.get("q")}”
              <button type="button" className="linkbtn" aria-label="Clear search" onClick={() => navigate(id ? `${deviceHref(id)}/${tab}` : "/clients", { replace: true })}>
                <Icon n="x" s={10} />
              </button>
            </span>
          )}
          <span className="t3 hide-m" style={{ marginLeft: "auto", fontSize: 12, whiteSpace: "nowrap" }}>
            Rates: live, 3 s average
          </span>
        </div>

        <div className="pn ctable">
          <table className="tb">
            <thead>
              <tr>
                {COLS.map(([k, l, , num]) => (
                  <th key={k} className={`s ${num ? "rt" : ""}`} style={k === "vpn" ? { width: 60 } : undefined} aria-sort={sort[0] === k ? (sort[1] > 0 ? "ascending" : "descending") : undefined}>
                    <button
                      type="button"
                      className="sortb"
                      onClick={() => setSort([k, sort[0] === k ? -sort[1] : num ? -1 : 1])}
                    >
                      {l}
                      {sort[0] === k && <span className="ar">{sort[1] > 0 ? "↑" : "↓"}</span>}
                    </button>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.length ? (
                rows.map((d) => {
                  const r = route(d);
                  const s = status(d, [], o.now);
                  const gw = d.usesGateway;
                  return (
                    <tr key={d.id} className={`rw ${id === d.id ? "sel" : ""} ${d.controllable ? "" : "dim"}`} onClick={() => open(d.id)}>
                      <td className="nm">
                        <Icon n={kindOf(d)} s={16} />
                        <a
                          href={`${deviceHref(d.id)}/overview`}
                          style={{ color: d.controllable ? "var(--text)" : "var(--text2)" }}
                          onClick={(e) => {
                            e.preventDefault();
                            e.stopPropagation();
                            open(d.id);
                          }}
                        >
                          {d.name}
                        </a>
                        {d.links && d.links.length > 0 && (
                          <span className="t3" title="Linked connections">
                            <Icon n="link" s={13} title="Linked connections" />
                          </span>
                        )}
                        {d.group && <span className="tag">{d.group}</span>}
                      </td>
                      <td>
                        <span style={{ display: "inline-flex", gap: 7, alignItems: "center" }}>
                          <span className={`dot ${s.c}`} />
                          <span className={s.c ? "" : "t3"}>{s.t}</span>
                        </span>
                      </td>
                      <td className="t2">{d.ip}</td>
                      <td className={r.c}>{r.t}</td>
                      <td className="rt">{gw ? rate(d.rateDown) : <span className="t3">—</span>}</td>
                      <td className="rt">{gw ? rate(d.rateUp) : <span className="t3">—</span>}</td>
                      <td className="rt">{gw ? bytes(totalToday(d)) : <span className="t3">Not counted</span>}</td>
                      <td onClick={(e) => e.stopPropagation()}>{vpnSwitch(d)}</td>
                    </tr>
                  );
                })
              ) : (
                <tr>
                  <td colSpan={8} style={{ height: 80, textAlign: "center" }} className="t3">
                    No clients match this filter.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
          {Array.from(ctl.errors.entries())
            .filter(([k]) => rows.some((d) => d.id === k))
            .map(([k, v]) => (
              <div key={k} className="err" role="alert" style={{ padding: "8px 14px", borderTop: "1px solid var(--line)" }}>
                {rows.find((d) => d.id === k)?.name}: {v}
              </div>
            ))}
        </div>

        <div className="pn plist">
          <PhoneList rows={rows} open={open} vpnSwitch={vpnSwitch} />
        </div>
      </div>
      {id && <ClientPanel id={id} tab={tab} o={o} onTab={(t) => open(id, t)} onClose={close} />}
    </div>
  );
}

function PhoneList({ rows, open, vpnSwitch }: { rows: Device[]; open: (id: string) => void; vpnSwitch: (d: Device) => ReactNode }) {
  if (!rows.length) return <div className="empty">No clients match this filter.</div>;
  const a = rows.filter((d) => d.controllable);
  const b = rows.filter((d) => !d.controllable);
  const row = (d: Device) => {
    const r = route(d);
    const s = status(d);
    return (
      <div key={d.id} className={`pr ${d.controllable ? "" : "dim"}`} onClick={() => open(d.id)}>
        <Icon n={kindOf(d)} s={18} />
        <div className="tx">
          <div className="n">
            <a
              href={`${deviceHref(d.id)}/overview`}
              onClick={(e) => {
                e.preventDefault();
                e.stopPropagation();
                open(d.id);
              }}
            >
              {d.name}
            </a>
            {d.group && <span className="tag">{d.group}</span>}
          </div>
          <div className="s">
            <span className={`dot ${s.c}`} style={{ width: 6, height: 6, marginRight: 5 }} />
            {d.ip} · {r.t}
          </div>
        </div>
        {d.usesGateway && (
          <div className="rate">
            ↓ {rate(d.rateDown)}
            <div className="t3">{bytes(totalToday(d))} today</div>
          </div>
        )}
        {d.controllable && <span onClick={(e) => e.stopPropagation()}>{vpnSwitch(d)}</span>}
      </div>
    );
  };
  return (
    <>
      {a.length > 0 && <div className="gh">Through the gateway · {a.length}</div>}
      {a.map(row)}
      {b.length > 0 && <div className="gh">Bypasses gateway · {b.length} · not counted, not controllable</div>}
      {b.map(row)}
    </>
  );
}
