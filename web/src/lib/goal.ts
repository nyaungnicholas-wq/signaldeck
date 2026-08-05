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

// ─── Dashboard layout per goal ──────────────────────────────────────────────

/** The dashboard's composable blocks, in their original top-to-bottom order. */
export type BlockId = "read" | "vol" | "spotlight" | "mood" | "gauges" | "changed" | "proof";

export const ALL_BLOCKS: readonly BlockId[] = [
  "read",
  "vol",
  "spotlight",
  "mood",
  "gauges",
  "changed",
  "proof",
];

export interface DashboardLayout {
  /** Shown up front, in this order. */
  lead: readonly BlockId[];
  /** One line saying what this arrangement is for, in the reader's words. */
  note: string;
}

/**
 * What each goal actually changes about the dashboard.
 *
 * The rule is DEMOTED, NEVER DELETED — the same doctrine as ProOnly and
 * StorySection. Whatever a goal does not lead with still renders, folded into
 * one disclosure at the bottom. Nobody loses a panel by answering a question
 * about themselves, so switching goals is never destructive and never
 * something to be warned about.
 *
 * `rest` is derived, not written down: ALL_BLOCKS minus `lead`, in the
 * original order. That way adding a block to the dashboard cannot silently
 * vanish from a goal someone forgot to update — it shows up folded.
 */
export const DASHBOARD_LAYOUTS: Record<Goal, DashboardLayout> = {
  // Learning: fewest panels, plain language first, and proof early — someone
  // deciding whether to trust this needs the scoreboard before the heatmap.
  // The 305-symbol heatmap and the volatility-regime table are the two most
  // overwhelming things on the page, so neither leads.
  learn: {
    lead: ["read", "spotlight", "gauges", "proof"],
    note: "Plain-English reads first. The dense market panels are folded at the bottom.",
  },
  // Tracking names: your symbols are the point. Market-wide context comes
  // after the things you actually chose to watch.
  track: {
    lead: ["changed", "read", "spotlight"],
    note: "Your watchlist and today's changes first. Market-wide panels are folded at the bottom.",
  },
  // Testing strategies: the validated forecast leads, proof comes early, and
  // nothing is folded — this reader wants the whole surface.
  test: {
    lead: ["vol", "read", "proof", "mood", "gauges", "changed", "spotlight"],
    note: "The validated forecast and the track record lead. Nothing is folded.",
  },
};

/**
 * For a visitor who has not chosen a goal — which, on a first visit, is
 * everyone.
 *
 * This deliberately does NOT show all seven blocks. The full dashboard is
 * 2,475 words over eight screens; leading with that is the single worst first
 * impression in the product, and the person seeing it is the one least
 * equipped to handle it. An unknown reader is far more likely to be new than
 * expert, so they get the same restrained arrangement as `learn` — plus a
 * banner saying so and a one-click way to change it. Nothing is hidden: the
 * other three panels are one disclosure away.
 */
export const DEFAULT_LAYOUT: DashboardLayout = {
  lead: DASHBOARD_LAYOUTS.learn.lead,
  note: "Arranged for someone new. Tell us what you're here for and we'll bring the parts you need to the top.",
};

export function layoutFor(goal: Goal | null): DashboardLayout {
  return goal === null ? DEFAULT_LAYOUT : DASHBOARD_LAYOUTS[goal];
}

/** Blocks the layout does not lead with, in their original order. */
export function foldedBlocks(layout: DashboardLayout): BlockId[] {
  return ALL_BLOCKS.filter((b) => !layout.lead.includes(b));
}

// ─── How long a list any reader sees ────────────────────────────────────────

/**
 * How tall one row of a list is. The cap exists to bound SCREENS, not rows, so
 * a page of tall evidence cards has to stop sooner than a page of one-line
 * filings to occupy the same space.
 */
export type RowDensity = "card" | "row" | "compact";

/**
 * Rows drawn before the "show everything" control, by goal and row height.
 * `null` = uncapped.
 *
 * One table so the three long-list pages cannot drift into three different
 * ideas of "a short list". The numbers are chosen to land every page in the
 * same 3–5 screen range, which is the budget in rubric.ts.
 */
export const LIST_LIMITS: Record<Goal, Record<RowDensity, number | null>> = {
  learn: { card: 10, row: 15, compact: 25 },
  track: { card: 20, row: 25, compact: 50 },
  // The only reader the unlimited list was ever right for.
  test: { card: null, row: null, compact: null },
};

/** Unknown reader → the shortest list, same reasoning as DEFAULT_LAYOUT. */
export function listLimitFor(goal: Goal | null, density: RowDensity): number | null {
  return LIST_LIMITS[goal ?? "learn"][density];
}

// ─── Market overview layout per goal ────────────────────────────────────────

/** The market overview's composable blocks, in their original top-to-bottom order. */
export type OverviewBlockId = "movers" | "discover" | "views" | "filters" | "results";

export const ALL_OVERVIEW_BLOCKS: readonly OverviewBlockId[] = [
  "movers",
  "discover",
  "views",
  "filters",
  "results",
];

export interface OverviewLayout {
  /** Shown up front, in this order. */
  lead: readonly OverviewBlockId[];
  /**
   * Number of result rows drawn before a "show everything" control.
   * `null` means no cap — uncapped, this screener renders the entire tracked
   * universe at 15,239 words over 147 screens, the longest surface in the
   * product by an order of magnitude. Nobody scrolls 300 rows; they filter or
   * they leave. The cap is a DEFAULT and never a limit, because the control to
   * lift it sits directly under the last row.
   */
  rowLimit: number | null;
  /** One line, in the reader's words, saying what this arrangement is for. */
  note: string;
}

/**
 * What each goal changes about the market overview screener.
 *
 * The same DEMOTED, NEVER DELETED doctrine as the dashboard: blocks not in
 * `lead` are folded into a disclosure at the bottom, so no panel is lost by
 * answering a question about yourself.
 */
export const OVERVIEW_LAYOUTS: Record<Goal, OverviewLayout> = {
  // Learning: movers are a human-sized list of what actually happened, and the
  // filter grid is real machinery folded until asked for.
  learn: {
    lead: ["movers", "results"],
    rowLimit: LIST_LIMITS.learn.row,
    note: "The biggest moves first, then a short list instead of the whole market. Filters are folded at the bottom.",
  },
  // Tracking: still short, but filters lead because this reader came to narrow
  // the market to something specific.
  track: {
    lead: ["movers", "filters", "results"],
    rowLimit: LIST_LIMITS.track.row,
    note: "Movers first, then the filters, then a trimmed list. Open the full table any time.",
  },
  // Testing: the whole tape, uncapped — the only reader the unlimited table
  // was ever right for.
  test: {
    lead: ["views", "filters", "results", "discover", "movers"],
    rowLimit: null,
    note: "Saved views and filters first, then every tracked symbol. Nothing is capped.",
  },
};

/**
 * For a visitor who has not chosen a goal — which, on a first visit, is
 * everyone. Same reasoning as DEFAULT_LAYOUT above: an unknown reader is far
 * more likely to be new than expert, and 147 screens is the worst possible
 * way to meet one.
 */
export const DEFAULT_OVERVIEW_LAYOUT: OverviewLayout = {
  lead: OVERVIEW_LAYOUTS.learn.lead,
  rowLimit: OVERVIEW_LAYOUTS.learn.rowLimit,
  note: "Showing the short version. Tell us what you're here for and we'll set the length to match.",
};

export function overviewLayoutFor(goal: Goal | null): OverviewLayout {
  return goal === null ? DEFAULT_OVERVIEW_LAYOUT : OVERVIEW_LAYOUTS[goal];
}

/** Blocks the layout does not lead with, in their original order. */
export function foldedOverviewBlocks(layout: OverviewLayout): OverviewBlockId[] {
  return ALL_OVERVIEW_BLOCKS.filter((b) => !layout.lead.includes(b));
}

// ─── Insights feed layout per goal ──────────────────────────────────────────

export interface InsightsLayout {
  /**
   * Insight cards drawn before the "show everything" control. `null` = all of
   * them, which is what the feed always did: 100 cards, 12,442 words, 34
   * screens. Every card carries its own evidence panel, so length here is a
   * multiple of card count and nothing else.
   */
  cardLimit: number | null;
  /**
   * Whether the "how to read this feed" primer leads the page. It is exactly
   * what a newcomer needs and exactly what someone on their fiftieth visit
   * scrolls past, so it is a lead block for two goals and folded for the
   * third — not deleted for anyone.
   */
  showPrimer: boolean;
  note: string;
}

export const INSIGHTS_LAYOUTS: Record<Goal, InsightsLayout> = {
  learn: {
    cardLimit: LIST_LIMITS.learn.card,
    showPrimer: true,
    note: "How to read the feed first, then the ten most recent insights.",
  },
  track: {
    cardLimit: LIST_LIMITS.track.card,
    showPrimer: true,
    note: "The twenty most recent insights, with the primer kept at the top.",
  },
  test: {
    cardLimit: null,
    showPrimer: false,
    note: "The whole feed, uncapped. The primer is folded at the bottom.",
  },
};

export const DEFAULT_INSIGHTS_LAYOUT: InsightsLayout = {
  cardLimit: INSIGHTS_LAYOUTS.learn.cardLimit,
  showPrimer: INSIGHTS_LAYOUTS.learn.showPrimer,
  note: "Showing the ten most recent. Tell us what you're here for and we'll set the length to match.",
};

export function insightsLayoutFor(goal: Goal | null): InsightsLayout {
  return goal === null ? DEFAULT_INSIGHTS_LAYOUT : INSIGHTS_LAYOUTS[goal];
}

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