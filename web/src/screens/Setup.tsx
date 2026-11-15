import { useEffect, useState, type ReactNode } from "react";
import { api2, ApiError } from "../api/client";
import type { SetupBody, SetupInfo } from "../api/types";
import { BrandMark } from "../components/Brand";
import { isRouter } from "../clients";
import { useLive } from "../live";
import { DEFAULT_BRAND, useSettings } from "../prefs";
import { navigate } from "../router";
import { Sw } from "../ui";

const STEPS = ["Network", "Internet & VPN", "Your devices", "Extras"];

/** First run: confirm what was detected, add VPN exits, pick devices, extras. Nothing changes until "Finish". */
export function Setup() {
  const [info, setInfo] = useState<SetupInfo | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState(false);
  const { overview } = useLive();
  const { set } = useSettings();
  const [b, setB] = useState<SetupBody | null>(null);
  const [exitText, setExitText] = useState<{ name: string; json: string }[]>([]);

  useEffect(() => {
    api2
      .setup()
      .then((i) => {
        setInfo(i);
        setB({ gatewayIP: i.detected.gatewayIP, lan: i.detected.lan, timezone: i.detected.timezone, exits: [], devicesOnVPN: [], dhcp: false, brand: { ...DEFAULT_BRAND } });
      })
      .catch((e) => setErr(e instanceof ApiError ? e.message : "Can't reach the gateway."));
  }, []);

  if (!info || !b) return <Solo>{err ? <div className="err">{err}</div> : <div className="t3">Looking at your network…</div>}</Solo>;
  const d = info.detected;
  const up = (p: Partial<SetupBody>) => setB({ ...b, ...p });
  const devices = (overview?.devices ?? []).filter((x) => !isRouter(x));

  async function finish() {
    setBusy(true);
    setErr(null);
    try {
      const exits = exitText.filter((x) => x.name.trim() && x.json.trim()).map((x) => ({ name: x.name.trim(), outbound: JSON.parse(x.json) }));
      set(await api2.finishSetup({ ...b!, exits }));
      navigate("/", { replace: true });
    } catch (e) {
      setErr(e instanceof SyntaxError ? "One of the VPN exits isn't valid JSON." : e instanceof ApiError ? e.message : "Setup didn't finish.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Solo>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 22 }}>
        <BrandMark b={{ ...DEFAULT_BRAND, ...b.brand }} size={44} />
        <div>
          <div style={{ fontSize: 18, fontWeight: 700 }}>Set up {b.brand?.name || "Junction"}</div>
          <div className="t3">About two minutes. You can change everything later in Settings.</div>
        </div>
      </div>
      <div className="steps" aria-label={`Step ${step + 1} of ${STEPS.length}`}>
        {STEPS.map((s, i) => (
          <div key={s} className={i < step ? "done" : i === step ? "cur" : ""}>
            <i>{i < step ? "✓" : i + 1}</i>
            <span>{s}</span>
          </div>
        ))}
      </div>

      {step === 0 && (
        <Card title="Is this your network?" right="Detected automatically">
          <Row k="This gateway" hint="Devices will use this address" ok>
            <input className="inp" style={{ width: 180 }} aria-label="Gateway address" value={b.gatewayIP} onChange={(e) => up({ gatewayIP: e.target.value })} />
            <span className="note">on {d.iface}</span>
          </Row>
          <Row k="Local network" ok>
            <input className="inp" style={{ width: 180 }} aria-label="Local network" value={b.lan} onChange={(e) => up({ lan: e.target.value })} />
          </Row>
          <Row k="Your router" hint="Where the internet comes from" ok={d.routerOK && d.internetOK}>
            <span className="inp" style={{ width: 180, display: "inline-flex", alignItems: "center" }}>{d.router}</span>
            <span className="note">{d.routerOK ? (d.internetOK ? "answers · internet reachable through it" : "answers, but the internet isn't reachable") : "doesn't answer"}</span>
          </Row>
          <Row k="Routing engine" hint="Installed by the setup script" ok={!!d.singbox && d.rulesOK}>
            {d.singbox ? `sing-box ${d.singbox} running` : "sing-box isn't running"} · {d.rulesOK ? "routing rules in place" : "routing rules missing: run the setup script"}
          </Row>
          <Row k="Time zone">
            <input className="inp" style={{ width: 240 }} aria-label="Time zone" value={b.timezone} onChange={(e) => up({ timezone: e.target.value })} />
          </Row>
        </Card>
      )}

      {step === 1 && (
        <Card title="Do you use a VPN?" right="Optional">
          <div className="note" style={{ padding: "10px 0" }}>
            Without one, everything goes direct and you still get usage per device, limits, schedules and pausing. Each exit is a sing-box outbound (WireGuard, Hysteria2, VLESS, Trojan…): paste its JSON.
          </div>
          {exitText.map((x, i) => (
            <div className="srow" key={i}>
              <div className="k">
                <input className="inp" style={{ width: 180 }} aria-label="Exit name" placeholder="e.g. Home VPN" value={x.name} onChange={(e) => setExitText(exitText.map((y, j) => (j === i ? { ...y, name: e.target.value } : y)))} />
              </div>
              <div className="v" style={{ alignItems: "flex-start" }}>
                <textarea className="paste" aria-label="Outbound JSON" placeholder={'{ "type": "wireguard", "server": "…", … }'} value={x.json} onChange={(e) => setExitText(exitText.map((y, j) => (j === i ? { ...y, json: e.target.value } : y)))} />
                <button type="button" className="btn sm ghost" onClick={() => setExitText(exitText.filter((_, j) => j !== i))}>
                  Remove
                </button>
              </div>
            </div>
          ))}
          <div style={{ padding: "8px 0 12px" }}>
            <button type="button" className="btn sm" onClick={() => setExitText([...exitText, { name: "", json: "" }])}>
              Add a VPN exit
            </button>
          </div>
        </Card>
      )}

      {step === 2 && (
        <Card title="Which devices should use the VPN?" right={`${devices.length} found`}>
          {!exitText.some((x) => x.json.trim()) ? (
            <div className="note" style={{ padding: "10px 0" }}>
              No VPN exit added, so every device goes direct. You'll see them all on the dashboard.
            </div>
          ) : (
            devices.map((x) => (
              <Row key={x.id} k={x.name} hint={x.ip}>
                <Sw
                  on={b.devicesOnVPN.includes(x.id)}
                  label={`VPN for ${x.name}`}
                  onChange={(on) => up({ devicesOnVPN: on ? [...b.devicesOnVPN, x.id] : b.devicesOnVPN.filter((y) => y !== x.id) })}
                />
              </Row>
            ))
          )}
        </Card>
      )}

      {step === 3 && (
        <Card title="A few extras" right="All optional">
          <Row k="Name" hint="Shown in the top bar">
            <input className="inp" style={{ width: 220 }} aria-label="Name" value={b.brand?.name ?? ""} onChange={(e) => up({ brand: { ...b.brand, name: e.target.value, letter: (e.target.value.trim()[0] ?? "J").toUpperCase() } })} />
          </Row>
          <Row k="Hand out addresses (DHCP)" hint="Turn the router's DHCP off first">
            <Sw on={b.dhcp} label="DHCP" onChange={(dhcp) => up({ dhcp })} />
          </Row>
          <div className="note" style={{ padding: "8px 0 10px" }}>
            A password for this page can be set later in Settings → Access.
          </div>
        </Card>
      )}

      {err && <div className="err" style={{ marginTop: 10 }}>{err}</div>}
      <div style={{ display: "flex", justifyContent: "space-between", marginTop: 14, alignItems: "center" }}>
        <span className="note">Nothing changes on your network until you finish.</span>
        <span style={{ display: "flex", gap: 8 }}>
          {step === 0 ? (
            <button type="button" className="btn ghost" onClick={() => navigate("/", { replace: true })}>
              Skip setup
            </button>
          ) : (
            <button type="button" className="btn ghost" onClick={() => setStep(step - 1)}>
              Back
            </button>
          )}
          {step < STEPS.length - 1 ? (
            <button type="button" className="btn pri" onClick={() => setStep(step + 1)}>
              Continue
            </button>
          ) : (
            <button type="button" className="btn pri" disabled={busy} onClick={finish}>
              {busy ? "Applying…" : "Finish"}
            </button>
          )}
        </span>
      </div>
    </Solo>
  );
}

export const Solo = ({ children }: { children: ReactNode }) => (
  <main className="solo">
    <div className="box">{children}</div>
  </main>
);

const Card = ({ title, right, children }: { title: string; right?: string; children: ReactNode }) => (
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

const Row = ({ k, hint, ok, children }: { k: string; hint?: string; ok?: boolean; children?: ReactNode }) => (
  <div className="srow">
    <div className="k">
      <b>{k}</b>
      {hint && <span>{hint}</span>}
    </div>
    <div className="v">
      {ok !== undefined && <span className={`dot ${ok ? "ok" : "warn"}`} />}
      {children}
    </div>
  </div>
);
