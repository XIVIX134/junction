// Vite plugin for `npm run dev:mock` / `npm run preview:mock`: serves /api from sample fixtures.
// Every response carries `X-Junction-Sample: 1`, which makes the UI label the data as sample.
//
// Extra (mock only): GET /__mock/down makes the API unreachable, GET /__mock/up brings it back,
// so the "Can't reach the gateway" state can be checked.

import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin } from "vite";
import * as fx from "./fixtures.ts";

type Next = (err?: unknown) => void;

const streams = new Set<ServerResponse>();
let down = false;

function json(res: ServerResponse, status: number, body: unknown) {
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json; charset=utf-8");
  res.setHeader("Cache-Control", "no-store");
  res.setHeader("X-Junction-Sample", "1");
  res.end(JSON.stringify(body));
}
const fail = (res: ServerResponse, status: number, error: string) => json(res, status, { error });

async function readBody(req: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  for await (const c of req) chunks.push(c as Buffer);
  const text = Buffer.concat(chunks).toString("utf8");
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return Symbol.for("bad-json");
  }
}

function stream(req: IncomingMessage, res: ServerResponse) {
  res.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache",
    Connection: "keep-alive",
    "X-Junction-Sample": "1",
  });
  const send = () => res.write(`event: overview\ndata: ${JSON.stringify(fx.overview())}\n\n`);
  send();
  const timer = setInterval(send, 2000);
  streams.add(res);
  const stop = () => {
    clearInterval(timer);
    streams.delete(res);
  };
  req.on("close", stop);
  res.on("close", stop);
}

async function handle(req: IncomingMessage, res: ServerResponse, next: Next) {
  const url = new URL(req.url ?? "/", "http://mock.local");
  const path = url.pathname;
  const method = req.method ?? "GET";

  if (path === "/__mock/down" || path === "/__mock/up") {
    down = path.endsWith("down");
    if (down) for (const s of streams) s.destroy();
    res.setHeader("Content-Type", "text/plain");
    res.end(down ? "Mock API is now DOWN (requests fail). Visit /__mock/up to restore.\n" : "Mock API is UP.\n");
    return;
  }
  if (!path.startsWith("/api/")) return next();
  if (down) {
    res.statusCode = 502;
    res.setHeader("Content-Type", "text/plain");
    res.end("Bad gateway (mock is down)");
    return;
  }

  try {
    if (path === "/api/overview" && method === "GET") return json(res, 200, fx.overview());
    if (path === "/api/stream" && method === "GET") return stream(req, res);
    if (path === "/api/devices" && method === "GET") return json(res, 200, fx.deviceList());
    if (path === "/api/exits" && method === "GET") {
      const o = fx.overview();
      return json(res, 200, { exits: o.exits, selector: o.selector });
    }
    if (path === "/api/usage" && method === "GET") {
      const n = Number(url.searchParams.get("days") ?? 30);
      return json(res, 200, fx.usage(new Date(), Number.isFinite(n) ? n : 30));
    }
    if (path === "/api/selector" && method === "PUT") {
      const body = await readBody(req);
      // A real switch takes a moment (sing-box reconnects); make the progress state visible.
      await new Promise((r) => setTimeout(r, 900));
      const r = fx.putSelector(typeof body === "symbol" ? undefined : body);
      return "error" in r ? fail(res, r.status, r.error) : json(res, 200, r);
    }
    if (path === "/api/bundle" && method === "GET") return json(res, 200, fx.bundleState());
    if ((path === "/api/bundle/renewals" || path === "/api/bundle/checks") && method === "POST") {
      const body = await readBody(req);
      const input = typeof body === "symbol" ? undefined : body;
      const r = path.endsWith("renewals") ? fx.addRenewal(input) : fx.addCheck(input);
      return typeof r === "string" ? fail(res, 400, r) : json(res, 200, r);
    }
    const ev = /^\/api\/bundle\/events\/(\d+)$/.exec(path);
    if (ev && method === "DELETE") {
      const r = fx.deleteEvent(Number(ev[1]));
      return r ? json(res, 200, r) : fail(res, 404, "No bundle entry with that id.");
    }
    // ---- v1: rules, profiles, speed, events, simulate ----
    const send = <T,>(r: T | { status: number; error: string }) =>
      r && typeof r === "object" && "error" in (r as object) && "status" in (r as object)
        ? fail(res, (r as { status: number }).status, (r as { error: string }).error)
        : json(res, 200, r);
    const bodyOf = async () => {
      const b = await readBody(req);
      return typeof b === "symbol" ? undefined : b;
    };
    if (path === "/api/events" && method === "GET") return json(res, 200, fx.eventList(url.searchParams));
    // v1.6: settings, health, setup, sign-in
    if (path === "/api/settings" && method === "GET") return json(res, 200, settings);
    if (path === "/api/settings" && method === "PUT") {
      const b = (await readBody(req)) as Record<string, Record<string, unknown>>;
      for (const k of Object.keys(b ?? {})) {
        const v = b[k];
        if (v && typeof v === "object") Object.assign((settings as unknown as Record<string, object>)[k], v);
        else (settings as unknown as Record<string, unknown>)[k] = v;
      }
      return json(res, 200, settings);
    }
    if (path === "/api/settings/exits/test" && method === "POST") {
      const d = (await readBody(req)) as { text?: string };
      const ok = /Endpoint|"server"/.test(d?.text ?? "");
      return json(res, 200, ok ? { ok: true, message: "", latencyMs: 38, server: "vpn2.example.net:51820" } : { ok: false, message: "That isn't a WireGuard config or a sing-box outbound: no server found.", latencyMs: null, server: null });
    }
    if (path === "/api/settings/exits" && method === "POST") {
      const d = (await readBody(req)) as { name: string };
      settings.vpn.exits.push({ key: d.name.toLowerCase().replace(/[^a-z0-9]+/g, "-"), name: d.name, kind: "wireguard", server: "vpn2.example.net:51820", healthy: true, latencyMs: 38 });
      return json(res, 200, settings);
    }
    if (path.startsWith("/api/settings/exits/") && method === "DELETE") {
      const k = decodeURIComponent(path.split("/").pop()!);
      settings.vpn.exits = settings.vpn.exits.filter((e) => e.key !== k);
      return json(res, 200, settings);
    }
    if (path === "/api/settings/password" && method === "POST") return (settings.access.passwordSet = true), json(res, 200, settings);
    if (path === "/api/health" && method === "GET") return json(res, 200, mockHealth());
    if (path === "/api/update" && method === "POST") return json(res, 200, mockHealth());
    if (path === "/api/setup" && method === "GET") return json(res, 200, mockSetup());
    if (path === "/api/setup" && method === "POST") return json(res, 200, settings);
    if (path === "/api/session" && method === "GET") return json(res, 200, { required: url.searchParams.has("locked"), signedIn: false });
    if (path === "/api/login" && method === "POST") return json(res, 200, { required: true, signedIn: true });
    if (path === "/api/logout" && method === "POST") return json(res, 200, { required: true, signedIn: false });
    if (path === "/api/traffic" && method === "GET") return json(res, 200, fx.traffic(url.searchParams));
    if (path === "/api/exits/history" && method === "GET") return json(res, 200, fx.exitHistory(url.searchParams));
    if (path === "/api/simulate" && method === "GET") return send(fx.simulate(url.searchParams));
    if (path === "/api/rules" && method === "GET") return json(res, 200, fx.listRules());
    if (path === "/api/rules" && method === "POST") return send(fx.createRule(await bodyOf()));
    if (path === "/api/rules/order" && method === "PUT") return send(fx.orderRules(await bodyOf()));
    const ex = /^\/api\/rules\/(\d+)\/exempt(?:\/([^/]+))?$/.exec(path);
    if (ex && method === "POST" && !ex[2]) return send(fx.exempt(Number(ex[1]), await bodyOf()));
    if (ex && method === "DELETE" && ex[2]) return send(fx.endExempt(Number(ex[1]), decodeURIComponent(ex[2])));
    const rr = /^\/api\/rules\/(\d+)$/.exec(path);
    if (rr && method === "PUT") return send(fx.updateRule(Number(rr[1]), await bodyOf()));
    if (rr && method === "DELETE") return send(fx.deleteRule(Number(rr[1])));
    const lk = /^\/api\/devices\/([^/]+)\/link$/.exec(path);
    if (lk && method === "PUT") return send(fx.linkDevice(decodeURIComponent(lk[1]), await bodyOf()));
    const ps = /^\/api\/devices\/([^/]+)\/(profile|speed)$/.exec(path);
    if (ps && method === "PUT") {
      const id = decodeURIComponent(ps[1]);
      return send(ps[2] === "profile" ? fx.setDeviceProfile(id, await bodyOf()) : fx.setDeviceSpeed(id, await bodyOf()));
    }
    const ctl = /^\/api\/devices\/([^/]+)\/(vpn|pause)$/.exec(path);
    if (ctl) {
      const id = decodeURIComponent(ctl[1]);
      const body = method === "PUT" ? await readBody(req) : undefined;
      const input = typeof body === "symbol" ? undefined : body;
      const r =
        ctl[2] === "vpn" && method === "PUT"
          ? fx.setDeviceVpn(id, input)
          : ctl[2] === "pause" && method === "PUT"
            ? fx.pauseDevice(id, input ?? {})
            : ctl[2] === "pause" && method === "DELETE"
              ? fx.resumeDevice(id)
              : { status: 405, error: "That method isn't supported here." };
      return "error" in r ? fail(res, r.status, r.error) : json(res, 200, r);
    }
    const grp = /^\/api\/groups\/([^/]+)\/vpn$/.exec(path);
    if (grp && method === "PUT") {
      const body = await readBody(req);
      const r = fx.setGroupVpn(decodeURIComponent(grp[1]), typeof body === "symbol" ? undefined : body);
      return "error" in r ? fail(res, r.status, r.error) : json(res, 200, r);
    }
    const m = /^\/api\/devices\/([^/]+)$/.exec(path);
    if (m) {
      const id = decodeURIComponent(m[1]);
      if (method === "GET") {
        const d = fx.deviceDetail(id);
        return d ? json(res, 200, d) : fail(res, 404, "No device with that id.");
      }
      if (method === "PATCH") {
        const body = await readBody(req);
        const r = fx.patchDevice(id, typeof body === "symbol" ? undefined : body);
        if (r === null) return fail(res, 404, "No device with that id.");
        return typeof r === "string" ? fail(res, 400, r) : json(res, 200, r);
      }
      return fail(res, 405, "That method isn't supported here.");
    }
    return fail(res, 404, "There is no such API endpoint.");
  } catch (e) {
    return fail(res, 500, `The mock server broke: ${(e as Error).message}`);
  }
}

export function mockApi(): Plugin {
  return {
    name: "junction-mock-api",
    configureServer(server) {
      server.middlewares.use((req, res, next) => void handle(req, res, next));
    },
    configurePreviewServer(server) {
      server.middlewares.use((req, res, next) => void handle(req, res, next));
    },
  };
}

const ago = (min: number) => new Date(Date.now() - min * 60000).toISOString();
const settings = {
  brand: { name: "Junction", color: "#4797ff", letter: "J", image: null as string | null },
  timezone: "Europe/London",
  timezoneDetected: "Europe/London",
  network: {
    gatewayIP: "192.168.50.4",
    lan: "192.168.50.0/24",
    iface: "eth0",
    ifaces: ["eth0", "wlan0"],
    detectedAt: ago(90),
    learnNamesFromDHCP: false,
    router: { url: "https://192.168.50.1", user: "admin", passwordSet: true, connected: true, devices: 14 },
    backup: { name: "Backup Pi", ip: "192.168.50.3", state: "standby" },
  },
  dhcp: {
    on: true,
    poolStart: "192.168.50.100",
    poolEnd: "192.168.50.179",
    leaseHours: 12,
    dns: ["1.1.1.1", "8.8.8.8"],
    leases: 23,
    reservations: [
      { name: "Office PC", mac: "02:4a:00:00:00:01", ip: "192.168.50.12" },
      { name: "NAS", mac: "02:4a:00:00:00:04", ip: "192.168.50.6" },
      { name: "Home server", mac: "02:4a:00:00:00:05", ip: "192.168.50.3" },
      { name: "Printer", mac: "02:4a:00:00:00:06", ip: "192.168.50.10" },
      { name: "PlayStation 5", mac: "02:4a:00:00:00:0e", ip: "192.168.50.56" },
    ],
  },
  vpn: {
    exits: [
      { key: "home", name: "Home VPN", kind: "wireguard", server: "vpn.example.net:51820", healthy: true, latencyMs: 41 },
      { key: "travel", name: "Travel VPN", kind: "hysteria2", server: "203.0.113.20:443", healthy: false, latencyMs: null },
    ],
    profiles: [
      { name: "default", checkSite: "" },
      { name: "streaming", checkSite: "www.example-tv.com" },
      { name: "gaming", checkSite: "www.example-games.com" },
    ],
    defaultProfile: "default",
    failover: { enabled: true, checkURL: "http://cp.cloudflare.com", everySec: 10, timeoutSec: 3, downAfter: 3, upAfter: 6, waitMin: 10 },
  },
  bundle: { autoRenew: { sizeGB: 250, day: 13 } as { sizeGB: number; day: number } | null },
  access: { required: false, passwordSet: false, sessionDays: 30 },
  updates: { check: true, url: "https://api.github.com/repos/XIVIX134/junction/releases/latest", lastCheck: ago(42), available: null as string | null },
  data: { usageDays: 400, sitesDays: 35, dbBytes: 18_400_000 },
  advanced: {
    configPath: "/etc/junction/config.json",
    listen: ":80",
    db: "/var/lib/junction/junction.db",
    singboxConfig: "/etc/sing-box/config.json",
    clashAPI: "http://127.0.0.1:9090",
    dnsmasqLeases: "/var/lib/misc/dnsmasq.leases",
    legacyFailoverFile: null as string | null,
    startingNames: [
      { mac: "02:4a:00:00:00:09", name: "Router", group: "Network" },
      { mac: "02:4a:00:00:00:0b", name: "Mesh point", group: "Network" },
    ],
  },
  version: "0.1.1",
};

function mockHealth() {
  return {
    checkedAt: ago(0),
    checks: [
      { key: "routing", name: "Traffic routing", status: "ok", detail: "sing-box running, 412 connections, answered in 3 ms", since: ago(3000), link: null },
      { key: "rules", name: "Routing rules", status: "ok", detail: "TPROXY rules and policy route in place", since: null, link: null },
      { key: "counting", name: "Usage counting", status: "ok", detail: "Content and on-the-wire counters running · overhead 6.2%", since: null, link: null },
      { key: "exits", name: "VPN exits", status: "warn", detail: "Travel VPN down · VPN devices use Home VPN", since: ago(1300), link: "/internet" },
      { key: "backup", name: "Backup gateway", status: "ok", detail: "This box holds 192.168.50.4 · backup on standby", since: ago(700), link: null },
      { key: "dhcp", name: "DHCP", status: "ok", detail: "dnsmasq answering · 23 leases", since: null, link: null },
      { key: "clock", name: "Clock", status: "ok", detail: "In sync (NTP) · no hardware clock, saved time is used at boot", since: null, link: null },
      { key: "storage", name: "Storage", status: "ok", detail: "Database 18 MB · disk 61% free · memory 41 MB", since: null, link: null },
    ],
    version: {
      current: "1.5.0",
      installedAt: ago(80),
      available: "1.6.0",
      history: [
        { version: "1.5.0", at: ago(80), result: "healthy", note: null },
        { version: "1.4.1", at: ago(170), result: "rolled-back", note: "rolled back after 41 s" },
        { version: "1.4.0", at: ago(250), result: "healthy", note: null },
      ],
    },
  };
}

function mockSetup() {
  return {
    needed: false,
    detected: { gatewayIP: "192.168.50.4", iface: "eth0", lan: "192.168.50.0/24", router: "192.168.50.1", routerOK: true, internetOK: true, singbox: "1.12.14", rulesOK: true, timezone: "Europe/London" },
  };
}
