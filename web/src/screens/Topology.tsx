import { useLayoutEffect, useRef, type ReactNode } from "react";
import type { Overview } from "../api/types";
import { gatewayShort, isRouter, route, selectedEndpoint, totalToday } from "../clients";
import { ClientPanel, type Tab } from "../components/ClientPanel";
import { NeedOverview } from "../components/Shell";
import { bytes, hm, rate } from "../fmt";
import { navigate, useLocation } from "../router";
import { Icon, kindOf } from "../ui";
import { backupLabel } from "../site";

export function Topology() {
  return <NeedOverview>{(o) => <TopologyView o={o} />}</NeedOverview>;
}

type NodeT = { tip?: string; x: number; y: number; w: number; icon: string; name: string; sub: string; dot: string; cls?: string; id?: string; go?: string };
type LineT = { d: string; stroke?: string; dash?: string; op?: number };
type LabelT = { x: number; y: number; t: ReactNode; cls?: string; c?: string };

function TopologyView({ o }: { o: Overview }) {
  const { query } = useLocation();
  const sel = query.get("client");
  const tab = (query.get("tab") as Tab) || "overview";
  const open = (id: string, t: Tab = tab) => navigate(`/topology?client=${encodeURIComponent(id)}&tab=${t}`, { replace: !!sel });

  const nodes: NodeT[] = [];
  const lines: LineT[] = [];
  const labels: LabelT[] = [];
  const N = (x: number, y: number, n: Omit<NodeT, "x" | "y">) => {
    nodes.push({ x, y, ...n });
    return { x, y, w: n.w, h: 42 };
  };
  const bc = (n: { x: number; y: number; w: number; h: number }): [number, number] => [n.x + n.w / 2, n.y + n.h];
  const tc = (n: { x: number; y: number; w: number }): [number, number] => [n.x + n.w / 2, n.y];
  const elbow = (a: [number, number], b: [number, number], my = (a[1] + b[1]) / 2, extra: Partial<LineT> = {}) =>
    lines.push({ d: `M${a[0]} ${a[1]}V${my}H${b[0]}V${b[1]}`, ...extra });

  const devs = o.devices;
  const nodeTip = (n: NodeT) => {
    const d = n.id ? devs.find((x) => x.id === n.id) : undefined;
    if (!d) return `${n.name}\n${n.sub}`;
    const lines = [d.name, `${d.ip} · ${d.online ? "online" : `offline, last seen ${hm(d.lastSeen)}`}${d.group ? ` · ${d.group}` : ""}`];
    if (d.usesGateway) lines.push(`Now ↓ ${rate(d.rateDown)} ↑ ${rate(d.rateUp)} · today ${bytes(totalToday(d))}`, `${route(d).t}${d.controllable ? "" : " · not controllable"}`);
    else lines.push("Bypasses the gateway: not counted");
    return lines.join("\n");
  };
  const router = devs.find(isRouter);
  const g = o.gateway;
  const ep = selectedEndpoint(o);
  const profile = o.selector.selected.split("-")[1] ?? "";
  const X = 540;
  const NW = 176;

  const inet = N(X - 88, 16, { w: NW, icon: "globe", name: "Internet", sub: `ISP · ${o.direct.latencyMs != null ? o.direct.latencyMs.toFixed(0) : "—"} ms direct`, dot: "ok", go: "/internet" });
  const rt = N(X - 88, 100, {
    w: NW,
    icon: "router",
    name: router ? `${router.name.replace(/\s*\(.*\)$/, "")} .1${/ont/i.test(router.name) ? " (ONT)" : ""}` : "Router .1",
    sub: router ? `${router.ip}${router.vendor ? ` · ${router.vendor.split(" ")[0]}` : ""}` : "192.168.1.1",
    dot: router?.online === false ? "" : "ok",
    id: router?.id,
  });
  elbow(bc(inet), tc(rt));
  const gwRole = g?.role ?? "master";
  const gwN = N(X - 95, 196, {
    w: 190,
    icon: "gateway",
    name: `Junction gateway ${gatewayShort()}`,
    sub: `${gwRole[0].toUpperCase() + gwRole.slice(1)}${g?.since ? ` · since ${hm(g.since)}` : ""}`,
    dot: gwRole === "master" ? "ok" : "bad",
    cls: "gw",
    go: "/internet",
  });
  elbow(bc(rt), tc(gwN));
  if (g) {
    const covering = gwRole === "backup" || gwRole === "fault";
    const bk = N(X + 150, 196, {
      w: 180,
      icon: "server",
      name: "Backup .3",
      sub: covering ? `Covering · ${backupLabel()}` : `Standby · ${backupLabel()}`,
      dot: covering ? "warn" : "",
      cls: covering ? "" : "ghost",
      go: "/internet",
    });
    lines.push({ d: `M${X + 95} ${gwN.y + 21}H${bk.x}`, dash: "3 3" });
    labels.push({ x: X + 104, y: gwN.y + 2, t: "VRRP" });
  }

  // VPN exits, left of the gateway
  const exX = X - 420;
  const jx = X - 160;
  labels.push({ x: exX, y: 140, t: <><b>VPN exits</b>Hysteria2</>, cls: "glabel" });
  o.exits.forEach((e, i) => {
    const y = 164 + i * 64;
    const inUse = ep?.key === e.key;
    N(exX, y, { w: NW, icon: "tunnel", name: e.name, sub: e.healthy ? `Healthy · ${Math.round(e.latencyMs ?? 0)} ms` : `Down · ${e.lastSuccess ? `since ${hm(e.lastSuccess)}` : "no probe reply"}`, dot: e.healthy ? "ok" : "bad", go: "/internet" });
    const yy = gwN.y + 15 + Math.min(i, 2) * 6;
    lines.push({
      d: `M${X - 95} ${yy}H${jx}V${y + 21}H${exX + NW}`,
      stroke: inUse ? "var(--accent)" : e.healthy ? "var(--edge)" : "var(--bad)",
      dash: inUse ? "4 3" : "2 4",
      op: inUse ? 1 : 0.7,
    });
    if (inUse) labels.push({ x: exX + NW + 10, y: y + 4, t: `in use · ${profile}`, c: "acc" });
    else if (!e.healthy) labels.push({ x: exX + NW + 10, y: y + 26, t: "tunnel down", c: "badc" });
  });

  // gateway clients by group
  const gcl = devs.filter((d) => d.usesGateway && !isRouter(d));
  const gName = (d: (typeof devs)[number]) => d.group || "Ungrouped";
  const groups = [...new Set(gcl.map(gName))].sort((a, b) => gcl.filter((d) => gName(d) === b).length - gcl.filter((d) => gName(d) === a).length || a.localeCompare(b));
  const CW = 200;
  const colX = groups.map((_, i) => X - (groups.length * CW) / 2 + i * CW);
  const gy = 304;
  const busY = 276;
  groups.forEach((gr, gi) => {
    const x = colX[gi];
    const members = gcl.filter((d) => gName(d) === gr).sort((a, b) => totalToday(b) - totalToday(a));
    labels.push({ x, y: gy, t: <><b>{gr}</b>{members.length}</>, cls: "glabel" });
    elbow(bc(gwN), [x + 8, gy - 4], busY);
    members.forEach((d, i) => {
      const y = gy + 26 + i * 54;
      N(x + 16, y, { w: NW, icon: kindOf(d), name: d.name, sub: `${d.ip} · ${route(d).t}`, dot: d.online ? "ok" : "", id: d.id, cls: d.online ? "" : "off" });
      lines.push({ d: `M${x + 8} ${gy + 14}V${y + 21}H${x + 16}` });
    });
  });

  // devices that bypass the gateway hang off the router
  const by = devs
    .filter((d) => !d.usesGateway && !isRouter(d))
    .sort((a, b) => Number(b.online) - Number(a.online) || a.ip.localeCompare(b.ip, undefined, { numeric: true }));
  const rightEdge = Math.max(X + 330, ...colX.map((x) => x + CW));
  const bx = rightEdge + 70;
  const bTop = 140;
  labels.push({ x: bx, y: bTop, t: <><b>Bypasses gateway</b>{by.length} · default route is .1</>, cls: "glabel" });
  if (by.length) lines.push({ d: `M${X + 88} ${rt.y + 21}H${bx + 8}V${bTop - 4}` });
  by.forEach((d, i) => {
    const y = bTop + 26 + i * 54;
    N(bx + 16, y, { w: 196, icon: kindOf(d), name: d.name, sub: `${d.ip}${d.online ? (d.group ? ` · ${d.group}` : "") : " · offline"}`, dot: d.online ? "ok" : "", id: d.id, cls: `ghost${d.online ? "" : " off"}` });
    lines.push({ d: `M${bx + 8} ${bTop + 14}V${y + 21}H${bx + 16}`, stroke: "var(--edge-bypass)", dash: "3 3" });
  });

  // normalise so nothing sits left of 0
  const minX = Math.min(...nodes.map((n) => n.x), ...labels.map((l) => l.x));
  const dx = minX < 0 ? -minX + 4 : 0;
  const W = bx + 16 + 196 + 4 + dx;
  const maxMembers = Math.max(0, ...groups.map((gr) => gcl.filter((d) => gName(d) === gr).length));
  const H = Math.max(gy + 26 + maxMembers * 54, bTop + 26 + by.length * 54, 300) + 4;
  const online = devs.filter((d) => d.online && !isRouter(d)).length;
  const box = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = box.current;
    if (el && el.scrollWidth > el.clientWidth) el.scrollLeft = X + dx - el.clientWidth / 2;
  }, [dx]);

  return (
    <div className="page">
      <div className={sel ? "with-side" : ""}>
        <div className="ph">
          <h1>Topology</h1>
          <span className="meta">
            {online} clients online · {gcl.length} through the gateway · {by.length} bypass
          </span>
          <div className="right legend hide-m">
            <span><svg width="22" height="6" aria-hidden="true"><line x1="0" x2="22" y1="3" y2="3" stroke="var(--btn-hover-line)" /></svg>LAN</span>
            <span><svg width="22" height="6" aria-hidden="true"><line x1="0" x2="22" y1="3" y2="3" stroke="var(--accent)" strokeDasharray="4 3" /></svg>VPN tunnel</span>
            <span><svg width="22" height="6" aria-hidden="true"><line x1="0" x2="22" y1="3" y2="3" stroke="var(--edge-bypass)" strokeDasharray="3 3" /></svg>Bypasses gateway</span>
            <span><i className="dot ok" />Online</span>
            <span><i className="dot bad" />Down</span>
            <span><i className="dot" />Offline</span>
          </div>
        </div>
        <div className="pn" ref={box} style={{ padding: "24px 12px", overflow: "auto" }}>
          <div className="topo" style={{ width: W, height: H, margin: "0 auto" }} role="group" aria-label="Network map">
            <svg width={W} height={H} style={{ position: "absolute", inset: 0 }} aria-hidden="true">
              <g transform={`translate(${dx} 0)`}>
                {lines.map((l, i) => (
                  <path key={i} d={l.d} fill="none" stroke={l.stroke ?? "var(--edge)"} strokeWidth="1" strokeDasharray={l.dash} opacity={l.op ?? 1} />
                ))}
              </g>
            </svg>
            {labels.map((l, i) => (
              <div key={i} className={`${l.cls ?? "elabel"} ${l.c ?? ""}`} style={{ left: l.x + dx, top: l.y }}>
                {l.t}
              </div>
            ))}
            {nodes.map((n, i) => (
              <button
                key={i}
                type="button"
                className={`node ${n.cls ?? ""} ${n.id && n.id === sel ? "sel" : ""}`}
                style={{ left: n.x + dx, top: n.y, width: n.w }}
                title={nodeTip(n)}
                aria-label={`${n.name}, ${n.sub}`}
                onClick={() => (n.id ? open(n.id) : n.go ? navigate(n.go) : undefined)}
              >
                <Icon n={n.icon} s={16} />
                <div className="tx">
                  <div className="n">{n.name}</div>
                  <div className="i">{n.sub}</div>
                </div>
                <span className={`dot ${n.dot}`} />
              </button>
            ))}
          </div>
        </div>
      </div>
      {sel && <ClientPanel id={sel} tab={tab} o={o} onTab={(t) => open(sel, t)} onClose={() => navigate("/topology", { replace: true })} />}
    </div>
  );
}
