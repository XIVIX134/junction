// Shapes from junction/API.md (v0, watch-only). Sizes are bytes, rates bytes/s, times RFC 3339 UTC.

/** A VPN profile: a sing-box selector named vpn-<profile> (Settings → VPN & profiles lists them). */
export type Profile = string;

export type RuleMatch = { all?: boolean; devices?: string[]; groups?: string[]; newDevices?: boolean };
export type RuleSchedule = { days: number[]; start: string; end: string }; // 0=Sun; "HH:MM" gateway time; end < start = past midnight
export type RuleUsage = { period: "day" | "week" | "month"; overGB: number };
export type RuleWhen = { schedule?: RuleSchedule | null; usage?: RuleUsage | null };
export type RuleThen = { pause?: boolean; speedMbps?: number; vpn?: boolean; profile?: Profile };
export type Rule = {
  id: number;
  name: string;
  enabled: boolean;
  order: number;
  match: RuleMatch;
  when: RuleWhen;
  then: RuleThen;
  graph?: unknown;
};
export type RuleDraft = Omit<Rule, "id" | "order"> & { id?: number; order?: number };

export type Effective = {
  vpn: boolean;
  profile: Profile;
  exit: string;
  paused: boolean;
  pausedBy: "manual" | "rule" | null;
  speedLimitMbps: number | null;
  rules: { id: number; name: string; why: string }[];
  exemptions: { ruleId: number; until: string }[];
};

export type EventKind = "exit" | "gateway" | "device" | "rule" | "control" | "limit" | "dhcp";
export type JEvent = { id: number; at: string; kind: EventKind; deviceId: string | null; ruleId: number | null; text: string };

export type SimulateResult = { effective: Effective; matched: { id: number; name: string; why: string }[] };

export type DeviceLink = { id: string; ip: string; mac: string; name: string; online: boolean };

export type TrafficHours = { hours: { hour: string; down: number; up: number; billed?: number /* whole network: what the ISP bills */ }[] };
export type ExitHistory = { samples: { at: string; exits: Record<string, number | null | undefined> }[] };

export type NameSource = "user" | "router" | "dhcp" | "mdns" | "upnp" | "vendor" | "ip";

export type Device = {
  id: string;
  short: string;
  ip: string;
  mac: string | null;
  name: string;
  nameSource: NameSource;
  autoName: string | null;
  model: string | null;
  group: string | null;
  vendor: string | null;
  randomMac: boolean;
  online: boolean;
  usesGateway: boolean;
  // Per-device controls (optional: older backends don't send them, and their devices stay watch-only).
  vpn?: boolean; // follows the VPN setting (selector); false = always direct
  paused?: boolean; // internet blocked
  pausedUntil?: string | null; // null while paused = until resumed
  controllable?: boolean; // the gateway carries its traffic, so vpn/pause take effect
  // v1 rules engine (optional: older backends don't send them)
  profile?: Profile | null; // manual choice; null = follow the global profile
  speedLimitMbps?: number | null; // manual cap
  monthDown?: number;
  monthUp?: number;
  weekDown?: number;
  weekUp?: number;
  effective?: Effective;
  // v1.1: extra connections folded into this device (optional: older backends don't send it)
  links?: DeviceLink[];
  firstSeen: string;
  lastSeen: string;
  rateDown: number;
  rateUp: number;
  todayDown: number;
  todayUp: number;
  exit: string | null;
  connections: number;
};

export type ExitHealth = {
  key: string;
  name: string;
  healthy: boolean;
  probed: boolean;
  latencyMs: number | null;
  lastSuccess: string | null;
  lastFailure: string | null;
  lastError: string | null;
  bytesToday: number;
  // Optional so older backends (without per-profile health) still type-check at runtime.
  profiles?: ExitProfile[];
  nextCheck?: string | null; // when a down exit is next checked (backs off up to 5 min)
};

export type ExitProfile = { profile: string; known: boolean; healthy: boolean; latencyMs: number | null; nextCheck: string };

// Floating gateway 192.168.1.4. master = this container carries traffic (VPN works);
// backup/fault = the backup box covers (internet direct, no VPN).
export type GatewayEvent = { at: string; state: "MASTER" | "BACKUP" | "FAULT" | "STOP" };
export type GatewayStatus = {
  enabled: boolean;
  role: "master" | "backup" | "fault" | "stopped";
  since: string | null;
  events: GatewayEvent[]; // newest first, up to 20
};

export type SelectorMode = "auto" | "manual" | "direct";
export type SelectorControl = { mode: string; profile: string; manualEndpoint: string };

export type Selector = {
  selected: string; // sing-box outbound in use
  mode: string; // "auto" | "manual" | "direct"
  lastSwitch: string | null;
  reason: string | null;
  control?: SelectorControl | null; // non-null = Junction can change it
};

export type SelectorBody = { mode: SelectorMode; endpoint?: string; profile?: string };

// Bundles vary in size and renewal date: no fixed cycle. The latest renewal or "set left" correction is the anchor.
export type Bundle = {
  renewedAt: string;
  size: number;
  expiresAt: string | null;
  anchor: { kind: "renewal" | "check"; at: string; amount: number };
  usedSinceRenewal: number;
  tunnel: number;
  direct: number;
  left: number; // can go negative
  pacePerDay: number; // 0 until there are >= 6 h of data
  overheadPct: number; // packet overhead added to content for hours not counted on the wire
  runsOut: string | null;
  // How your ISP's count compares to Junction's, from the figures you logged (null until known).
  calibration?: { factor: number; isp: number; junction: number; intervals: number; since: string } | null;
};

export type BundleEvent = {
  id: number;
  kind: "renewal" | "check";
  at: string;
  amount: number;
  expiresAt: string | null;
  expected?: number; // checks: what Junction said was left at that moment
};
export type BundleState = { current: Bundle | null; events: BundleEvent[] };
export type RenewalBody = { gb: number; at?: string; expiresAt?: string | null };
export type CheckBody = { gbLeft: number; at?: string };

export type Overview = {
  brand?: { name: string; color: string; letter: string; image: null }; // the image only comes with /api/settings
  gatewayIP?: string;
  timezone?: string;
  backupName?: string;
  speedUnit?: "bits" | "bytes"; // how every screen shows speeds (Settings → General)
  timeFormat?: "24h" | "12h"; // how every screen shows times
  now: string;
  live: { down: number; up: number };
  top: { deviceId: string; name: string; rateDown: number } | null;
  selector: Selector;
  exits: ExitHealth[];
  direct: { latencyMs: number | null; bytesToday: number };
  bundle: Bundle | null;
  gateway?: GatewayStatus | null; // optional: older backends don't send it
  counts: { online: number; viaGateway: number; waiting: number };
  devices: Device[];
};

export type HourUsage = { hour: string; down: number; up: number; byExit: Record<string, number> };
export type DayUsage = { day: string; down: number; up: number };

export type DeviceDetail = Device & {
  hours: HourUsage[];
  days: DayUsage[];
  topHosts: { host: string; bytes: number }[];
};

export type UsageDay = { day: string; tunnel: number; direct: number; byExit: Record<string, number>; billed: number /* whole packets, what the ISP bills */ };

export type DevicePatch = { name?: string; group?: string | null };

// ---- v1.6: settings, health, setup, login ----

export type Brand = { name: string; color: string; letter: string; image: string | null /* data: URL */ };
// Settings is the whole gateway configuration (config.json plus what the app
// stores itself). Saving a value that's only read at start restarts Junction
// (the response says so); traffic never depends on Junction, so that's safe.
export type ExitInfo = {
  key: string;
  name: string;
  kind: "wireguard" | "hysteria2" | "vless" | "trojan" | "shadowsocks" | "other";
  server: string; // host:port, for display
  healthy: boolean;
  latencyMs: number | null;
};
export type Reservation = { mac: string; ip: string; name: string };
export type StartingName = { mac: string; name: string; group: string };
export type Settings = {
  brand: Brand;
  timezone: string;
  timezoneDetected: string;
  speedUnit: "bits" | "bytes";
  timeFormat: "24h" | "12h";
  network: {
    gatewayIP: string;
    lan: string;
    iface: string;
    ifaces: string[]; // network cards on this box
    detectedAt: string | null;
    learnNamesFromDHCP: boolean;
    router: { url: string; user: string; passwordSet: boolean; connected: boolean; devices: number } | null;
    backup: { name: string; ip: string; state: "standby" | "covering" | "unknown" } | null;
  };
  dhcp: {
    on: boolean; // dnsmasq hands out addresses
    poolStart: string;
    poolEnd: string;
    leaseHours: number;
    dns: string[];
    leases: number; // active now
    reservations: Reservation[];
  } | null; // null: dnsmasq isn't installed (the setup script adds it)
  vpn: {
    exits: ExitInfo[];
    profiles: { name: string; checkSite: string /* site name (TLS SNI) its exit connections use; "" = the exit's own */ }[];
    defaultProfile: string;
    failover: {
      enabled: boolean;
      checkURL: string;
      everySec: number;
      timeoutSec: number;
      downAfter: number; // failed checks in a row
      upAfter: number; // good checks in a row
      waitMin: number; // before trusting a recovered exit again
    };
  };
  bundle: { autoRenew: { sizeGB: number; day: number } | null };
  access: { required: boolean; passwordSet: boolean; sessionDays: number /* 0 = until sign-out */ };
  updates: { check: boolean; url: string; lastCheck: string | null; available: string | null; error: string | null /* the last check failed */ };
  data: { usageDays: number; sitesDays: number; dbBytes: number };
  advanced: {
    configPath: string;
    listen: string;
    db: string;
    singboxConfig: string;
    clashAPI: string;
    dnsmasqLeases: string;
    legacyFailoverFile: string | null; // still read when the failover section is absent
    startingNames: StartingName[];
  };
  version: string;
};
export type SettingsPatch = {
  brand?: Partial<Brand>;
  timezone?: string;
  speedUnit?: "bits" | "bytes";
  timeFormat?: "24h" | "12h";
  network?: Partial<{ gatewayIP: string; lan: string; iface: string; learnNamesFromDHCP: boolean; backupName: string; backupIP: string }> & {
    router?: { url: string; user?: string; password?: string } | null;
  };
  dhcp?: Partial<{ poolStart: string; poolEnd: string; leaseHours: number; dns: string[]; reservations: Reservation[] }>;
  vpn?: {
    defaultProfile?: string;
    profiles?: { name: string; checkSite: string }[];
    failover?: Partial<Settings["vpn"]["failover"]>;
    renameExit?: { key: string; name: string };
  };
  bundle?: { autoRenew: { sizeGB: number; day: number } | null };
  access?: { required?: boolean; sessionDays?: number };
  updates?: { check?: boolean; url?: string };
  data?: { usageDays?: number; sitesDays?: number };
  advanced?: Partial<Omit<Settings["advanced"], "configPath" | "legacyFailoverFile">>;
};
/** PUT /api/settings answers with the new settings; restarting = Junction restarts to apply them. */
export type SettingsSaved = Settings & { restarting?: boolean };
export type ExitDraft = { name: string; kind: "wireguard" | "outbound"; text: string /* a WireGuard .conf, or sing-box outbound JSON */ };
export type ExitTest = { ok: boolean; message: string; latencyMs: number | null; server: string | null };

export type HealthCheck = { key: string; name: string; status: "ok" | "warn" | "bad" | "off"; detail: string; since: string | null; link: string | null };
export type HealthUpdate = { version: string; at: string; result: "healthy" | "rolled-back" | "pending"; note: string | null };
export type Health = {
  checkedAt: string;
  checks: HealthCheck[];
  version: { current: string; installedAt: string | null; available: string | null; history: HealthUpdate[] };
};

export type SetupInfo = {
  needed: boolean;
  detected: { gatewayIP: string; iface: string; lan: string; router: string; routerOK: boolean; internetOK: boolean; singbox: string | null; rulesOK: boolean; timezone: string };
};
export type SetupBody = {
  gatewayIP: string;
  lan: string;
  timezone: string;
  brand?: Partial<Brand>;
  exits: { name: string; outbound: unknown }[]; // sing-box outbound JSON, one per VPN exit
  devicesOnVPN: string[]; // device ids
  dhcp: boolean;
};

export type Session = { required: boolean; signedIn: boolean };

// ---- v1.9: what the network is used for (categories), daily usage per device ----

export type CategoryUsage = {
  key: "video" | "social" | "messaging" | "ai" | "gaming" | "music" | "updates" | "cloud" | "speedtest" | "web" | "unnamed";
  name: string;
  bytes: number;
  prev: number; // the same-length period before
  sites: { host: string; bytes: number }[]; // top 6
  devices: { id: string; name: string; bytes: number }[]; // top 5
};
export type CategoriesView = { from: string; to: string; days: number; since: string /* first day with site history; "" = none */; total: number; prevTotal: number; categories: CategoryUsage[] };
export type DevicesDaily = { days: string[]; devices: Record<string, number[]> };
