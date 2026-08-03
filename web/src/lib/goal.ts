"use client";

/**
 * Goal / Onboarding / Steps state — stored in localStorage.
 *
 * Every read/write is wrapped in try/catch to survive private browsing.
 * `useSyncExternalStore` is used for hooks so the server snapshot matches
 * a brand-new visitor (no hydration mismatch). Steps are cached to avoid
 * infinite renders caused by new array references on every call.
 */

import { useSyncExternalStore } from "react";

// ─── Types ──────────────────────────────────────────────────────────────────

export type Goal = "learn" | "track" | "test";
export type StepId = "watch" | "report" | "record" | "backtest";

export interface GoalDef {
  key: Goal;
  title: string;
  blurb: string;
  pro: boolean;
  home: string;
}

export interface StepDef {
  id: StepId;
  label: string;
  href: string;
}

// ─── Keys & Events ─────────────────────────────────────────────────────────

export const GOAL_KEY = "sd-goal";
export const GOAL_EVENT = "sd-goal";
export const ONBOARDED_KEY = "sd-onboarded";
export const STEPS_KEY = "sd-steps";
export const STEPS_EVENT = "sd-steps";
export const VIEW_MODE_KEY = "sd-view-mode";
export const VIEW_MODE_EVENT = "sd-view-mode";

// ─── Static Data ───────────────────────────────────────────────────────────

export const GOALS: readonly GoalDef[] = [
  {
    key: "learn",
    title: "I'm learning",
    blurb: "Plain English everywhere, and the advanced research tools stay tucked away until you want them.",
    pro: false,
    home: "/",
  },
  {
    key: "track",
    title: "I'm tracking a few names",
    blurb: "Your watchlist up front, with alerts when something actually changes.",
    pro: false,
    home: "/watchlist",
  },
  {
    key: "test",
    title: "I'm testing strategies",
    blurb: "Full terminal: raw metrics first, every research and validation surface visible.",
    pro: true,
    home: "/lab/backtest",
  },
];

export const STEPS: readonly StepDef[] = [
  { id: "watch", label: "Add a symbol to your watchlist", href: "/welcome" },
  { id: "report", label: "Open a company report", href: "/market/overview" },
  { id: "record", label: "See how accurate we've been", href: "/lab/track-record" },
  { id: "backtest", label: "Run your first backtest", href: "/lab/backtest" },
];

// ─── Internal Helpers ───────────────────────────────────────────────────────

const VALID_GOALS: readonly Goal[] = GOALS.map((g) => g.key);
const VALID_STEPS: readonly StepId[] = STEPS.map((s) => s.id);

// Empty array shared by the server snapshot and every empty read. It must be
// ONE reference: useSyncExternalStore compares with Object.is and a fresh []
// each call is an infinite render loop. Typed mutable so all three snapshots
// agree on a type; frozen at runtime so nothing can actually push to it.
export const EMPTY: StepId[] = [];
Object.freeze(EMPTY);

// Cache for steps — avoids new reference every call, which would loop useSyncExternalStore.
let _cachedStepsRaw: string | null = null;
let _cachedSteps: StepId[] = [];

// ─── Core Read/Write Functions ──────────────────────────────────────────────

export function readGoal(): Goal | null {
  try {
    const v = localStorage.getItem(GOAL_KEY);
    return VALID_GOALS.includes(v as Goal) ? (v as Goal) : null;
  } catch {
    return null;
  }
}

export function applyGoal(g: Goal): void {
  if (typeof window === "undefined") return;
  try {
    const def = GOALS.find((d) => d.key === g);
    if (!def) return;
    const viewMode = def.pro ? "pro" : "simple";
    localStorage.setItem(GOAL_KEY, g);
    localStorage.setItem(VIEW_MODE_KEY, viewMode);
    document.documentElement.setAttribute("data-view-mode", viewMode);
    window.dispatchEvent(new Event(GOAL_EVENT));
    window.dispatchEvent(new Event(VIEW_MODE_EVENT));
  } catch {
    // Silent fail — change still holds for this render.
  }
}

export function readOnboarded(): boolean {
  try {
    return localStorage.getItem(ONBOARDED_KEY) !== null;
  } catch {
    // If storage is blocked, treat as already onboarded so user is not trapped.
    return true;
  }
}

export function markOnboarded(): void {
  try {
    localStorage.setItem(ONBOARDED_KEY, "1");
    window.dispatchEvent(new Event(GOAL_EVENT));
  } catch {
    // Silent fail.
  }
}

export function readSteps(): StepId[] {
  try {
    const raw = localStorage.getItem(STEPS_KEY);
    if (raw === _cachedStepsRaw) return _cachedSteps;

    const parsed: unknown = JSON.parse(raw ?? "[]");
    const arr = Array.isArray(parsed) ? parsed : [];

    const valid = arr.filter((x): x is StepId => VALID_STEPS.includes(x as StepId));

    _cachedStepsRaw = raw;
    _cachedSteps = valid;
    return _cachedSteps;
  } catch {
    _cachedStepsRaw = null;
    _cachedSteps = [];
    return _cachedSteps;
  }
}

export function noteVisit(pathname: string): void {
  let milestone: StepId | undefined;

  if (pathname.startsWith("/s/") || pathname.startsWith("/signals/report/")) {
    milestone = "report";
  } else if (
    pathname === "/lab/track-record" ||
    pathname === "/proof" ||
    pathname === "/accuracy"
  ) {
    milestone = "record";
  } else if (
    pathname === "/lab/backtest" ||
    pathname === "/lab/signal-backtest"
  ) {
    milestone = "backtest";
  }

  if (milestone === undefined) return;

  try {
    const steps = readSteps();
    if (steps.includes(milestone)) return;
    const next = [...steps, milestone];
    localStorage.setItem(STEPS_KEY, JSON.stringify(next));
    // Update cache so the next readSteps returns the same array reference.
    _cachedStepsRaw = localStorage.getItem(STEPS_KEY);
    _cachedSteps = next;
    window.dispatchEvent(new Event(STEPS_EVENT));
  } catch {
    // Silent fail.
  }
}

// ─── Subscriptions ─────────────────────────────────────────────────────────

function subscribe(cb: () => void, eventName: string): () => void {
  const handler = () => cb();
  window.addEventListener(eventName, handler);
  window.addEventListener("storage", handler);
  return () => {
    window.removeEventListener(eventName, handler);
    window.removeEventListener("storage", handler);
  };
}

export function subscribeGoal(cb: () => void): () => void {
  return subscribe(cb, GOAL_EVENT);
}

export function subscribeSteps(cb: () => void): () => void {
  return subscribe(cb, STEPS_EVENT);
}

// ─── React Hooks ───────────────────────────────────────────────────────────

// Server snapshots are the values a brand‑new visitor would see.
// `useOnboarded` returns true on the server so the onboarding UI never flashes.
// `useSteps` returns the frozen EMPTY array — same reference on server & client when empty.
export function useGoal(): Goal | null {
  return useSyncExternalStore(subscribeGoal, readGoal, () => null);
}

export function useOnboarded(): boolean {
  return useSyncExternalStore(subscribeGoal, readOnboarded, () => true);
}

export function useSteps(): StepId[] {
  return useSyncExternalStore(subscribeSteps, readSteps, () => EMPTY);
}