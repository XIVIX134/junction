import { CountUp } from "../charts";
import { useEffect, useRef, type ReactNode } from "react";
import type { Overview } from "../api/types";
import { gatewayIP, gatewayShort, selectedEndpoint } from "../clients";
import { dmy, hm, rate } from "../fmt";
import { useLive } from "../live";
import { Link, navigate, useLocation } from "../router";
import { Icon } from "../ui";
import { useBrand, useTheme } from "../prefs";
import { BrandMark } from "./Brand";

export type Page = "dashboard" | "topology" | "clients" | "rules" | "activity" | "internet" | "health" | "settings" | "none";

const NAV: { page: Page; href: string; label: string; short: string; icon: string }[] = [
  { page: "dashboard", href: "/", label: "Dashboard", short: "Home", icon: "dashboard" },
  { page: "topology", href: "/topology", label: "Topology", short: "Topology", icon: "topology" },
  { page: "clients", href: "/clients", label: "Clients", short: "Clients", icon: "clients" },
  { page: "rules", href: "/rules", label: "Rules", short: "Rules", icon: "rules" },
  { page: "activity", href: "/activity", label: "Activity", short: "Activity", icon: "activity" },
  { page: "internet", href: "/internet", label: "Internet", short: "Internet", icon: "internet" },
  { page: "health", href: "/health", label: "Health", short: "Health", icon: "health" },
];

export function Shell({ page, children }: { page: Page; children: ReactNode }) {
  const { overview } = useLive();
  const brand = useBrand();
  return (
    <>
      <aside className="rail" aria-label="Main">
        <Link href="/" className="mark" title={brand.name} aria-label={`${brand.name}: dashboard`}>
          <BrandMark b={brand} size={26} />
        </Link>
        <nav aria-label="Pages">
          {NAV.map((n) => (
            <Link key={n.page} href={n.href} className={(page === n.page ? "ri on" : "ri") + (n.page === "health" ? " hide-m" : "")} aria-current={page === n.page ? "page" : undefined} aria-label={n.label}>
              <Icon n={n.icon} s={18} />
              <span className="tip" aria-hidden="true">{n.label}</span>
              <span className="lbl" aria-hidden="true">{n.short}</span>
            </Link>
          ))}
        </nav>
        <div className="foot">
          <Link href="/settings" className={page === "settings" ? "ri on" : "ri"} aria-current={page === "settings" ? "page" : undefined} aria-label="Settings">
            <Icon n="settings" s={18} />
            <span className="tip" aria-hidden="true">Settings</span>
          </Link>
        </div>
      </aside>
      <TopBar o={overview} />
      <main className="main" id="main">
        {children}
      </main>
    </>
  );
}

function TopBar({ o }: { o: Overview | null }) {
  const { status, sample } = useLive();
  const brand = useBrand();
  const [, theme, setTheme] = useTheme();
  const { path, query } = useLocation();
  const input = useRef<HTMLInputElement>(null);
  const q = path === "/clients" ? query.get("q") ?? "" : "";

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (e.key === "/" && !/INPUT|TEXTAREA|SELECT/.test(t.tagName)) {
        e.preventDefault();
        input.current?.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const search = (v: string) => {
    const next = new URLSearchParams(path === "/clients" ? query : undefined);
    if (v) next.set("q", v);
    else next.delete("q");
    const s = next.toString();
    navigate(`/clients${s ? `?${s}` : ""}`, { replace: path === "/clients" });
  };

  const gw = o?.gateway;
  const ep = o ? selectedEndpoint(o) : null;
  const role = gw ? (gw.role === "master" ? "active" : gw.role) : "active";
  return (
    <header className={sample ? "top sample" : "top"}>
      <div className="app">
        {brand.name}
        <span>{gatewayIP()}</span>
      </div>
      <label className="search">
        <Icon n="search" s={14} />
        <span className="sr-only">Search clients, IPs, MACs</span>
        <input
          ref={input}
          placeholder="Search clients, IPs, MACs"
          value={q}
          onChange={(e) => search(e.target.value)}
          onKeyDown={(e) => e.key === "Escape" && (search(""), input.current?.blur())}
        />
        <kbd aria-hidden="true">/</kbd>
      </label>
      <div className="live" role="status" aria-live="polite">
        {sample && <span className="tag warn" title="Mock mode: nothing here comes from the gateway">Sample data</span>}
        {status === "down" ? (
          <span style={{ display: "flex", gap: 7, alignItems: "center" }} className="badc">
            <span className="dot bad" />
            Can't reach the gateway — retrying
          </span>
        ) : !o ? (
          <span className="t3">Connecting…</span>
        ) : (
          <>
            <span style={{ display: "flex", gap: 7, alignItems: "center" }}>
              <span className={`dot pulse ${gw && gw.role !== "master" ? "warn" : "ok"}`} />
              Gateway {gatewayShort()} {role}
              <span className="hide-m" style={{ display: "contents" }}>
                <span className="t3">·</span>via {ep ? ep.name : "Direct"}
              </span>
            </span>
            <span className="sep hide-m" />
            <span className="hide-m num">
              ↓ <CountUp value={o.live.down} fmt={rate} ms={1200} /> &nbsp;↑ <CountUp value={o.live.up} fmt={rate} ms={1200} />
            </span>
            <span className="sep hide-m" />
            <span className="t3 hide-m">
              {dmy(o.now)} {hm(o.now)}
            </span>
          </>
        )}
        <Link href="/health" className="btn icon ghost show-m" aria-label="Health" title="Health">
          <Icon n="health" s={15} />
        </Link>
        <Link href="/settings" className="btn icon ghost show-m" aria-label="Settings" title="Settings">
          <Icon n="settings" s={15} />
        </Link>
        <button
          type="button"
          className="btn icon ghost themebtn"
          onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
          aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
          title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
        >
          <Icon n={theme === "dark" ? "moon" : "sun"} s={15} />
        </button>
      </div>
    </header>
  );
}

export function Loading() {
  return (
    <div className="page" aria-busy="true">
      <div className="ph">
        <div className="skel" style={{ width: 160 }} />
      </div>
      <div className="grid">
        {[0, 1, 2].map((i) => (
          <div key={i} className="pn c4" style={{ height: 180 }} />
        ))}
      </div>
    </div>
  );
}

export function Unreachable({ error }: { error: string | null }) {
  return (
    <div className="page">
      <div className="ph">
        <h1>Can't reach the gateway — retrying</h1>
      </div>
      <div className="pn">
        <div className="bd">
          <p style={{ margin: 0 }}>Junction tries again every few seconds. Check that the gateway at {gatewayIP()} is up and that you're on the home network.</p>
          {error && error !== "Can't reach the gateway." && <p className="note">{error}</p>}
        </div>
      </div>
    </div>
  );
}

/** Loading / unreachable until the first overview arrives; afterwards a slim banner while it's down. */
export function NeedOverview({ children }: { children: (o: Overview) => ReactNode }) {
  const { overview, status, error } = useLive();
  if (!overview) return status === "down" ? <Unreachable error={error} /> : <Loading />;
  return (
    <>
      {status === "down" && (
        <div className="page" style={{ paddingTop: 10, paddingBottom: 0 }}>
          <div className="line-alert" role="alert" style={{ marginTop: 0 }}>
            <span className="dot bad" />
            <span>
              <b>Can't reach the gateway — retrying.</b> <span className="t2">Showing what it reported at {hm(overview.now)}.</span>
            </span>
          </div>
        </div>
      )}
      {children(overview)}
    </>
  );
}
