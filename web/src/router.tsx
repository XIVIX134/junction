import { useSyncExternalStore, type AnchorHTMLAttributes, type MouseEvent } from "react";

// A tiny History API router: a handful of screens don't need a routing library.

const listeners = new Set<() => void>();
const notify = () => listeners.forEach((l) => l());
if (typeof window !== "undefined") window.addEventListener("popstate", notify);

function subscribe(l: () => void) {
  listeners.add(l);
  return () => void listeners.delete(l);
}
const snapshot = () => window.location.pathname + window.location.search + window.location.hash;

export function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (to === snapshot()) return;
  if (opts.replace) history.replaceState(null, "", to);
  else history.pushState(null, "", to);
  notify();
}

export function useLocation() {
  const full = useSyncExternalStore(subscribe, snapshot);
  const url = new URL(full, window.location.origin);
  return { path: url.pathname.replace(/\/+$/, "") || "/", query: url.searchParams };
}

export const deviceHref = (id: string) => `/clients/${encodeURIComponent(id)}`;

export function Link({ href, onClick, ...rest }: AnchorHTMLAttributes<HTMLAnchorElement> & { href: string }) {
  function handle(e: MouseEvent<HTMLAnchorElement>) {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (rest.target && rest.target !== "_self") return;
    const url = new URL(href, window.location.href);
    if (url.origin !== window.location.origin) return;
    e.preventDefault();
    // The hash is kept in the URL; screens that own the target focus it once their data is in.
    navigate(url.pathname + url.search + url.hash);
  }
  return <a href={href} onClick={handle} {...rest} />;
}
