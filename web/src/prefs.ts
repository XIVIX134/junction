import { useEffect, useState, useSyncExternalStore } from "react";
import { api2 } from "./api/client";
import type { Settings, SettingsPatch } from "./api/types";

// ---- light / dark ----
// Kept per browser (localStorage), like most consoles: someone on a phone at
// night can pick dark without changing it for everyone.

export type ThemeMode = "dark" | "light" | "system";
const THEME_KEY = "theme";
const media = () => matchMedia("(prefers-color-scheme: light)");

export const themeMode = (): ThemeMode => {
  const v = localStorage.getItem(THEME_KEY);
  return v === "light" || v === "system" ? v : "dark";
};
export const effectiveTheme = (m = themeMode()): "dark" | "light" => (m === "system" ? (media().matches ? "light" : "dark") : m);

export function applyTheme() {
  document.documentElement.dataset.theme = effectiveTheme();
}

const themeListeners = new Set<() => void>();
export function setThemeMode(m: ThemeMode) {
  localStorage.setItem(THEME_KEY, m);
  applyTheme();
  themeListeners.forEach((f) => f());
}
media().addEventListener("change", () => {
  applyTheme();
  themeListeners.forEach((f) => f());
});

export function useTheme(): [ThemeMode, "dark" | "light", (m: ThemeMode) => void] {
  const mode = useSyncExternalStore(
    (f) => (themeListeners.add(f), () => void themeListeners.delete(f)),
    themeMode,
  );
  return [mode, effectiveTheme(mode), setThemeMode];
}

// ---- settings (name, icon, ...) shared by every screen ----

let current: Settings | null = null;
let loading: Promise<void> | null = null;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((f) => f());

function load() {
  loading ??= api2
    .settings()
    .then((s) => {
      current = s;
      emit();
    })
    .catch(() => {})
    .finally(() => (loading = null));
  return loading;
}

export function useSettings() {
  const s = useSyncExternalStore(
    (f) => (listeners.add(f), () => void listeners.delete(f)),
    () => current,
  );
  useEffect(() => {
    if (!current) load();
  }, []);
  return {
    settings: s,
    reload: load,
    async save(p: SettingsPatch) {
      const saved = await api2.saveSettings(p);
      current = saved;
      emit();
      return saved;
    },
    set(next: Settings) {
      current = next;
      emit();
    },
  };
}

/** The gateway's VPN profiles, read from its sing-box config. */
export function useProfiles(): string[] {
  const { settings } = useSettings();
  return settings?.vpn.profiles.length ? settings.vpn.profiles.map((p) => p.name) : ["default"];
}

export const DEFAULT_BRAND = { name: "Junction", color: "#4797ff", letter: "J", image: null };

export function useBrand() {
  const { settings } = useSettings();
  const b = settings?.brand ?? DEFAULT_BRAND;
  useEffect(() => {
    // the browser tab icon follows the brand
    const link = document.querySelector<HTMLLinkElement>("link[rel=icon]") ?? Object.assign(document.createElement("link"), { rel: "icon" });
    link.href = b.image ?? `data:image/svg+xml,${encodeURIComponent(brandSVG(b.letter, b.color))}`;
    document.head.appendChild(link);
  }, [b.image, b.letter, b.color]);
  return b;
}

export const brandSVG = (letter: string, color: string) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="8" fill="${color}"/><text x="16" y="22" font-family="Lato,system-ui,sans-serif" font-size="17" font-weight="700" fill="#fff" text-anchor="middle">${letter.replace(/[<&"]/g, "")}</text></svg>`;

export function useNow(ms: number) {
  const [n, setN] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setN(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return n;
}
