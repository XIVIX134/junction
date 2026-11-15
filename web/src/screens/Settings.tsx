import { useEffect, useRef, useState, type ReactNode } from "react";
import { api2, ApiError } from "../api/client";
import type { Settings as S, SettingsPatch } from "../api/types";
import { BrandMark } from "../components/Brand";
import { dmy, hm } from "../fmt";
import { useSettings, useTheme, type ThemeMode } from "../prefs";
import { Link, useLocation } from "../router";
import { Sw } from "../ui";

const SECTIONS = [
  ["general", "General"],
  ["network", "Network"],
  ["vpn", "VPN & profiles"],
  ["access", "Access"],
] as const;
type Section = (typeof SECTIONS)[number][0];

const COLORS = ["#4797ff", "#3fc36f", "#a77bf3", "#f2a93b", "#f0545a", "#5b6068"];

export function Settings() {
  const { settings: s, save } = useSettings();
  const loc = useLocation();
  const hash = (location.hash.slice(1) || "general") as Section;
  const sec: Section = SECTIONS.some(([k]) => k === hash) ? hash : "general";
  const [msg, setMsg] = useState<{ ok: boolean; t: string } | null>(null);

  async function apply(p: SettingsPatch, done = "Saved.") {
    setMsg(null);
    try {
      await save(p);
      setMsg({ ok: true, t: done });
    } catch (e) {
      setMsg({ ok: false, t: e instanceof ApiError ? e.message : "Couldn't save that." });
    }
  }
  void loc;

  return (
    <div className="page">
      <div className="ph">
        <h1>Settings</h1>
        <span className="meta">Changes apply right away. Nothing here can cut the internet.</span>
        {msg && <span className={msg.ok ? "okmsg" : "err"} style={{ marginLeft: "auto" }}>{msg.t}</span>}
      </div>
      {!s ? (
        <div className="t3">Loading…</div>
      ) : (
        <div className="set">
          <nav className="subnav" aria-label="Settings sections">
            {SECTIONS.map(([k, l]) => (
              <Link key={k} href={`/settings#${k}`} className={sec === k ? "on" : ""} aria-current={sec === k ? "page" : undefined}>
                {l}
              </Link>
            ))}
          </nav>
          <div style={{ display: "flex", flexDirection: "column", gap: 12, minWidth: 0 }}>
            {sec === "general" && <General s={s} apply={apply} />}
            {sec === "network" && <Network s={s} apply={apply} />}
            {sec === "vpn" && <Vpn s={s} apply={apply} />}
            {sec === "access" && <Access s={s} apply={apply} />}
          </div>
        </div>
      )}
    </div>
  );
}

type P = { s: S; apply: (p: SettingsPatch, done?: string) => Promise<void> };

const Panel = ({ title, right, children }: { title: string; right?: ReactNode; children: ReactNode }) => (
  <section className="pn">
    <div className="hd">
      <h2>{title}</h2>
      {right && <div className="r">{right}</div>}
    </div>
    <div className="bd" style={{ paddingTop: 2, paddingBottom: 2 }}>
      {children}
    </div>
  </section>
);

const Row = ({ k, hint, children }: { k: string; hint?: string; children: ReactNode }) => (
  <div className="srow">
    <div className="k">
      <b>{k}</b>
      {hint && <span>{hint}</span>}
    </div>
    <div className="v">{children}</div>
  </div>
);

/** A text field that saves on Enter or when it loses focus, if it changed. */
function Field({ value, onSave, width = 260, label, placeholder }: { value: string; onSave: (v: string) => void; width?: number; label: string; placeholder?: string }) {
  const [v, setV] = useState(value);
  useEffect(() => setV(value), [value]);
  const commit = () => v.trim() !== value && v.trim() !== "" && onSave(v.trim());
  return (
    <input
      className="inp"
      style={{ width }}
      aria-label={label}
      placeholder={placeholder}
      value={v}
      onChange={(e) => setV(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()}
    />
  );
}

function Seg<T extends string | number>({ value, options, onPick, label }: { value: T; options: [T, string][]; onPick: (v: T) => void; label: string }) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map(([k, l]) => (
        <button key={String(k)} type="button" className={value === k ? "on" : ""} aria-pressed={value === k} onClick={() => value !== k && onPick(k)}>
          {l}
        </button>
      ))}
    </div>
  );
}

// ---------- general ----------

function General({ s, apply }: P) {
  const [mode, , setMode] = useTheme();
  const file = useRef<HTMLInputElement>(null);
  const b = s.brand;
  async function upload(f: File) {
    if (f.size > 256 * 1024) return alert("Pick an image under 256 KB.");
    const url = await new Promise<string>((res) => {
      const r = new FileReader();
      r.onload = () => res(String(r.result));
      r.readAsDataURL(f);
    });
    apply({ brand: { image: url } }, "Icon saved.");
  }
  return (
    <Panel title="General">
      <Row k="Name" hint="Shown in the top bar and the browser tab">
        <Field label="Name" value={b.name} onSave={(name) => apply({ brand: { name } }, "Name saved.")} placeholder="Junction" />
        <span className="note">e.g. "Home Gateway"</span>
      </Row>
      <Row k="Icon" hint="A letter on a colour, or your own image">
        <BrandMark b={b} size={40} />
        {!b.image && (
          <>
            <div className="swatches" role="group" aria-label="Icon colour">
              {COLORS.map((c) => (
                <button key={c} type="button" style={{ background: c }} aria-label={`Colour ${c}`} aria-pressed={b.color === c} onClick={() => apply({ brand: { color: c } })} />
              ))}
            </div>
            <Field label="Icon letter" width={52} value={b.letter} onSave={(letter) => apply({ brand: { letter: letter.slice(0, 2) } })} />
          </>
        )}
        <input ref={file} type="file" accept="image/png,image/svg+xml,image/jpeg,image/webp" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        <button type="button" className="btn sm" onClick={() => file.current?.click()}>
          {b.image ? "Change image…" : "Upload image…"}
        </button>
        {b.image && (
          <button type="button" className="btn sm ghost" onClick={() => apply({ brand: { image: null } }, "Back to the letter icon.")}>
            Use a letter instead
          </button>
        )}
        {!b.image && <span className="note">PNG or SVG, square</span>}
      </Row>
      <Row k="Appearance" hint="Also the sun / moon button in the top bar. Saved in this browser.">
        <Seg<ThemeMode>
          label="Appearance"
          value={mode}
          options={[
            ["dark", "Dark"],
            ["light", "Light"],
            ["system", "Match system"],
          ]}
          onPick={setMode}
        />
      </Row>
      <Row k="Time zone" hint="Times, days and schedules use it">
        <Field label="Time zone" value={s.timezone} onSave={(timezone) => apply({ timezone }, "Time zone saved.")} placeholder="Europe/London" />
        {s.timezone === s.timezoneDetected ? <span className="note">detected from the gateway</span> : <span className="note">the gateway says {s.timezoneDetected}</span>}
      </Row>
    </Panel>
  );
}

// ---------- network ----------

function Network({ s, apply }: P) {
  const n = s.network;
  return (
    <Panel title="Network" right={n.detectedAt ? <span>Detected {dmy(n.detectedAt)} {hm(n.detectedAt)}</span> : undefined}>
      <Row k="Gateway address" hint="The address devices use as their gateway">
        <Field label="Gateway address" width={200} value={n.gatewayIP} onSave={(gatewayIP) => apply({ network: { gatewayIP } })} />
        {n.backup && <span className="tag">floating, held by keepalived</span>}
      </Row>
      <Row k="Local network" hint="Scanned to find devices">
        <Field label="Local network" width={200} value={n.lan} onSave={(lan) => apply({ network: { lan } })} />
        <span className="note">interface {n.iface}</span>
      </Row>
      <Row k="Router" hint="Optional: read its device list for names">
        {n.router ? (
          <>
            <span className="inp" style={{ width: 200, display: "inline-flex", alignItems: "center" }}>{n.router.url}</span>
            <span className={`tag ${n.router.connected ? "" : "warn"}`}>{n.router.connected ? `connected · ${n.router.devices} devices` : "can't log in"}</span>
          </>
        ) : (
          <span className="t3">Not set up</span>
        )}
      </Row>
      <Row k="Backup gateway" hint="Takes over if this box fails (keepalived)">
        {n.backup ? (
          <>
            <span className={`dot ${n.backup.state === "covering" ? "warn" : "ok"}`} />
            <span>
              {n.backup.name} · {n.backup.ip}
            </span>
            <span className="t3">{n.backup.state === "covering" ? "covering now" : n.backup.state}</span>
          </>
        ) : (
          <span className="t3">None. The setup script can add one.</span>
        )}
      </Row>
      <Row k="DHCP" hint="Hand out addresses with this gateway">
        {n.dhcp?.on ? (
          <>
            <span className="dot ok" />
            <span>
              Pool {n.dhcp.pool} · {n.dhcp.leaseHours} h leases · {n.dhcp.reservations} reservations
            </span>
          </>
        ) : (
          <span className="t3">Off: the router hands out addresses</span>
        )}
      </Row>
    </Panel>
  );
}

// ---------- VPN ----------

function Vpn({ s, apply }: P) {
  const v = s.vpn;
  return (
    <Panel title="VPN & profiles" right={<span>Read from the sing-box config</span>}>
      <Row k="VPN exits" hint="The ways out other than direct">
        {v.exits.length ? (
          v.exits.map((e) => (
            <span key={e.key} className="tag" style={{ gap: 6 }}>
              <span className={`dot ${e.healthy ? "ok" : "bad"}`} />
              {e.name}
            </span>
          ))
        ) : (
          <span className="t3">None: everything goes direct</span>
        )}
      </Row>
      <Row k="Profiles" hint="Groups of rules a device can follow">
        {v.profiles.map((p) => (
          <span key={p} className="tag">
            {p}
          </span>
        ))}
      </Row>
      <Row k="Default profile" hint="For devices on VPN without their own">
        <select className="inp" aria-label="Default profile" value={v.defaultProfile} onChange={(e) => apply({ vpn: { defaultProfile: e.target.value } })}>
          {v.profiles.map((p) => (
            <option key={p}>{p}</option>
          ))}
        </select>
      </Row>
      <div className="note" style={{ padding: "8px 0 10px" }}>
        To add or change an exit, edit the outbounds in the sing-box config (the setup script can do it). Junction picks them up when sing-box reloads.
      </div>
    </Panel>
  );
}

// ---------- access ----------

function Access({ s, apply }: P) {
  const a = s.access;
  const [pw, setPw] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; t: string } | null>(null);
  const { set } = useSettings();
  async function savePw() {
    if (pw.length < 8) return setMsg({ ok: false, t: "Use at least 8 characters." });
    setBusy(true);
    try {
      set(await api2.setPassword(pw));
      setPw("");
      setMsg({ ok: true, t: "Password set." });
    } catch (e) {
      setMsg({ ok: false, t: e instanceof ApiError ? e.message : "Couldn't set it." });
    } finally {
      setBusy(false);
    }
  }
  return (
    <Panel title="Access">
      <Row k="Require a password" hint={a.required ? "On: this page asks for the password" : "Off: anyone on your network can open this page"}>
        <Sw
          on={a.required}
          label="Require a password"
          disabled={!a.passwordSet && !a.required}
          reason={!a.passwordSet ? "Set a password first" : undefined}
          onChange={(required) => apply({ access: { required } }, required ? "Password required from now on." : "Password no longer required.")}
        />
        <span className="t2">{a.required ? "On" : "Off"}</span>
      </Row>
      <Row k="Password" hint={a.passwordSet ? "Set. Type a new one to change it." : "Set it before turning the switch on"}>
        <input className="inp" type="password" style={{ width: 220 }} aria-label="New password" placeholder={a.passwordSet ? "••••••••••" : "New password"} value={pw} onChange={(e) => setPw(e.target.value)} />
        <button type="button" className="btn sm" disabled={!pw || busy} onClick={savePw}>
          {a.passwordSet ? "Change password" : "Set password"}
        </button>
        {msg && <span className={msg.ok ? "okmsg" : "err"}>{msg.t}</span>}
      </Row>
      <Row k="Stay signed in" hint="On each browser">
        <Seg
          label="Stay signed in"
          value={a.sessionDays}
          options={[
            [1, "1 day"],
            [30, "30 days"],
            [0, "Until I sign out"],
          ]}
          onPick={(sessionDays) => apply({ access: { sessionDays } })}
        />
      </Row>
      <Row k="Forgot it?" hint="Run this on the gateway">
        <span className="kbdline">junctiond -reset-password</span>
      </Row>
    </Panel>
  );
}
