import type { Health, Session, Settings, SettingsPatch, SetupBody, SetupInfo } from "./types";
import type {
  BundleState,
  CheckBody,
  Device,
  DeviceDetail,
  DevicePatch,
  ExitHealth,
  Overview,
  RenewalBody,
  Effective,
  JEvent,
  Profile,
  Rule,
  RuleDraft,
  Selector,
  SelectorBody,
  SimulateResult,
  ExitHistory,
  TrafficHours,
  UsageDay,
} from "./types";

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

// The mock server tags every response with this header so the UI can label sample data.
// The real gateway never sends it.
const SAMPLE_HEADER = "x-junction-sample";
let sample = false;
const sampleListeners = new Set<(v: boolean) => void>();
export const isSample = () => sample;
export function onSampleChange(fn: (v: boolean) => void) {
  sampleListeners.add(fn);
  return () => void sampleListeners.delete(fn);
}
function noteSample(res: Response) {
  const v = res.headers.get(SAMPLE_HEADER) === "1";
  if (v !== sample) {
    sample = v;
    sampleListeners.forEach((fn) => fn(v));
  }
}

const TIMEOUT_MS = 8000;

async function request<T>(method: string, path: string, body?: unknown, timeoutMs = TIMEOUT_MS): Promise<T> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? { Accept: "application/json" } : { Accept: "application/json", "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: ctrl.signal,
      cache: "no-store",
    });
  } catch {
    throw new ApiError("Can't reach the gateway.", 0);
  } finally {
    clearTimeout(timer);
  }
  noteSample(res);
  const text = await res.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    // Not JSON: a proxy error page, or the SPA's index.html when the API isn't there.
  }
  if (!res.ok) {
    const msg =
      data && typeof data === "object" && "error" in data && typeof (data as { error: unknown }).error === "string"
        ? (data as { error: string }).error
        : res.status >= 500
          ? "Can't reach the gateway."
          : `The gateway answered ${res.status}.`;
    throw new ApiError(msg, res.status);
  }
  if (data === undefined) throw new ApiError("The gateway sent something that isn't JSON.", res.status);
  return data as T;
}

const enc = encodeURIComponent;

export const api = {
  overview: () => request<Overview>("GET", "/api/overview"),
  devices: () => request<Device[]>("GET", "/api/devices"),
  device: (id: string) => request<DeviceDetail>("GET", `/api/devices/${enc(id)}`),
  patchDevice: (id: string, patch: DevicePatch) => request<Device>("PATCH", `/api/devices/${enc(id)}`, patch),
  exits: () => request<{ exits: ExitHealth[]; selector: Selector }>("GET", "/api/exits"),
  usage: (days = 30) => request<UsageDay[]>("GET", `/api/usage?days=${days}`),
  putSelector: (b: SelectorBody) => request<{ exits: ExitHealth[]; selector: Selector }>("PUT", "/api/selector", b),
  setDeviceVpn: (id: string, on: boolean) => request<Device>("PUT", `/api/devices/${enc(id)}/vpn`, { on }),
  pauseDevice: (id: string, minutes?: number) =>
    request<Device>("PUT", `/api/devices/${enc(id)}/pause`, minutes ? { minutes } : {}),
  resumeDevice: (id: string) => request<Device>("DELETE", `/api/devices/${enc(id)}/pause`),
  setGroupVpn: (name: string, on: boolean) =>
    request<{ group: string; on: boolean; devices: Device[] }>("PUT", `/api/groups/${enc(name)}/vpn`, { on }),
  setDeviceProfile: (id: string, profile: Profile | null) => request<Device>("PUT", `/api/devices/${enc(id)}/profile`, { profile }),
  setDeviceSpeed: (id: string, mbps: number | null) => request<Device>("PUT", `/api/devices/${enc(id)}/speed`, { mbps }),
  linkDevice: (id: string, to: string | null) => request<{ ok: string }>("PUT", `/api/devices/${enc(id)}/link`, { to }),
  rules: () => request<Rule[]>("GET", "/api/rules"),
  createRule: (r: RuleDraft) => request<Rule>("POST", "/api/rules", r),
  updateRule: (r: Rule) => request<Rule>("PUT", `/api/rules/${r.id}`, r),
  deleteRule: (id: number) => request<{ deleted: number }>("DELETE", `/api/rules/${id}`),
  orderRules: (ids: number[]) => request<Rule[]>("PUT", "/api/rules/order", { ids }),
  exempt: (ruleId: number, deviceId: string, minutes: number) =>
    request<Effective>("POST", `/api/rules/${ruleId}/exempt`, { deviceId, minutes }),
  endExempt: (ruleId: number, deviceId: string) => request<Effective>("DELETE", `/api/rules/${ruleId}/exempt/${enc(deviceId)}`),
  simulate: (deviceId: string, at: string) =>
    request<SimulateResult>("GET", `/api/simulate?device=${enc(deviceId)}&at=${enc(at)}`),
  events: (q: { limit?: number; device?: string | null; before?: number | null } = {}) => {
    const p = new URLSearchParams({ limit: String(q.limit ?? 100) });
    if (q.device) p.set("device", q.device);
    if (q.before != null) p.set("before", String(q.before));
    return request<JEvent[]>("GET", `/api/events?${p}`);
  },
  traffic: (hours = 24) => request<TrafficHours>("GET", `/api/traffic?hours=${hours}`),
  exitHistory: (minutes = 60) => request<ExitHistory>("GET", `/api/exits/history?minutes=${minutes}`),
  bundle: () => request<BundleState>("GET", "/api/bundle"),
  renew: (b: RenewalBody) => request<BundleState>("POST", "/api/bundle/renewals", b),
  check: (b: CheckBody) => request<BundleState>("POST", "/api/bundle/checks", b),
  deleteBundleEvent: (id: number) => request<BundleState>("DELETE", `/api/bundle/events/${id}`),
};

export const STREAM_URL = "/api/stream";

// ---- v1.6: settings, health, setup, login ----
export const api2 = {
  settings: () => request<Settings>("GET", "/api/settings"),
  saveSettings: (p: SettingsPatch) => request<Settings>("PUT", "/api/settings", p),
  setPassword: (password: string) => request<Settings>("POST", "/api/settings/password", { password }),
  health: () => request<Health>("GET", "/api/health"),
  installUpdate: (version: string) => request<Health>("POST", "/api/update", { version }, 120000),
  setup: () => request<SetupInfo>("GET", "/api/setup"),
  finishSetup: (b: SetupBody) => request<Settings>("POST", "/api/setup", b, 60000),
  session: () => request<Session>("GET", "/api/session"),
  login: (password: string) => request<Session>("POST", "/api/login", { password }),
  logout: () => request<Session>("POST", "/api/logout", {}),
};
