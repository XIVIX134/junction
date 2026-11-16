// Charts: plain SVG, one hover layer each (crosshair + tooltip on lines, a
// tooltip per mark on bars and cells), keyboard reachable, animated in once
// when they first appear (not on every refresh), still for reduced motion.
// Every chart that carries numbers has a table view next to it (callers).

import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";

export const reducedMotion = () => typeof matchMedia !== "undefined" && matchMedia("(prefers-reduced-motion: reduce)").matches;

/** Width of the element, kept up to date. */
export function useWidth<T extends HTMLElement>(fallback = 600) {
  const ref = useRef<T>(null);
  const [w, setW] = useState(fallback);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setW(el.clientWidth || fallback));
    ro.observe(el);
    setW(el.clientWidth || fallback);
    return () => ro.disconnect();
  }, [fallback]);
  return [ref, w] as const;
}

export function niceMax(v: number) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 4, 6, 8, 10]) if (m * p >= v) return m * p; // quarters stay round: 0.25, 0.5, 1, 1.5, 2, 2.5
  return 10 * p;
}

/** A number that eases from its last value to the new one (count-up on first show). */
export function useCountUp(value: number, ms = 700) {
  const [shown, setShown] = useState(() => (reducedMotion() ? value : 0));
  const from = useRef(reducedMotion() ? value : 0);
  useEffect(() => {
    if (!isFinite(value)) return setShown(value);
    if (reducedMotion()) {
      from.current = value;
      return setShown(value);
    }
    const a = from.current;
    const t0 = performance.now();
    let raf = 0;
    const step = (t: number) => {
      const k = Math.min(1, (t - t0) / ms);
      const e = 1 - Math.pow(1 - k, 3);
      const v = a + (value - a) * e;
      from.current = v;
      setShown(v);
      if (k < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [value, ms]);
  return shown;
}

/** Renders fmt(value) counting up to it. */
export function CountUp({ value, fmt, ms }: { value: number; fmt: (v: number) => string; ms?: number }) {
  const v = useCountUp(value, ms);
  return <>{fmt(v)}</>;
}

// ---------- tooltip ----------

export type TipRow = { label: string; value: string; color?: string; dash?: boolean; muted?: boolean };

/** The one tooltip style: title, then rows with a line key, value first. Positioned in its chart (left/top in px). */
export function ChartTip({ x, y, w, title, rows, side }: { x: number; y: number; w: number; title: ReactNode; rows: TipRow[]; side?: "auto" | "above" }) {
  const flip = x > w - 190;
  const style: React.CSSProperties =
    side === "above"
      ? { left: Math.max(4, Math.min(w - 4, x)), top: y, transform: `translate(${x > w - 110 ? "-100%" : x < 110 ? "0" : "-50%"}, calc(-100% - 10px))` }
      : { left: flip ? undefined : x + 14, right: flip ? w - x + 14 : undefined, top: Math.max(0, y) };
  return (
    <div className="ctip" style={style} role="presentation">
      <div className="ctip-t">{title}</div>
      {rows.map((r, i) => (
        <div className={`ctip-r${r.muted ? " mut" : ""}`} key={i}>
          {r.color && <i className={r.dash ? "k dash" : "k"} style={{ borderColor: r.color }} />}
          <b>{r.value}</b>
          <span>{r.label}</span>
        </div>
      ))}
    </div>
  );
}

// ---------- line / area ----------

export type LineSeries = {
  name: string;
  values: (number | null)[];
  color: string;
  fill?: boolean; // a 10% wash under the line
  dash?: boolean; // a projection
  thin?: boolean; // context (1px)
  endLabel?: boolean; // name + last value at the right end
  hideInTip?: boolean;
};

export type Marker = { at: number /* index, may be fractional */; label: string; tone?: "now" | "warn" };

/** Line/area chart: hairline grid, x ticks, markers, crosshair + tooltip, draw-in. */
export function LineChart({
  h,
  series,
  labels,
  tipTitle,
  yFmt,
  tipFmt,
  markers = [],
  padL = 62,
  padR = 20,
  ticks,
  yMax,
  label,
}: {
  h: number;
  series: LineSeries[];
  labels: string[];
  tipTitle?: (i: number) => ReactNode;
  yFmt: (v: number) => string;
  tipFmt?: (v: number, s: LineSeries) => string;
  markers?: Marker[];
  padL?: number;
  padR?: number;
  ticks?: number[];
  yMax?: number;
  label: string;
}) {
  const [ref, w] = useWidth<HTMLDivElement>();
  const [hi, setHi] = useState<number | null>(null);
  const n = Math.max(2, labels.length);
  const ends = series.some((s) => s.endLabel);
  const pad = { l: padL, r: ends ? Math.max(padR, 118) : padR, t: 12, b: 24 };
  const iw = Math.max(10, w - pad.l - pad.r);
  const ih = h - pad.t - pad.b;
  const all = series.flatMap((s) => s.values.filter((v): v is number => v != null));
  const max = yMax ?? niceMax(all.length ? Math.max(...all) : 0);
  const x = (i: number) => pad.l + (iw * i) / (n - 1);
  const y = (v: number) => pad.t + ih - (ih * Math.min(v, max)) / max;
  const tks = (ticks ?? (w < 520 ? [0, 0.25, 0.5, 0.75, 1] : [0, 0.125, 0.25, 0.375, 0.5, 0.625, 0.75, 0.875, 1]).map((f) => Math.round(f * (n - 1)))).filter((i, k, a) => i < n && a.indexOf(i) === k);
  const fmt = (v: number, s: LineSeries) => (tipFmt ? tipFmt(v, s) : yFmt(v));

  const pick = (e: PointerEvent<SVGRectElement>) => {
    const r = (e.currentTarget.ownerSVGElement ?? e.currentTarget).getBoundingClientRect();
    const i = Math.round(((e.clientX - r.left - pad.l) / iw) * (n - 1));
    setHi(Math.max(0, Math.min(labels.length - 1, i)));
  };
  const key = (e: KeyboardEvent) => {
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      e.preventDefault();
      const last = labels.length - 1;
      setHi((c) => Math.max(0, Math.min(last, (c ?? last) + (e.key === "ArrowLeft" ? -1 : 1))));
    }
    if (e.key === "Escape") setHi(null);
  };

  const paths = series.map((s) => {
    const segs: string[] = [];
    let cur = "";
    let first: number | null = null;
    let last: number | null = null;
    s.values.forEach((v, i) => {
      if (v == null) {
        if (cur) segs.push(cur);
        cur = "";
        return;
      }
      cur += `${cur ? "L" : "M"}${x(i).toFixed(1)} ${y(v).toFixed(1)}`;
      if (first == null) first = i;
      last = i;
    });
    if (cur) segs.push(cur);
    return { s, d: segs.join(""), segs, first: first as number | null, last: last as number | null };
  });
  // End labels, nudged apart only when they'd overlap (with a leader line).
  const endPos = paths
    .filter((p) => p.s.endLabel && p.last != null)
    .map((p) => ({ p, y0: y(p.s.values[p.last!]!), y: y(p.s.values[p.last!]!) }))
    .sort((a, b) => a.y0 - b.y0);
  for (let i = 1; i < endPos.length; i++) if (endPos[i].y - endPos[i - 1].y < 14) endPos[i].y = endPos[i - 1].y + 14;

  return (
    <div ref={ref} className="chart">
      <svg
        width={w}
        height={h}
        viewBox={`0 0 ${w} ${h}`}
        role="img"
        aria-label={label}
        tabIndex={0}
        onKeyDown={key}
        onFocus={() => setHi((c) => c ?? labels.length - 1)}
        onBlur={() => setHi(null)}
      >
        {[0, 1, 2, 3, 4].map((k) => {
          const v = (max * k) / 4;
          const yy = y(v);
          return (
            <g key={k}>
              <line x1={pad.l} x2={w - pad.r} y1={yy} y2={yy} stroke={k ? "var(--chart-grid)" : "var(--chart-axis)"} />
              <text x={pad.l - 8} y={yy + 4} textAnchor="end" className="tick">
                {yFmt(v)}
              </text>
            </g>
          );
        })}
        {tks.map((i) => (
          <text key={i} x={x(i)} y={h - 6} textAnchor={i === 0 ? "start" : i === n - 1 ? "end" : "middle"} className="tick">
            {labels[i]}
          </text>
        ))}
        {markers.map((m, k) =>
          m.at >= 0 && m.at <= n - 1 ? (
            <g key={k} className={`mark ${m.tone ?? "now"}`}>
              <line x1={x(m.at)} x2={x(m.at)} y1={pad.t} y2={pad.t + ih} />
              <text x={x(m.at) + (x(m.at) > w - pad.r - 120 ? -5 : 5)} y={pad.t + 10} textAnchor={x(m.at) > w - pad.r - 120 ? "end" : "start"}>
                {m.label}
              </text>
            </g>
          ) : null,
        )}
        {paths.map(({ s, d, segs, last }, si) =>
          d ? (
            <g key={si}>
              {s.fill &&
                segs.map((sd, k) => {
                  const xs = [...sd.matchAll(/[ML]([\d.]+) /g)].map((m) => m[1]);
                  return <path key={k} className="wash" d={`${sd}L${xs[xs.length - 1]} ${y(0)}L${xs[0]} ${y(0)}Z`} fill={s.color} />;
                })}
              <path className={s.dash ? "ln dash" : "ln draw"} pathLength={s.dash ? undefined : 1} d={d} stroke={s.color} strokeWidth={s.thin ? 1 : 2} />
              {last != null && !s.thin && <circle className="enddot" cx={x(last)} cy={y(s.values[last]!)} r={4} fill={s.color} />}
            </g>
          ) : null,
        )}
        {endPos.map(({ p, y0, y: ly }) => (
          <g key={p.s.name} className="endlab">
            {Math.abs(ly - y0) > 1 && <line x1={x(p.last!) + 6} x2={x(p.last!) + 12} y1={y0} y2={ly} stroke="var(--line2)" />}
            <text x={x(p.last!) + 14} y={ly + 4}>
              <tspan className="v">{fmt(p.s.values[p.last!]!, p.s)}</tspan>
              <tspan dx="5">{p.s.name}</tspan>
            </text>
          </g>
        ))}
        {hi != null && (
          <g className="xhair">
            <line x1={x(hi)} x2={x(hi)} y1={pad.t} y2={pad.t + ih} />
            {series.map((s, si) => (s.values[hi] != null && !s.hideInTip ? <circle key={si} cx={x(hi)} cy={y(s.values[hi]!)} r={4} fill={s.color} /> : null))}
          </g>
        )}
        {!all.length && (
          <text x={pad.l + iw / 2} y={pad.t + ih / 2} textAnchor="middle" className="tick">
            No data yet
          </text>
        )}
        <rect x={pad.l} y={0} width={iw} height={h} fill="transparent" onPointerMove={pick} onPointerDown={pick} onPointerLeave={() => setHi(null)} />
      </svg>
      {hi != null && (
        <ChartTip
          x={x(hi)}
          y={pad.t}
          w={w}
          title={tipTitle ? tipTitle(hi) : labels[hi]}
          rows={series
            .filter((s) => !s.hideInTip)
            .map((s) => ({ label: s.name, color: s.color, dash: s.dash, value: s.values[hi] == null ? "—" : fmt(s.values[hi]!, s), muted: s.values[hi] == null }))}
        />
      )}
    </div>
  );
}

// ---------- columns ----------

export type Column = { key: string; value: number | null /* null = not yet */; tip: ReactNode; rows?: TipRow[]; accent?: boolean };

/** Columns from one baseline: <=24px wide, 4px rounded tops, a tooltip per column. */
export function Columns({ h, cols, yFmt, xLabel, label, padL = 54 }: { h: number; cols: Column[]; yFmt: (v: number) => string; xLabel: (i: number) => string | null; label: string; padL?: number }) {
  const [ref, w] = useWidth<HTMLDivElement>();
  const [hi, setHi] = useState<number | null>(null);
  const pad = { l: padL, r: 8, t: 12, b: 24 };
  const iw = Math.max(10, w - pad.l - pad.r);
  const ih = h - pad.t - pad.b;
  const max = niceMax(Math.max(0, ...cols.map((c) => c.value ?? 0)));
  const sw = iw / Math.max(7, cols.length);
  const bw = Math.max(3, Math.min(24, sw - 4));
  const base = pad.t + ih;
  const key = (e: KeyboardEvent) => {
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      e.preventDefault();
      const last = cols.length - 1;
      setHi((c) => Math.max(0, Math.min(last, (c ?? last) + (e.key === "ArrowLeft" ? -1 : 1))));
    }
    if (e.key === "Escape") setHi(null);
  };
  return (
    <div ref={ref} className="chart">
      <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} role="img" aria-label={label} tabIndex={0} onKeyDown={key} onBlur={() => setHi(null)} onFocus={() => setHi((c) => c ?? cols.length - 1)}>
        {[0, 1, 2, 3, 4].map((k) => {
          const yy = base - (ih * k) / 4;
          return (
            <g key={k}>
              <line x1={pad.l} x2={w - pad.r} y1={yy} y2={yy} stroke={k ? "var(--chart-grid)" : "var(--chart-axis)"} />
              <text x={pad.l - 8} y={yy + 4} textAnchor="end" className="tick">
                {yFmt((max * k) / 4)}
              </text>
            </g>
          );
        })}
        {cols.map((c, i) => {
          const cx = pad.l + i * sw + sw / 2;
          const lab = xLabel(i);
          const bh = c.value == null ? 0 : Math.max(c.value > 0 ? 2 : 0, (c.value / max) * ih);
          const r = Math.min(4, bh, bw / 2);
          const x0 = cx - bw / 2;
          const top = base - bh;
          return (
            <g key={c.key} className={`col${hi === i ? " on" : ""}${hi != null && hi !== i ? " off" : ""}`}>
              {c.value == null ? (
                <rect x={x0} y={base - 2} width={bw} height={2} rx={1} fill="var(--track)" />
              ) : bh > 0 ? (
                <path
                  className="grow"
                  style={{ animationDelay: `${Math.min(i, 40) * 14}ms` }}
                  d={`M${x0} ${base}V${top + r}Q${x0} ${top} ${x0 + r} ${top}H${x0 + bw - r}Q${x0 + bw} ${top} ${x0 + bw} ${top + r}V${base}Z`}
                  fill={c.accent ? "var(--accent)" : "var(--accent-col)"}
                />
              ) : null}
              {lab && (
                <text x={cx} y={h - 6} textAnchor="middle" className="tick">
                  {lab}
                </text>
              )}
              <rect x={pad.l + i * sw} y={pad.t} width={sw} height={ih} fill="transparent" onPointerEnter={() => setHi(i)} onPointerDown={() => setHi(i)} onPointerLeave={() => setHi(null)} />
            </g>
          );
        })}
      </svg>
      {hi != null && cols[hi] && (
        <ChartTip
          side="above"
          x={pad.l + hi * sw + sw / 2}
          y={base - (cols[hi].value == null ? 0 : ((cols[hi].value ?? 0) / max) * ih)}
          w={w}
          title={cols[hi].tip}
          rows={cols[hi].rows ?? (cols[hi].value == null ? [{ label: "", value: "not yet", muted: true }] : [{ label: "", value: yFmt(cols[hi].value!) }])}
        />
      )}
    </div>
  );
}

// ---------- heatmap ----------

/** Rows x columns of cells in one hue, more = stronger; a tooltip per cell; a Less-More scale. */
export function Heatmap({
  rows,
  cols,
  value,
  tip,
  rowLabel,
  colLabel,
  fmt,
  label,
  current,
}: {
  rows: number;
  cols: number;
  value: (r: number, c: number) => number | null; // null = no data (future)
  tip: (r: number, c: number) => string;
  rowLabel: (r: number) => string;
  colLabel: (c: number) => string | null;
  fmt: (v: number) => string;
  label: string;
  current?: [number, number];
}) {
  const [ref, w] = useWidth<HTMLDivElement>();
  const [hi, setHi] = useState<[number, number] | null>(null);
  const padL = 44;
  const gap = 2;
  const cw = Math.max(6, (w - padL - 2) / cols - gap);
  const ch = Math.min(26, Math.max(14, cw * 0.62));
  const H = rows * (ch + gap) + 22;
  const vals: number[] = [];
  for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) vals.push(value(r, c) ?? 0);
  const max = Math.max(1, ...vals);
  const STEPS = [14, 30, 48, 70, 100]; // % of the accent over the panel: one hue, light to strong
  const step = (v: number) => (v <= 0 ? -1 : Math.min(4, Math.floor((v / max) * 5 - 1e-9)));
  const key = (e: KeyboardEvent) => {
    const d: Record<string, [number, number]> = { ArrowLeft: [0, -1], ArrowRight: [0, 1], ArrowUp: [-1, 0], ArrowDown: [1, 0] };
    if (d[e.key]) {
      e.preventDefault();
      setHi((c) => {
        const [r, k] = c ?? current ?? [rows - 1, cols - 1];
        return [Math.max(0, Math.min(rows - 1, r + d[e.key][0])), Math.max(0, Math.min(cols - 1, k + d[e.key][1]))];
      });
    }
    if (e.key === "Escape") setHi(null);
  };
  return (
    <div ref={ref} className="chart">
      <svg width={w} height={H} viewBox={`0 0 ${w} ${H}`} role="img" aria-label={label} tabIndex={0} onKeyDown={key} onBlur={() => setHi(null)} onFocus={() => setHi((c) => c ?? current ?? [rows - 1, cols - 1])}>
        {Array.from({ length: rows }, (_, r) => (
          <g key={r}>
            <text x={padL - 8} y={r * (ch + gap) + ch / 2 + 4} textAnchor="end" className="tick">
              {rowLabel(r)}
            </text>
            {Array.from({ length: cols }, (_, c) => {
              const v = value(r, c);
              const s = v == null ? -2 : step(v);
              const on = hi && hi[0] === r && hi[1] === c;
              const cur = current && current[0] === r && current[1] === c;
              return (
                <rect
                  key={c}
                  className={`cell${on ? " on" : ""}${cur ? " cur" : ""}`}
                  style={{ animationDelay: `${c * 12 + r * 18}ms` }}
                  x={padL + c * (cw + gap)}
                  y={r * (ch + gap)}
                  width={cw}
                  height={ch}
                  rx={3}
                  fill={s === -2 ? "transparent" : s < 0 ? "var(--track)" : `color-mix(in oklab, var(--accent) ${STEPS[s]}%, var(--panel))`}
                  stroke={s === -2 ? "var(--track)" : undefined}
                  strokeDasharray={s === -2 ? "2 2" : undefined}
                  onPointerEnter={() => setHi([r, c])}
                  onPointerDown={() => setHi([r, c])}
                  onPointerLeave={() => setHi(null)}
                />
              );
            })}
          </g>
        ))}
        {Array.from({ length: cols }, (_, c) => {
          const l = colLabel(c);
          return l ? (
            <text key={c} x={padL + c * (cw + gap) + cw / 2} y={H - 4} textAnchor="middle" className="tick">
              {l}
            </text>
          ) : null;
        })}
      </svg>
      {hi && (
        <ChartTip
          side="above"
          x={padL + hi[1] * (cw + gap) + cw / 2}
          y={hi[0] * (ch + gap)}
          w={w}
          title={tip(hi[0], hi[1])}
          rows={[{ label: "", value: value(hi[0], hi[1]) == null ? "not yet" : fmt(value(hi[0], hi[1])!), muted: value(hi[0], hi[1]) == null }]}
        />
      )}
      <div className="hscale" aria-hidden="true">
        <span>Less</span>
        <i style={{ background: "var(--track)" }} />
        {STEPS.map((p) => (
          <i key={p} style={{ background: `color-mix(in oklab, var(--accent) ${p}%, var(--panel))` }} />
        ))}
        <span>More</span>
      </div>
    </div>
  );
}

// ---------- sparkline ----------

/** Small trend line; hover shows the value at the pointer. A dashed baseline when there's no data. */
export function Spark({
  vals,
  w = 150,
  h = 26,
  color = "var(--accent)",
  max,
  label,
  fmt = (v) => `${Math.round(v)} ms`,
  tipLabel,
}: {
  vals: (number | null)[];
  w?: number;
  h?: number;
  color?: string;
  max?: number;
  label: string;
  fmt?: (v: number) => string;
  tipLabel?: (i: number) => string;
}) {
  const [hi, setHi] = useState<number | null>(null);
  const v = vals.filter((x): x is number => x != null);
  if (!v.length)
    return (
      <svg width={w} height={h} role="img" aria-label={`${label}: no data`}>
        <line x1="0" x2={w} y1={h - 2} y2={h - 2} stroke={vals.length ? "var(--bad)" : "var(--text3)"} strokeDasharray="2 3" opacity=".7" />
      </svg>
    );
  const mx = max || Math.max(...v) * 1.15 || 1;
  const X = (i: number) => (w * i) / Math.max(1, vals.length - 1);
  const Y = (x: number) => h - 2 - ((h - 4) * Math.min(x, mx)) / mx;
  let d = "";
  vals.forEach((x, i) => {
    if (x == null) return;
    d += `${d && vals[i - 1] != null ? "L" : "M"}${X(i).toFixed(1)} ${Y(x).toFixed(1)}`;
  });
  const lastI = vals.length - 1 - [...vals].reverse().findIndex((x) => x != null);
  const firstI = vals.findIndex((x) => x != null);
  return (
    <span className="spark" style={{ width: w, height: h }}>
      <svg
        width={w}
        height={h}
        role="img"
        aria-label={`${label}: ${fmt(Math.min(...v))}–${fmt(Math.max(...v))}`}
        onPointerMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          setHi(Math.max(0, Math.min(vals.length - 1, Math.round(((e.clientX - r.left) / w) * (vals.length - 1)))));
        }}
        onPointerLeave={() => setHi(null)}
      >
        <path className="wash" d={`${d}L${X(lastI)} ${h}L${X(firstI)} ${h}Z`} fill={color} />
        <path className="ln draw" pathLength={1} d={d} stroke={color} strokeWidth="1.5" />
        {hi != null && vals[hi] != null && <circle cx={X(hi)} cy={Y(vals[hi]!)} r={3} fill={color} className="ring" />}
      </svg>
      {hi != null && (
        <span className="stip" style={{ left: Math.min(w - 4, Math.max(4, X(hi))) }}>
          <b>{vals[hi] == null ? "no reply" : fmt(vals[hi]!)}</b>
          {tipLabel && <span> · {tipLabel(hi)}</span>}
        </span>
      )}
    </span>
  );
}

// ---------- stat tile ----------

/** label · value (counts up) · optional delta · optional trend sparkline. */
export function StatTile({
  label,
  value,
  fmt,
  unit,
  delta,
  trend,
  trendLabel,
  trendFmt,
  sub,
  tip,
}: {
  label: string;
  value: number | null;
  fmt: (v: number) => string;
  unit?: string;
  delta?: { text: string; dir: "up" | "down" | "flat" } | null;
  trend?: (number | null)[];
  trendLabel?: string;
  trendFmt?: (v: number) => string;
  sub?: ReactNode;
  tip?: string; // hover popup on the figure (first line the title, the rest details)
}) {
  const shown = useCountUp(value ?? 0);
  return (
    <div className="stat">
      <div className="stat-l">{label}</div>
      <div className="stat-v" title={tip}>
        {value == null ? "—" : fmt(shown)}
        {unit && <span className="stat-u">{unit}</span>}
      </div>
      <div className="stat-f">
        {delta && <span className={`delta ${delta.dir}`}>{delta.dir === "up" ? "▲" : delta.dir === "down" ? "▼" : "•"} {delta.text}</span>}
        {sub && <span className="t3">{sub}</span>}
      </div>
      {trend && trend.length > 1 && (
        <div className="stat-s">
          <Spark vals={trend} w={120} h={28} label={trendLabel ?? label} fmt={trendFmt} />
        </div>
      )}
    </div>
  );
}
