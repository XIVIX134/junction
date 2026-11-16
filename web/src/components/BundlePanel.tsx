import { useEffect, useRef, useState, type RefObject } from "react";
import { Columns, CountUp, LineChart } from "../charts";
import { api, ApiError } from "../api/client";
import type { Bundle, BundleEvent, BundleState, Overview, UsageDay } from "../api/types";
import { bumpEvents } from "../events";
import { bytes, dayKey, dmy, hm, MON } from "../fmt";
import { fromLocalInput, toLocalInput } from "../format";
import { useFetch, useLive } from "../live";

// The bundle as Junction counts it: whole packets on the wire (wire.go), counting
// down from the last renewal or from the "left" you set by hand.

const gbs = (b: number) =>
  `${(b / 1e9).toFixed(Math.abs(b) >= 10e9 ? 1 : 2)} GB`;
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

export function bundleView(
  b: Bundle | null | undefined,
  now: number,
): BundleView | null {
  if (!b) return null;
  const cycleEnd = b.expiresAt;
  const runsOut = b.runsOut ? Date.parse(b.runsOut) : null;
  const earlyDays =
    runsOut && cycleEnd
      ? Math.floor((Date.parse(cycleEnd) - runsOut) / DAY)
      : null;
  return {
    size: b.size,
    cycleStart: b.renewedAt,
    cycleEnd,
    renewInDays: cycleEnd
      ? Math.max(0, Math.ceil((Date.parse(cycleEnd) - now) / DAY))
      : null,
    used: Math.max(0, b.size - b.left),
    left: b.left,
    setLeft:
      b.anchor.kind === "check"
        ? { amount: b.anchor.amount, at: b.anchor.at }
        : null,
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
    if (loaded && (location.hash === "#bundle" || location.hash === "#renew"))
      ref.current?.scrollIntoView({ block: "start" });
  }, [loaded]);

  const now = Date.parse(o.now);
  const b = o.bundle;
  const v = bundleView(b, now);

  function renewed() {
    setForm("renew");
    setLogOpen(true);
    requestAnimationFrame(() =>
      logRef.current?.scrollIntoView({ block: "nearest" }),
    );
  }

  return (
    <section
      className="pn c12"
      id="bundle"
      ref={ref}
      aria-labelledby="b-h"
      style={{ scrollMarginTop: 12 }}
    >
      <div className="hd">
        <h2 id="b-h">Bundle</h2>
        {v && <span className="t2">{(v.size / 1e9).toFixed(0)} GB</span>}
        {v && (
          <span className="t3">
            {dmy(v.cycleStart)}
            {v.cycleEnd ? ` → ${dmy(v.cycleEnd)}` : ""}
            {v.renewInDays != null
              ? ` · renews in ${v.renewInDays} day${v.renewInDays === 1 ? "" : "s"}`
              : ""}
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
              <span className={`big ${v.left <= 0 ? "badc" : ""}`}>
                <CountUp value={v.left} fmt={gbs} ms={900} />
              </span>{" "}
              <span className="t2">left</span>
            </div>
            <div className="note" style={{ marginTop: 2 }}>
              {bytes(v.used)} used, whole packets as your ISP bills them ·{" "}
              {v.setLeft
                ? `counting from the ${gbs(v.setLeft.amount)} you set on ${dmy(v.setLeft.at)} ${hm(v.setLeft.at)}`
                : `since you renewed on ${dmy(v.cycleStart)} ${hm(v.cycleStart)}`}
            </div>
            <UsedBar v={v} />
            <div className="facts">
              <Fact
                k="Pace"
                v={v.pace ? `${bytes(v.pace)}/day` : "Needs 6 h"}
              />
              <Fact
                k="Runs out"
                v={
                  v.runsOut
                    ? `~${dmy(v.runsOut)}${v.earlyDays ? `, ${v.earlyDays} day${v.earlyDays === 1 ? "" : "s"} early` : ""}`
                    : "—"
                }
                cls={v.earlyDays ? "warnc" : ""}
              />
              <Fact
                k="Matched to your ISP"
                v={
                  b.calibration
                    ? b.calibration.factor === 1
                      ? "exactly"
                      : `ISP counts ${Math.abs((1 - b.calibration.factor) * 100).toFixed(1)}% ${b.calibration.factor < 1 ? "less" : "more"}`
                    : "after 2 checks"
                }
                tip={
                  b.calibration
                    ? `Your ISP counted ${bytes(b.calibration.isp)} where the gateway counted ${bytes(b.calibration.junction)}, between the figures you logged since ${dmy(b.calibration.since)}.\nSo Junction counts down at your ISP's rate.`
                    : "Log what's left from your ISP's app a couple of times, at least 10 GB apart. Junction then learns how your ISP counts and matches it."
                }
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
      <div
        className={`dbar ${compact ? "compact" : ""}`}
        role="img"
        aria-label={`${bytes(v.used)} used of ${(v.size / 1e9).toFixed(0)} GB`}
      >
        <i
          className="fill"
          style={{ width: pct }}
          title={`${bytes(v.used)} used`}
        />
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

const Fact = ({ k, v, cls, tip }: { k: string; v: string; cls?: string; tip?: string }) => (
  <div title={tip}>
    <div className="note">{k}</div>
    <div style={{ fontSize: 14 }} className={cls ?? "num"}>
      {v}
    </div>
  </div>
);

// ---------- every day of the bundle ----------

type Slot = {
  day: string;
  gw: number | null;
  content: number;
  tunnel: number;
  direct: number;
  future: boolean;
};

/** The next calendar day of a "YYYY-MM-DD" key (time-zone free). */
const nextKey = (k: string) => {
  const d = new Date(`${k}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().slice(0, 10);
};

function Days({
  v,
  usage,
  now,
}: {
  v: BundleView | null;
  usage: UsageDay[];
  now: number;
}) {
  const [view, setView] = useState<"day" | "total" | "table">(() =>
    matchMedia("(max-width: 600px)").matches ? "table" : "day",
  ); // chart text is unreadable on a phone
  const today = dayKey(now);
  const byDay = new Map(usage.map((u) => [u.day, u]));
  // The bundle's days when we know them; otherwise the last 30.
  const cycleStart = v?.cycleStart ?? null;
  const cycleEnd = v?.cycleEnd ?? null;
  const start =
    cycleStart && now - Date.parse(cycleStart) < 31 * DAY
      ? Date.parse(cycleStart)
      : now - 29 * DAY;
  const cycle = !!cycleStart && start === Date.parse(cycleStart);
  const end = cycle && cycleEnd ? Date.parse(cycleEnd) : now;
  const lastKey = dayKey(
    cycle && cycleEnd ? Math.min(end, start + 31 * DAY) - 1 : now,
  ); // a cycle ending at midnight doesn't own that day
  const slots: Slot[] = [];
  for (
    let k = dayKey(start);
    k <= lastKey && slots.length < 32;
    k = nextKey(k)
  ) {
    const u = byDay.get(k);
    const future = k > today;
    slots.push({
      day: k,
      gw: future ? null : u ? u.billed || u.tunnel + u.direct : 0,
      content: u ? u.tunnel + u.direct : 0,
      tunnel: u?.tunnel ?? 0,
      direct: u?.direct ?? 0,
      future,
    });
  }
  const label = (k: string) => `${+k.slice(8)} ${MON[+k.slice(5, 7) - 1]}`;
  const name = (k: string) => (k === today ? "Today" : label(k));
  const gbFmt = (x: number) =>
    `${+(x / 1e9).toFixed(x >= 10e9 || x === 0 ? 0 : 1)} GB`;
  const every = slots.length > 20 ? 3 : slots.length > 10 ? 2 : 1;
  const tIdx = slots.findIndex((s) => s.day === today);

  // Running total: the days added up, lined up with what the bundle says is used now.
  let run = 0;
  const cum = slots.map((s) => (s.future ? null : (run += s.gw ?? 0)));
  const off = v && tIdx >= 0 ? v.used - (cum[tIdx] ?? 0) : 0;
  const used = cum.map((c) => (c == null ? null : Math.max(0, c + off)));
  const size = v?.size ?? 0;
  const n = slots.length;
  const even = slots.map((_, i) =>
    cycle && size ? (size * (i + 1)) / n : null,
  );
  const pace = v?.pace ?? null;
  // At this pace: from today until it reaches the bundle's size (there it runs out).
  let full = false;
  const proj = slots.map((_, i) => {
    if (full || tIdx < 0 || i < tIdx || !pace || used[tIdx] == null)
      return null;
    const x = used[tIdx]! + pace * (i - tIdx);
    if (size && x >= size) {
      full = true;
      return size;
    }
    return x;
  });
  const runsOutAt =
    v?.runsOut != null && slots.length
      ? (v.runsOut - Date.parse(`${slots[0].day}T00:00:00Z`)) / DAY - 0.5
      : null;

  return (
    <div style={{ marginTop: 20 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          flexWrap: "wrap",
        }}
      >
        <b style={{ fontSize: 12.5 }}>
          {cycle ? "This bundle, day by day" : "Last 30 days"}
        </b>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          {view === "total"
            ? "Used so far against an even pace to the end of the bundle."
            : "What the gateway counted on the wire, whole packets."}
        </span>
        <div
          className="seg sm"
          role="group"
          aria-label="Show"
          style={{ marginLeft: "auto" }}
        >
          {(
            [
              ["day", "Per day"],
              ["total", "Running total"],
              ["table", "Table"],
            ] as const
          ).map(([k, l]) => (
            <button
              key={k}
              type="button"
              aria-pressed={view === k}
              className={view === k ? "on" : ""}
              onClick={() => setView(k)}
              disabled={k === "total" && !cycle}
            >
              {l}
            </button>
          ))}
        </div>
      </div>
      {view === "table" ? (
        <div className="scroll-x">
          <table className="tb compact" style={{ marginTop: 6, maxWidth: 820 }}>
            <thead>
              <tr>
                <th>Day</th>
                <th className="rt">Used</th>
                <th className="rt">Running total</th>
                <th className="rt hide-m">Content</th>
                <th className="rt">VPN</th>
                <th className="rt">Direct</th>
              </tr>
            </thead>
            <tbody>
              {slots
                .map((s, i) => ({ s, i }))
                .filter(({ s }) => !s.future)
                .reverse()
                .map(({ s, i }) => (
                  <tr key={s.day}>
                    <td>{name(s.day)}</td>
                    <td className="rt">{bytes(s.gw ?? 0)}</td>
                    <td className="rt t2">{bytes(used[i] ?? 0)}</td>
                    <td className="rt t2 hide-m">{bytes(s.content)}</td>
                    <td className="rt t2">{bytes(s.tunnel)}</td>
                    <td className="rt t2">{bytes(s.direct)}</td>
                  </tr>
                ))}
            </tbody>
          </table>
          <div className="note" style={{ marginTop: 4 }}>
            Used is whole packets on the wire. Content, VPN and Direct are what
            devices sent and received, without the packet overhead.
          </div>
        </div>
      ) : view === "total" ? (
        <div style={{ marginTop: 8 }}>
          <LineChart
            h={220}
            label="Bundle used so far, against an even pace and the bundle size"
            labels={slots.map((s) => label(s.day))}
            ticks={slots
              .map((_, i) => i)
              .filter((i) => i % (every * 2) === 0 || i === n - 1)}
            tipTitle={(i) =>
              name(slots[i].day) + (slots[i].future ? " · ahead" : "")
            }
            yMax={
              Math.ceil(
                (Math.max(
                  size,
                  ...proj.map((x) => x ?? 0),
                  ...used.map((x) => x ?? 0),
                ) *
                  1.05) /
                  4 /
                  5e9,
              ) *
              4 *
              5e9 /* quarter steps of whole 5 GB */
            }
            yFmt={gbFmt}
            tipFmt={(x) => bytes(x)}
            series={[
              {
                name: "Used",
                values: used,
                color: "var(--accent)",
                fill: true,
              },
              {
                name: "At this pace",
                values: proj,
                color: "var(--accent)",
                dash: true,
              },
              {
                name: "Even pace",
                values: even,
                color: "var(--series-direct)",
                thin: true,
              },
              {
                name: `Bundle ${gbFmt(size)}`,
                values: slots.map(() => size || null),
                color: "var(--warn)",
                thin: true,
              },
            ]}
            markers={[
              ...(tIdx >= 0 ? [{ at: tIdx, label: "Today" }] : []),
              ...(runsOutAt != null && runsOutAt >= 0 && runsOutAt <= n - 1
                ? [
                    {
                      at: runsOutAt,
                      label: `Runs out ~${dmy(v!.runsOut!)}`,
                      tone: "warn" as const,
                    },
                  ]
                : []),
            ]}
          />
          <div
            className="legend"
            style={{
              display: "flex",
              gap: 14,
              fontSize: 11.5,
              marginTop: 4,
              flexWrap: "wrap",
            }}
          >
            <span>
              <i className="lk" style={{ borderColor: "var(--accent)" }} />
              Used
            </span>
            <span>
              <i className="lk dash" style={{ borderColor: "var(--accent)" }} />
              At this pace
            </span>
            <span>
              <i
                className="lk thin"
                style={{ borderColor: "var(--series-direct)" }}
              />
              Even pace
            </span>
            <span>
              <i className="lk thin" style={{ borderColor: "var(--warn)" }} />
              Bundle size
            </span>
          </div>
        </div>
      ) : (
        <div style={{ marginTop: 8 }}>
          <Columns
            h={190}
            label="Data per day: what the gateway counted on the wire"
            yFmt={gbFmt}
            xLabel={(i) =>
              slots[i].day === today
                ? "today"
                : i % every === 0 && Math.abs(i - tIdx) > 1
                  ? label(slots[i].day)
                  : null
            }
            cols={slots.map((s) => ({
              key: s.day,
              value: s.gw,
              accent: s.day === today,
              tip: s.future
                ? label(s.day)
                : `${name(s.day)}${s.day === today ? " · so far" : ""}`,
              rows: s.future
                ? [{ label: "", value: "not yet", muted: true }]
                : [
                    { label: "on the wire", value: bytes(s.gw ?? 0) },
                    { label: "through VPN", value: bytes(s.tunnel) },
                    { label: "direct", value: bytes(s.direct) },
                  ],
            }))}
          />
        </div>
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
    if (s && typeof s === "object" && "events" in (s as object))
      onChange(s as BundleState);
  });
  const evs = st?.events ?? [];
  return (
    <details
      className="entries"
      ref={detailsRef}
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary>
        Renewals and corrections <span className="t3">· {evs.length}</span>
      </summary>
      <div
        style={{ display: "flex", gap: 8, flexWrap: "wrap", margin: "10px 0" }}
      >
        <button
          type="button"
          className="btn sm"
          aria-pressed={form === "renew"}
          onClick={() => setForm(form === "renew" ? null : "renew")}
        >
          I renewed
        </button>
        {hasBundle && (
          <button
            type="button"
            className="btn sm"
            aria-pressed={form === "check"}
            onClick={() => setForm(form === "check" ? null : "check")}
          >
            Set what's left…
          </button>
        )}
        <span className="note" style={{ alignSelf: "center" }}>
          Log each renewal, and set what's left whenever you check your ISP's
          app: Junction counts down from it.
        </span>
      </div>
      {form === "renew" && (
        <RenewForm onDone={(s) => (onChange(s), setForm(null))} />
      )}
      {form === "check" && (
        <CheckForm onDone={(s) => (onChange(s), setForm(null))} />
      )}
      <div className="scroll-x">
        <table className="tb compact" style={{ maxWidth: 820 }}>
          <thead>
            <tr>
              <th>Logged</th>
              <th>Entry</th>
              <th className="rt">Amount</th>
              <th className="rt" title="What Junction said was left at that moment, counting from the entry before">
                Junction said
              </th>
              <th>Difference</th>
              <th>Ends</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {evs.map((e, i) => (
              <tr key={e.id}>
                <td className="t2">
                  {dmy(e.at)} {hm(e.at)}
                </td>
                <td>{e.kind === "renewal" ? "Renewed" : "Set left"}</td>
                <td className="rt">
                  {(e.amount / 1e9).toFixed(e.amount % 1e9 ? 1 : 0)} GB
                </td>
                <Drift e={e} older={evs.slice(i + 1)} />
                <td className="t3">{e.expiresAt ? dmy(e.expiresAt) : "—"}</td>
                <td className="rt">
                  {confirm === e.id ? (
                    <span style={{ display: "inline-flex", gap: 6 }}>
                      <button
                        type="button"
                        className="btn sm danger"
                        disabled={a.busy}
                        onClick={() =>
                          a
                            .run(
                              () => api.deleteBundleEvent(e.id),
                              "Entry deleted.",
                              "Couldn't delete it.",
                            )
                            .then(() => setConfirm(null))
                        }
                      >
                        Delete
                      </button>
                      <button
                        type="button"
                        className="btn sm ghost"
                        onClick={() => setConfirm(null)}
                      >
                        Keep
                      </button>
                    </span>
                  ) : (
                    <button
                      type="button"
                      className="btn sm ghost"
                      aria-label={`Undo ${e.kind === "renewal" ? "renewal" : "left set"} of ${dmy(e.at)} ${hm(e.at)}`}
                      onClick={() => setConfirm(e.id)}
                    >
                      Undo
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {st && !evs.length && (
              <tr>
                <td colSpan={7} className="t3">
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

/** How far Junction was from your ISP at a check, as counted since the entry before it. */
export function driftOf(e: BundleEvent, older: BundleEvent[]) {
  if (e.kind !== "check" || e.expected == null) return null;
  const prev = older[0];
  const diff = e.amount - e.expected; // > 0: the ISP says more is left, Junction counted too much
  // As a share of what the ISP counted since the entry before; only when that was
  // enough to say something (the ISP's app is about 0.1 GB either way).
  const isp = prev ? prev.amount - e.amount : 0;
  const pct = isp >= 2e9 ? (Math.abs(diff) / isp) * 100 : null;
  return { diff, pct, isp, close: Math.abs(diff) <= 0.1e9 || (pct != null && pct < 0.5) };
}

function Drift({ e, older }: { e: BundleEvent; older: BundleEvent[] }) {
  const d = driftOf(e, older);
  if (!d)
    return (
      <>
        <td className="rt t3">—</td>
        <td className="t3">—</td>
      </>
    );
  const amt = gbs(Math.abs(d.diff));
  const pct = d.pct != null ? ` (${d.pct < 10 ? d.pct.toFixed(1) : Math.round(d.pct)}%)` : "";
  const tip = d.close
    ? "Junction and your ISP agreed (within 0.1 GB)."
    : (d.diff > 0
        ? `Since the entry before, your ISP counted ${gbs(d.isp)} and Junction ${amt} more than that.`
        : `Since the entry before, your ISP counted ${gbs(d.isp)} and Junction ${amt} less than that: traffic that didn't go through the gateway, for example.`) +
      (d.pct == null ? "\nToo little in between for a percentage." : "");
  return (
    <>
      <td className="rt num">{gbs(e.expected!)}</td>
      <td className={d.close ? "okc" : "t2"} title={tip}>
        {d.close ? "matched" : d.diff > 0 ? `counted ${amt} more${pct}` : `counted ${amt} less${pct}`}
      </td>
    </>
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
        if (!(g > 0))
          return a.setMsg({ ok: false, t: "Enter the bundle size in GB." });
        const atIso = TIME_OK.test(at) ? fromLocalInput(at) : null;
        if (!atIso)
          return a.setMsg({
            ok: false,
            t: "That date and time doesn't look right.",
          });
        if (Date.parse(atIso) > Date.now() + 5 * 60000)
          return a.setMsg({
            ok: false,
            t: "The renewal can't be in the future.",
          });
        const expIso = exp ? fromLocalInput(exp, "23:59:59") : null;
        let res: BundleState | null = null;
        await a.run(
          async () =>
            (res = await api.renew({ gb: g, at: atIso, expiresAt: expIso })),
          `Logged a ${g} GB renewal.`,
          "Couldn't log the renewal.",
        );
        if (res) onDone(res);
      }}
    >
      <b>I renewed</b>
      <label
        className="t2"
        style={{ display: "flex", gap: 6, alignItems: "center" }}
      >
        Size
        <input
          className="inp num"
          style={{ width: 70 }}
          inputMode="decimal"
          aria-label="Size (GB)"
          placeholder="250"
          value={gbv}
          onChange={(e) => setGb(e.target.value)}
          required
        />
        GB
      </label>
      <label
        className="t2"
        style={{ display: "flex", gap: 6, alignItems: "center" }}
      >
        on
        <input
          className="inp num"
          type="datetime-local"
          aria-label="Renewed at (gateway time)"
          value={at}
          onChange={(e) => setAt(e.target.value)}
        />
      </label>
      <label
        className="t2"
        style={{ display: "flex", gap: 6, alignItems: "center" }}
      >
        ends
        <input
          className="inp num"
          type="date"
          aria-label="Ends (optional)"
          value={exp}
          onChange={(e) => setExp(e.target.value)}
        />
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
        if (!(g >= 0) || val.trim() === "")
          return a.setMsg({ ok: false, t: "Enter how much is left, in GB." });
        let res: BundleState | null = null;
        await a.run(
          async () => (res = await api.check({ gbLeft: g })),
          "Logged.",
          "Couldn't log it.",
        );
        if (res) onDone(res);
      }}
    >
      <b>Left right now</b>
      <input
        className="inp num"
        style={{ width: 80 }}
        inputMode="decimal"
        aria-label="GB left right now"
        placeholder="96.5"
        value={val}
        onChange={(e) => setVal(e.target.value)}
        required
      />
      <span className="t2">GB · Junction counts down from this</span>
      <button type="submit" className="btn pri sm" disabled={a.busy}>
        {a.busy ? "Saving…" : "Log it"}
      </button>
      {a.msg && !a.msg.ok && <span className="err">{a.msg.t}</span>}
    </form>
  );
}
