import { useState } from "react";
import { api2, ApiError } from "../api/client";
import type { Health as H } from "../api/types";
import { dmy, hm } from "../fmt";
import { useFetch } from "../live";
import { Link } from "../router";

const DOT: Record<string, string> = { ok: "ok", warn: "warn", bad: "bad", off: "stop" };

export function Health() {
  const h = useFetch("health", api2.health, 30000);
  const d = h.data;
  const ok = d?.checks.filter((c) => c.status === "ok" || c.status === "off").length ?? 0;
  return (
    <div className="page">
      <div className="ph">
        <h1>Health</h1>
        <span className="meta">
          {d ? `Checked every 30 s · ${ok} of ${d.checks.length} OK · last ${hm(d.checkedAt)}` : h.error ?? "Checking…"}
        </span>
        <div className="right">
          <button type="button" className="btn sm" onClick={h.reload} disabled={h.loading}>
            Check now
          </button>
        </div>
      </div>
      {d && (
        <div className="hgrid">
          <section className="pn" aria-labelledby="h-parts">
            <div className="hd">
              <h2 id="h-parts">Parts</h2>
            </div>
            {d.checks.map((c) => (
              <div className="chk" key={c.key}>
                <span className={`dot ${DOT[c.status]}`} aria-label={c.status} />
                <b>{c.name}</b>
                <span className="what">{c.detail}</span>
                {c.link ? (
                  <Link className="btn sm ghost" href={c.link}>
                    Details
                  </Link>
                ) : (
                  <span className="t3" style={{ fontSize: 12 }}>
                    {c.since ? `since ${dmy(c.since)} ${hm(c.since)}` : ""}
                  </span>
                )}
              </div>
            ))}
          </section>
          <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <Version d={d} onDone={(n) => h.setData(n)} />
            <section className="pn" aria-labelledby="h-upd">
              <div className="hd">
                <h2 id="h-upd">Recent updates</h2>
              </div>
              <div className="bd" style={{ padding: "4px 14px" }}>
                {d.version.history.map((u) => (
                  <div className="lr" key={u.version + u.at} style={{ fontSize: 12.5 }}>
                    <span className={`dot ${u.result === "healthy" ? "ok" : u.result === "pending" ? "warn" : "bad"}`} />
                    {u.version}
                    <span className="t3">
                      {dmy(u.at)} {hm(u.at)}
                    </span>
                    <span className={`v ${u.result === "rolled-back" ? "warnc" : "t2"}`}>{u.result === "rolled-back" ? (u.note ?? "rolled back") : u.result === "pending" ? "checking…" : "healthy"}</span>
                  </div>
                ))}
                {!d.version.history.length && <div className="note" style={{ padding: "8px 0" }}>No updates yet.</div>}
                <div className="note" style={{ padding: "8px 0" }}>
                  Restarting doesn't drop connections: sing-box carries the traffic and keeps running.
                </div>
              </div>
            </section>
          </div>
        </div>
      )}
    </div>
  );
}

function Version({ d, onDone }: { d: H; onDone: (h: H) => void }) {
  const v = d.version;
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  async function install() {
    if (!v.available) return;
    setBusy(true);
    setErr(null);
    try {
      onDone(await api2.installUpdate(v.available));
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "The update didn't start.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="pn" aria-labelledby="h-ver">
      <div className="hd">
        <h2 id="h-ver">Version</h2>
        {v.available && (
          <div className="r">
            <span className="tag acc">update ready</span>
          </div>
        )}
      </div>
      <div className="bd">
        <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
          <span className="big">{v.current}</span>
          {v.installedAt && (
            <span className="t2">
              installed {dmy(v.installedAt)} {hm(v.installedAt)}
            </span>
          )}
        </div>
        {v.available ? (
          <>
            <div className="note" style={{ margin: "4px 0 12px" }}>
              {v.available} is ready. Junction keeps {v.current} and switches back by itself if {v.available} doesn't pass its health check within 60 s.
            </div>
            <button type="button" className="btn pri sm" disabled={busy} onClick={install}>
              {busy ? "Installing…" : `Install ${v.available}`}
            </button>
          </>
        ) : (
          <div className="note" style={{ marginTop: 4 }}>
            Up to date.
          </div>
        )}
        {err && <div className="err" style={{ marginTop: 8 }}>{err}</div>}
      </div>
    </section>
  );
}
