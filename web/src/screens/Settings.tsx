import { useRef, useState } from "react";
import { api2, ApiError } from "../api/client";
import type { Settings as S, SettingsPatch, SettingsSaved } from "../api/types";
import { BrandMark } from "../components/Brand";
import { useSettings, useTheme, type ThemeMode } from "../prefs";
import { Link, useLocation } from "../router";
import { Sw } from "../ui";
import { Field, Panel, Row, Seg } from "./SettingsParts";
import { Advanced, Bundle, Data, Dhcp, Network, Updates, Vpn } from "./SettingsConfig";

const SECTIONS = [
  ["general", "General"],
  ["network", "Network"],
  ["dhcp", "DHCP"],
  ["vpn", "VPN"],
  ["bundle", "Bundle"],
  ["access", "Access"],
  ["updates", "Updates"],
  ["data", "Data"],
  ["advanced", "Advanced"],
] as const;
type Section = (typeof SECTIONS)[number][0];

const COLORS = ["#4797ff", "#3fc36f", "#a77bf3", "#f2a93b", "#f0545a", "#5b6068"];

export function Settings() {
  const { settings: s, save, set, reload } = useSettings();
  const loc = useLocation();
  const hash = (location.hash.slice(1) || "general") as Section;
  const sec: Section = SECTIONS.some(([k]) => k === hash) ? hash : "general";
  const [msg, setMsg] = useState<{ ok: boolean; t: string } | null>(null);

  async function settle(r: SettingsSaved, done: string) {
    set(r);
    if (r.restarting) {
      // Junction restarts to apply it (traffic isn't affected); read it back once it's up.
      setMsg({ ok: true, t: "Saved. Junction is restarting to apply it…" });
      for (let i = 0; i < 20; i++) {
        await new Promise((res) => setTimeout(res, 1500));
        try {
          await reload();
          break;
        } catch {}
      }
    }
    setMsg({ ok: true, t: done });
  }

  async function apply(p: SettingsPatch, done = "Saved.") {
    setMsg(null);
    try {
      await settle(await save(p), done);
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
            {sec === "dhcp" && <Dhcp s={s} apply={apply} />}
            {sec === "vpn" && <Vpn s={s} apply={apply} setFull={(r, done) => void settle(r, done)} />}
            {sec === "bundle" && <Bundle s={s} apply={apply} />}
            {sec === "access" && <Access s={s} apply={apply} />}
            {sec === "updates" && <Updates s={s} apply={apply} />}
            {sec === "data" && <Data s={s} apply={apply} />}
            {sec === "advanced" && <Advanced s={s} apply={apply} />}
          </div>
        </div>
      )}
    </div>
  );
}

type P = { s: S; apply: (p: SettingsPatch, done?: string) => Promise<void> };

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
