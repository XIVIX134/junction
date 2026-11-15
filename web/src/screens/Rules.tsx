import { lazy, Suspense, useState } from "react";
import { api, ApiError } from "../api/client";
import type { Device, Overview, Rule, RuleDraft, SimulateResult } from "../api/types";
import { isRouter, totalToday } from "../clients";
import { RuleForm } from "../components/RuleParts";
import { NeedOverview } from "../components/Shell";
import { bumpEvents } from "../events";
import { bytes, DAY3, exitName, hm } from "../fmt";
import { fromLocalInput } from "../format";
import { useFetch, useLive } from "../live";
import { Link, navigate, useLocation } from "../router";
import { blankRule, gatewayClock, thenText, whenText, whoText } from "../rules";
import { Icon, Sw } from "../ui";

const RuleDiagram = lazy(() => import("./RuleDiagram"));

export function Rules({ id }: { id: number | "new" | null }) {
  return <NeedOverview>{(o) => (id == null ? <RulesList o={o} /> : <RuleDetail o={o} id={id} />)}</NeedOverview>;
}

/** Devices a rule's `match` covers (as the gateway would see them now). */
function members(r: Pick<Rule, "match">, devs: Device[]) {
  const now = Date.now();
  return devs.filter(
    (d) =>
      !isRouter(d) &&
      (r.match.all ||
        r.match.devices?.includes(d.id) ||
        (d.group && r.match.groups?.includes(d.group)) ||
        (r.match.newDevices && d.nameSource !== "user" && now - Date.parse(d.firstSeen) < 864e5)),
  );
}
const appliesTo = (r: Pick<Rule, "match">, devs: Device[]) => {
  const n = members(r, devs).length;
  const who = r.match.groups?.length && !r.match.devices?.length && !r.match.all ? `Group ${r.match.groups.join(", ")}` : whoText(r.match, devs);
  return { who, n };
};

function RulesList({ o }: { o: Overview }) {
  const { refresh } = useLive();
  const { query } = useLocation();
  const rules = useFetch("rules", api.rules, 30000);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [drag, setDrag] = useState<number | null>(null);
  const [over, setOver] = useState<number | null>(null);
  const list = [...(rules.data ?? [])].sort((a, b) => a.order - b.order);
  const selId = Number(query.get("sel")) || list[0]?.id;
  const sel = list.find((r) => r.id === selId) ?? null;

  async function act(key: string, fn: () => Promise<void>) {
    setBusy(key);
    setErr(null);
    try {
      await fn();
      refresh();
      bumpEvents();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "That didn't work.");
      rules.reload();
    } finally {
      setBusy(null);
    }
  }
  const reorder = (ids: number[]) =>
    act("order", async () => {
      rules.setData(ids.map((id, i) => ({ ...list.find((r) => r.id === id)!, order: i + 1 })));
      rules.setData(await api.orderRules(ids));
    });
  const move = (id: number, by: number) => {
    const ids = list.map((r) => r.id);
    const i = ids.indexOf(id);
    const j = i + by;
    if (j < 0 || j >= ids.length) return;
    [ids[i], ids[j]] = [ids[j], ids[i]];
    reorder(ids);
  };
  const toggle = (r: Rule, enabled: boolean) =>
    act(`t${r.id}`, async () => {
      rules.setData(list.map((x) => (x.id === r.id ? { ...x, enabled } : x)));
      const s = await api.updateRule({ ...r, enabled });
      rules.setData(list.map((x) => (x.id === r.id ? s : x)));
    });

  return (
    <div className="page">
      <div className="ph">
        <h1>Rules</h1>
        <span className="meta">{list.length} on the gateway · evaluated every 15 s, top to bottom</span>
        <div className="right">
          <Link className="btn pri" href="/rules/new">
            Create rule
          </Link>
        </div>
      </div>
      {err && (
        <div className="line-alert" role="alert" style={{ marginTop: 0, marginBottom: 10 }}>
          <span className="dot bad" />
          {err}
        </div>
      )}
      <div className="pn scroll-x" style={{ marginBottom: 12 }}>
        <table className="tb">
          <thead>
            <tr>
              <th style={{ width: 44 }}>Order</th>
              <th>Name</th>
              <th>Applies to</th>
              <th>When</th>
              <th>Then</th>
              <th style={{ width: 70 }}>Enabled</th>
              <th style={{ width: 70 }}>
                <span className="sr-only">Move</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {rules.data == null && (
              <tr>
                <td colSpan={7} className="t3">
                  {rules.error ?? "Loading…"}
                </td>
              </tr>
            )}
            {rules.data && !list.length && (
              <tr>
                <td colSpan={7} style={{ height: 64 }} className="wrap">
                  <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                    <span className="t3">
                      <Icon n="rules" s={18} />
                    </span>
                    <div>
                      <div>No rules on the gateway.</div>
                      <div className="note">Rules pause, cap or route devices by schedule or usage. Device settings (limits, schedules) also create rules here.</div>
                    </div>
                    <Link className="btn" href="/rules/new" style={{ marginLeft: "auto" }}>
                      Create rule
                    </Link>
                  </div>
                </td>
              </tr>
            )}
            {list.map((r, i) => {
              const a = appliesTo(r, o.devices);
              return (
                <tr
                  key={r.id}
                  className={`rw ${sel?.id === r.id ? "sel" : ""} ${drag === r.id ? "drag" : ""} ${over === r.id && drag !== r.id ? "dropto" : ""}`}
                  draggable
                  onDragStart={(e) => {
                    setDrag(r.id);
                    e.dataTransfer.effectAllowed = "move";
                  }}
                  onDragEnd={() => (setDrag(null), setOver(null))}
                  onDragOver={(e) => {
                    if (drag != null && drag !== r.id) {
                      e.preventDefault();
                      setOver(r.id);
                    }
                  }}
                  onDrop={(e) => {
                    e.preventDefault();
                    if (drag == null || drag === r.id) return;
                    const ids = list.map((x) => x.id).filter((x) => x !== drag);
                    ids.splice(ids.indexOf(r.id) + (list.findIndex((x) => x.id === drag) < i ? 1 : 0), 0, drag);
                    setDrag(null);
                    setOver(null);
                    reorder(ids);
                  }}
                  onClick={() => navigate(`/rules?sel=${r.id}`, { replace: true })}
                >
                  <td className="t3" title="Drag to reorder">
                    {i + 1}
                  </td>
                  <td>
                    <Link href={`/rules/${r.id}`} onClick={(e) => e.stopPropagation()} style={{ opacity: r.enabled ? 1 : 0.6 }}>
                      {r.name}
                    </Link>
                  </td>
                  <td>
                    {a.who} <span className="t3">· {a.n === 1 ? "1 device" : `${a.n} devices`}</span>
                  </td>
                  <td>{whenText(r.when) ?? <span className="t3">Always</span>}</td>
                  <td>{thenText(r.then)}</td>
                  <td onClick={(e) => e.stopPropagation()}>
                    <Sw on={r.enabled} label={`${r.name} enabled`} busy={busy === `t${r.id}`} onChange={(on) => toggle(r, on)} />
                  </td>
                  <td onClick={(e) => e.stopPropagation()} style={{ display: "flex", gap: 2, alignItems: "center", height: 38 }}>
                    <button type="button" className="btn sm ghost icon" aria-label={`Move ${r.name} up`} disabled={i === 0 || !!busy} onClick={() => move(r.id, -1)}>
                      <Icon n="up" s={12} />
                    </button>
                    <button type="button" className="btn sm ghost icon" aria-label={`Move ${r.name} down`} disabled={i === list.length - 1 || !!busy} onClick={() => move(r.id, 1)}>
                      <Icon n="down" s={12} />
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="grid">
        <section className="pn c8" aria-labelledby="rule-h">
          {sel ? (
            <>
              <div className="hd">
                <h2 id="rule-h">{sel.name}</h2>
                {!sel.enabled && <span className="tag">Off</span>}
                <div className="r">
                  <Link className="btn sm" href={`/rules/${sel.id}`}>
                    Edit
                  </Link>
                </div>
              </div>
              <div className="bd scroll-x" style={{ padding: "22px 18px 18px" }}>
                <Flow r={sel} o={o} />
              </div>
            </>
          ) : (
            <>
              <div className="hd">
                <h2 id="rule-h">Rule</h2>
              </div>
              <div className="bd note">Create a rule to see how it flows from who, to when, to what happens.</div>
            </>
          )}
        </section>
        <section className="pn c4" aria-labelledby="test-h">
          <TestBox o={o} rules={list} />
        </section>
      </div>
    </div>
  );
}

/** Who → When → Then, drawn like Topology's nodes. */
export function Flow({ r, o }: { r: Pick<Rule, "match" | "when" | "then"> & { id?: number }; o: Overview }) {
  const devs = members(r, o.devices);
  const a = appliesTo(r, o.devices);
  const when = whenText(r.when);
  const resets = r.when.usage ? { day: "Resets at 00:00", week: "Resets Sunday 00:00", month: "Resets on the 1st," }[r.when.usage.period] : null;
  const box = (x: number, label: string, title: string, sub: string, icon: string, acc = false) => (
    <>
      <div className={`node static ${acc ? "acc" : ""}`} style={{ left: x, top: 24, width: 210, height: 58 }}>
        <Icon n={icon} s={16} />
        <div className="tx">
          <div className="n">{title}</div>
          <div className="i">{sub}</div>
        </div>
      </div>
      <div className="glabel" style={{ left: x, top: 0 }}>
        {label}
      </div>
    </>
  );
  const affected = r.id != null ? o.devices.filter((d) => d.effective?.rules.some((x) => x.id === r.id)) : [];
  const bypass = devs.filter((d) => !d.usesGateway);
  return (
    <>
      <div className="topo" style={{ height: 150, width: 760 }} role="img" aria-label={`${a.who}; ${when ?? "always"}; then ${thenText(r.then)}`}>
        <svg width="760" height="150" style={{ position: "absolute", inset: 0 }} aria-hidden="true">
          <path d="M210 53H262M472 53H524" stroke="var(--edge)" />
          <path d="M256 49l6 4-6 4M518 49l6 4-6 4" fill="none" stroke="var(--edge-arrow)" />
          {when && <path d="M367 82V118H629V82" fill="none" stroke="var(--edge)" strokeDasharray="3 3" />}
        </svg>
        {box(0, "Who", a.who, `${a.n === 1 ? "1 device" : `${a.n} devices`}${devs.length ? ` · ${devs.slice(0, 2).map((d) => d.name).join(", ")}${devs.length > 2 ? "…" : ""}` : ""}`, "who")}
        {box(262, "When", when ?? "Always", resets ?? (r.when.schedule ? "Gateway time" : "No condition"), "clock")}
        {box(524, "Then", thenText(r.then), r.then.speedMbps != null ? `Down and up${r.when.usage?.period === "day" ? " · until midnight" : ""}` : r.then.pause ? "Blocks the internet" : r.then.profile && r.then.vpn == null ? "Only while on VPN" : "While it applies", "rules", true)}
        {when && (
          <div className="elabel" style={{ left: 420, top: 110 }}>
            Otherwise: no change
          </div>
        )}
      </div>
      <div className="note" style={{ marginTop: 4 }}>
        {affected.length
          ? `Affecting ${affected.map((d) => d.name).join(", ")} right now.`
          : devs.length
            ? `Not affecting anyone right now${r.when.usage ? `. Highest use today: ${bytes(Math.max(...devs.map(totalToday)))}` : ""}.`
            : "No device matches yet."}
        {bypass.length > 0 && ` ${bypass.map((d) => d.name).join(", ")} bypass${bypass.length === 1 ? "es" : ""} the gateway, so ${bypass.length === 1 ? "its" : "their"} usage is not counted and this rule can't act on ${bypass.length === 1 ? "it" : "them"} yet.`}
      </div>
    </>
  );
}

/** "What happens to X at 23:00 Thursday" via GET /api/simulate (saved rules only). */
function TestBox({ o, rules }: { o: Overview; rules: Rule[] }) {
  const devs = o.devices.filter((d) => !isRouter(d)).sort((a, b) => Number(!!b.controllable) - Number(!!a.controllable) || a.name.localeCompare(b.name));
  const [dev, setDev] = useState(devs[0]?.id ?? "");
  const [day, setDay] = useState(0);
  const [time, setTime] = useState("23:00");
  const [res, setRes] = useState<{ r: SimulateResult; dev: string; label: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const now = new Date(o.now);
  const today = gatewayClock(now);
  const dayLabel = (n: number) => (n === 0 ? "Today" : n === 1 ? "Tomorrow" : DAY3[(today.dow + n) % 7]);
  async function run() {
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(time)) return setErr("Times look like 23:00.");
    const [y, m, d] = today.ymd.split("-").map(Number);
    const t = new Date(Date.UTC(y, m - 1, d + day, 12)).toISOString().slice(0, 10);
    const at = fromLocalInput(`${t}T${time}`);
    if (!at) return;
    setBusy(true);
    setErr(null);
    try {
      setRes({ r: await api.simulate(dev, at), dev, label: `${dayLabel(day).toLowerCase() === "today" ? "today" : dayLabel(day)} at ${time}` });
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Couldn't run the test.");
    } finally {
      setBusy(false);
    }
  }
  const e = res?.r.effective;
  const name = devs.find((d) => d.id === res?.dev)?.name;
  return (
    <>
      <div className="hd">
        <h2 id="test-h">Test</h2>
        <div className="r">Gateway simulator</div>
      </div>
      <div className="bd">
        <form
          onSubmit={(ev) => {
            ev.preventDefault();
            run();
          }}
          style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: "8px 10px", alignItems: "center" }}
        >
          <label className="t2" htmlFor="sim-dev">
            What happens to
          </label>
          <select id="sim-dev" className="inp" style={{ width: "100%" }} value={dev} onChange={(ev) => setDev(ev.target.value)}>
            {devs.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
          <span className="t2">at</span>
          <div style={{ display: "flex", gap: 8 }}>
            <select className="inp" style={{ width: 100 }} aria-label="Day" value={day} onChange={(ev) => setDay(Number(ev.target.value))}>
              {[0, 1, 2, 3, 4, 5, 6].map((n) => (
                <option key={n} value={n}>
                  {dayLabel(n)}
                </option>
              ))}
            </select>
            <input className="inp num" style={{ width: 64 }} aria-label="Time" value={time} onChange={(ev) => setTime(ev.target.value)} />
            <button type="submit" className="btn pri" style={{ marginLeft: "auto" }} disabled={busy || !dev}>
              {busy ? "Testing…" : "Test"}
            </button>
          </div>
        </form>
        {err && <div className="err" style={{ marginTop: 8 }}>{err}</div>}
        <dl className="kv" style={{ marginTop: 12 }}>
          <dt>Matched rules</dt>
          <dd className={res?.r.matched.length ? "" : "t3"} style={{ whiteSpace: "normal" }}>
            {res ? (res.r.matched.length ? res.r.matched.map((m) => `${m.name} (${m.why})`).join("; ") : "None") : "—"}
          </dd>
          <dt>Internet</dt>
          <dd>{e ? e.paused ? <span className="warnc">Paused</span> : "Allowed" : "—"}</dd>
          <dt>Route</dt>
          <dd>{e ? (e.vpn ? `VPN · ${exitName(e.exit)}` : "Direct") : "—"}</dd>
          <dt>Speed limit</dt>
          <dd>{e ? (e.speedLimitMbps != null ? `${e.speedLimitMbps} Mbit/s` : "None") : "—"}</dd>
        </dl>
        <div className="note" style={{ marginTop: 8 }}>
          {res
            ? `Result from the gateway for ${name} ${res.label}. Uses the ${rules.length} saved rule${rules.length === 1 ? "" : "s"} and today's usage so far.`
            : `Pick a device and a time; the gateway works out what would happen with the saved rules. Now ${hm(o.now)}, gateway time.`}
        </div>
      </div>
    </>
  );
}

function RuleDetail({ o, id }: { o: Overview; id: number | "new" }) {
  const { query } = useLocation();
  const view = query.get("view") === "flow" ? "flow" : "form";
  const rules = useFetch(`rules:${id}`, api.rules);
  const { refresh } = useLive();
  const [del, setDel] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const r: RuleDraft | null = id === "new" ? blankRule() : (rules.data?.find((x) => x.id === id) ?? null);
  const setView = (v: "form" | "flow") => navigate(`/rules/${id}${v === "flow" ? "?view=flow" : ""}`, { replace: true });
  const saved = (s: Rule) => {
    refresh();
    bumpEvents();
    navigate(`/rules?sel=${s.id}`);
  };
  if (id !== "new" && !r)
    return (
      <div className="page">
        <div className="ph">
          <Link href="/rules" className="t2">
            Rules
          </Link>
          <h1>{rules.error ? "Couldn't load the rule" : rules.data ? "No such rule" : "Loading…"}</h1>
        </div>
      </div>
    );
  return (
    <div className="page">
      <div className="ph">
        <Link href="/rules" className="t2">
          Rules
        </Link>
        <span className="t3">/</span>
        <h1>{id === "new" ? "New rule" : r!.name}</h1>
        <div className="seg" role="group" aria-label="View">
          <button type="button" aria-pressed={view === "form"} onClick={() => setView("form")}>
            Form
          </button>
          <button type="button" aria-pressed={view === "flow"} onClick={() => setView("flow")}>
            Diagram
          </button>
        </div>
        {id !== "new" && (
          <div className="right">
            {del ? (
              <>
                <span className="t2">Delete this rule?</span>
                <button
                  type="button"
                  className="btn danger"
                  onClick={async () => {
                    try {
                      await api.deleteRule(id);
                      refresh();
                      bumpEvents();
                      navigate("/rules", { replace: true });
                    } catch (e) {
                      setErr(e instanceof ApiError ? e.message : "Couldn't delete it.");
                    }
                  }}
                >
                  Delete
                </button>
                <button type="button" className="btn ghost" onClick={() => setDel(false)}>
                  Keep
                </button>
              </>
            ) : (
              <button type="button" className="btn ghost" onClick={() => setDel(true)}>
                Delete rule
              </button>
            )}
          </div>
        )}
      </div>
      {err && <div className="err" style={{ marginBottom: 8 }}>{err}</div>}
      <div className="grid">
        <section className="pn c8" aria-label={view === "flow" ? "Rule diagram" : "Rule form"}>
          {view === "form" ? (
            <div className="bd">
              <RuleForm key={String(id)} initial={r!} devices={o.devices} onSaved={saved} onCancel={() => navigate("/rules")} />
            </div>
          ) : (
            <Suspense fallback={<div className="bd t3">Loading the diagram…</div>}>
              <RuleDiagram key={String(id)} o={o} initial={r!} onSaved={saved} />
            </Suspense>
          )}
        </section>
        <section className="pn c4" aria-labelledby="test-h">
          <TestBox o={o} rules={rules.data ?? []} />
        </section>
      </div>
    </div>
  );
}
