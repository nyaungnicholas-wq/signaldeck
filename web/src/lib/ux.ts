"use client";

/**
 * UX SIGNAL STORE — the counters behind the self-grade.
 *
 * Written by UxProbe, read by /health, graded by lib/rubric.ts. Everything
 * lives in this browser: no network calls, no identifiers, no per-symbol
 * history. What is stored is the set of counters in `LiveSignals` and nothing
 * else, so the worst a user can leak to themselves is that they got stuck.
 *
 * Same storage discipline as lib/goal.ts: every access in try/catch so private
 * browsing cannot throw, and `readSignals()` returns a CACHED reference while
 * the underlying string is unchanged — a fresh object per call would spin
 * useSyncExternalStore forever.
 */

import { useSyncExternalStore } from "react";
import { EMPTY_SIGNALS, clamp, type LiveSignals } from "@/lib/rubric";

export const UX_KEY = "sd-ux";
export const UX_EVENT = "sd-ux";
export const SESSION_KEY = "sd-ux-session";

/** Every LiveSignals key whose value is a number — derived, never hand-listed. */
export type NumericSignalKey = {
  [K in keyof LiveSignals]: LiveSignals[K] extends number ? K : never;
}[keyof LiveSignals];

// ─── Cache ──────────────────────────────────────────────────────────────────

let cachedRaw: string | null = null;
let cachedValue: LiveSignals = EMPTY_SIGNALS;

/**
 * Rebuild a LiveSignals from untrusted JSON. A stored value is only taken when
 * its type matches the default's, so a corrupt or outdated payload degrades to
 * defaults instead of poisoning the score with NaN or undefined.
 */
function coerce(parsed: unknown): LiveSignals {
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return EMPTY_SIGNALS;
  }
  const source = parsed as Record<string, unknown>;
  const out: Record<string, unknown> = { ...EMPTY_SIGNALS };
  for (const [key, fallback] of Object.entries(EMPTY_SIGNALS)) {
    const value = source[key];
    if (typeof value !== typeof fallback) continue;
    if (typeof value === "number" && !Number.isFinite(value)) continue;
    out[key] = value;
  }
  return out as unknown as LiveSignals;
}

export function readSignals(): LiveSignals {
  try {
    const raw = localStorage.getItem(UX_KEY);
    if (raw === cachedRaw) return cachedValue;
    cachedRaw = raw;
    cachedValue = raw === null ? EMPTY_SIGNALS : coerce(JSON.parse(raw));
    return cachedValue;
  } catch {
    cachedRaw = null;
    cachedValue = EMPTY_SIGNALS;
    return cachedValue;
  }
}

function write(next: LiveSignals): void {
  if (typeof window === "undefined") return;
  try {
    const raw = JSON.stringify(next);
    localStorage.setItem(UX_KEY, raw);
    cachedRaw = raw;
    cachedValue = next;
    window.dispatchEvent(new Event(UX_EVENT));
  } catch {
    // Storage blocked. These counters are diagnostics — losing them is fine,
    // throwing inside a click handler is not.
  }
}

// ─── Mutations ──────────────────────────────────────────────────────────────

export function bump(field: NumericSignalKey, by = 1): void {
  if (typeof window === "undefined") return;
  if (!Number.isFinite(by)) return;
  const current = readSignals();
  write({ ...current, [field]: Math.max(0, current[field] + by) });
}

export function setGoalSet(v: boolean): void {
  if (typeof window === "undefined") return;
  const current = readSignals();
  if (current.goalSet === v) return;
  write({ ...current, goalSet: v });
}

export function noteInteraction(viaKeyboard: boolean): void {
  if (typeof window === "undefined") return;
  const current = readSignals();
  write({
    ...current,
    interactions: current.interactions + 1,
    keyboardUse: current.keyboardUse + (viaKeyboard ? 1 : 0),
  });
}

export function resetSignals(): void {
  write({ ...EMPTY_SIGNALS });
}

// Module-level guard so repeated calls within one page life are free even when
// sessionStorage is unavailable.
let sessionStarted = false;

/**
 * Count this tab's visit exactly once, however many times Shell remounts or
 * the page is reloaded.
 *
 * Returns true only for the call that actually started the session, so callers
 * can hang other once-per-session work off it. Anything counted per mount
 * instead would out-run `sessions` and produce rates above 100%.
 */
export function startSession(): boolean {
  if (typeof window === "undefined" || sessionStarted) return false;
  sessionStarted = true;
  try {
    if (sessionStorage.getItem(SESSION_KEY) !== null) return false;
    sessionStorage.setItem(SESSION_KEY, "1");
  } catch {
    // No sessionStorage: the module flag above still prevents a runaway count
    // within this page life, which is the case that actually matters.
  }
  bump("sessions");
  return true;
}

const BOUNCE_KEY = "sd-ux-bounced";

/**
 * Record that this session ended without the person doing anything — at most
 * once, however many times the tab is hidden, reloaded, or restored.
 *
 * Session-scoped like `startSession`, and for the same reason: `bounces` is
 * divided by `sessions`, so a per-mount guard would let one visit log three
 * bounces against one session and report a 300% bounce rate.
 */
export function noteBounce(): void {
  if (typeof window === "undefined") return;
  try {
    if (sessionStorage.getItem(BOUNCE_KEY) !== null) return;
    sessionStorage.setItem(BOUNCE_KEY, "1");
  } catch {
    return; // No sessionStorage means no way to deduplicate — do not guess.
  }
  bump("bounces");
}

export function exportSignals(): string {
  return JSON.stringify(readSignals(), null, 2);
}

// ─── Subscription ───────────────────────────────────────────────────────────

function subscribe(cb: () => void): () => void {
  window.addEventListener(UX_EVENT, cb);
  window.addEventListener("storage", cb); // cross-tab sync
  return () => {
    window.removeEventListener(UX_EVENT, cb);
    window.removeEventListener("storage", cb);
  };
}

export function useSignals(): LiveSignals {
  return useSyncExternalStore(subscribe, readSignals, () => EMPTY_SIGNALS);
}

// ─── Confidence ─────────────────────────────────────────────────────────────

/**
 * "How well is this going for the person in front of it?" — 0–100.
 *
 * Penalties are per-session rates, so one long session cannot look like a
 * disaster and a hundred short ones cannot hide one. With no sessions recorded
 * the answer is 100: absence of evidence is not evidence of failure, and the
 * caller decides whether a score with no data behind it is worth showing.
 */
export function confidenceScore(s: LiveSignals): number {
  if (s.sessions <= 0) return 100;

  const per = (n: number) => n / s.sessions;
  const ratio = (n: number, d: number) => (d > 0 ? n / d : 0);

  const score =
    100 -
    12 * per(s.rageClicks) -
    5 * per(s.deadClicks) -
    6 * per(s.backThrash) -
    8 * per(s.stalls) -
    4 * per(s.helpOpens) -
    25 * per(s.bounces) +
    20 * ratio(s.tasksCompleted, s.tasksStarted) +
    10 * ratio(s.promptsTaken, s.promptsShown) +
    (s.goalSet ? 5 : 0);

  return Math.round(clamp(score, 0, 100));
}

export function confidenceLabel(score: number): string {
  if (score >= 85) return "Confident";
  if (score >= 70) return "Doing fine";
  if (score >= 50) return "Some friction";
  if (score >= 30) return "Struggling";
  return "Stuck";
}
