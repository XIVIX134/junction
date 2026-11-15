import type { Overview } from "./api/types";

// What this gateway is: its address, time zone, VPN exits and backup. Comes with
// every overview; kept in localStorage so the first paint is already right.

const browserTZ = (() => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
})();

/** The gateway's time zone. All times on every screen use it, not the browser's. */
export const SITE_TZ = localStorage.getItem("tz") || browserTZ;

const exitNames = new Map<string, string>();
let exitOrder: string[] = JSON.parse(localStorage.getItem("exits") || "[]");
let backupName = localStorage.getItem("backup") || "Backup";

export function syncSite(o: Overview) {
  const ext = o;
  if (ext.timezone && ext.timezone !== SITE_TZ) {
    // Formatters are built once at load: the simplest correct thing is to reload once.
    localStorage.setItem("tz", ext.timezone);
    location.reload();
    return;
  }
  if (ext.gatewayIP) setGatewayIP(ext.gatewayIP);
  if (ext.backupName) {
    backupName = ext.backupName;
    localStorage.setItem("backup", backupName);
  }
  const keys = o.exits.map((e) => e.key);
  for (const e of o.exits) exitNames.set(e.key, e.name);
  if (keys.join() !== exitOrder.join()) {
    exitOrder = keys;
    localStorage.setItem("exits", JSON.stringify(keys));
  }
}

/** "vpn1" → its display name from the failover config; unknown keys get a capital. */
export const exitDisplayName = (key: string) => exitNames.get(key) ?? key.charAt(0).toUpperCase() + key.slice(1);
/** A stable colour per exit, by its position in the config. */
export const exitColorVar = (key: string) => {
  const i = exitOrder.indexOf(key);
  return i < 0 ? "var(--other)" : `var(--exit-${(i % 4) + 1})`;
};
export const backupLabel = () => backupName;

// ---- the gateway's address ----
const fromURL = /^\d+\.\d+\.\d+\.\d+$/.test(location.hostname) ? location.hostname : "";
let gw = localStorage.getItem("gw") || fromURL;
const gwListeners = new Set<() => void>();
function setGatewayIP(ip: string) {
  if (ip === gw) return;
  gw = ip;
  localStorage.setItem("gw", ip);
  gwListeners.forEach((f) => f());
}
/** "192.168.1.4" */
export const gatewayIP = () => gw;
/** ".4" */
export const gatewayShort = () => (gw ? `.${gw.split(".").pop()}` : "");
