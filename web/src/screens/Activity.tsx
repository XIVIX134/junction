import { useEffect, useState, type ReactNode } from "react";
import { api, ApiError } from "../api/client";
import type { JEvent, Overview } from "../api/types";
import { NeedOverview } from "../components/Shell";
import { feed, KIND_LABEL, useEventsVersion } from "../events";
import { cz, DAYN, dayKey, hms, MON } from "../fmt";
import { deviceHref, navigate, useLocation } from "../router";
import { Icon, kindOf, stateDot } from "../ui";

const PAGE = 100;
const KINDS = ["control", "rule", "limit", "device", "gateway", "exit", "isp", "dhcp"]; // "isp": old WE-counter events (removed in v1.7), shown only when there are some

export function Activity() {
  return <NeedOverview>{(o) => <ActivityView o={o} />}</NeedOverview>;
}

function ActivityView({ o }: { o: Overview }) {
  const { query } = useLocation();
  const kind = query.get("kind") ?? "all";
  const dev = query.get("device") ?? "";
  const v = useEventsVersion();
  const [events, setEvents] = useState<JEvent[] | null>(null);
  const [more, setMore] = useState(true);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function load(before: number | null) {
    setBusy(true);
    setErr(null);
    try {
      const page = await api.events({ limit: PAGE, device: dev || null, before });
      setMore(page.length === PAGE);
      setEvents((prev) => (before == null ? page : [...(prev ?? []), ...page]));
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "Couldn't load events.");
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    load(null);
    const t = setInterval(() => {
      api
        .events({ limit: PAGE, device: dev || null })
        .then((page) =>
          setEvents((prev) => {
            if (!prev) return page;
            const seen = new Set(prev.map((e) => e.id));
            return [...page.filter((e) => !seen.has(e.id)), ...prev];
          }),
        )
        .catch(() => {});
    }, 20000);
    return () => clearInterval(t);
  }, [dev, v]); // eslint-disable-line react-hooks/exhaustive-deps

  const set = (k: string, val: string) => {
    const q = new URLSearchParams(query);
    if (val && val !== "all") q.set(k, val);
    else q.delete(k);
    const s = q.toString();
    navigate(`/activity${s ? `?${s}` : ""}`, { replace: true });
  };

  // gateway state changes come from the overview; only include them when no device filter is set
  const all = feed(events ?? [], dev ? null : o);
  const rows = all.filter((e) => kind === "all" || e.kind === kind);
  const byId = new Map(o.devices.map((d) => [d.id, d]));
  const devIds = [...new Set([...(events ?? []).map((e) => e.deviceId).filter(Boolean) as string[], ...(dev ? [dev] : [])])];
  const name = (id: string) => byId.get(id)?.name ?? null;

  const body: ReactNode[] = [];
  let last = "";
  for (const e of rows) {
    const k = dayKey(e.at);
    if (k !== last) {
      const d = cz(e.at);
      const n = rows.filter((x) => dayKey(x.at) === k).length;
      body.push(
        <tr className="day" key={`d${k}`}>
          <td colSpan={4}>
            {DAYN[d.dow]}, {d.d} {MON[d.mo - 1]} <span className="t3">· {n} events</span>
          </td>
        </tr>,
      );
      last = k;
    }
    const dn = e.deviceId ? name(e.deviceId) : null;
    const dd = e.deviceId ? byId.get(e.deviceId) : undefined;
    body.push(
      <tr key={e.id}>
        <td className="t2" style={{ width: 90 }}>
          {hms(e.at)}
        </td>
        <td style={{ width: 96 }}>
          {e.kind === "gateway" && e.st ? (
            <span style={{ display: "inline-flex", gap: 6, alignItems: "center" }}>
              <span className={`dot ${stateDot(e.st)}`} />
              <span className="t2">Gateway</span>
            </span>
          ) : (
            <span className={e.kind === "limit" ? "warnc" : "t2"}>{KIND_LABEL[e.kind] ?? e.kind}</span>
          )}
        </td>
        <td style={{ width: 220 }}>
          {e.deviceId ? (
            dn && dd ? (
              <a
                href={`${deviceHref(e.deviceId)}/overview`}
                className="t2"
                style={{ display: "inline-flex", gap: 7, alignItems: "center" }}
                onClick={(ev) => {
                  ev.preventDefault();
                  navigate(`${deviceHref(e.deviceId!)}/overview`);
                }}
              >
                <Icon n={kindOf(dd)} s={14} />
                {dn}
              </a>
            ) : (
              <>
                <span className="t3 mono">{e.deviceId}</span> <span className="t3">· removed</span>
              </>
            )
          ) : (
            <span className="t3">—</span>
          )}
        </td>
        <td className="wrap">{e.text}</td>
      </tr>,
    );
  }

  return (
    <div className="page">
      <div className="ph">
        <h1>Activity</h1>
        <span className="meta">{all.length} events loaded · gateway log and failover state changes</span>
      </div>
      <div className="toolbar">
        <div className="seg" role="group" aria-label="Event type">
          <button type="button" aria-pressed={kind === "all"} onClick={() => set("kind", "all")}>
            All<span className="c">{all.length}</span>
          </button>
          {KINDS.filter((k) => (k !== "dhcp" && k !== "isp") || all.some((e) => e.kind === k)).map((k) => (
            <button key={k} type="button" aria-pressed={kind === k} onClick={() => set("kind", k)}>
              {KIND_LABEL[k]}
              <span className="c">{all.filter((e) => e.kind === k).length}</span>
            </button>
          ))}
        </div>
        <select className="inp" style={{ width: 210 }} aria-label="Device" value={dev} onChange={(e) => set("device", e.target.value)}>
          <option value="">All devices</option>
          {[...new Set([...o.devices.map((d) => d.id), ...devIds])]
            .map((id) => ({ id, n: name(id) ?? `${id} (removed)` }))
            .sort((a, b) => a.n.localeCompare(b.n))
            .map((x) => (
              <option key={x.id} value={x.id}>
                {x.n}
              </option>
            ))}
        </select>
        <span className="t3 hide-m" style={{ marginLeft: "auto", fontSize: 12 }}>
          Newest first
        </span>
      </div>
      <div className="pn scroll-x">
        <table className="tb acttable">
          <thead>
            <tr>
              <th>Time</th>
              <th>Type</th>
              <th>Device</th>
              <th>Message</th>
            </tr>
          </thead>
          <tbody>
            {events == null && !err ? (
              <tr>
                <td colSpan={4} className="t3">
                  Loading…
                </td>
              </tr>
            ) : body.length ? (
              body
            ) : (
              <tr>
                <td colSpan={4} className="t3" style={{ height: 70, textAlign: "center" }}>
                  {err ?? "No events of this type."}
                </td>
              </tr>
            )}
          </tbody>
        </table>
        {events && more && (
          <div style={{ padding: "10px 14px", borderTop: "1px solid var(--line)" }}>
            <button type="button" className="btn sm" disabled={busy} onClick={() => load(events[events.length - 1]?.id ?? null)}>
              {busy ? "Loading…" : "Load older"}
            </button>
            {err && <span className="err" style={{ marginLeft: 10 }}>{err}</span>}
          </div>
        )}
      </div>
    </div>
  );
}
