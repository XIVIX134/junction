// More chart pieces: stacked columns, mini bars for table rows, smooth
// reordering of lists, and a highlight for rows that just arrived.

import {
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import {
  ChartTip,
  niceMax,
  reducedMotion,
  useWidth,
  type TipRow,
} from "./charts";

export type Stack = { key: string; name: string; color: string };

/** Stacked columns: one column per x, one segment per stack (2px gaps, rounded top), a tooltip per column. */
export function StackedColumns({
  h,
  stacks,
  values,
  xLabel,
  tipTitle,
  yFmt,
  label,
  padL = 54,
}: {
  h: number;
  stacks: Stack[];
  values: number[][]; // [column][stack]
  xLabel: (i: number) => string | null;
  tipTitle: (i: number) => ReactNode;
  yFmt: (v: number) => string;
  label: string;
  padL?: number;
}) {
  const [ref, w] = useWidth<HTMLDivElement>();
  const [hi, setHi] = useState<number | null>(null);
  const pad = { l: padL, r: 8, t: 12, b: 24 };
  const iw = Math.max(10, w - pad.l - pad.r);
  const ih = h - pad.t - pad.b;
  const totals = values.map((c) => c.reduce((a, b) => a + b, 0));
  const max = niceMax(Math.max(0, ...totals));
  const n = values.length;
  const sw = iw / Math.max(7, n);
  const bw = Math.max(3, Math.min(24, sw - 4));
  const base = pad.t + ih;
  const key = (e: KeyboardEvent) => {
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      e.preventDefault();
      setHi((c) =>
        Math.max(
          0,
          Math.min(n - 1, (c ?? n - 1) + (e.key === "ArrowLeft" ? -1 : 1)),
        ),
      );
    }
    if (e.key === "Escape") setHi(null);
  };
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
        onBlur={() => setHi(null)}
        onFocus={() => setHi((c) => c ?? n - 1)}
      >
        {[0, 1, 2, 3, 4].map((k) => {
          const yy = base - (ih * k) / 4;
          return (
            <g key={k}>
              <line
                x1={pad.l}
                x2={w - pad.r}
                y1={yy}
                y2={yy}
                stroke={k ? "var(--chart-grid)" : "var(--chart-axis)"}
              />
              <text x={pad.l - 8} y={yy + 4} textAnchor="end" className="tick">
                {yFmt((max * k) / 4)}
              </text>
            </g>
          );
        })}
        {values.map((col, i) => {
          const cx = pad.l + i * sw + sw / 2;
          const x0 = cx - bw / 2;
          let y = base;
          const segs = col.map((v, s) => ({ v, s })).filter((x) => x.v > 0);
          const lab = xLabel(i);
          return (
            <g
              key={i}
              className={`col${hi === i ? " on" : ""}${hi != null && hi !== i ? " off" : ""}`}
            >
              <g
                className="grow"
                style={{ animationDelay: `${Math.min(i, 40) * 14}ms` }}
              >
                {segs.map(({ v, s }, k) => {
                  const sh = Math.max(1, (v / max) * ih);
                  const top = y - sh;
                  const last = k === segs.length - 1;
                  const r = last ? Math.min(4, sh, bw / 2) : 0;
                  const gap = k > 0 ? 2 : 0; // the surface shows between segments
                  const d = `M${x0} ${y - gap}V${top + r}${r ? `Q${x0} ${top} ${x0 + r} ${top}H${x0 + bw - r}Q${x0 + bw} ${top} ${x0 + bw} ${top + r}` : `H${x0 + bw}`}V${y - gap}Z`;
                  y = top;
                  return <path key={s} d={d} fill={stacks[s].color} />;
                })}
              </g>
              {lab && (
                <text x={cx} y={h - 6} textAnchor="middle" className="tick">
                  {lab}
                </text>
              )}
              <rect
                x={pad.l + i * sw}
                y={pad.t}
                width={sw}
                height={ih}
                fill="transparent"
                onPointerEnter={() => setHi(i)}
                onPointerDown={() => setHi(i)}
                onPointerLeave={() => setHi(null)}
              />
            </g>
          );
        })}
      </svg>
      {hi != null && (
        <ChartTip
          side="above"
          x={pad.l + hi * sw + sw / 2}
          y={base - (totals[hi] / max) * ih}
          w={w}
          title={tipTitle(hi)}
          rows={[
            ...stacks
              .map((s, k) => ({
                label: s.name,
                value: yFmt(values[hi][k]),
                color: s.color,
                v: values[hi][k],
              }))
              .filter((r) => r.v > 0)
              .reverse()
              .map(({ v: _v, ...r }) => r as TipRow),
            { label: "in total", value: yFmt(totals[hi]) },
          ]}
        />
      )}
    </div>
  );
}

/** Tiny columns for a table row (e.g. 7 days); the last is the accent; a tooltip per column. */
export function MiniBars({
  vals,
  labels,
  fmt,
  w = 84,
  h = 22,
  label,
}: {
  vals: number[];
  labels: string[];
  fmt: (v: number) => string;
  w?: number;
  h?: number;
  label: string;
}) {
  const [hi, setHi] = useState<number | null>(null);
  const max = Math.max(1, ...vals);
  const n = vals.length;
  const sw = w / n;
  const bw = Math.max(2, Math.min(8, sw - 3));
  return (
    <span
      className="minibars"
      style={{ width: w, height: h }}
      onPointerLeave={() => setHi(null)}
    >
      <svg
        width={w}
        height={h}
        role="img"
        aria-label={`${label}: ${vals.map((v, i) => `${labels[i]} ${fmt(v)}`).join(", ")}`}
      >
        {vals.map((v, i) => {
          const bh = v > 0 ? Math.max(2, (v / max) * (h - 2)) : 1;
          const x = i * sw + (sw - bw) / 2;
          return (
            <g key={i}>
              <rect
                className="grow"
                style={{ animationDelay: `${i * 30}ms` }}
                x={x}
                y={h - bh}
                width={bw}
                height={bh}
                rx={Math.min(2, bw / 2)}
                fill={
                  i === n - 1
                    ? "var(--accent)"
                    : v > 0
                      ? "var(--accent-col)"
                      : "var(--track)"
                }
                opacity={hi != null && hi !== i ? 0.5 : 1}
              />
              <rect
                x={i * sw}
                y={0}
                width={sw}
                height={h}
                fill="transparent"
                onPointerEnter={() => setHi(i)}
              />
            </g>
          );
        })}
      </svg>
      {hi != null && (
        <span className="stip" style={{ left: hi * sw + sw / 2 }}>
          <b>{fmt(vals[hi])}</b> · {labels[hi]}
        </span>
      )}
    </span>
  );
}

/** FLIP: children with data-flip move smoothly to their new place when the list reorders. */
export function useFlip(order: string) {
  const ref = useRef<HTMLDivElement>(null);
  const last = useRef(new Map<string, number>());
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const now = new Map<string, number>();
    el.querySelectorAll<HTMLElement>("[data-flip]").forEach((c) =>
      now.set(c.dataset.flip!, c.getBoundingClientRect().top),
    );
    if (!reducedMotion())
      el.querySelectorAll<HTMLElement>("[data-flip]").forEach((c) => {
        const before = last.current.get(c.dataset.flip!);
        const after = now.get(c.dataset.flip!)!;
        if (before == null || Math.abs(before - after) < 1) return;
        c.animate(
          [
            { transform: `translateY(${before - after}px)` },
            { transform: "none" },
          ],
          { duration: 420, easing: "cubic-bezier(.2,.7,.2,1)" },
        );
      });
    last.current = now;
  }, [order]);
  return ref;
}

/** The ids that weren't there on the previous render (none on the first), to highlight new rows. */
export function useFresh(ids: (string | number)[]) {
  const seen = useRef<Set<string | number> | null>(null);
  const fresh = new Set<string | number>();
  if (seen.current)
    for (const id of ids) if (!seen.current.has(id)) fresh.add(id);
  useLayoutEffect(() => {
    seen.current = new Set([...(seen.current ?? []), ...ids]);
  });
  return fresh;
}
