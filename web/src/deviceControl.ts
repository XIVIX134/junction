import { useSyncExternalStore } from "react";
import { api, ApiError } from "./api/client";
import type { Device } from "./api/types";
import { useLive } from "./live";

// Per-device VPN and pause. Changes apply optimistically: the device shows its new state at once,
// rolls back with the API's error text if the gateway refuses, and the overview is refreshed after.

export const NOT_ON_GATEWAY =
  "This device isn't using the gateway yet. It moves over the next time it reconnects to Wi-Fi.";

type Patch = Partial<Pick<Device, "vpn" | "paused" | "pausedUntil">>;
type State = { patches: Map<string, Patch>; busy: Set<string>; errors: Map<string, string> };
let state: State = { patches: new Map(), busy: new Set(), errors: new Map() };
const listeners = new Set<() => void>();
function update(fn: (s: State) => void) {
  const next: State = { patches: new Map(state.patches), busy: new Set(state.busy), errors: new Map(state.errors) };
  fn(next);
  state = next;
  listeners.forEach((l) => l());
}
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};
export const useDeviceControlState = () => useSyncExternalStore(subscribe, () => state);

/** Older backends don't send the control fields; their devices stay watch-only. */
export const hasControls = (d: Device) => typeof d.vpn === "boolean";

/** The device with any in-flight optimistic change applied. */
export function withPending(d: Device, s: State = state): Device {
  const p = s.patches.get(d.id);
  return p ? { ...d, ...p } : d;
}

const pick = (d: Device): Patch => ({ vpn: d.vpn, paused: d.paused, pausedUntil: d.pausedUntil });
const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

async function run(keys: string[], optimistic: Map<string, Patch>, call: () => Promise<Device[]>, refresh: () => Promise<void>, fail: string, errKey: string) {
  update((s) => {
    keys.forEach((k) => s.busy.add(k));
    s.errors.delete(errKey);
    optimistic.forEach((p, id) => s.patches.set(id, { ...s.patches.get(id), ...p }));
  });
  try {
    const confirmed = await call();
    // Hold the server's answer until the refreshed overview carries it, so nothing flickers back.
    update((s) => confirmed.forEach((d) => s.patches.set(d.id, pick(d))));
    await refresh();
    update((s) => {
      optimistic.forEach((_, id) => s.patches.delete(id));
      confirmed.forEach((d) => s.patches.delete(d.id));
    });
  } catch (e) {
    update((s) => {
      optimistic.forEach((_, id) => s.patches.delete(id)); // roll back
      s.errors.set(errKey, message(e, fail));
    });
  } finally {
    update((s) => keys.forEach((k) => s.busy.delete(k)));
  }
}

export function useDeviceActions() {
  const { refresh } = useLive();
  return {
    setVpn: (d: Device, on: boolean) =>
      run([d.id], new Map([[d.id, { vpn: on }]]), async () => [await api.setDeviceVpn(d.id, on)], refresh, "Couldn't change the VPN.", d.id),
    pause: (d: Device, minutes?: number) =>
      run(
        [d.id],
        new Map([[d.id, { paused: true, pausedUntil: minutes ? new Date(Date.now() + minutes * 60000).toISOString() : null }]]),
        async () => [await api.pauseDevice(d.id, minutes)],
        refresh,
        "Couldn't pause it.",
        d.id,
      ),
    resume: (d: Device) =>
      run([d.id], new Map([[d.id, { paused: false, pausedUntil: null }]]), async () => [await api.resumeDevice(d.id)], refresh, "Couldn't resume it.", d.id),
    setGroupVpn: (name: string, devices: Device[], on: boolean) => {
      const affected = devices.filter((d) => d.controllable);
      return run(
        [`group:${name}`, ...affected.map((d) => d.id)],
        new Map(affected.map((d) => [d.id, { vpn: on }])),
        async () => (await api.setGroupVpn(name, on)).devices,
        refresh,
        `Couldn't change the VPN for ${name}.`,
        `group:${name}`,
      );
    },
    dismiss: (key: string) => update((s) => void s.errors.delete(key)),
  };
}
