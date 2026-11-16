import { useEffect, useRef, useState, type ReactNode } from "react";
import type { Device, GatewayEvent } from "./api/types";
import { LineChart } from "./charts";
import { bytes, hm, hms, speedAxis, speedValue } from "./fmt";

// Small shared pieces of the console: icons, switch, charts, state strip, pop-up menu.

const I: Record<string, string> = {
  dashboard: '<rect x="2.5" y="2.5" width="5.5" height="6.5" rx="1"/><rect x="10" y="2.5" width="5.5" height="4" rx="1"/><rect x="2.5" y="11" width="5.5" height="4.5" rx="1"/><rect x="10" y="8.5" width="5.5" height="7" rx="1"/>',
  topology: '<rect x="6.5" y="1.8" width="5" height="3.6" rx=".8"/><rect x="1.8" y="12.6" width="5" height="3.6" rx=".8"/><rect x="11.2" y="12.6" width="5" height="3.6" rx=".8"/><path d="M9 5.4v3.4M4.3 12.6V8.8h9.4v3.8"/>',
  clients: '<rect x="1.8" y="3" width="10.5" height="7.5" rx="1"/><path d="M4.5 13.5h5M7 10.5v3"/><rect x="11.8" y="6.5" width="4.4" height="8.3" rx="1"/>',
  rules: '<path d="M2 4.5h8M13 4.5h3M2 9h3M8 9h8M2 13.5h6M11 13.5h5"/><circle cx="11.5" cy="4.5" r="1.5"/><circle cx="6.5" cy="9" r="1.5"/><circle cx="9.5" cy="13.5" r="1.5"/>',
  activity: '<path d="M1.8 9.5h3l2-5 3.5 9 2-4h3.9"/>',
  internet: '<circle cx="9" cy="9" r="7"/><path d="M2 9h14M9 2c2.2 2.3 2.2 11.7 0 14M9 2c-2.2 2.3-2.2 11.7 0 14"/>',
  phone: '<rect x="5" y="1.8" width="8" height="14.4" rx="1.6"/><path d="M8 13.6h2"/>',
  desktop: '<rect x="1.8" y="2.6" width="14.4" height="9.6" rx="1"/><path d="M6 15.6h6M9 12.2v3.4"/>',
  laptop: '<rect x="3.4" y="3.4" width="11.2" height="7.8" rx="1"/><path d="M1.6 14.4h14.8l-1.4-3.2H3z"/>',
  server: '<rect x="2.4" y="2.4" width="13.2" height="5.4" rx="1"/><rect x="2.4" y="10.2" width="13.2" height="5.4" rx="1"/><path d="M5 5.1h.01M5 12.9h.01M8 5.1h5M8 12.9h5"/>',
  tv: '<rect x="1.6" y="3" width="14.8" height="9.4" rx="1"/><path d="M5.5 15.4h7"/>',
  console: '<path d="M5.4 5.2h7.2a3.8 3.8 0 0 1 3.7 4.6l-.5 2.4a1.9 1.9 0 0 1-3.3.8l-1.4-1.6H6.9L5.5 13a1.9 1.9 0 0 1-3.3-.8l-.5-2.4a3.8 3.8 0 0 1 3.7-4.6z"/><path d="M5.6 7.7v2.4M4.4 8.9h2.4M12.3 8.4h.01M13.3 9.6h.01"/>',
  router: '<rect x="1.8" y="9.4" width="14.4" height="5.6" rx="1"/><path d="M5 12.2h.01M7.6 12.2h.01M12.6 9.4l1.6-6M5.4 9.4l-1.6-6"/>',
  unknown: '<rect x="2.4" y="2.4" width="13.2" height="13.2" rx="2" stroke-dasharray="2 2"/><path d="M7.2 7.2a1.9 1.9 0 1 1 2.6 1.7c-.5.2-.8.6-.8 1.1v.4M9 12.6h.01"/>',
  gateway: '<rect x="1.8" y="4" width="14.4" height="10" rx="1.2"/><path d="M4.6 9h3.2M7.8 9l3.6-2.6M7.8 9l3.6 2.6"/><circle cx="7.8" cy="9" r=".9" fill="currentColor"/>',
  tunnel: '<path d="M9 1.9l5.8 2.2v4.2c0 3.6-2.5 6.4-5.8 7.8-3.3-1.4-5.8-4.2-5.8-7.8V4.1z"/>',
  globe: '<circle cx="9" cy="9" r="7"/><path d="M2 9h14M9 2c2.2 2.3 2.2 11.7 0 14M9 2c-2.2 2.3-2.2 11.7 0 14"/>',
  link: '<path d="M7.4 10.6l3.2-3.2M6 8.4L4.6 9.8a2.5 2.5 0 0 0 3.6 3.6L9.6 12M12 9.6l1.4-1.4a2.5 2.5 0 0 0-3.6-3.6L8.4 6"/>',
  clock: '<circle cx="9" cy="9" r="6.8"/><path d="M9 5v4.2l2.6 1.6"/>',
  x: '<path d="M4 4l10 10M14 4L4 14"/>',
  chev: '<path d="M5 7l4 4 4-4"/>',
  who: '<circle cx="9" cy="6.2" r="3"/><path d="M3 15.8c.6-3.2 3-4.8 6-4.8s5.4 1.6 6 4.8"/>',
  search: '<circle cx="7.7" cy="7.7" r="5.5"/><path d="M12 12l4.6 4.6"/>',
  settings: '<circle cx="9" cy="9" r="2.3"/><circle cx="9" cy="9" r="4.9"/><circle cx="9" cy="9" r="6.3" stroke-width="2.3" stroke-dasharray="2.05 2.9"/>',
  health: '<path d="M9 15.6s-6.3-3.7-6.3-8.4A3.5 3.5 0 0 1 9 5.1a3.5 3.5 0 0 1 6.3 2.1c0 4.7-6.3 8.4-6.3 8.4z"/><path d="M4.6 9.4h2.2l1.1-2 2.1 4 1.1-2h2.3"/>',
  sun: '<circle cx="9" cy="9" r="3.2"/><path d="M9 1.6v1.8M9 14.6v1.8M1.6 9h1.8M14.6 9h1.8M3.8 3.8l1.3 1.3M12.9 12.9l1.3 1.3M3.8 14.2l1.3-1.3M12.9 5.1l1.3-1.3"/>',
  moon: '<path d="M14.8 11.2A6.4 6.4 0 0 1 6.8 3.2a6.4 6.4 0 1 0 8 8z"/>',
  lock: '<rect x="3.4" y="8" width="11.2" height="8" rx="1.4"/><path d="M5.8 8V5.6a3.2 3.2 0 0 1 6.4 0V8"/>',
  mark: '<path d="M2.7 9h5.4M8.1 9l6.3-4.5M8.1 9l6.3 4.5"/><circle cx="8.1" cy="9" r="1.4" fill="currentColor"/>',
  up: '<path d="M9 14V4M5 8l4-4 4 4"/>',
  down: '<path d="M9 4v10M5 10l4 4 4-4"/>',
};

export function Icon({ n, s = 16, w = 1.3, title }: { n: string; s?: number; w?: number; title?: string }) {
  return (
    <svg
      className="ic"
      width={s}
      height={s}
      viewBox="0 0 18 18"
      fill="none"
      stroke="currentColor"
      strokeWidth={w}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={title ? undefined : true}
      role={title ? "img" : undefined}
      aria-label={title}
      dangerouslySetInnerHTML={{ __html: I[n] ?? I.unknown }}
    />
  );
}

/** Device icon from its name/model (the API has no device type). */
export function kindOf(d: Pick<Device, "name" | "model" | "group" | "ip">): string {
  const n = `${d.name} ${d.model ?? ""}`.toLowerCase();
  if (/\.1$/.test(d.ip) || /zte|router|\bont\b/.test(n)) return "router";
  if (/\btv\b|webos/.test(n)) return "tv";
  if (/xbox|playstation|ps5|console/.test(n)) return "console";
  if (d.group === "Servers" || /proxmox|server|nas|media|homelab|workstation/.test(n)) return "server";
  if (/laptop|macbook/.test(n)) return "laptop";
  if (/desktop|\bpc\b|windows/.test(n)) return "desktop";
  if (/s2\d|poco|realme|infinix|iphone|android|phone|galaxy|ipad|pixel|xiaomi|redmi|oppo|samsung/.test(n)) return "phone";
  return "unknown";
}

type SwProps = {
  on: boolean;
  label: string;
  onChange?: (next: boolean) => void;
  disabled?: boolean;
  reason?: string;
  busy?: boolean;
};
/** The concept's small switch; a real role="switch" with an accessible name. */
export function Sw({ on, label, onChange, disabled, reason, busy }: SwProps) {
  const off = disabled || !onChange || busy;
  return (
    <button
      type="button"
      role="switch"
      className={on ? "sw on" : "sw"}
      aria-checked={on}
      aria-label={label}
      aria-disabled={off ? "true" : undefined}
      aria-busy={busy ? "true" : undefined}
      title={reason}
      onClick={(e) => {
        e.stopPropagation();
        if (!off) onChange!(!on);
      }}
    />
  );
}

export const Radio = ({ on }: { on: boolean }) => <span className={on ? "radio on" : "radio"} aria-hidden="true" />;

// ---------- charts (see charts.tsx) ----------

export { Spark, useWidth } from "./charts";


/** Hourly traffic as an average rate (Mbit/s or MB/s), with the current hour averaged over the minutes so far. */
export function HourChart({ hours, now, h = 196, label }: { hours: { hour: string; down: number; up: number }[]; now: number; h?: number; label: string }) {
  const start = hours.length ? Date.parse(hours[0].hour) : now;
  const cur = Math.floor((now - start) / 3600e3);
  const secs = (i: number) => (i < cur ? 3600 : Math.max(60, (now - Date.parse(hours[i].hour)) / 1e3));
  const vals = (k: "down" | "up") => hours.map((x, i) => (i <= cur ? speedValue(x[k] / secs(i)) : null));
  const span = Math.max(1, hours.length - 1);
  return (
    <LineChart
      h={h}
      label={label}
      labels={hours.map((x) => hm(x.hour))}
      tipTitle={(i) => (
        <>
          {hm(hours[i].hour)}–{hm(Date.parse(hours[i].hour) + 3600e3)}
          {i === cur && <span className="t3"> · so far</span>}
          <span className="t3"> · {bytes(hours[i].down + hours[i].up)}</span>
        </>
      )}
      series={[
        { name: "Download", values: vals("down"), color: "var(--series-down)", fill: true },
        { name: "Upload", values: vals("up"), color: "var(--series-up)" },
      ]}
      yFmt={speedAxis}
      markers={hours.length ? [{ at: (now - start) / 3600e3 > span ? span : (now - start) / 3600e3, label: `Now ${hm(now)}` }] : []}
    />
  );
}

const STATE_COL: Record<string, string> = {
  MASTER: "var(--state-master)",
  BACKUP: "var(--state-backup)",
  FAULT: "var(--state-fault)",
  STOP: "var(--state-stop)",
};
export const stateDot = (s: string) => ({ MASTER: "ok", BACKUP: "warn", FAULT: "bad", STOP: "stop" })[s] ?? "";

/** Gateway state timeline across its recorded changes. */
const STATE_NAME: Record<string, string> = { MASTER: "Master", BACKUP: "Backup", FAULT: "Fault", STOP: "Stopped" };
const STATE_MEANS: Record<string, string> = {
  MASTER: "this gateway carries traffic",
  BACKUP: "the backup carries traffic, direct, without VPN or controls",
  FAULT: "this gateway failed its health check and the backup covers",
  STOP: "keepalived was stopped",
};
const span = (ms: number) => {
  const m = Math.round(ms / 60e3);
  return m < 1 ? "under a minute" : m < 60 ? `${m} min` : m < 1440 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${Math.floor(m / 1440)} d ${Math.floor((m % 1440) / 60)} h`;
};

export function StateStrip({ events, now }: { events: GatewayEvent[]; now: number }) {
  if (!events.length) return <div className="note">No state changes recorded.</div>;
  const asc = [...events].sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  const t0 = Date.parse(asc[0].at);
  const t1 = Math.max(now, t0 + 60000);
  return (
    <div>
      <div style={{ position: "relative", height: 10, background: "var(--track)", borderRadius: 2, overflow: "hidden" }} role="img" aria-label="Gateway state over time">
        {asc.map((e, i) => {
          const a = Date.parse(e.at);
          const z = i + 1 < asc.length ? Date.parse(asc[i + 1].at) : t1;
          return (
            <i
              key={i}
              className="ss-seg"
              tabIndex={0}
              title={`${STATE_NAME[e.state] ?? e.state}: ${STATE_MEANS[e.state] ?? ""}\n${hms(e.at)} → ${i + 1 < asc.length ? hms(asc[i + 1].at) : "now"} · ${span(z - a)}`}
              style={{
                position: "absolute",
                top: 0,
                bottom: 0,
                left: `${((a - t0) / (t1 - t0)) * 100}%`,
                width: `max(2px, ${((z - a) / (t1 - t0)) * 100}%)`,
                background: STATE_COL[e.state] ?? "var(--state-stop)",
                opacity: e.state === "MASTER" ? 0.5 : 0.95,
              }}
            />
          );
        })}
      </div>
      <div className="num" style={{ display: "flex", justifyContent: "space-between", fontSize: 11, color: "var(--text3)", marginTop: 4 }}>
        <span>{hm(t0)}</span>
        <span>{hm((t0 + t1) / 2)}</span>
        <span>{hm(t1)}</span>
      </div>
      <div className="legend" style={{ marginTop: 6, fontSize: 11.5 }}>
        <span><i className="dot ok" />Master</span>
        <span><i className="dot warn" />Backup</span>
        <span><i className="dot bad" />Fault</span>
        <span><i className="dot stop" />Stopped</span>
      </div>
    </div>
  );
}

/** A small pop-up menu anchored under a button; closes on outside click or Escape. */
export function MenuButton({
  label,
  items,
  className = "btn",
  disabled,
  ariaLabel,
}: {
  label: ReactNode;
  items: { label: string; onPick: () => void }[];
  className?: string;
  disabled?: boolean;
  ariaLabel?: string;
}) {
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  const btn = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!pos) return;
    menu.current?.querySelector("button")?.focus();
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent && e.key !== "Escape") return;
      if (e instanceof MouseEvent && (menu.current?.contains(e.target as Node) || btn.current?.contains(e.target as Node))) return;
      setPos(null);
      if (e instanceof KeyboardEvent) btn.current?.focus();
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", close);
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", close);
    };
  }, [pos]);
  return (
    <>
      <button
        ref={btn}
        type="button"
        className={className}
        disabled={disabled}
        aria-haspopup="menu"
        aria-expanded={!!pos}
        aria-label={ariaLabel}
        onClick={() => {
          const r = btn.current!.getBoundingClientRect();
          setPos(pos ? null : { left: Math.min(r.left, window.innerWidth - 180), top: r.bottom + 4 });
        }}
      >
        {label}
      </button>
      {pos && (
        <div ref={menu} className="menu" role="menu" style={{ left: pos.left, top: pos.top }}>
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              onClick={() => {
                setPos(null);
                it.onPick();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      )}
    </>
  );
}
