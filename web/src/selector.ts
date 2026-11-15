import { useSyncExternalStore } from "react";
import { api, ApiError } from "./api/client";
import type { Overview, SelectorBody } from "./api/types";
import { endpointName, endpointOf, profileOf } from "./model";

// Global VPN selector changes (PUT /api/selector). One request at a time, shared by every control
// (Control header, now bar, Exits page) so all of them disable together and show the same progress/error.

type State = { busy: string | null; error: string | null };
let state: State = { busy: null, error: null };
const listeners = new Set<() => void>();
const set = (p: Partial<State>) => {
  state = { ...state, ...p };
  listeners.forEach((l) => l());
};
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};

export function useSelectorState() {
  return useSyncExternalStore(subscribe, () => state);
}
export const dismissSelectorError = () => set({ error: null });

/** `label` describes the change in progress, e.g. "Switching to Direct". */
export async function changeSelector(body: SelectorBody, label: string, refresh: () => Promise<void>) {
  if (state.busy) return;
  set({ busy: label, error: null });
  try {
    await api.putSelector(body);
    await refresh();
    set({ busy: null });
  } catch (e) {
    set({ busy: null, error: e instanceof ApiError ? e.message : "The gateway didn't accept the change." });
  }
}

export const DROPS_NOTE = "Switching briefly drops open connections; they reconnect on their own.";
export const DOWN_REASON = "Down, checked every few minutes";

/** "Automatic: Home VPN (gaming)", "Manual: Work VPN (default)", "Direct: through the ISP". */
export function activeLine(o: Overview): string {
  const mode = o.selector.control?.mode ?? o.selector.mode;
  const sel = o.selector.selected;
  const ep = endpointOf(sel);
  const where = ep === "direct" ? "through the ISP" : `${endpointName(ep, o.exits)}${profileOf(sel) ? ` (${profileOf(sel)})` : ""}`;
  const m = mode === "auto" ? "Automatic" : mode === "manual" ? "Manual" : mode === "direct" ? "Direct" : mode;
  return `${m}: ${where}`;
}

/** Body for a profile change that keeps the current mode (and the manual exit, if manual). */
export function profileBody(o: Overview, profile: string): SelectorBody {
  const c = o.selector.control;
  const mode = (c?.mode === "manual" || c?.mode === "direct" ? c.mode : "auto") as SelectorBody["mode"];
  return mode === "manual" ? { mode, endpoint: c?.manualEndpoint, profile } : { mode, profile };
}
