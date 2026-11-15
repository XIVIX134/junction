// Dashboard additions: the row of live figures and the busy-hours heatmap.

import { useState } from "react";
import type { Device, Overview } from "../api/types";
import { Heatmap, StatTile } from "../charts";
import { bytes, cz, DAY3, MON, rate } from "../fmt";
import { liveHistory } from "../live";

type Hour = { hour: string; down: number; up: number };

const pct = (a: number, b: number) => Math.round((Math.abs(a - b) / Math.max(1, b)) * 100);

/** Download now, upload now, today so far (against yesterday by this time), busiest device now. */
export function StatsRow({ o, hours, devices }: { o: Overview; hours: Hour[]; devices: Device[] }) {
  const now = Date.parse(o.now);
  const hist = liveHistory();
  const today = cz(now).key;
  const yKey = cz(now - 86400e3).key;
  // Today so far, and yesterday up to the same time (its last hour pro rata).
  let t = 0;
  let y = 0;
  const todayHours: number[] = [];
  for (const h of hours) {
    const at = Date.parse(h.hour);
    const k = cz(at).key;
    const b = h.down + h.up;
    if (k === today) {
      t += b;
      todayHours.push(b);
    } else if (k === yKey && at + 86400e3 <= now) {
      const part = Math.min(1, (now - (at + 86400e3)) / 3600e3);
      y += b * (at + 86400e3 + 3600e3 <= now ? 1 : part);
    }
  }
  const busiest = [...devices].filter((d) => d.online).sort((a, b) => b.rateDown + b.rateUp - (a.rateDown + a.rateUp))[0];
  const bRate = busiest ? busiest.rateDown + busiest.rateUp : 0;
  const trend = (k: "down" | "up") => (hist.length > 2 ? hist.map((s) => s[k]) : undefined);
  const peak = (k: "down" | "up") => (hist.length > 2 ? Math.max(...hist.map((s) => s[k])) : null);
  return (
    <div className="stats" role="region" aria-label="Right now">
      <StatTile label="Download now" value={o.live.down} fmt={rate} trend={trend("down")} trendFmt={rate} trendLabel="Download, last 5 minutes" sub={peak("down") != null ? `peak ${rate(peak("down"))} · 5 min` : "whole network"} />
      <StatTile label="Upload now" value={o.live.up} fmt={rate} trend={trend("up")} trendFmt={rate} trendLabel="Upload, last 5 minutes" sub={peak("up") != null ? `peak ${rate(peak("up"))} · 5 min` : "whole network"} />
      <StatTile
        label="Used today"
        value={t}
        fmt={(v) => bytes(v)}
        delta={y > 0 ? { dir: t > y * 1.03 ? "up" : t < y * 0.97 ? "down" : "flat", text: t > y * 1.03 ? `${pct(t, y)}% more than yesterday` : t < y * 0.97 ? `${pct(t, y)}% less than yesterday` : "same as yesterday" } : null}
        trend={todayHours.length > 1 ? todayHours : undefined}
        trendFmt={(v) => bytes(v)}
        trendLabel="Each hour today"
      />
      <StatTile label="Busiest right now" value={bRate > 0 ? bRate : null} fmt={rate} sub={bRate > 0 ? busiest!.name : "everything is quiet"} />
    </div>
  );
}

/** 7 days x 24 hours of whole-network traffic. */
export function BusyHours({ hours, now }: { hours: Hour[]; now: number }) {
  const [table, setTable] = useState(false);
  const days: string[] = [];
  for (let i = 6; i >= 0; i--) days.push(cz(now - i * 86400e3).key);
  const grid = days.map(() => Array<number | null>(24).fill(null));
  for (const h of hours) {
    const p = cz(h.hour);
    const r = days.indexOf(p.key);
    if (r >= 0) grid[r][+p.h] = (grid[r][+p.h] ?? 0) + h.down + h.up;
  }
  const nowP = cz(now);
  // Hours of the last 7 days with no row (Junction wasn't counting yet) read as 0, the future as "not yet".
  for (let r = 0; r < 7; r++) for (let c = 0; c < 24; c++) if (grid[r][c] == null && !(r === 6 && c > +nowP.h)) grid[r][c] = 0;
  const dayLabel = (k: string) => {
    const t = Date.parse(`${k}T12:00:00Z`);
    return `${DAY3[new Date(t).getUTCDay()]} ${+k.slice(8)}`;
  };
  const totals = grid.map((row) => row.reduce<number>((s, v) => s + (v ?? 0), 0));
  const busiest = grid.map((row) => row.reduce<[number, number]>((m, v, c) => ((v ?? 0) > m[1] ? [c, v ?? 0] : m), [-1, 0]));
  const hh = (c: number) => `${String(c).padStart(2, "0")}:00`;
  // The busiest hour across the week, for the subtitle.
  const peak = busiest.reduce<[number, number, number]>((m, [c, v], r) => (v > m[2] ? [r, c, v] : m), [-1, -1, 0]);
  return (
    <section className="pn c8" aria-labelledby="w-busy">
      <div className="hd">
        <h2 id="w-busy">Busy hours</h2>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          Last 7 days · whole network · one square per hour
          {peak[0] >= 0 && ` · busiest ${dayLabel(days[peak[0]])} ${hh(peak[1])}`}
        </span>
        <div className="r">
          <button type="button" className="btn sm ghost" aria-pressed={table} onClick={() => setTable(!table)}>
            {table ? "Chart" : "Table"}
          </button>
        </div>
      </div>
      <div className="bd" style={{ padding: "12px 14px 10px" }}>
        {table ? (
          <table className="tb compact">
            <thead>
              <tr>
                <th>Day</th>
                <th className="rt">Total</th>
                <th>Busiest hour</th>
                <th className="rt">In that hour</th>
              </tr>
            </thead>
            <tbody>
              {days
                .map((k, r) => (
                  <tr key={k}>
                    <td>{r === 6 ? "Today" : `${dayLabel(k)} ${MON[+k.slice(5, 7) - 1]}`}</td>
                    <td className="rt">{bytes(totals[r])}</td>
                    <td className="t2">{busiest[r][0] >= 0 ? `${hh(busiest[r][0])}–${hh((busiest[r][0] + 1) % 24)}` : "—"}</td>
                    <td className="rt t2">{busiest[r][0] >= 0 ? bytes(busiest[r][1]) : "—"}</td>
                  </tr>
                ))
                .reverse()}
            </tbody>
          </table>
        ) : (
          <Heatmap
            rows={7}
            cols={24}
            value={(r, c) => grid[r][c]}
            fmt={(v) => bytes(v)}
            rowLabel={(r) => (r === 6 ? "Today" : dayLabel(days[r]))}
            colLabel={(c) => (c % 3 === 0 ? String(c).padStart(2, "0") : null)}
            tip={(r, c) => `${r === 6 ? "Today" : `${dayLabel(days[r])} ${MON[+days[r].slice(5, 7) - 1]}`}, ${hh(c)}–${hh((c + 1) % 24)}`}
            current={[6, +nowP.h]}
            label="Whole-network traffic per hour over the last 7 days"
          />
        )}
      </div>
    </section>
  );
}
