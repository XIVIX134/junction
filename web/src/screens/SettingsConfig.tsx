import { useState, type ReactNode } from "react";
import { api2, ApiError } from "../api/client";
import type { ExitDraft, ExitTest, Reservation, Settings as S, SettingsPatch, StartingName } from "../api/types";
import { bytes, dmy, hm } from "../fmt";
import { Link } from "../router";
import { Sw } from "../ui";
import { Field, NumField, Panel, Row, Seg } from "./SettingsParts";

// The rest of the gateway's configuration: everything config.json holds, plus
// DHCP and VPN exits. Saving writes config.json (a backup is kept next to it);
// values read only at start restart Junction, which traffic doesn't depend on.

export type P = { s: S; apply: (p: SettingsPatch, done?: string) => Promise<void>; setFull?: (s: S) => void };

const Restarts = () => <span className="tag" title="Junction restarts to apply this: a few seconds, traffic isn't affected">restarts Junction</span>;

// ---------- network ----------

export function Network({ s, apply }: P) {
  const n = s.network;
  const [user, setUser] = useState(n.router?.user ?? "");
  const [pw, setPw] = useState("");
  const [url, setUrl] = useState(n.router?.url ?? "");
  return (
    <>
      <Panel title="Network" right={n.detectedAt ? <span>Detected {dmy(n.detectedAt)} {hm(n.detectedAt)}</span> : undefined}>
        <Row k="Network card" hint="The one facing your devices">
          <select className="inp" aria-label="Network card" value={n.iface} onChange={(e) => apply({ network: { iface: e.target.value } })}>
            {n.ifaces.map((i) => (
              <option key={i}>{i}</option>
            ))}
          </select>
          <Restarts />
        </Row>
        <Row k="Local network" hint="Scanned to find devices; empty = from the network card">
          <Field label="Local network" width={200} value={n.lan} onSave={(lan) => apply({ network: { lan } })} placeholder="192.168.1.0/24" />
          <Restarts />
        </Row>
        <Row k="Gateway address" hint="The address devices use as their gateway">
          <Field label="Gateway address" width={200} value={n.gatewayIP} onSave={(gatewayIP) => apply({ network: { gatewayIP } })} />
          {n.backup && <span className="tag">floating, held by keepalived</span>}
          <Restarts />
        </Row>
        <Row k="Learn names from DHCP" hint="Listen to devices asking for an address. Off when this gateway hands out addresses itself (DHCP section).">
          <Sw on={n.learnNamesFromDHCP} label="Learn names from DHCP" disabled={!!s.dhcp?.on} reason={s.dhcp?.on ? "Names come from the DHCP leases" : undefined} onChange={(v) => apply({ network: { learnNamesFromDHCP: v } })} />
          <Restarts />
        </Row>
      </Panel>
      <Panel title="Router" right={n.router ? <span className={n.router.connected ? "okc" : "warnc"}>{n.router.connected ? `connected · ${n.router.devices} devices` : "can't log in"}</span> : <span>optional, not needed</span>}>
        <div className="note" style={{ padding: "10px 0 4px" }}>
          Optional: Junction works with any router. If yours is supported, Junction can read its device list for better names; without it, names come from DHCP, mDNS and UPnP. <a href="https://github.com/XIVIX134/junction/blob/main/docs/routers.md" target="_blank" rel="noreferrer">Supported routers</a>
        </div>
        <Row k="Router page">
          <input className="inp" style={{ width: 240 }} aria-label="Router page" placeholder="https://192.168.1.1" value={url} onChange={(e) => setUrl(e.target.value)} />
        </Row>
        <Row k="Login">
          <input className="inp" style={{ width: 150 }} aria-label="Router user" placeholder="user" value={user} onChange={(e) => setUser(e.target.value)} />
          <input className="inp" style={{ width: 170 }} type="password" aria-label="Router password" placeholder={n.router?.passwordSet ? "•••••••• (saved)" : "password"} value={pw} onChange={(e) => setPw(e.target.value)} />
          <button type="button" className="btn sm" disabled={!url.trim()} onClick={() => apply({ network: { router: { url: url.trim(), user: user.trim(), ...(pw ? { password: pw } : {}) } } }, "Router saved.").then(() => setPw(""))}>
            Save
          </button>
          {n.router && (
            <button type="button" className="btn sm ghost" onClick={() => apply({ network: { router: null } }, "Router disconnected.")}>
              Don't read the router
            </button>
          )}
        </Row>
      </Panel>
      <Panel title="Backup gateway" right={<span>keepalived</span>}>
        {n.backup ? (
          <>
            <Row k="Status">
              <span className={`dot ${n.backup.state === "covering" ? "warn" : "ok"}`} />
              <span>{n.backup.state === "covering" ? "Covering now: internet goes direct, no VPN" : n.backup.state === "standby" ? "On standby" : "Unknown"}</span>
            </Row>
            <Row k="Name" hint="How it's shown in the app">
              <Field label="Backup name" width={200} value={n.backup.name} onSave={(backupName) => apply({ network: { backupName } })} />
            </Row>
            <Row k="Address">
              <Field label="Backup address" width={200} value={n.backup.ip} onSave={(backupIP) => apply({ network: { backupIP } })} />
            </Row>
          </>
        ) : (
          <div className="note" style={{ padding: "10px 0" }}>
            None. A second box (another Pi, a NAS, a server) can take over the gateway address within seconds if this one fails: run the setup script with <code>--vip</code>.
          </div>
        )}
      </Panel>
    </>
  );
}

// ---------- DHCP ----------

export function Dhcp({ s, apply }: P) {
  const d = s.dhcp;
  const [add, setAdd] = useState<Reservation>({ name: "", mac: "", ip: "" });
  if (!d)
    return (
      <Panel title="DHCP">
        <div className="note" style={{ padding: "10px 0" }}>
          This gateway doesn't hand out addresses: your router does. The setup script can install dnsmasq and take over (<code>setup.sh --dhcp</code>).
        </div>
      </Panel>
    );
  const saveRes = (reservations: Reservation[], done: string) => apply({ dhcp: { reservations } }, done);
  return (
    <>
      <Panel title="DHCP" right={<span className={d.on ? "okc" : "t3"}>{d.on ? `handing out addresses · ${d.leases} active` : "off"}</span>}>
        <Row k="Address pool" hint="Addresses given to devices without a reservation">
          <Field label="Pool start" width={140} value={d.poolStart} onSave={(poolStart) => apply({ dhcp: { poolStart } })} />
          <span className="t3">to</span>
          <Field label="Pool end" width={140} value={d.poolEnd} onSave={(poolEnd) => apply({ dhcp: { poolEnd } })} />
        </Row>
        <Row k="Lease time" hint="How long a device keeps its address">
          <NumField label="Lease hours" value={d.leaseHours} min={1} max={168} unit="hours" onSave={(leaseHours) => apply({ dhcp: { leaseHours } })} />
        </Row>
        <Row k="DNS servers" hint="Handed to devices, comma separated">
          <Field label="DNS servers" width={260} value={d.dns.join(", ")} onSave={(v) => apply({ dhcp: { dns: v.split(/[\s,]+/).filter(Boolean) } })} />
        </Row>
      </Panel>
      <Panel title="Reservations" right={<span>{d.reservations.length} fixed addresses</span>}>
        <div className="scroll-x">
          <table className="tb compact">
            <thead>
              <tr>
                <th>Name</th>
                <th>MAC address</th>
                <th>Address</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {d.reservations.map((r) => (
                <tr key={r.mac}>
                  <td>{r.name || <span className="t3">—</span>}</td>
                  <td className="t2 num">{r.mac}</td>
                  <td className="num">{r.ip}</td>
                  <td className="rt">
                    <button type="button" className="btn sm ghost" aria-label={`Remove the reservation for ${r.name || r.mac}`} onClick={() => saveRes(d.reservations.filter((x) => x.mac !== r.mac), "Reservation removed.")}>
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
              <tr>
                <td>
                  <input className="inp" style={{ width: 150 }} aria-label="New reservation name" placeholder="Name" value={add.name} onChange={(e) => setAdd({ ...add, name: e.target.value })} />
                </td>
                <td>
                  <input className="inp num" style={{ width: 160 }} aria-label="New reservation MAC" placeholder="aa:bb:cc:dd:ee:ff" value={add.mac} onChange={(e) => setAdd({ ...add, mac: e.target.value })} />
                </td>
                <td>
                  <input className="inp num" style={{ width: 130 }} aria-label="New reservation address" placeholder="192.168.1.50" value={add.ip} onChange={(e) => setAdd({ ...add, ip: e.target.value })} />
                </td>
                <td className="rt">
                  <button
                    type="button"
                    className="btn sm"
                    disabled={!add.mac || !add.ip}
                    onClick={() => saveRes([...d.reservations, { ...add, mac: add.mac.trim().toLowerCase(), ip: add.ip.trim(), name: add.name.trim() }], "Reservation added.").then(() => setAdd({ name: "", mac: "", ip: "" }))}
                  >
                    Add
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div className="note" style={{ padding: "8px 0 10px" }}>
          A device keeps its reserved address the next time it connects.
        </div>
      </Panel>
    </>
  );
}

// ---------- VPN ----------

const KIND: Record<string, string> = { wireguard: "WireGuard", hysteria2: "Hysteria2", vless: "VLESS", trojan: "Trojan", shadowsocks: "Shadowsocks", other: "sing-box" };

export function Vpn({ s, apply, setFull }: P) {
  const v = s.vpn;
  const f = v.failover;
  const [adding, setAdding] = useState(false);
  const [newProfile, setNewProfile] = useState("");
  return (
    <>
      <Panel
        title="VPN exits"
        right={
          <button type="button" className="btn sm" aria-pressed={adding} onClick={() => setAdding(!adding)}>
            Add exit
          </button>
        }
      >
        {adding && <AddExit onDone={(n) => (setFull?.(n), setAdding(false))} onCancel={() => setAdding(false)} />}
        {v.exits.length ? (
          <div className="scroll-x">
            <table className="tb compact">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                  <th>Server</th>
                  <th>Status</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {v.exits.map((e) => (
                  <ExitRow key={e.key} e={e} apply={apply} setFull={setFull} />
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          !adding && <div className="note" style={{ padding: "10px 0" }}>No VPN exits: every device goes direct. Add one to send chosen devices through a VPN.</div>
        )}
      </Panel>
      <Panel title="Profiles" right={<span>sing-box selectors vpn-&lt;profile&gt;</span>}>
        <div className="note" style={{ padding: "10px 0 4px" }}>
          A device on VPN follows a profile. Each profile can use a different exit, and is checked against a site you choose (so an exit that blocks that site counts as down for it).
        </div>
        {v.profiles.map((p) => (
          <Row key={p.name} k={p.name} hint={p.name === v.defaultProfile ? "the default profile" : undefined}>
            <Field label={`Check site for ${p.name}`} width={240} value={p.checkSite} placeholder="any site (cp.cloudflare.com)" onSave={(checkSite) => apply({ vpn: { profiles: v.profiles.map((x) => (x.name === p.name ? { ...x, checkSite } : x)) } })} />
            {p.name !== v.defaultProfile && (
              <>
                <button type="button" className="btn sm ghost" onClick={() => apply({ vpn: { defaultProfile: p.name } }, `${p.name} is the default profile.`)}>
                  Make default
                </button>
                <button type="button" className="btn sm ghost" onClick={() => apply({ vpn: { profiles: v.profiles.filter((x) => x.name !== p.name) } }, `Profile ${p.name} removed.`)}>
                  Remove
                </button>
              </>
            )}
          </Row>
        ))}
        <Row k="Add a profile" hint="Letters, digits and dashes">
          <input className="inp" style={{ width: 160 }} aria-label="New profile name" placeholder="streaming" value={newProfile} onChange={(e) => setNewProfile(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ""))} />
          <button type="button" className="btn sm" disabled={!newProfile || v.profiles.some((p) => p.name === newProfile)} onClick={() => apply({ vpn: { profiles: [...v.profiles, { name: newProfile, checkSite: "" }] } }, `Profile ${newProfile} added.`).then(() => setNewProfile(""))}>
            Add
          </button>
        </Row>
      </Panel>
      <Panel title="Automatic failover" right={<span className={f.enabled ? "okc" : "t3"}>{f.enabled ? "on" : "off"}</span>}>
        <Row k="Switch exits by itself" hint="When an exit stops answering, move its devices to a healthy one (or direct if none)">
          <Sw on={f.enabled} label="Automatic failover" disabled={v.exits.length === 0} reason={v.exits.length === 0 ? "Add an exit first" : undefined} onChange={(enabled) => apply({ vpn: { failover: { enabled } } })} />
          <Restarts />
        </Row>
        <Row k="Check against" hint="A page that answers quickly">
          <Field label="Check address" width={240} value={f.checkURL} onSave={(checkURL) => apply({ vpn: { failover: { checkURL } } })} />
        </Row>
        <Row k="Check every">
          <NumField label="Check every" value={f.everySec} min={5} max={300} unit="s" onSave={(everySec) => apply({ vpn: { failover: { everySec } } })} />
          <span className="t3">give up after</span>
          <NumField label="Check timeout" value={f.timeoutSec} min={1} max={30} unit="s" onSave={(timeoutSec) => apply({ vpn: { failover: { timeoutSec } } })} />
        </Row>
        <Row k="Down after" hint="Failed checks in a row">
          <NumField label="Down after" value={f.downAfter} min={1} max={20} unit="checks" onSave={(downAfter) => apply({ vpn: { failover: { downAfter } } })} />
        </Row>
        <Row k="Back after" hint="Good checks in a row, then a wait, before trusting it again">
          <NumField label="Back after" value={f.upAfter} min={1} max={60} unit="checks" onSave={(upAfter) => apply({ vpn: { failover: { upAfter } } })} />
          <span className="t3">and</span>
          <NumField label="Wait" value={f.waitMin} min={0} max={120} unit="min" onSave={(waitMin) => apply({ vpn: { failover: { waitMin } } })} />
        </Row>
      </Panel>
    </>
  );
}

function ExitRow({ e, apply, setFull }: { e: S["vpn"]["exits"][number]; apply: P["apply"]; setFull?: (s: S) => void }) {
  const [confirm, setConfirm] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  return (
    <tr>
      <td>
        <Field label={`Name of ${e.name}`} width={160} value={e.name} onSave={(name) => apply({ vpn: { renameExit: { key: e.key, name } } }, "Exit renamed.")} />
      </td>
      <td className="t2">{KIND[e.kind]}</td>
      <td className="t2 num">{e.server}</td>
      <td>
        <span className={`dot ${e.healthy ? "ok" : "bad"}`} /> {e.healthy ? `${e.latencyMs ?? "—"} ms` : <span className="badc">Down</span>}
      </td>
      <td className="rt">
        {confirm ? (
          <span style={{ display: "inline-flex", gap: 6 }}>
            <button
              type="button"
              className="btn sm danger"
              onClick={async () => {
                try {
                  setFull?.(await api2.removeExit(e.key));
                } catch (x) {
                  setErr(x instanceof ApiError ? x.message : "Couldn't remove it.");
                }
              }}
            >
              Remove
            </button>
            <button type="button" className="btn sm ghost" onClick={() => setConfirm(false)}>
              Keep
            </button>
          </span>
        ) : (
          <button type="button" className="btn sm ghost" onClick={() => setConfirm(true)}>
            Remove…
          </button>
        )}
        {err && <div className="err">{err}</div>}
      </td>
    </tr>
  );
}

function AddExit({ onDone, onCancel }: { onDone: (s: S) => void; onCancel: () => void }) {
  const [d, setD] = useState<ExitDraft>({ name: "", kind: "wireguard", text: "" });
  const [test, setTest] = useState<ExitTest | null>(null);
  const [busy, setBusy] = useState<"test" | "save" | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const run = async (what: "test" | "save") => {
    setBusy(what);
    setErr(null);
    try {
      if (what === "test") setTest(await api2.testExit(d));
      else onDone(await api2.addExit(d));
    } catch (x) {
      setErr(x instanceof ApiError ? x.message : what === "test" ? "The test didn't run." : "Couldn't add it.");
    } finally {
      setBusy(null);
    }
  };
  return (
    <div className="addbox">
      <Row k="Name" hint="Shown everywhere in the app">
        <input className="inp" style={{ width: 200 }} aria-label="Exit name" placeholder="Home VPN" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} />
      </Row>
      <Row k="Paste" hint="From your VPN provider or your own server">
        <Seg<ExitDraft["kind"]>
          label="Exit type"
          value={d.kind}
          options={[
            ["wireguard", "WireGuard config"],
            ["outbound", "sing-box outbound (any type)"],
          ]}
          onPick={(kind) => setD({ ...d, kind })}
        />
      </Row>
      <textarea
        className="paste"
        aria-label={d.kind === "wireguard" ? "WireGuard config" : "sing-box outbound JSON"}
        placeholder={d.kind === "wireguard" ? "[Interface]\nPrivateKey = …\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = …\nEndpoint = vpn.example.com:51820\nAllowedIPs = 0.0.0.0/0" : '{ "type": "hysteria2", "server": "vpn.example.com", "server_port": 443, … }'}
        value={d.text}
        onChange={(e) => setD({ ...d, text: e.target.value })}
        style={{ minHeight: 120, marginTop: 6 }}
      />
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", margin: "10px 0 12px" }}>
        <button type="button" className="btn sm" disabled={!d.text.trim() || !!busy} onClick={() => run("test")}>
          {busy === "test" ? "Testing…" : "Test"}
        </button>
        <button type="button" className="btn sm pri" disabled={!d.text.trim() || !d.name.trim() || !!busy} onClick={() => run("save")}>
          {busy === "save" ? "Adding…" : "Add exit"}
        </button>
        <button type="button" className="btn sm ghost" onClick={onCancel}>
          Cancel
        </button>
        {test && (
          <span className={test.ok ? "okmsg" : "err"}>
            {test.ok ? `Works: ${test.server} answered in ${test.latencyMs} ms` : test.message}
          </span>
        )}
        {err && <span className="err">{err}</span>}
      </div>
      <div className="note" style={{ paddingBottom: 10 }}>
        Junction adds it to sing-box's config, checks the config with <code>sing-box check</code> and reloads sing-box. If sing-box doesn't come back healthy, the previous config goes back by itself. The keys stay on the gateway.
      </div>
    </div>
  );
}

// ---------- bundle ----------

export function Bundle({ s, apply }: P) {
  const a = s.bundle.autoRenew;
  const [size, setSize] = useState(String(a?.sizeGB ?? ""));
  const [day, setDay] = useState(String(a?.day ?? 1));
  return (
    <Panel title="Bundle">
      <Row k="Renews by itself" hint="Logs the renewal every month, so the count starts again on its own">
        <Sw on={!!a} label="Monthly renewal" onChange={(on) => apply({ bundle: { autoRenew: on ? { sizeGB: Number(size) || 250, day: Number(day) || 1 } : null } }, on ? "Monthly renewal on." : "Monthly renewal off.")} />
      </Row>
      <Row k="Size and day" hint="Your plan's size, and the day of the month it renews">
        <input className="inp num" style={{ width: 80 }} aria-label="Bundle size in GB" inputMode="decimal" value={size} onChange={(e) => setSize(e.target.value)} />
        <span className="t3">GB, on day</span>
        <input className="inp num" style={{ width: 60 }} aria-label="Renewal day of the month" inputMode="numeric" value={day} onChange={(e) => setDay(e.target.value)} />
        <button type="button" className="btn sm" disabled={!a || !(Number(size) > 0) || !(Number(day) >= 1 && Number(day) <= 28)} onClick={() => apply({ bundle: { autoRenew: { sizeGB: Number(size), day: Number(day) } } })}>
          Save
        </button>
      </Row>
      <div className="note" style={{ padding: "8px 0 10px" }}>
        One-off renewals and "what's left" corrections are on the <Link href="/internet#renew">Internet page</Link>.
      </div>
    </Panel>
  );
}

// ---------- updates ----------

export function Updates({ s, apply }: P) {
  const u = s.updates;
  return (
    <Panel title="Updates" right={<span>installed {s.version}</span>}>
      <Row k="Check for new versions" hint="Looks at the releases page once every few hours">
        <Sw on={u.check} label="Check for updates" onChange={(check) => apply({ updates: { check } })} />
        {u.check && <span className="t3">{u.available ? `${u.available} is ready` : u.lastCheck ? `up to date · checked ${hm(u.lastCheck)}` : "not checked yet"}</span>}
      </Row>
      <Row k="Releases" hint="Where to look; change it to follow a fork">
        <Field label="Releases address" width={420} value={u.url} onSave={(url) => apply({ updates: { url } })} />
      </Row>
      <div className="note" style={{ padding: "8px 0 10px" }}>
        Installing is on the <Link href="/health">Health page</Link>: the current version is kept, and comes back by itself if the new one isn't healthy within a minute.
      </div>
    </Panel>
  );
}

// ---------- data ----------

export function Data({ s, apply }: P) {
  const d = s.data;
  return (
    <Panel title="Data" right={<span>database {bytes(d.dbBytes)}</span>}>
      <Row k="Keep usage history" hint="Per device, per hour">
        <NumField label="Usage history days" value={d.usageDays} min={30} max={1000} unit="days" onSave={(usageDays) => apply({ data: { usageDays } })} />
      </Row>
      <Row k="Keep site lists" hint="Which sites each device used">
        <NumField label="Site list days" value={d.sitesDays} min={7} max={400} unit="days" onSave={(sitesDays) => apply({ data: { sitesDays } })} />
      </Row>
      <Row k="Backup" hint="The database and config.json, as one file">
        <a className="btn sm" href={api2.backupURL} download>
          Download a backup
        </a>
        <span className="note">Includes device names, rules and history. Router and VPN keys stay out.</span>
      </Row>
    </Panel>
  );
}

// ---------- advanced ----------

export function Advanced({ s, apply }: P) {
  const a = s.advanced;
  const [add, setAdd] = useState<StartingName>({ mac: "", name: "", group: "" });
  const setNames = (startingNames: StartingName[], done: string) => apply({ advanced: { startingNames } }, done);
  return (
    <>
      <Panel title="Advanced" right={<span>{a.configPath}</span>}>
        <div className="note" style={{ padding: "10px 0 4px" }}>
          Wrong values here can stop the page from loading (the internet keeps working). Each change keeps a copy of the previous <code>config.json</code> next to it.
        </div>
        <Row k="Web page port" hint="Where this page is served">
          <Field label="Listen" width={120} value={a.listen} onSave={(listen) => apply({ advanced: { listen } })} />
          <Restarts />
        </Row>
        <Row k="Database">
          <Field label="Database file" width={320} value={a.db} onSave={(db) => apply({ advanced: { db } })} />
          <Restarts />
        </Row>
        <Row k="sing-box config">
          <Field label="sing-box config file" width={320} value={a.singboxConfig} onSave={(singboxConfig) => apply({ advanced: { singboxConfig } })} />
          <Restarts />
        </Row>
        <Row k="sing-box API">
          <Field label="sing-box API address" width={320} value={a.clashAPI} onSave={(clashAPI) => apply({ advanced: { clashAPI } })} />
          <Restarts />
        </Row>
        <Row k="DHCP lease file">
          <Field label="DHCP lease file" width={320} value={a.dnsmasqLeases} onSave={(dnsmasqLeases) => apply({ advanced: { dnsmasqLeases } })} />
          <Restarts />
        </Row>
        {a.legacyFailoverFile && (
          <Row k="Old failover file" hint="Still read: exits and failover were set up before they moved into the config">
            <span className="t2 num">{a.legacyFailoverFile}</span>
            <span className="note">Saving anything in VPN moves it into the config.</span>
          </Row>
        )}
      </Panel>
      <Panel title="Starting names" right={<span>{a.startingNames.length}</span>}>
        <div className="note" style={{ padding: "10px 0 4px" }}>
          Names and groups a device gets the first time it's seen. Renaming a device on its page always wins.
        </div>
        <div className="scroll-x">
          <table className="tb compact">
            <thead>
              <tr>
                <th>MAC address</th>
                <th>Name</th>
                <th>Group</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {a.startingNames.map((n) => (
                <tr key={n.mac}>
                  <td className="t2 num">{n.mac}</td>
                  <td>{n.name}</td>
                  <td className="t2">{n.group || "—"}</td>
                  <td className="rt">
                    <button type="button" className="btn sm ghost" onClick={() => setNames(a.startingNames.filter((x) => x.mac !== n.mac), "Removed.")}>
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
              <tr>
                <td>
                  <input className="inp num" style={{ width: 160 }} aria-label="MAC address" placeholder="aa:bb:cc:dd:ee:ff" value={add.mac} onChange={(e) => setAdd({ ...add, mac: e.target.value })} />
                </td>
                <td>
                  <input className="inp" style={{ width: 150 }} aria-label="Name" placeholder="Name" value={add.name} onChange={(e) => setAdd({ ...add, name: e.target.value })} />
                </td>
                <td>
                  <input className="inp" style={{ width: 120 }} aria-label="Group" placeholder="Group" value={add.group} onChange={(e) => setAdd({ ...add, group: e.target.value })} />
                </td>
                <td className="rt">
                  <button type="button" className="btn sm" disabled={!add.mac || !add.name} onClick={() => setNames([...a.startingNames, { mac: add.mac.trim().toLowerCase(), name: add.name.trim(), group: add.group.trim() }], "Added.").then(() => setAdd({ mac: "", name: "", group: "" }))}>
                    Add
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </Panel>
    </>
  );
}

export const Note = ({ children }: { children: ReactNode }) => <div className="note">{children}</div>;
