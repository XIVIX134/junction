import {
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/base.css";
import { useEffect, useState, useSyncExternalStore } from "react";
import { api, ApiError } from "../api/client";
import type { Device, Overview, Profile, Rule, RuleDraft, RuleThen, RuleWhen } from "../api/types";
import { ScheduleEditor, UsageEditor, validateRule, WhoEditor } from "../components/RuleParts";
import { scheduleText, thenText, usageText, whoText } from "../rules";
import { useProfiles } from "../prefs";
import { Icon } from "../ui";

// Diagram editor for ONE rule: Who → When (conditions) → Then (actions), drawn like Topology's nodes.
// Only nodes connected to Who count when saving. Layout is stored in rule.graph ({ positions, edges }).

type Kind = "who" | "schedule" | "usage" | "pause" | "speed" | "vpn" | "profile";
const CONDITIONS: Kind[] = ["schedule", "usage"];
const ACTIONS: Kind[] = ["pause", "speed", "vpn", "profile"];
const COL = { who: 0, when: 200, then: 400 };
const TITLES: Record<Kind, string> = { who: "Who", schedule: "Schedule", usage: "Data used", pause: "Pause", speed: "Slow down", vpn: "VPN", profile: "VPN profile" };
const ICON: Record<Kind, string> = { who: "who", schedule: "clock", usage: "clock", pause: "rules", speed: "rules", vpn: "tunnel", profile: "tunnel" };
const colOf = (k: Kind) => (k === "who" ? "who" : CONDITIONS.includes(k) ? "when" : "then");
type Pos = Record<string, { x: number; y: number }>;
type Graph = { positions: Pos; edges: { source: string; target: string }[] };
type NodeData = { kind: Kind; text: string; off: boolean };

function useNarrow() {
  const q = "(max-width: 700px)";
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia(q);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    () => window.matchMedia(q).matches,
  );
}

function kindsOf(r: RuleDraft): Kind[] {
  const k: Kind[] = ["who"];
  if (r.when.schedule) k.push("schedule");
  if (r.when.usage) k.push("usage");
  if (r.then.pause) k.push("pause");
  if (r.then.speedMbps != null) k.push("speed");
  if (r.then.vpn != null) k.push("vpn");
  if (r.then.profile) k.push("profile");
  return k;
}
function defaultGraph(kinds: Kind[]): Graph {
  const positions: Pos = { who: { x: COL.who, y: 60 } };
  const conds = kinds.filter((k) => CONDITIONS.includes(k));
  const acts = kinds.filter((k) => ACTIONS.includes(k));
  conds.forEach((k, i) => (positions[k] = { x: COL.when, y: 20 + i * 90 }));
  acts.forEach((k, i) => (positions[k] = { x: COL.then, y: 20 + i * 90 }));
  const edges: Graph["edges"] = [];
  if (conds.length) {
    conds.forEach((c) => edges.push({ source: "who", target: c }));
    conds.forEach((c) => acts.forEach((a) => edges.push({ source: c, target: a })));
  } else acts.forEach((a) => edges.push({ source: "who", target: a }));
  return { positions, edges };
}
function readGraph(r: RuleDraft): Graph {
  const kinds = kindsOf(r);
  const fb = defaultGraph(kinds);
  const g = r.graph as Partial<Graph> | undefined;
  if (!g || !g.positions || !Array.isArray(g.edges)) return fb;
  const positions: Pos = { ...fb.positions };
  for (const k of kinds) if (g.positions[k]) positions[k] = g.positions[k];
  const edges = g.edges.filter((e) => kinds.includes(e.source as Kind) && kinds.includes(e.target as Kind));
  return { positions, edges: edges.length ? edges : fb.edges };
}
function connected(edges: Graph["edges"]) {
  const seen = new Set(["who"]);
  for (let grew = true; grew; ) {
    grew = false;
    for (const e of edges)
      if (seen.has(e.source) && !seen.has(e.target)) {
        seen.add(e.target);
        grew = true;
      }
  }
  return seen;
}
function effectiveDraft(r: RuleDraft, g: Graph): RuleDraft {
  const on = connected(g.edges);
  const when: RuleWhen = { schedule: on.has("schedule") ? r.when.schedule : null, usage: on.has("usage") ? r.when.usage : null };
  const then: RuleThen = {};
  if (on.has("pause") && r.then.pause) then.pause = true;
  if (on.has("speed") && r.then.speedMbps != null) then.speedMbps = r.then.speedMbps;
  if (on.has("vpn") && r.then.vpn != null) then.vpn = r.then.vpn;
  if (on.has("profile") && r.then.profile) then.profile = r.then.profile;
  return { ...r, when, then, graph: g };
}
function nodeText(k: Kind, r: RuleDraft, devices: Device[]) {
  switch (k) {
    case "who":
      return whoText(r.match, devices);
    case "schedule":
      return r.when.schedule ? scheduleText(r.when.schedule) : "";
    case "usage":
      return r.when.usage ? usageText(r.when.usage) : "";
    case "pause":
      return "Blocks the internet";
    case "speed":
      return `Slow to ${r.then.speedMbps ?? "?"} Mbit/s`;
    case "vpn":
      return r.then.vpn === false ? "Force VPN off" : "Force VPN on";
    case "profile":
      return `${r.then.profile ?? "?"} profile`;
  }
}

function RuleNode({ data, selected }: NodeProps<Node<NodeData>>) {
  const col = colOf(data.kind);
  return (
    <div className={`rf-node ${col}${selected ? " sel" : ""}${data.off ? " off" : ""}`}>
      {col !== "who" && <Handle type="target" position={Position.Left} />}
      <Icon n={ICON[data.kind]} s={16} />
      <div style={{ minWidth: 0 }}>
        <div className="n">{TITLES[data.kind]}</div>
        <div className="i">{data.off ? "Not connected to Who" : data.text}</div>
      </div>
      {col !== "then" && <Handle type="source" position={Position.Right} />}
    </div>
  );
}
const nodeTypes = { rule: RuleNode };

export default function RuleDiagram({ o, initial, onSaved }: { o: Overview; initial: RuleDraft; onSaved: (r: Rule) => void }) {
  const narrow = useNarrow();
  const [r, setR] = useState<RuleDraft>(initial);
  const [g, setG] = useState<Graph>(() => readGraph(initial));
  const [sel, setSel] = useState<string | null>("who");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; t: string } | null>(null);
  const kinds = kindsOf(r);
  const on = connected(g.edges);
  const draft = effectiveDraft(r, g);
  const dataOf = (k: Kind): NodeData => ({ kind: k, text: nodeText(k, r, o.devices), off: k !== "who" && !on.has(k) });
  const [nodes, setNodes] = useState<Node<NodeData>[]>(() =>
    kinds.map((k) => ({ id: k, type: "rule", position: g.positions[k] ?? { x: COL[colOf(k)], y: 0 }, data: dataOf(k), deletable: k !== "who", selected: k === "who" })),
  );
  const syncKey = `${kinds.join()}|${[...on].sort().join()}|${JSON.stringify([r.match, r.when, r.then])}|${o.devices.length}`;
  useEffect(() => {
    setNodes((prev) =>
      kinds.map((k) => {
        const old = prev.find((n) => n.id === k);
        return old ? { ...old, data: dataOf(k) } : { id: k, type: "rule", position: g.positions[k] ?? { x: COL[colOf(k)], y: 0 }, data: dataOf(k), deletable: k !== "who" };
      }),
    );
  }, [syncKey]); // eslint-disable-line react-hooks/exhaustive-deps
  const edges: Edge[] = g.edges.map((e) => ({ id: `${e.source}->${e.target}`, source: e.source, target: e.target }));

  function removeKind(k: Kind) {
    if (k === "who") return;
    const when = { ...r.when };
    const then = { ...r.then };
    if (k === "schedule") when.schedule = null;
    if (k === "usage") when.usage = null;
    if (k === "pause") delete then.pause;
    if (k === "speed") delete then.speedMbps;
    if (k === "vpn") delete then.vpn;
    if (k === "profile") delete then.profile;
    setR({ ...r, when, then });
    setG((p) => ({ ...p, edges: p.edges.filter((e) => e.source !== k && e.target !== k) }));
    if (sel === k) setSel("who");
  }
  function onNodesChange(ch: NodeChange<Node<NodeData>>[]) {
    for (const c of ch) if (c.type === "remove") removeKind(c.id as Kind);
    const next = applyNodeChanges(ch.filter((c) => c.type !== "remove"), nodes);
    setNodes(next);
    if (ch.some((c) => c.type === "position")) setG((p) => ({ ...p, positions: { ...p.positions, ...Object.fromEntries(next.map((n) => [n.id, n.position])) } }));
    if (ch.some((c) => c.type === "select")) setSel(next.find((n) => n.selected)?.id ?? null);
  }
  function onEdgesChange(ch: EdgeChange[]) {
    const next = applyEdgeChanges(ch, edges);
    setG((p) => ({ ...p, edges: next.map((e) => ({ source: e.source, target: e.target })) }));
  }
  const valid = (c: Connection | Edge) => c.source !== c.target && colOf(c.target as Kind) !== "who" && colOf(c.source as Kind) !== "then";
  function onConnect(c: Connection) {
    if (!valid(c)) return;
    const next = addEdge(c, edges);
    setG((p) => ({ ...p, edges: next.map((e) => ({ source: e.source, target: e.target })) }));
  }
  function add(k: Kind) {
    const when = { ...r.when };
    const then = { ...r.then };
    if (k === "schedule") when.schedule = { days: [0, 1, 2, 3, 4], start: "23:00", end: "07:00" };
    if (k === "usage") when.usage = { period: "day", overGB: 4 };
    if (k === "pause") then.pause = true;
    if (k === "speed") then.speedMbps = 2;
    if (k === "vpn") then.vpn = true;
    if (k === "profile") then.profile = "gaming";
    setR({ ...r, when, then });
    const col = colOf(k);
    const same = kinds.filter((x) => colOf(x) === col).length;
    const conds = kinds.filter((x) => CONDITIONS.includes(x));
    const acts = kinds.filter((x) => ACTIONS.includes(x));
    const ne = col === "when" ? [{ source: "who", target: k }, ...acts.map((a) => ({ source: k, target: a }))] : (conds.length ? conds : ["who"]).map((s) => ({ source: s, target: k }));
    setG((p) => ({ positions: { ...p.positions, [k]: { x: COL[col], y: 20 + same * 90 } }, edges: [...p.edges, ...ne] }));
    setSel(k);
  }
  async function save() {
    const v = validateRule(draft);
    if (v) return setMsg({ ok: false, t: v });
    setBusy(true);
    setMsg(null);
    try {
      onSaved(draft.id != null ? await api.updateRule(draft as Rule) : await api.createRule(draft));
    } catch (e) {
      setMsg({ ok: false, t: e instanceof ApiError ? e.message : "Couldn't save the rule." });
    } finally {
      setBusy(false);
    }
  }
  const unconnected = kinds.filter((k) => k !== "who" && !on.has(k));

  const header = (
    <div className="hd">
      <label className="sr-only" htmlFor="rd-name">
        Rule name
      </label>
      <input id="rd-name" className="inp" style={{ width: 220 }} value={r.name} placeholder="Rule name" maxLength={60} onChange={(e) => setR({ ...r, name: e.target.value })} />
      <label className="check">
        <input type="checkbox" checked={r.enabled} onChange={(e) => setR({ ...r, enabled: e.target.checked })} />
        Enabled
      </label>
      <div className="r">
        <button type="button" className="btn pri sm" onClick={save} disabled={busy}>
          {busy ? "Saving…" : draft.id != null ? "Save rule" : "Add rule"}
        </button>
      </div>
    </div>
  );

  if (narrow) {
    const steps: [string, string, string][] = [
      ["Who", whoText(draft.match, o.devices), "who"],
      ...(draft.when.schedule ? [["When", scheduleText(draft.when.schedule), "clock"] as [string, string, string]] : []),
      ...(draft.when.usage ? [["When", usageText(draft.when.usage), "clock"] as [string, string, string]] : []),
      ...(!draft.when.schedule && !draft.when.usage ? [["When", "Always", "clock"] as [string, string, string]] : []),
      ["Then", thenText(draft.then), "rules"],
    ];
    return (
      <>
        {header}
        <div className="bd">
          <ol style={{ listStyle: "none", padding: 0, margin: 0, display: "grid", gap: 18 }} aria-label="Rule, step by step">
            {steps.map(([label, text, icon], i) => (
              <li key={i} style={{ position: "relative" }}>
                <div className="t2" style={{ fontSize: 12, marginBottom: 4 }}>
                  {label}
                </div>
                <div className={`rf-node ${label === "Then" ? "then" : ""}`} style={{ width: "100%" }}>
                  <Icon n={icon} s={16} />
                  <div className="n">{text}</div>
                </div>
              </li>
            ))}
          </ol>
          <p className="note">Editing the diagram needs a bigger screen; use the Form view to change this rule.</p>
          {msg && <div className={msg.ok ? "okmsg" : "err"}>{msg.t}</div>}
        </div>
      </>
    );
  }

  return (
    <>
      {header}
      <div className="bd" style={{ display: "grid", gridTemplateColumns: "minmax(0,1fr) 280px", gap: 12 }}>
        <div>
          <div className="fieldrow" style={{ marginBottom: 8 }} role="group" aria-label="Add a node">
            <span className="t3">Add</span>
            {[...CONDITIONS, ...ACTIONS].map((k) => (
              <button key={k} type="button" className="btn sm" disabled={kinds.includes(k)} onClick={() => add(k)}>
                {TITLES[k]}
              </button>
            ))}
          </div>
          <div className="rf-wrap" aria-label="Rule diagram">
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              isValidConnection={valid}
              colorMode="dark"
              fitView
              fitViewOptions={{ padding: 0.06, maxZoom: 1 }}
              proOptions={{ hideAttribution: true }}
              minZoom={0.4}
              maxZoom={1.5}
            >
              <Background gap={20} />
              <Controls showInteractive={false} />
            </ReactFlow>
          </div>
          <p className="note">Drag from a node's right dot to another node's left dot to connect. Select a link or node and press Backspace to remove it.</p>
          {unconnected.length > 0 && (
            <div className="line-alert">
              <span className="dot bad" />
              Not connected: {unconnected.map((k) => TITLES[k]).join(", ")}. These are ignored when saving.
            </div>
          )}
          {msg && <div className={msg.ok ? "okmsg" : "err"}>{msg.t}</div>}
        </div>
        <aside aria-label="Selected node" className="form" style={{ alignContent: "start" }}>
          {sel ? <Panel k={sel as Kind} r={r} setR={setR} devices={o.devices} onRemove={() => removeKind(sel as Kind)} /> : <p className="note">Select a node to edit it.</p>}
        </aside>
      </div>
    </>
  );
}

function Panel({ k, r, setR, devices, onRemove }: { k: Kind; r: RuleDraft; setR: (r: RuleDraft) => void; devices: Device[]; onRemove: () => void }) {
  const profiles = useProfiles();
  const then = (p: Partial<RuleThen>) => setR({ ...r, then: { ...r.then, ...p } });
  return (
    <>
      <b>{TITLES[k]}</b>
      {k === "who" && <WhoEditor m={r.match} devices={devices} onChange={(match) => setR({ ...r, match })} />}
      {k === "schedule" && r.when.schedule && <ScheduleEditor s={r.when.schedule} onChange={(schedule) => setR({ ...r, when: { ...r.when, schedule } })} />}
      {k === "usage" && r.when.usage && <UsageEditor u={r.when.usage} onChange={(usage) => setR({ ...r, when: { ...r.when, usage } })} />}
      {k === "pause" && <p className="note">Blocks the internet for matching devices while the conditions hold.</p>}
      {k === "speed" && (
        <label className="field">
          Cap (Mbit/s, both directions)
          <input className="inp" type="number" min={0.5} max={1000} step={0.5} value={r.then.speedMbps ?? 2} onChange={(e) => then({ speedMbps: Number(e.target.value) })} />
        </label>
      )}
      {k === "vpn" && (
        <div className="seg" role="group" aria-label="VPN">
          <button type="button" aria-pressed={r.then.vpn !== false} onClick={() => then({ vpn: true })}>
            VPN on
          </button>
          <button type="button" aria-pressed={r.then.vpn === false} onClick={() => then({ vpn: false })}>
            VPN off
          </button>
        </div>
      )}
      {k === "profile" && (
        <label className="field">
          Profile (only while on VPN)
          <select className="inp" value={r.then.profile ?? (profiles.includes("gaming") ? "gaming" : profiles[0])} onChange={(e) => then({ profile: e.target.value as Profile })}>
            {profiles.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </label>
      )}
      {k !== "who" && (
        <button type="button" className="btn sm ghost" onClick={onRemove} style={{ justifySelf: "start" }}>
          Remove node
        </button>
      )}
    </>
  );
}
