import { useSyncExternalStore } from "react";
import type { JEvent, Overview } from "./api/types";
import { gatewayShort } from "./clients";
import { backupLabel } from "./site";

// Bumped after any change the user makes, so event lists refresh right away instead of on their timer.
let version = 0;
const listeners = new Set<() => void>();
export function bumpEvents() {
  version++;
  listeners.forEach((l) => l());
}
export const useEventsVersion = () =>
  useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => void listeners.delete(l);
    },
    () => version,
  );

export type FeedEvent = Omit<JEvent, "id"> & { id: number | string; st?: string };

const GW_TEXT = (st: string): string =>
  ({
    MASTER: `Gateway ${gatewayShort()} is master: traffic goes through this gateway`,
    BACKUP: `Gateway ${gatewayShort()} is backup: ${backupLabel()} carries traffic, no VPN`,
    FAULT: `Gateway ${gatewayShort()} fault: health check failed`,
    STOP: `Gateway ${gatewayShort()} keepalived stopped`,
  })[st] ?? st;

/** /api/events plus the gateway's failover state changes from the overview, newest first. */
export function feed(events: JEvent[], o: Overview | null): FeedEvent[] {
  const g: FeedEvent[] = (o?.gateway?.events ?? []).map((e, i) => ({
    id: `gw${i}-${e.at}`,
    at: e.at,
    kind: "gateway",
    deviceId: null,
    ruleId: null,
    text: GW_TEXT(e.state),
    st: e.state,
  }));
  return [...events, ...g].sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
}

export const KIND_LABEL: Record<string, string> = {
  gateway: "Gateway",
  exit: "Exit",
  device: "Device",
  rule: "Rule",
  control: "Control",
  limit: "Limit",
  dhcp: "DHCP",
  isp: "WE", // events from the WE counter, removed in v1.7; old ones still show
};
