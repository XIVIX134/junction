import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { api, ApiError, isSample, onSampleChange, STREAM_URL } from "./api/client";
import { syncSite } from "./site";
import type { Overview } from "./api/types";

// Live overview: one EventSource on /api/stream (event "overview"); if that fails or goes quiet,
// poll /api/overview every 3 s, and try the stream again after ~30 s of healthy polling.

export type LiveStatus = "connecting" | "live" | "polling" | "down";

type LiveValue = {
  overview: Overview | null;
  status: LiveStatus;
  error: string | null;
  lastOk: number | null;
  sample: boolean;
  refresh: () => Promise<void>;
};

const LiveContext = createContext<LiveValue | null>(null);

const POLL_MS = 3000;
const STREAM_SILENCE_MS = 8000; // the server sends every 2 s
const RETRY_STREAM_AFTER_POLLS = 10;

export function LiveProvider({ children }: { children: ReactNode }) {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [status, setStatus] = useState<LiveStatus>("connecting");
  const [error, setError] = useState<string | null>(null);
  const [lastOk, setLastOk] = useState<number | null>(null);
  const [sample, setSample] = useState(isSample());
  const accept = useRef<(o: Overview, via: LiveStatus) => void>(() => {});

  accept.current = (o, via) => {
    syncSite(o);
    setOverview(o);
    setStatus(via);
    setError(null);
    setLastOk(Date.now());
  };

  useEffect(() => onSampleChange(setSample), []);

  useEffect(() => {
    let stopped = false;
    let es: EventSource | null = null;
    let pollTimer: ReturnType<typeof setTimeout> | undefined;
    let watchdog: ReturnType<typeof setTimeout> | undefined;
    let goodPolls = 0;
    const canStream = typeof EventSource !== "undefined";

    const fail = (e: unknown) => {
      if (stopped) return;
      setStatus("down");
      setError(e instanceof ApiError ? e.message : "Can't reach the gateway.");
    };

    function armWatchdog() {
      clearTimeout(watchdog);
      watchdog = setTimeout(fallBack, STREAM_SILENCE_MS);
    }

    function openStream() {
      if (!canStream || stopped) return schedulePoll(0);
      es = new EventSource(STREAM_URL);
      es.addEventListener("overview", (ev) => {
        try {
          const o = JSON.parse((ev as MessageEvent<string>).data) as Overview;
          if (!stopped) accept.current(o, "live");
          armWatchdog();
        } catch {
          // ignore a malformed frame; the watchdog catches a stream that only sends junk
        }
      });
      es.onerror = () => fallBack();
      armWatchdog();
    }

    function fallBack() {
      if (es) {
        es.close();
        es = null;
      }
      clearTimeout(watchdog);
      goodPolls = 0;
      schedulePoll(0);
    }

    function schedulePoll(delay: number) {
      clearTimeout(pollTimer);
      pollTimer = setTimeout(poll, delay);
    }

    async function poll() {
      if (stopped || es) return;
      try {
        const o = await api.overview();
        if (stopped) return;
        accept.current(o, "polling");
        goodPolls++;
        if (canStream && goodPolls >= RETRY_STREAM_AFTER_POLLS) {
          goodPolls = 0;
          openStream();
          return;
        }
      } catch (e) {
        goodPolls = 0;
        fail(e);
      }
      if (!es) schedulePoll(POLL_MS);
    }

    (async () => {
      try {
        const o = await api.overview();
        if (stopped) return;
        accept.current(o, "polling");
        openStream();
      } catch (e) {
        fail(e);
        schedulePoll(POLL_MS);
      }
    })();

    return () => {
      stopped = true;
      es?.close();
      clearTimeout(pollTimer);
      clearTimeout(watchdog);
    };
  }, []);

  const refresh = useCallback(async () => {
    try {
      const o = await api.overview();
      syncSite(o);
      setOverview(o);
      setLastOk(Date.now());
    } catch {
      // the live loop reports reachability
    }
  }, []);

  const value = useMemo(
    () => ({ overview, status, error, lastOk, sample, refresh }),
    [overview, status, error, lastOk, sample, refresh],
  );
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>;
}

export function useLive(): LiveValue {
  const v = useContext(LiveContext);
  if (!v) throw new Error("useLive outside LiveProvider");
  return v;
}

/** Fetch something once per key (and optionally every `everyMs`), with loading/error state. */
export function useFetch<T>(key: string | null, load: () => Promise<T>, everyMs?: number) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const loader = useRef(load);
  loader.current = load;
  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    if (key == null) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const run = async (first: boolean) => {
      if (first) setLoading(true);
      try {
        const d = await loader.current();
        if (stopped) return;
        setData(d);
        setError(null);
      } catch (e) {
        if (stopped) return;
        setError(e instanceof ApiError ? e.message : "Something went wrong.");
      } finally {
        if (!stopped) setLoading(false);
      }
      if (everyMs && !stopped) timer = setTimeout(() => run(false), everyMs);
    };
    run(true);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [key, everyMs, tick]);

  // When the key changes, drop stale data from the previous key.
  const lastKey = useRef(key);
  if (lastKey.current !== key) {
    lastKey.current = key;
    if (data !== null) setData(null);
  }

  return { data, error, loading, reload, setData };
}
