// How fast each exit answered its checks over the last hours: one line per
// exit (its colour everywhere in the app) plus Direct; a gap = no reply.

import { useState } from "react";
import { api } from "../api/client";
import type { Overview } from "../api/types";
import { LineChart, type LineSeries } from "../charts";
import { hm } from "../fmt";
import { useFetch } from "../live";
import { exitColorVar } from "../site";

const MINUTES = 360;

export function LatencyPanel({ o }: { o: Overview }) {
  const [table, setTable] = useState(false);
  const hist = useFetch("exit-history-6h", () => api.exitHistory(MINUTES), 60000);
  const samples = hist.data?.samples ?? [];
  const keys = [...o.exits.map((e) => [e.key, e.name] as const), ["direct", "Direct"] as const];
  const series: LineSeries[] = keys.map(([k, name]) => ({
    name,
    values: samples.map((s) => (k in s.exits ? s.exits[k] ?? null : null)),
    color: k === "direct" ? "var(--series-direct)" : exitColorVar(k),
    endLabel: true,
  }));
  // A line that has no value at its end still gets a name: say when it last answered.
  const lastSeen = (s: LineSeries) => {
    for (let i = s.values.length - 1; i >= 0; i--) if (s.values[i] != null) return i;
    return -1;
  };
  const down = series.filter((s) => s.values.length && s.values[s.values.length - 1] == null && lastSeen(s) >= 0);
  const span = samples.length > 1 ? Math.round((Date.parse(samples[samples.length - 1].at) - Date.parse(samples[0].at)) / 60e3) : 0;
  const ms = (v: number) => `${Math.round(v)} ms`;
  // Table: 10-minute averages, newest first.
  const buckets: { at: string; vals: (number | null)[] }[] = [];
  for (let i = 0; i < samples.length; i += 10) {
    const part = samples.slice(i, i + 10);
    buckets.push({
      at: part[0].at,
      vals: series.map((s) => {
        const v = s.values.slice(i, i + 10).filter((x): x is number => x != null);
        return v.length ? v.reduce((a, b) => a + b, 0) / v.length : null;
      }),
    });
  }
  return (
    <section className="pn" style={{ marginBottom: 12 }} aria-labelledby="lat-h">
      <div className="hd">
        <h2 id="lat-h">Latency</h2>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          {span ? `Last ${span >= 90 ? `${Math.round(span / 60)} h` : `${span} min`}` : "Since Junction started"} · how fast each exit answered its check · a gap means no reply
        </span>
        <div className="r">
          <div className="legend" style={{ display: "flex", gap: 12 }}>
            {series.map((s) => (
              <span key={s.name} style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                <i style={{ width: 12, height: 2, background: s.color, display: "inline-block" }} />
                {s.name}
              </span>
            ))}
          </div>
          <button type="button" className="btn sm ghost" aria-pressed={table} onClick={() => setTable(!table)}>
            {table ? "Chart" : "Table"}
          </button>
        </div>
      </div>
      <div className="bd" style={{ padding: "10px 10px 8px" }}>
        {table ? (
          <div className="scroll-x" style={{ maxHeight: 260, overflowY: "auto" }}>
            <table className="tb compact">
              <thead>
                <tr>
                  <th>From</th>
                  {series.map((s) => (
                    <th key={s.name} className="rt">
                      {s.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {[...buckets].reverse().map((b) => (
                  <tr key={b.at}>
                    <td>{hm(b.at)}</td>
                    {b.vals.map((v, i) => (
                      <td key={i} className={`rt ${v == null ? "t3" : ""}`}>
                        {v == null ? "no reply" : ms(v)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
            <div className="note" style={{ marginTop: 4 }}>
              10-minute averages.
            </div>
          </div>
        ) : (
          <>
            <LineChart
              h={210}
              label="Latency of each exit over time"
              labels={samples.map((s) => hm(s.at))}
              series={series}
              yFmt={(v) => `${Math.round(v)} ms`}
              tipFmt={(v) => ms(v)}
              markers={down.map((s) => ({ at: lastSeen(s) + 1, label: `${s.name} stopped answering ${hm(samples[lastSeen(s) + 1]?.at ?? samples[lastSeen(s)].at)}`, tone: "warn" as const }))}
            />
            {!samples.length && !hist.loading && <div className="note">{hist.error ? "Latency history unavailable." : "No latency history yet: it builds up while Junction runs."}</div>}
          </>
        )}
      </div>
    </section>
  );
}
