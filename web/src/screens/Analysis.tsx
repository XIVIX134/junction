// Analysis: what the network is used for (categories), this week at a glance,
// where traffic went (exits per day), events per day, and one device's week.

import { useState } from "react";
import { api } from "../api/client";
import type { CategoriesView, Overview, UsageDay } from "../api/types";
import { Columns, Heatmap, Spark } from "../charts";
import { StackedColumns, useFlip, type Stack } from "../charts2";
import { bytes, cz, DAY3, dmy, MON } from "../fmt";
import { useFetch } from "../live";
import { navigate } from "../router";
import { exitColorVar, exitDisplayName } from "../site";
import { Icon } from "../ui";

const PERIODS = [
  [1, "Today"],
  [7, "7 days"],
  [30, "30 days"],
] as const;

const pctOf = (a: number, b: number) => (b > 0 ? Math.round((a / b) * 100) : 0);
const dayName = (k: string) => {
  const t = Date.parse(`${k}T12:00:00Z`);
  return `${DAY3[new Date(t).getUTCDay()]} ${+k.slice(8)} ${MON[+k.slice(5, 7) - 1]}`;
};
function change(now: number, prev: number) {
  if (prev <= 0) return now > 0 ? { t: "new", dir: "up" as const } : null;
  const r = now / prev;
  if (r > 1.05)
    return { t: `▲ ${Math.round((r - 1) * 100)}%`, dir: "up" as const };
  if (r < 0.95)
    return { t: `▼ ${Math.round((1 - r) * 100)}%`, dir: "down" as const };
  return { t: "≈ same", dir: "flat" as const };
}

const CAT_HELP: Record<string, string> = {
  unnamed:
    "Traffic to an address with no site name (apps that connect by IP, some game and video servers). Junction doesn't guess what it is.",
  web: "Sites that aren't in one of the other categories.",
};

/** Ranked bars of categories; hover for the details, click to open them. */
function CategoryList({ v, device }: { v: CategoriesView; device?: string }) {
  const [open, setOpen] = useState<string | null>(null);
  const cats = v.categories.filter((c) => c.bytes > 0);
  const max = Math.max(1, ...cats.map((c) => c.bytes));
  const flip = useFlip(cats.map((c) => c.key).join());
  const prevLabel = v.days === 1 ? "yesterday" : `the ${v.days} days before`;
  if (!cats.length)
    return (
      <div className="note" style={{ padding: "10px 0" }}>
        No sites seen in this period yet.
      </div>
    );
  return (
    <div className="cats" ref={flip}>
      {cats.map((c) => {
        const ch = change(c.bytes, c.prev);
        const tip = [
          `${c.name}: ${bytes(c.bytes)}, ${pctOf(c.bytes, v.total)}% of all`,
          ...(CAT_HELP[c.key] ? [CAT_HELP[c.key]] : []),
          c.sites.length
            ? `Top sites: ${c.sites
                .slice(0, 3)
                .map((s) => `${s.host} ${bytes(s.bytes)}`)
                .join(", ")}`
            : "",
          !device && c.devices.length
            ? `Most by: ${c.devices
                .slice(0, 3)
                .map((d) => `${d.name} ${bytes(d.bytes)}`)
                .join(", ")}`
            : "",
          `${prevLabel[0].toUpperCase()}${prevLabel.slice(1)}: ${bytes(c.prev)}`,
          "Click for more",
        ]
          .filter(Boolean)
          .join("\n");
        return (
          <div key={c.key} data-flip={c.key}>
            <button
              type="button"
              className="catrow"
              aria-expanded={open === c.key}
              onClick={() => setOpen(open === c.key ? null : c.key)}
              title={tip}
            >
              <span
                style={{
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {c.name}
              </span>
              <span className="bar">
                <i
                  style={{
                    width: `${(c.bytes / max) * 100}%`,
                    background:
                      c.key === "unnamed"
                        ? "var(--series-direct)"
                        : "var(--accent)",
                  }}
                />
              </span>
              <span className="num">{bytes(c.bytes)}</span>
              <span className="num t2">{pctOf(c.bytes, v.total)}%</span>
              <span className="chg">{ch?.t ?? ""}</span>
            </button>
            {open === c.key && (
              <div className="catdet">
                <div>
                  <h4>Top sites</h4>
                  {c.sites.map((s) => (
                    <div className="it" key={s.host}>
                      <span title={s.host}>{s.host}</span>
                      <span>{bytes(s.bytes)}</span>
                    </div>
                  ))}
                </div>
                {!device && (
                  <div>
                    <h4>Most by</h4>
                    {c.devices.map((d) => (
                      <div className="it" key={d.id}>
                        <span>
                          <button
                            type="button"
                            className="linkbtn"
                            onClick={() =>
                              navigate(
                                `/clients/${encodeURIComponent(d.id)}/insights`,
                              )
                            }
                          >
                            {d.name}
                          </button>
                        </span>
                        <span>{bytes(d.bytes)}</span>
                      </div>
                    ))}
                  </div>
                )}
                {CAT_HELP[c.key] && (
                  <div className="note" style={{ gridColumn: "1 / -1" }}>
                    {CAT_HELP[c.key]}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function PeriodSeg({
  days,
  setDays,
}: {
  days: number;
  setDays: (d: number) => void;
}) {
  return (
    <div className="seg sm" role="group" aria-label="Period">
      {PERIODS.map(([d, l]) => (
        <button
          key={d}
          type="button"
          aria-pressed={days === d}
          className={days === d ? "on" : ""}
          onClick={() => setDays(d)}
        >
          {l}
        </button>
      ))}
    </div>
  );
}

/** Dashboard: what the whole network is used for. */
export function CategoriesPanel() {
  const [days, setDays] = useState(
    () => Number(localStorage.getItem("cat-days")) || 7,
  );
  const pick = (d: number) => (
    setDays(d),
    localStorage.setItem("cat-days", String(d))
  );
  const r = useFetch(`categories-${days}`, () => api.categories(days), 120000);
  const v = r.data;
  const top = v?.categories[0];
  return (
    <section className="pn c7" aria-labelledby="w-cat">
      <div className="hd">
        <h2 id="w-cat">What it's used for</h2>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          {v && top
            ? `${top.name} leads with ${pctOf(top.bytes, v.total)}% · by site name`
            : "By site name"}
        </span>
        <div className="r">
          <PeriodSeg days={days} setDays={pick} />
        </div>
      </div>
      <div
        className="bd"
        style={{
          padding: "4px 14px 10px",
          opacity: r.loading && v ? 0.6 : 1,
          transition: "opacity .2s",
        }}
      >
        {v ? (
          <CategoryList v={v} />
        ) : (
          <div className="note" style={{ padding: "10px 0" }}>
            {r.error ? "Couldn't read the sites." : "Loading…"}
          </div>
        )}
        {v && (
          <div className="note" style={{ marginTop: 6 }}>
            {bytes(v.total)} with a site name or address · change against{" "}
            {days === 1 ? "yesterday" : `the ${days} days before`} · hover a row
            for its sites and devices
          </div>
        )}
      </div>
    </section>
  );
}

/** Device panel: what this device is used for. */
export function DeviceCategories({ id }: { id: string }) {
  const [days, setDays] = useState(7);
  const r = useFetch(
    `categories-${days}-${id}`,
    () => api.categories(days, id),
    120000,
  );
  return (
    <div className="sec">
      <div className="st" style={{ display: "flex", alignItems: "center" }}>
        What it's used for
        <span style={{ marginLeft: "auto" }}>
          <PeriodSeg days={days} setDays={setDays} />
        </span>
      </div>
      <div style={{ marginTop: 6 }}>
        {r.data ? (
          <CategoryList v={r.data} device={id} />
        ) : (
          <div className="note">
            {r.error ? "Couldn't read the sites." : "Loading…"}
          </div>
        )}
      </div>
    </div>
  );
}

/** Dashboard: this week, in a few sentences, each against the week before. */
export function InsightsPanel({
  o,
  week,
}: {
  o: Overview;
  week: { hour: string; down: number; up: number }[];
}) {
  const cats = useFetch("categories-7", () => api.categories(7), 120000).data;
  const daily = useFetch(
    "devices-daily-14",
    () => api.devicesDaily(14),
    120000,
  ).data;
  const usage = useFetch("usage-14", () => api.usage(14), 120000).data;
  const name = (id: string) => o.devices.find((d) => d.id === id)?.name ?? id;
  const items: {
    icon: string;
    text: React.ReactNode;
    side?: React.ReactNode;
    tip?: string;
  }[] = [];

  if (daily) {
    const tot = daily.days.map((_, i) =>
      Object.values(daily.devices).reduce((s, v) => s + (v[i] ?? 0), 0),
    );
    const now = tot.slice(7).reduce((a, b) => a + b, 0);
    const prev = tot.slice(0, 7).reduce((a, b) => a + b, 0);
    const ch = change(now, prev);
    items.push({
      icon: ch?.dir === "down" ? "down" : "up",
      text: (
        <>
          <b>{bytes(now)}</b> in the last 7 days{" "}
          <span className="t3">
            {ch && prev > 0
              ? `${ch.dir === "flat" ? "about the same as" : `${ch.t.slice(2)} ${ch.dir === "up" ? "more than" : "less than"}`} the week before (${bytes(prev)})`
              : ""}
          </span>
        </>
      ),
      side: (
        <Spark
          vals={tot}
          w={96}
          h={24}
          fmt={(v) => bytes(v)}
          label="Data per day, last 14 days"
          tipLabel={(i) => dayName(daily.days[i])}
        />
      ),
    });
    // the device whose use changed the most (ignoring small ones)
    let best: { id: string; now: number; prev: number; r: number } | null =
      null;
    for (const [id, v] of Object.entries(daily.devices)) {
      const n = v.slice(7).reduce((a, b) => a + b, 0);
      const p = v.slice(0, 7).reduce((a, b) => a + b, 0);
      if (Math.max(n, p) < 5e8 || !o.devices.some((d) => d.id === id)) continue;
      const r = (n + 1e8) / (p + 1e8);
      if (!best || Math.abs(Math.log(r)) > Math.abs(Math.log(best.r)))
        best = { id, now: n, prev: p, r };
    }
    if (best && Math.abs(Math.log(best.r)) > Math.log(1.15)) {
      const up = best.r > 1;
      const v = daily.devices[best.id];
      items.push({
        icon: "laptop",
        text: (
          <>
            <b>{name(best.id)}</b> used{" "}
            {up
              ? best.r >= 1.95
                ? `${best.r.toFixed(1)}× as much`
                : `${Math.round((best.r - 1) * 100)}% more`
              : `${Math.round((1 - best.r) * 100)}% less`}{" "}
            <span className="t3">
              than the week before · {bytes(best.now)} vs {bytes(best.prev)}
            </span>
          </>
        ),
        side: (
          <Spark
            vals={v}
            w={96}
            h={24}
            fmt={(x) => bytes(x)}
            label={`${name(best.id)} per day`}
            tipLabel={(i) => dayName(daily.days[i])}
          />
        ),
      });
    }
    // who used the most, and the busiest day
    const per = Object.entries(daily.devices)
      .map(([id, v]) => ({ id, b: v.slice(7).reduce((a, x) => a + x, 0) }))
      .filter((x) => o.devices.some((d) => d.id === x.id))
      .sort((a, b) => b.b - a.b);
    const weekTot = per.reduce((a, x) => a + x.b, 0);
    if (per[0] && weekTot > 0)
      items.push({
        icon: "who",
        text: (
          <>
            <b>{name(per[0].id)}</b> used the most <span className="t3">· {bytes(per[0].b)}, {pctOf(per[0].b, weekTot)}% of everything</span>
          </>
        ),
        tip: per
          .slice(0, 5)
          .map((x) => `${name(x.id)}: ${bytes(x.b)} (${pctOf(x.b, weekTot)}%)`)
          .join("\n"),
      });
    let bd = 7;
    for (let i = 7; i < tot.length - 1; i++) if (tot[i] > tot[bd]) bd = i; // whole days only
    if (tot[bd] > 0)
      items.push({
        icon: "activity",
        text: (
          <>
            Busiest day: <b>{dayName(daily.days[bd])}</b> <span className="t3">· {bytes(tot[bd])}</span>
          </>
        ),
      });
  }
  {
    const since = Date.parse(o.now) - 7 * 86400e3;
    // Devices there when Junction started counting aren't new: only ones first seen a day later.
    const start = Math.min(...o.devices.map((d) => (d.firstSeen ? Date.parse(d.firstSeen) : Infinity)));
    const fresh = o.devices.filter((d) => d.firstSeen && Date.parse(d.firstSeen) >= Math.max(since, start + 86400e3));
    if (fresh.length)
      items.push({
        icon: "unknown",
        text: (
          <>
            <b>
              {fresh.length} new device{fresh.length === 1 ? "" : "s"}
            </b>{" "}
            <span className="t3">· {fresh.slice(0, 3).map((d) => d.name).join(", ")}</span>
          </>
        ),
        tip: fresh.map((d) => `${d.name} · ${d.ip} · first seen ${dmy(d.firstSeen)}`).join("\n"),
      });
  }
  if (cats?.categories.length) {
    const top = cats.categories[0];
    const was = pctOf(top.prev, cats.prevTotal);
    items.push({
      icon: "globe",
      text: (
        <>
          <b>{top.name}</b> was {pctOf(top.bytes, cats.total)}% of it{" "}
          <span className="t3">
            {cats.prevTotal > 0 ? `(${was}% the week before)` : ""}
          </span>
        </>
      ),
      tip: cats.categories
        .slice(0, 5)
        .map((c) => `${c.name}: ${pctOf(c.bytes, cats.total)}%`)
        .join("\n"),
    });
  }
  if (week.length) {
    let bi = 0;
    week.forEach(
      (h, i) => h.down + h.up > week[bi].down + week[bi].up && (bi = i),
    );
    const p = cz(week[bi].hour);
    items.push({
      icon: "clock",
      text: (
        <>
          Busiest hour: <b>{`${DAY3[p.dow]} ${p.h}:00`}</b>{" "}
          <span className="t3">
            · {bytes(week[bi].down + week[bi].up)} in that hour
          </span>
        </>
      ),
    });
  }
  if (usage && usage.length >= 8) {
    const share = (u: UsageDay[]) => {
      const t = u.reduce((s, x) => s + x.tunnel, 0);
      const a = u.reduce((s, x) => s + x.tunnel + x.direct, 0);
      return a > 0 ? Math.round((t / a) * 100) : 0;
    };
    const nowS = share(usage.slice(-7));
    const prevS = share(usage.slice(0, -7));
    items.push({
      icon: "tunnel",
      text:
        nowS === 0 && prevS === 0 ? (
          <>
            Everything went <b>direct</b>{" "}
            <span className="t3">· no VPN in the last two weeks</span>
          </>
        ) : (
          <>
            <b>{nowS}%</b> went through a VPN{" "}
            <span className="t3">({prevS}% the week before)</span>
          </>
        ),
    });
  }
  return (
    <section className="pn c5" aria-labelledby="w-ins">
      <div className="hd">
        <h2 id="w-ins">This week</h2>
        <span className="t3" style={{ fontSize: 12 }}>
          against the week before
        </span>
      </div>
      <div className="bd" style={{ padding: "2px 14px 6px" }}>
        {items.length ? (
          <div className="ins">
            {items.map((it, i) => (
              <div className="ins-i" key={i} title={it.tip}>
                <span className="ic">
                  <Icon n={it.icon} s={14} />
                </span>
                <span className="tx">{it.text}</span>
                <span>{it.side}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="note" style={{ padding: "10px 0" }}>
            Loading…
          </div>
        )}
      </div>
    </section>
  );
}

/** Internet: where traffic went, per day: each VPN exit and direct. */
export function WhereTrafficWent({ o }: { o: Overview }) {
  const [table, setTable] = useState(false);
  const usage = useFetch("usage-30", () => api.usage(30), 120000).data ?? [];
  const keys = new Set<string>();
  usage.forEach((u) => Object.keys(u.byExit).forEach((k) => keys.add(k)));
  const exits = o.exits.map((e) => e.key).filter((k) => keys.has(k));
  const others = [...keys].filter((k) => k !== "direct" && !exits.includes(k));
  const stacks: Stack[] = [
    ...(keys.has("direct")
      ? [{ key: "direct", name: "Direct", color: "var(--series-direct)" }]
      : []),
    ...exits.map((k) => ({
      key: k,
      name: exitDisplayName(k),
      color: exitColorVar(k),
    })),
    ...others.map((k) => ({
      key: k,
      name: exitDisplayName(k),
      color: "var(--other)",
    })),
  ];
  const values = usage.map((u) => stacks.map((s) => u.byExit[s.key] ?? 0));
  const tot = (k: string) => usage.reduce((s, u) => s + (u.byExit[k] ?? 0), 0);
  const all = stacks.reduce((s, x) => s + tot(x.key), 0);
  const label = (k: string) => `${+k.slice(8)} ${MON[+k.slice(5, 7) - 1]}`;
  return (
    <section
      className="pn"
      style={{ marginBottom: 12 }}
      aria-labelledby="where-h"
    >
      <div className="hd">
        <h2 id="where-h">Where traffic went</h2>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          Last {usage.length || 30} days · per day · what devices sent and
          received
        </span>
        <div className="r">
          <div className="legend" style={{ display: "flex", gap: 12 }}>
            {stacks.map((s) => (
              <span
                key={s.key}
                title={`${s.name}: ${bytes(tot(s.key))} in ${usage.length} days, ${pctOf(tot(s.key), all)}%`}
                style={{ display: "inline-flex", alignItems: "center", gap: 6 }}
              >
                <i
                  style={{
                    width: 10,
                    height: 10,
                    borderRadius: 2,
                    background: s.color,
                    display: "inline-block",
                  }}
                />
                {s.name} <span className="t3">{pctOf(tot(s.key), all)}%</span>
              </span>
            ))}
          </div>
          <button
            type="button"
            className="btn sm ghost"
            aria-pressed={table}
            onClick={() => setTable(!table)}
          >
            {table ? "Chart" : "Table"}
          </button>
        </div>
      </div>
      <div className="bd" style={{ padding: "10px 10px 8px" }}>
        {table ? (
          <div
            className="scroll-x"
            style={{ maxHeight: 260, overflowY: "auto" }}
          >
            <table className="tb compact">
              <thead>
                <tr>
                  <th>Day</th>
                  {stacks.map((s) => (
                    <th key={s.key} className="rt">
                      {s.name}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {[...usage].reverse().map((u) => (
                  <tr key={u.day}>
                    <td>{dayName(u.day)}</td>
                    {stacks.map((s) => (
                      <td key={s.key} className="rt">
                        {bytes(u.byExit[s.key] ?? 0)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <StackedColumns
            h={200}
            label="Traffic per day through each VPN exit and direct"
            stacks={stacks}
            values={values}
            xLabel={(i) =>
              i === usage.length - 1
                ? "today"
                : i % 5 === 0 && i < usage.length - 3
                  ? label(usage[i].day)
                  : null
            }
            tipTitle={(i) =>
              i === usage.length - 1 ? "Today · so far" : dayName(usage[i].day)
            }
            yFmt={(v) => (v === 0 ? "0" : bytes(v, 1))}
          />
        )}
      </div>
    </section>
  );
}

const KIND_NAME: Record<string, string> = {
  exit: "VPN exits",
  gateway: "Gateway",
  device: "Devices",
  rule: "Rules",
  control: "Controls",
  limit: "Limits",
  dhcp: "DHCP",
};

/** Activity: events per day over the last 14 days. */
export function EventsChart() {
  const ev =
    useFetch("events-500", () => api.events({ limit: 500 }), 60000).data ?? [];
  const now = Date.now();
  const days: string[] = [];
  for (let i = 13; i >= 0; i--) days.push(cz(now - i * 86400e3).key);
  const counts = days.map(() => new Map<string, number>());
  for (const e of ev) {
    const i = days.indexOf(cz(e.at).key);
    if (i >= 0) counts[i].set(e.kind, (counts[i].get(e.kind) ?? 0) + 1);
  }
  const total = (i: number) =>
    [...counts[i].values()].reduce((a, b) => a + b, 0);
  return (
    <section
      className="pn"
      style={{ marginBottom: 12 }}
      aria-labelledby="evc-h"
    >
      <div className="hd">
        <h2 id="evc-h">Events per day</h2>
        <span className="t3 hide-m" style={{ fontSize: 12 }}>
          Last 14 days · hover a day for what happened
        </span>
      </div>
      <div className="bd" style={{ padding: "8px 10px 6px" }}>
        <Columns
          h={130}
          padL={36}
          label="Events per day over the last 14 days"
          yFmt={(v) => String(Math.round(v))}
          xLabel={(i) =>
            i === days.length - 1
              ? "today"
              : i % 2 === 0 && i < days.length - 2
                ? dayName(days[i]).slice(0, 6)
                : null
          }
          cols={days.map((d, i) => ({
            key: d,
            value: total(i),
            accent: i === days.length - 1,
            tip: i === days.length - 1 ? "Today" : dayName(d),
            rows: total(i)
              ? [...counts[i]]
                  .sort((a, b) => b[1] - a[1])
                  .map(([k, n]) => ({
                    label: KIND_NAME[k] ?? k,
                    value: String(n),
                  }))
              : [{ label: "", value: "nothing happened", muted: true }],
          }))}
        />
      </div>
    </section>
  );
}

/** Device panel: this device's last 7 days, hour by hour. */
export function DeviceWeek({ id }: { id: string }) {
  const hours =
    useFetch(`traffic-168-${id}`, () => api.traffic(168, id), 120000).data
      ?.hours ?? [];
  const now = Date.now();
  const days: string[] = [];
  for (let i = 6; i >= 0; i--) days.push(cz(now - i * 86400e3).key);
  const grid = days.map(() => Array<number | null>(24).fill(null));
  for (const h of hours) {
    const p = cz(h.hour);
    const r = days.indexOf(p.key);
    if (r >= 0) grid[r][+p.h] = (grid[r][+p.h] ?? 0) + h.down + h.up;
  }
  const nowP = cz(now);
  for (let r = 0; r < 7; r++)
    for (let c = 0; c < 24; c++)
      if (grid[r][c] == null && !(r === 6 && c > +nowP.h)) grid[r][c] = 0;
  const hh = (c: number) => `${String(c).padStart(2, "0")}:00`;
  return (
    <div className="sec">
      <div className="st">
        When it's busy<span className="r t3">last 7 days, by hour</span>
      </div>
      <div style={{ marginTop: 8 }}>
        <Heatmap
          rows={7}
          cols={24}
          value={(r, c) => grid[r][c]}
          fmt={(v) => bytes(v)}
          rowLabel={(r) => (r === 6 ? "Today" : dayName(days[r]).slice(0, 3))}
          colLabel={(c) => (c % 6 === 0 ? String(c).padStart(2, "0") : null)}
          tip={(r, c) =>
            `${r === 6 ? "Today" : dayName(days[r])}, ${hh(c)}–${hh((c + 1) % 24)}`
          }
          current={[6, +nowP.h]}
          label="This device's traffic per hour over the last 7 days"
        />
      </div>
    </div>
  );
}
