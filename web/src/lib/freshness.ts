// ─────────────────────────────────────────────────────────────────────────
// FRESHNESS — one app-wide connectivity/staleness store.
//
// The api.ts fetch wrappers call recordSuccess()/recordFailure() on every
// request, so any component can read "when did we last hear from the daemon"
// without wiring anything up. Offline is declared when the browser says so
// (navigator.onLine === false) OR after 3 consecutive API failures.
//
// requestRetry()/onRetry() is the retry bus: the OfflineBanner's Retry button
// calls retry(), and every managed poll loop (api.ts pollMs) re-fires
// immediately.
// ─────────────────────────────────────────────────────────────────────────

import { useSyncExternalStore } from "react";

interface FreshnessState {
  lastSuccessTs: number | null;
  consecutiveFailures: number;
  browserOffline: boolean;
}

const OFFLINE_FAILURE_THRESHOLD = 3;

// Immutable snapshots — a new object per change so useSyncExternalStore's
// reference equality works; the same object is returned between changes.
let state: FreshnessState = {
  lastSuccessTs: null,
  consecutiveFailures: 0,
  browserOffline: false,
};

const SERVER_SNAPSHOT: FreshnessState = state;

const listeners = new Set<() => void>();
const retryListeners = new Set<() => void>();

function emit(): void {
  for (const l of listeners) l();
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

function getSnapshot(): FreshnessState {
  return state;
}

function getServerSnapshot(): FreshnessState {
  return SERVER_SNAPSHOT;
}

// Browser online/offline events feed the store directly (module-level, once).
if (typeof window !== "undefined") {
  state = { ...state, browserOffline: window.navigator.onLine === false };
  window.addEventListener("online", () => {
    state = { ...state, browserOffline: false };
    emit();
  });
  window.addEventListener("offline", () => {
    state = { ...state, browserOffline: true };
    emit();
  });
}

/** A daemon response arrived — reset the failure streak, stamp the time. */
export function recordSuccess(): void {
  state = { ...state, lastSuccessTs: Date.now(), consecutiveFailures: 0 };
  emit();
}

/** A network error or 5xx — count toward the offline threshold. */
export function recordFailure(): void {
  state = { ...state, consecutiveFailures: state.consecutiveFailures + 1 };
  emit();
}

/** Current failure streak (drives the poll loop's exponential backoff). */
export function getConsecutiveFailures(): number {
  return state.consecutiveFailures;
}

/** Fire every onRetry subscriber (poll loops refetch immediately). */
export function requestRetry(): void {
  for (const cb of retryListeners) cb();
}

/** Subscribe to retry requests; returns the unsubscribe function. */
export function onRetry(cb: () => void): () => void {
  retryListeners.add(cb);
  return () => {
    retryListeners.delete(cb);
  };
}

export interface AppFreshness {
  /** Epoch ms of the last successful API response, or null before any. */
  lastSuccessTs: number | null;
  /** Whole seconds since the last success, or null before any. Ticks every second. */
  staleSeconds: number | null;
  /** navigator.onLine === false OR ≥3 consecutive API failures. */
  offline: boolean;
  /** Ask every poll loop to refetch right now. */
  retry: () => void;
}

// Shared 1-second clock — a second external store, so staleSeconds ticks
// without any render-time Date.now(). The interval runs only while at least
// one component is subscribed.
let clockNow = Date.now();
const clockListeners = new Set<() => void>();
let clockTimer: ReturnType<typeof setInterval> | null = null;

function subscribeClock(cb: () => void): () => void {
  clockListeners.add(cb);
  if (clockTimer == null) {
    clockNow = Date.now();
    clockTimer = setInterval(() => {
      clockNow = Date.now();
      for (const l of clockListeners) l();
    }, 1000);
  }
  return () => {
    clockListeners.delete(cb);
    if (clockListeners.size === 0 && clockTimer != null) {
      clearInterval(clockTimer);
      clockTimer = null;
    }
  };
}

function getClockSnapshot(): number {
  return clockNow;
}

function getClockServerSnapshot(): number {
  return 0; // never used: staleSeconds is null on the server (no lastSuccessTs)
}

/** App-wide freshness for banners/badges. Re-renders once per second so
 *  staleSeconds ticks, and on every store change. */
export function useAppFreshness(): AppFreshness {
  const snap = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
  const now = useSyncExternalStore(subscribeClock, getClockSnapshot, getClockServerSnapshot);
  return {
    lastSuccessTs: snap.lastSuccessTs,
    staleSeconds:
      snap.lastSuccessTs == null
        ? null
        : Math.max(0, Math.floor((now - snap.lastSuccessTs) / 1000)),
    offline: snap.browserOffline || snap.consecutiveFailures >= OFFLINE_FAILURE_THRESHOLD,
    retry: requestRetry,
  };
}
