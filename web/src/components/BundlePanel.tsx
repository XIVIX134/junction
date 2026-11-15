import { useEffect, useRef, useState, type RefObject } from "react";
import { api, ApiError } from "../api/client";
import type { Bundle, BundleState, Overview, UsageDay } from "../api/types";
import { bumpEvents } from "../events";
import { bytes, dayKey, dmy, hm, MON } from "../fmt";
import { fromLocalInput, toLocalInput } from "../format";
import { useFetch, useLive } from "../live";

// The bundle as Junction counts it: whole packets on the wire (wire.go), counting
// down from the last renewal or from the "left" you set by hand.

const gbs = (b: number) => `${(b / 1e9).toFixed(Math.abs(b) >= 10e9 ? 1 : 2)} GB`;
const DAY = 864e5;

/** The current bundle, worked out for display. Bytes throughout. */
export type BundleView = {
  size: number;
  cycleStart: string;
  cycleEnd: string | null;
  renewInDays: number | null;
  used: number;
  left: number;
  setLeft: { amount: number; at: string } | null; // the anchor, when it is a "Set left" correction
  pace: number | null;
  runsOut: number | null; // ms
  earlyDays: number | null; // runs out this many days before the bundle ends
};

export function bundleView(b: Bundle | null | undefined, now: number): BundleView | null {
  if (!b) return null;
  const cycleEnd = b.expiresAt;
  const runsOut = b.runsOut ? Date.parse(b.runsOut) : null;
  const earlyDays = runsOut && cycleEnd ? Math.floor((Date.parse(cycleEnd) - runsOut) / DAY) : null;
  return {
    size: b.size,
    cycleStart: b.renewedAt,
    cycleEnd,
    renewInDays: cycleEnd ? Math.max(0, Math.ceil((Date.parse(cycleEnd) - now) / DAY)) : null,
    used: Math.max(0, b.size - b.left),
    left: b.left,
    setLeft: b.anchor.kind === "check" ? { amount: b.anchor.amount, at: b.anchor.at } : null,
    pace: b.pacePerDay > 0 ? b.pacePerDay : null,
    runsOut,
    earlyDays: earlyDays != null && earlyDays > 0 ? earlyDays : null,
  };
}

type Form = "renew" | "check" | null;

export function BundlePanel({ o }: { o: Overview }) {
  const st = useFetch("bundle", api.bundle, 30000);
  const usage = useFetch("usage30", () => api.usage(30), 60000);
  const openLog = location.hash === "#renew";
  const [form, setForm] = useState<Form>(openLog ? "renew" : null);
  const [logOpen, setLogOpen] = useState(openLog);
  const ref = useRef<HTMLElement>(null);
  const logRef = useRef<HTMLDetailsElement>(null);
  const loaded = !!st.data;
  useEffect(() => {
    if (loaded && (location.hash === "#bundle" || location.hash === "#renew")) ref.current?.scrollIntoView({ block: "start" });
  }, [loaded]);

  const now = Date.parse(o.now);
  const b = o.bundle;
  const v = bundleView(b, now);

  function renewed() {
    setForm("renew");
    setLogOpen(true);
    requestAnimationFrame(() => logRef.current?.scrollIntoView({ block: "nearest" }));
  }

  return (
    <section className="pn c12" id="bundle" ref={ref} aria-labelledby="b-h" style={{ scrollMarginTop: 12 }}>
      <div className="hd">
        <h2 id="b-h">Bundle</h2>
        {v && <span className="t2">{(v.size / 1e9).toFixed(0)} GB</span>}
        {v && (
          <span className="t3">
            {dmy(v.cycleStart)}
            {v.cycleEnd ? ` → ${dmy(v.cycleEnd)}` : ""}
            {v.renewInDays != null ? ` · renews in ${v.renewInDays} day${v.renewInDays === 1 ? "" : "s"}` : ""}
          </span>
        )}
        <div className="r">
          <button type="button" className="btn sm" onClick={renewed}>
            I renewed…
          </button>
        </div>
      </div>
      <div className="bd">
        {!b || !v ? (
          <div style={{ padding: "6px 0 10px" }}>
            <div className="big" style={{ fontSize: 18 }}>
              No bundle yet
            </div>
            <div className="note" style={{ marginTop: 4 }}>
              Use “I renewed” to log the size and when you renewed.
            </div>
          </div>
        ) : (
          <>
            <div>
              <span className={`big ${v.left <= 0 ? "badc" : ""}`}>{gbs(v.left)}</span> <span className="t2">left</span>
            </div>
            <div className="note" style={{ marginTop: 2 }}>
              {bytes(v.used)} used, whole packets as your ISP bills them ·{" "}
              {v.setLeft
                ? `counting from the ${gbs(v.setLeft.amount)} you set on ${dmy(v.setLeft.at)} ${hm(v.setLeft.at)}`
                : `since you renewed on ${dmy(v.cycleStart)} ${hm(v.cycleStart)}`}
            </div>
            <UsedBar v={v} />
            <div className="facts">
              <Fact k="Pace" v={v.pace ? `${bytes(v.pace)}/day` : "Needs 6 h"} />
              <Fact
                k="Runs out"
                v={v.runsOut ? `~${dmy(v.runsOut)}${v.earlyDays ? `, ${v.earlyDays} day${v.earlyDays === 1 ? "" : "s"} early` : ""}` : "—"}
                cls={v.earlyDays ? "warnc" : ""}
              />
              <Fact k="Through VPN" v={bytes(b.tunnel)} />
              <Fact k="Direct" v={bytes(b.direct)} />
            </div>
          </>
        )}
        <Days v={v} usage={usage.data ?? []} now={now} />
        <Entries
          st={st.data}
          onChange={(s) => st.setData(s)}
          form={form}
          setForm={setForm}
          open={logOpen}
          setOpen={setLogOpen}
          detailsRef={logRef}
          hasBundle={!!b}
        />
      </div>
    </section>
  );
}

export function UsedBar({ v, compact }: { v: BundleView; compact?: boolean }) {
  if (v.size <= 0) return null;
  const pct = `${Math.max(0, Math.min(100, (v.used / v.size) * 100))}%`;
  return (
    <>
      <div className={`dbar ${compact ? "compact" : ""}`} role="img" aria-label={`${bytes(v.used)} used of ${(v.size / 1e9).toFixed(0)} GB`}>
        <i className="fill" style={{ width: pct }} title={`${bytes(v.used)} used`} />
      </div>
      {!compact && (
        <div className="scale">
          <span>0</span>
          <span>Used of {(v.size / 1e9).toFixed(0)} GB</span>
          <span>{(v.size / 1e9).toFixed(0)} GB</span>
        </div>
      )}
    </>
  );
}

const Fact = ({ k, v, cls }: { k: string; v: string; cls?: string }) => (
  <div>
    <div className="note">{k}</div>
    <div style={{ fontSize: 14 }} className={cls ?? "num"}>
      {v}
    </div>
  </div>
);

// ---------- every day of the bundle ----------

type Slot = { day: string; gw: number | null; content: number; tunnel: number; direct: number; future: boolean };

function Days({ v, usage, now }: { v: BundleView | null; usage: UsageDay[]; now: number }) {
  const [table, setTable] = useState(() => matchMedia("(max-width: 600px)").matches); // chart text is unreadable on a phone
  const today = dayKey(now);
  const byDay = new Map(usage.map((u) => [u.day, u]));
  // The bundle's days when we know them; otherwise the last 30.
  const cycleStart = v?.cycleStart ?? null;
  const cycleEnd = v?.cycleEnd ?? null;
  const start = cycleStart && now - Date.parse(cycleStart) < 31 * DAY ? Date.parse(cycleStart) : now - 29 * DAY;
  const cycle = !!cycleStart && start === Date.parse(cycleStart);
  const end = cycle && cycleEnd ? Date.parse(cycleEnd) : now;
  const lastKey = dayKey(cycle && cycleEnd ? Math.min(end, start + 31 * DAY) - 1 : now); // a cycle ending at midnight doesn't own that day
  const slots: Slot[] = [];
  // Step from noon so a daylight-saving hour never skips or repeats a day.
  for (let t = Date.parse(`${dayKey(start)}T12:00:00+02:00`); slots.length < 32; t += DAY) {
    const k = dayKey(t);
    if (k > lastKey) break;
    if (slots.length && slots[slots.length - 1].day === k) continue;
    const u = byDay.get(k);
    const future = k > today;
    slots.push({ day: k, gw: future ? null : u ? u.billed || u.tunnel + u.direct : 0, content: u ? u.tunnel + u.direct : 0, tunnel: u?.tunnel ?? 0, direct: u?.direct ?? 0, future });
  }
  const label = (k: string) => `${+k.slice(8)} ${MON[+k.slice(5, 7) - 1]}`;

  const peak = Math.max(1e9, ...slots.map((s) => s.gw ?? 0)) / 1e9;
  const p10 = Math.pow(10, Math.floor(Math.log10(peak)));
  const max = ([1, 2, 2.5, 5, 10].find((m) => m * p10 >= peak) ?? 10) * p10 * 1e9;
  const W = 1100;
  const H = 190;
  const T = 18;
  const B = 24;
  const ph = H - T - B;
  const L = 50;
  const sw = (W - L) / Math.max(7, slots.length);
  const bw = Math.min(18, sw - 8);

  return (
    <div style={{ marginTop: 20 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <b style={{ fontSize: 12.5 }}>{cycle ? "Every day of this bundle" : "Last 30 days"}</b>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          What the gateway counted on the wire, whole packets.
        </span>
        <div className="legend" style={{ marginLeft: "auto", fontSize: 11.5 }}>
          <button type="button" className="btn sm ghost" aria-pressed={table} onClick={() => setTable(!table)}>
            {table ? "Chart" : "Table"}
          </button>
        </div>
      </div>
      {table ? (
        <div className="scroll-x">
          <table className="tb compact" style={{ marginTop: 6, maxWidth: 820 }}>
            <thead>
              <tr>
                <th>Day</th>
                <th className="rt">Used</th>
                <th className="rt hide-m">Content</th>
                <th className="rt">VPN</th>
                <th className="rt">Direct</th>
              </tr>
            </thead>
            <tbody>
              {slots
                .filter((s) => !s.future)
                .reverse()
                .map((s) => (
                  <tr key={s.day}>
                    <td>{s.day === today ? "Today" : label(s.day)}</td>
                    <td className="rt">{bytes(s.gw ?? 0)}</td>
                    <td className="rt t2 hide-m">{bytes(s.content)}</td>
                    <td className="rt t2">{bytes(s.tunnel)}</td>
                    <td className="rt t2">{bytes(s.direct)}</td>
                  </tr>
                ))}
            </tbody>
          </table>
          <div className="note" style={{ marginTop: 4 }}>
            Used is whole packets on the wire. Content, VPN and Direct are what devices sent and received, without the packet overhead.
          </div>
        </div>
      ) : (
        <svg width="100%" viewBox={`0 0 ${W} ${H}`} style={{ marginTop: 6, display: "block" }} role="img" aria-label="Data per day: what the gateway counted on the wire">
          {[0, 1, 2, 3, 4].map((k) => {
            const y = T + ph * (1 - k / 4);
            return (
              <g key={k}>
                <line x1={L} x2={W} y1={y} y2={y} stroke={k ? "var(--chart-grid)" : "var(--chart-axis)"} />
                <text x={L - 8} y={y + 4} textAnchor="end" fill="var(--text3)" fontSize="11">
                  {+((max * k) / 4 / 1e9).toFixed(1)} GB
                </text>
              </g>
            );
          })}
          {slots.map((s, i) => {
            const x = L + i * sw + (sw - bw) / 2;
            const isToday = s.day === today;
            if (s.future) return <rect key={s.day} x={x} y={T + ph - 2} width={bw} height={2} rx={1} fill="var(--track)" />;
            const h = ((s.gw ?? 0) / max) * ph;
            const r = Math.min(3, h / 2, bw / 2);
            return (
              <g key={s.day}>
                <title>{`${isToday ? "Today" : label(s.day)} · ${bytes(s.gw ?? 0)} on the wire (VPN ${bytes(s.tunnel)}, direct ${bytes(s.direct)})`}</title>
                <rect x={L + i * sw} y={T} width={sw} height={ph} fill="transparent" />
                {h > 0.5 && (
                  <path
                    d={`M${x},${T + ph}v-${h - r}a${r},${r} 0 0 1 ${r},-${r}h${bw - 2 * r}a${r},${r} 0 0 1 ${r},${r}v${h - r}z`}
                    fill="var(--accent)"
                    opacity={isToday ? 0.55 : 1}
                  />
                )}
                {(i % 2 === 0 || isToday) && (
                  <text x={x + bw / 2} y={H - 6} textAnchor="middle" fill="var(--text3)" fontSize="11">
                    {isToday ? "today" : label(s.day)}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
      )}
    </div>
  );
}

// ---------- what's been logged, and logging by hand ----------

function useAct(after: (s?: unknown) => void) {
  const { refresh } = useLive();
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; t: string } | null>(null);
  async function run<T>(fn: () => Promise<T>, ok: string, fail: string) {
    setBusy(true);
    setMsg(null);
    try {
      const r = await fn();
      setMsg({ ok: true, t: ok });
      after(r);
      refresh();
      bumpEvents();
      return true;
    } catch (e) {
      setMsg({ ok: false, t: e instanceof ApiError ? e.message : fail });
      return false;
    } finally {
      setBusy(false);
    }
  }
  return { busy, run, msg, setMsg };
}

function Entries({
  st,
  onChange,
  form,
  setForm,
  open,
  setOpen,
  detailsRef,
  hasBundle,
}: {
  st: BundleState | null;
  onChange: (s: BundleState) => void;
  form: Form;
  setForm: (f: Form) => void;
  open: boolean;
  setOpen: (o: boolean) => void;
  detailsRef: RefObject<HTMLDetailsElement | null>;
  hasBundle: boolean;
}) {
  const [confirm, setConfirm] = useState<number | null>(null);
  const a = useAct((s) => {
    if (s && typeof s === "object" && "events" in (s as object)) onChange(s as BundleState);
  });
  const evs = st?.events ?? [];
  return (
    <details className="entries" ref={detailsRef} open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>
        Renewals and corrections <span className="t3">· {evs.length}</span>
      </summary>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", margin: "10px 0" }}>
        <button type="button" className="btn sm" aria-pressed={form === "renew"} onClick={() => setForm(form === "renew" ? null : "renew")}>
          I renewed
        </button>
        {hasBundle && (
          <button type="button" className="btn sm" aria-pressed={form === "check"} onClick={() => setForm(form === "check" ? null : "check")}>
            Set what's left…
          </button>
        )}
        <span className="note" style={{ alignSelf: "center" }}>
          Log each renewal, and set what's left whenever you check your ISP's app: Junction counts down from it.
        </span>
      </div>
      {form === "renew" && <RenewForm onDone={(s) => (onChange(s), setForm(null))} />}
      {form === "check" && <CheckForm onDone={(s) => (onChange(s), setForm(null))} />}
      <div className="scroll-x">
        <table className="tb compact" style={{ maxWidth: 820 }}>
          <thead>
            <tr>
              <th>Logged</th>
              <th>Entry</th>
              <th className="rt">Amount</th>
              <th>Ends</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {evs.map((e) => (
              <tr key={e.id}>
                <td className="t2">
                  {dmy(e.at)} {hm(e.at)}
                </td>
                <td>{e.kind === "renewal" ? "Renewed" : "Set left"}</td>
                <td className="rt">{(e.amount / 1e9).toFixed(e.amount % 1e9 ? 1 : 0)} GB</td>
                <td className="t3">{e.expiresAt ? dmy(e.expiresAt) : "—"}</td>
                <td className="rt">
                  {confirm === e.id ? (
                    <span style={{ display: "inline-flex", gap: 6 }}>
                      <button type="button" className="btn sm danger" disabled={a.busy} onClick={() => a.run(() => api.deleteBundleEvent(e.id), "Entry deleted.", "Couldn't delete it.").then(() => setConfirm(null))}>
                        Delete
                      </button>
                      <button type="button" className="btn sm ghost" onClick={() => setConfirm(null)}>
                        Keep
                      </button>
                    </span>
                  ) : (
                    <button type="button" className="btn sm ghost" aria-label={`Undo ${e.kind === "renewal" ? "renewal" : "left set"} of ${dmy(e.at)} ${hm(e.at)}`} onClick={() => setConfirm(e.id)}>
                      Undo
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {st && !evs.length && (
              <tr>
                <td colSpan={5} className="t3">
                  Nothing logged yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {a.msg && <div className={a.msg.ok ? "okmsg" : "err"}>{a.msg.t}</div>}
    </details>
  );
}

const TIME_OK = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/;

function RenewForm({ onDone }: { onDone: (s: BundleState) => void }) {
  const [gbv, setGb] = useState("");
  const [at, setAt] = useState(() => toLocalInput());
  const [exp, setExp] = useState("");
  const a = useAct(() => {});
  return (
    <form
      className="line-alert"
      style={{ marginTop: 0, marginBottom: 12, flexWrap: "wrap", gap: 10 }}
      onSubmit={async (ev) => {
        ev.preventDefault();
        const g = Number(gbv.replace(",", "."));
        if (!(g > 0)) return a.setMsg({ ok: false, t: "Enter the bundle size in GB." });
        const atIso = TIME_OK.test(at) ? fromLocalInput(at) : null;
        if (!atIso) return a.setMsg({ ok: false, t: "That date and time doesn't look right." });
        if (Date.parse(atIso) > Date.now() + 5 * 60000) return a.setMsg({ ok: false, t: "The renewal can't be in the future." });
        const expIso = exp ? fromLocalInput(exp, "23:59:59") : null;
        let res: BundleState | null = null;
        await a.run(async () => (res = await api.renew({ gb: g, at: atIso, expiresAt: expIso })), `Logged a ${g} GB renewal.`, "Couldn't log the renewal.");
        if (res) onDone(res);
      }}
    >
      <b>I renewed</b>
      <label className="t2" style={{ display: "flex", gap: 6, alignItems: "center" }}>
        Size
        <input className="inp num" style={{ width: 70 }} inputMode="decimal" aria-label="Size (GB)" placeholder="250" value={gbv} onChange={(e) => setGb(e.target.value)} required />
        GB
      </label>
      <label className="t2" style={{ display: "flex", gap: 6, alignItems: "center" }}>
        on
        <input className="inp num" type="datetime-local" aria-label="Renewed at (gateway time)" value={at} onChange={(e) => setAt(e.target.value)} />
      </label>
      <label className="t2" style={{ display: "flex", gap: 6, alignItems: "center" }}>
        ends
        <input className="inp num" type="date" aria-label="Ends (optional)" value={exp} onChange={(e) => setExp(e.target.value)} />
      </label>
      <button type="submit" className="btn pri sm" disabled={a.busy}>
        {a.busy ? "Saving…" : "Log renewal"}
      </button>
      {a.msg && !a.msg.ok && <span className="err">{a.msg.t}</span>}
    </form>
  );
}

function CheckForm({ onDone }: { onDone: (s: BundleState) => void }) {
  const [val, setVal] = useState("");
  const a = useAct(() => {});
  return (
    <form
      className="line-alert"
      style={{ marginTop: 0, marginBottom: 12, flexWrap: "wrap", gap: 10 }}
      onSubmit={async (ev) => {
        ev.preventDefault();
        const g = Number(val.replace(",", "."));
        if (!(g >= 0) || val.trim() === "") return a.setMsg({ ok: false, t: "Enter how much is left, in GB." });
        let res: BundleState | null = null;
        await a.run(async () => (res = await api.check({ gbLeft: g })), "Logged.", "Couldn't log it.");
        if (res) onDone(res);
      }}
    >
      <b>Left right now</b>
      <input className="inp num" style={{ width: 80 }} inputMode="decimal" aria-label="GB left right now" placeholder="96.5" value={val} onChange={(e) => setVal(e.target.value)} required />
      <span className="t2">GB · Junction counts down from this</span>
      <button type="submit" className="btn pri sm" disabled={a.busy}>
        {a.busy ? "Saving…" : "Log it"}
      </button>
      {a.msg && !a.msg.ok && <span className="err">{a.msg.t}</span>}
    </form>
  );
}
