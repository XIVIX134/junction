import { useState } from "react";
import { api2, ApiError } from "../api/client";
import type { Session } from "../api/types";
import { BrandMark } from "../components/Brand";
import { useBrand } from "../prefs";
import { Solo } from "./Setup";

/** Shown instead of the app when Settings → Access → "Require a password" is on. */
export function Login({ onIn }: { onIn: (s: Session) => void }) {
  const b = useBrand();
  const [pw, setPw] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  async function go(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      onIn(await api2.login(pw));
    } catch (x) {
      setErr(x instanceof ApiError && x.status === 401 ? "That's not the password." : x instanceof ApiError ? x.message : "Can't reach the gateway.");
      setPw("");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Solo>
      <div style={{ width: 340, margin: "0 auto" }}>
        <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 10, marginBottom: 18 }}>
          <BrandMark b={b} size={52} />
          <div style={{ fontSize: 17, fontWeight: 700 }}>{b.name}</div>
          <div className="t3">Sign in to manage your network</div>
        </div>
        <form className="pn" onSubmit={go}>
          <div className="bd" style={{ display: "flex", flexDirection: "column", gap: 10 }}>
            <label className="t2" style={{ fontSize: 12 }} htmlFor="pw">
              Password
            </label>
            <input id="pw" className="inp" type="password" autoFocus autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} style={{ width: "100%" }} />
            {err && <div className="err">{err}</div>}
            <button type="submit" className="btn pri" style={{ justifyContent: "center" }} disabled={!pw || busy}>
              {busy ? "Signing in…" : "Sign in"}
            </button>
          </div>
        </form>
        <div className="note" style={{ textAlign: "center", marginTop: 12 }}>
          Internet keeps working whether you sign in or not.
        </div>
      </div>
    </Solo>
  );
}
