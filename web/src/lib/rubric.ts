/**
 * UX SELF-GRADING RUBRIC — the single source of truth for how SignalDeck
 * grades its own user experience.
 *
 * Two inputs, one score:
 *   1. PageMeasurement[] — objective facts crawled from every route by
 *      `e2e/ux-audit.spec.ts` (word counts, controls, a11y defects, …).
 *   2. LiveSignals      — anonymous confusion/success counters recorded in the
 *      browser by `src/lib/ux.ts` (rage clicks, stalls, tasks completed, …).
 *
 * Deliberately dependency-free and DOM-free so the Playwright crawler, the
 * server component at /health, and the client dashboard can all import it.
 *
 * The honesty rule this file exists to enforce: a category with no evidence
 * scores `null`, never a flattering default. `scoreSite` reports coverage so a
 * grade built on three signals can never masquerade as a grade built on three
 * thousand.
 */

// ─── Levels ─────────────────────────────────────────────────────────────────

export type LevelKey = "excellent" | "good" | "needs-improvement" | "poor" | "critical";

export interface Level {
  key: LevelKey;
  label: string;
  min: number;
  max: number;
  /** What this band means, in the words a user would use. */
  meaning: string;
  /** What the team should do while sitting in this band. */
  action: string;
  tone: "ok" | "warn" | "bad";
}

export const LEVELS: readonly Level[] = [
  {
    key: "excellent",
    label: "Excellent",
    min: 90,
    max: 100,
    meaning: "Very user-friendly, interactive, and helpful. A new person gets somewhere useful without being told how.",
    action: "Hold the line. Guard against creep: every new page must clear the same bar.",
    tone: "ok",
  },
  {
    key: "good",
    label: "Good",
    min: 75,
    max: 89,
    meaning: "Mostly useful. People succeed, but a few rough edges cost them time.",
    action: "Fix the single weakest category. Do not start new surfaces.",
    tone: "ok",
  },
  {
    key: "needs-improvement",
    label: "Needs Improvement",
    min: 60,
    max: 74,
    meaning: "Some parts are confusing or passive. People can finish, but only if they are patient.",
    action: "Freeze new features. Spend the next cycle entirely on the two weakest categories.",
    tone: "warn",
  },
  {
    key: "poor",
    label: "Poor",
    min: 40,
    max: 59,
    meaning: "People likely struggle to understand or use this. Most visitors leave without doing anything.",
    action: "Treat as a bug, not a polish item. Cut surface area before adding guidance.",
    tone: "bad",
  },
  {
    key: "critical",
    label: "Critical Failure",
    min: 0,
    max: 39,
    meaning: "Not user-friendly or helpful enough to ship to anyone who is not the author.",
    action: "Stop. Rebuild the first-run path before anything else.",
    tone: "bad",
  },
] as const;

export function levelFor(score: number): Level {
  const s = clamp(score, 0, 100);
  return LEVELS.find((l) => s >= l.min && s <= l.max) ?? LEVELS[LEVELS.length - 1];
}

// ─── Categories ─────────────────────────────────────────────────────────────

export type CategoryKey =
  | "clarity"
  | "friendliness"
  | "helpfulness"
  | "interactivity"
  | "guidance"
  | "accessibility"
  | "layout"
  | "feedback"
  | "personalization";

export interface Category {
  key: CategoryKey;
  label: string;
  /** The question this category answers, phrased as a user would ask it. */
  question: string;
  /** Share of the overall score. Must sum to 1. */
  weight: number;
}

// Weights encode a product opinion: a person who cannot tell what a page is for
// (clarity) or cannot find the next move (guidance) is stuck in a way that a
// slightly cramped layout never makes them. Accessibility is weighted like a
// correctness property, not a nice-to-have.
export const CATEGORIES: readonly Category[] = [
  { key: "clarity", label: "Clarity", question: "Can I tell what this does?", weight: 0.15 },
  { key: "friendliness", label: "User-Friendliness", question: "Can I find my way around?", weight: 0.13 },
  { key: "helpfulness", label: "Helpfulness", question: "Did it actually help me do something?", weight: 0.15 },
  { key: "interactivity", label: "Interactivity", question: "Am I doing things, or just reading?", weight: 0.11 },
  { key: "guidance", label: "Guidance", question: "Do I know what to do next?", weight: 0.14 },
  { key: "accessibility", label: "Accessibility", question: "Does it work however I browse?", weight: 0.1 },
  { key: "layout", label: "Visual / Layout", question: "Is this calm enough to read?", weight: 0.1 },
  { key: "feedback", label: "Feedback", question: "Do I know what just happened?", weight: 0.07 },
  { key: "personalization", label: "Personalization", question: "Does it fit how I work?", weight: 0.05 },
] as const;

// ─── Inputs ─────────────────────────────────────────────────────────────────

/** One crawled route. Every field is measured, never estimated. */
export interface PageMeasurement {
  route: string;
  title: string;
  /** Visible words in <main>. */
  words: number;
  /** scrollHeight / viewportHeight — "how many screens tall is this page". */
  screens: number;
  links: number;
  buttons: number;
  /** Inputs, selects, textareas. */
  inputs: number;
  /**
   * Controls that change what this page SHOWS, rather than navigating away:
   * form inputs, plus anything carrying the ARIA state attributes that exist
   * to say "this selects or toggles something" (`aria-pressed`, `aria-sort`,
   * `aria-selected`, `role=tab`, `role=switch`), plus `<summary>`.
   *
   * Counting plain `<button>` would be wrong — a button may equally be a link
   * in disguise. Requiring the state attribute means a page only gets credit
   * for a control it has actually described, which is the same thing a screen
   * reader needs. A segmented control with no `aria-pressed` scores zero here
   * and deserves to.
   */
  viewControls: number;
  /** Numeric tokens rendered in <main>. A proxy for data density. */
  numbers: number;
  /** A line on the page saying what the page is for. */
  hasPurpose: boolean;
  /** An outbound "do this next" control, not counting global nav. */
  hasNextStep: boolean;
  /** Jargon rendered with no tooltip and no glossary entry. */
  jargonUncovered: readonly string[];
  /** Buttons/links with no accessible name. */
  unnamedControls: number;
  /** Heading levels jumped (h2 → h4). */
  headingSkips: number;
  /** Interactive elements under the 40px touch-target floor. */
  smallTargets: number;
  /**
   * Elements that look clickable (cursor: pointer) but cannot be reached by
   * keyboard — not a button, link or input, and no tabindex.
   *
   * Added after the insiders table was found sorting via `onClick` on a bare
   * `<th>`: fully usable with a mouse, completely unusable without one, and
   * invisible to every other check here. Naming, heading order and target size
   * all say nothing about whether a control can be operated at all.
   */
  mouseOnlyControls: number;
  imagesNoAlt: number;
  /** Async surfaces announce updates to screen readers. */
  liveRegions: number;
  /** Page renders an empty state rather than a blank panel when data is absent. */
  hasEmptyState: boolean;
  /** Page renders a skeleton/loading affordance. */
  hasLoading: boolean;
  /** Page renders an error state. */
  hasError: boolean;
  /** Content changes with the user's goal or view mode. */
  personalized: boolean;
  /** Folded behind /advanced in SIMPLE view. */
  advanced: boolean;
  /** Set when the crawl itself failed; the page scores 0 and says why. */
  crawlError?: string;
}

/**
 * Anonymous behavioural counters. Never leaves the browser unless the user
 * exports them; no identifiers, no routes-with-symbols, no timings finer than
 * a second.
 */
export interface LiveSignals {
  sessions: number;
  /** 3+ clicks inside 700ms on the same spot — "it isn't responding". */
  rageClicks: number;
  /** Click that hit nothing interactive — "I thought that was a button". */
  deadClicks: number;
  /** A → B → A inside 20s — "that wasn't it either". */
  backThrash: number;
  /** 90s+ on one screen with zero interaction — "I don't know what to do". */
  stalls: number;
  /** Help panel, glossary, or tooltip opened — "I had to go ask". */
  helpOpens: number;
  tourSkips: number;
  tourCompletes: number;
  /** A suggested next step was rendered on screen. */
  promptsShown: number;
  /** …and clicked. */
  promptsTaken: number;
  /** Setup steps finished (watchlist, report, track record, backtest). */
  tasksCompleted: number;
  /** Setup steps started. */
  tasksStarted: number;
  /** Total meaningful interactions (click on a real control, key command). */
  interactions: number;
  /** Minutes with at least one interaction. */
  activeMinutes: number;
  /** Interactions arriving via keyboard. */
  keyboardUse: number;
  /** Sessions that ended with zero interactions. */
  bounces: number;
  goalSet: boolean;
}

export const EMPTY_SIGNALS: LiveSignals = {
  sessions: 0,
  rageClicks: 0,
  deadClicks: 0,
  backThrash: 0,
  stalls: 0,
  helpOpens: 0,
  tourSkips: 0,
  tourCompletes: 0,
  promptsShown: 0,
  promptsTaken: 0,
  tasksCompleted: 0,
  tasksStarted: 0,
  interactions: 0,
  activeMinutes: 0,
  keyboardUse: 0,
  bounces: 0,
  goalSet: false,
};

// ─── Metric thresholds ──────────────────────────────────────────────────────

/**
 * `good` scores 10, `poor` scores 0, in between interpolates. `good` may be
 * above or below `poor` — direction is inferred, so "fewer is better" and
 * "more is better" metrics share one code path.
 */
export interface Threshold {
  good: number;
  poor: number;
}

/** Per-page budgets. Numbers are defended in UX_SCORE.md, not folklore. */
export const BUDGETS = {
  /** ~400 words is a comfortable screenful of prose; 1500 is a document. */
  words: { good: 400, poor: 1500 } as Threshold,
  /** Three screens of scroll is a page; eight is a scroll trap. */
  screens: { good: 3, poor: 8 } as Threshold,
  /** Numeric tokens on screen at once before the eye gives up. */
  numbers: { good: 120, poor: 450 } as Threshold,
  /** Controls on one page: enough to act, few enough to choose. */
  controlsFloor: { good: 6, poor: 0 } as Threshold,
  controlsCeiling: { good: 60, poor: 220 } as Threshold,
  /** Ways to change what you see. A page with none is a poster. */
  viewControls: { good: 3, poor: 0 } as Threshold,
  jargon: { good: 0, poor: 8 } as Threshold,
  /**
   * Numeric tokens below which a page is prose or navigation, not a metrics
   * surface. A glossary or a door page has nothing to say two ways, so it is
   * exempt from the personalization requirement rather than failing it — see
   * `scorePage`. Above this, showing a beginner and an expert the identical
   * screen is a real gap.
   */
  metricPageFloor: 20,
  unnamedControls: { good: 0, poor: 6 } as Threshold,
  headingSkips: { good: 0, poor: 4 } as Threshold,
  smallTargets: { good: 0, poor: 12 } as Threshold,
  /** Keyboard-unreachable controls. One is a bug; a handful is a barrier. */
  mouseOnlyControls: { good: 0, poor: 5 } as Threshold,
} as const;

/** Site-wide live-signal budgets, all expressed per session. */
export const SIGNAL_BUDGETS = {
  rageClicksPerSession: { good: 0, poor: 2 } as Threshold,
  deadClicksPerSession: { good: 0.5, poor: 5 } as Threshold,
  backThrashPerSession: { good: 0.5, poor: 4 } as Threshold,
  stallsPerSession: { good: 0, poor: 3 } as Threshold,
  helpOpensPerSession: { good: 0.3, poor: 4 } as Threshold,
  interactionsPerActiveMinute: { good: 6, poor: 0.5 } as Threshold,
  taskCompletionRate: { good: 0.8, poor: 0.15 } as Threshold,
  promptTakeRate: { good: 0.4, poor: 0.02 } as Threshold,
  bounceRate: { good: 0.15, poor: 0.7 } as Threshold,
  tourCompletionRate: { good: 0.6, poor: 0.1 } as Threshold,
} as const;

// ─── Scoring primitives ─────────────────────────────────────────────────────

export function clamp(n: number, lo: number, hi: number): number {
  return n < lo ? lo : n > hi ? hi : n;
}

/** Map a raw measurement onto 0–10 against a threshold pair. */
export function scoreMetric(value: number, t: Threshold): number {
  if (!Number.isFinite(value)) return 0;
  if (t.good === t.poor) return value === t.good ? 10 : 0;
  const span = t.good - t.poor;
  return clamp(((value - t.poor) / span) * 10, 0, 10);
}

/** Score a boolean as a full-marks / no-marks metric. */
function bool(v: boolean): number {
  return v ? 10 : 0;
}

/** Average, ignoring nulls. Returns null when nothing was measurable. */
function mean(xs: readonly (number | null)[]): number | null {
  const real = xs.filter((x): x is number => x !== null && Number.isFinite(x));
  if (real.length === 0) return null;
  return real.reduce((a, b) => a + b, 0) / real.length;
}

function rate(numerator: number, denominator: number): number | null {
  return denominator > 0 ? numerator / denominator : null;
}

// ─── Page scoring ───────────────────────────────────────────────────────────

export type PageScores = Record<CategoryKey, number | null>;

export interface ScoredPage {
  measurement: PageMeasurement;
  scores: PageScores;
  /** Weighted 0–100 for this route alone. */
  overall: number;
}

export function scorePage(m: PageMeasurement): ScoredPage {
  if (m.crawlError) {
    const zeroes = Object.fromEntries(CATEGORIES.map((c) => [c.key, 0])) as PageScores;
    return { measurement: m, scores: zeroes, overall: 0 };
  }

  const controls = m.links + m.buttons + m.inputs;

  const scores: PageScores = {
    // Can I tell what this does? A purpose line, plus jargon the page never explains.
    clarity: mean([
      bool(m.hasPurpose),
      scoreMetric(m.jargonUncovered.length, BUDGETS.jargon),
      scoreMetric(m.words, BUDGETS.words),
    ]),

    // Can I find my way around? Advanced surfaces are allowed to be denser, so
    // they are judged on labelling rather than on restraint.
    friendliness: mean([
      bool(m.hasPurpose),
      scoreMetric(m.jargonUncovered.length, BUDGETS.jargon),
      m.advanced ? null : scoreMetric(m.screens, BUDGETS.screens),
    ]),

    // Did it help me do something? Something to act on, and states that hold up
    // when the data is missing.
    helpfulness: mean([
      bool(m.hasNextStep),
      bool(m.hasEmptyState),
      scoreMetric(controls, BUDGETS.controlsFloor),
    ]),

    // Am I doing things, or just reading? Something to press at all, and
    // something that changes what is on screen rather than sending you
    // elsewhere. A page of nothing but links passes the first and fails the
    // second, which is exactly the distinction worth drawing.
    interactivity: mean([
      scoreMetric(controls, BUDGETS.controlsFloor),
      scoreMetric(m.viewControls, BUDGETS.viewControls),
    ]),

    // Do I know what to do next?
    guidance: mean([bool(m.hasNextStep), bool(m.hasPurpose)]),

    accessibility: mean([
      scoreMetric(m.unnamedControls, BUDGETS.unnamedControls),
      scoreMetric(m.headingSkips, BUDGETS.headingSkips),
      scoreMetric(m.smallTargets, BUDGETS.smallTargets),
      // Can the controls be operated at all without a mouse? Everything else
      // here grades how a control is described; this grades whether it works.
      scoreMetric(m.mouseOnlyControls, BUDGETS.mouseOnlyControls),
      bool(m.imagesNoAlt === 0),
      bool(m.liveRegions > 0),
    ]),

    // Is this calm enough to read?
    layout: mean([
      scoreMetric(m.words, BUDGETS.words),
      scoreMetric(m.screens, BUDGETS.screens),
      scoreMetric(m.numbers, BUDGETS.numbers),
      scoreMetric(controls, BUDGETS.controlsCeiling),
    ]),

    // Do I know what just happened? Loading, error and empty are the three
    // moments a page has to speak for itself.
    feedback: mean([bool(m.hasLoading), bool(m.hasError), bool(m.hasEmptyState)]),

    // Only metrics surfaces are judged here. Punishing /glossary for having no
    // expert variant measures nothing — but a page dense with numbers that
    // shows a beginner exactly what it shows a quant is a real gap.
    personalization:
      m.numbers < BUDGETS.metricPageFloor ? null : bool(m.personalized),
  };

  return { measurement: m, scores, overall: weighted(scores) };
}

/** Collapse per-category 0–10 into a weighted 0–100, skipping unmeasured ones. */
export function weighted(scores: PageScores): number {
  let sum = 0;
  let weight = 0;
  for (const c of CATEGORIES) {
    const s = scores[c.key];
    if (s === null) continue;
    sum += s * c.weight;
    weight += c.weight;
  }
  return weight === 0 ? 0 : clamp((sum / weight) * 10, 0, 100);
}

// ─── Live-signal scoring ────────────────────────────────────────────────────

/**
 * Turn behaviour into the same 0–10 currency as the crawl. Returns nulls when a
 * signal has no denominator — three sessions is not evidence, and the score
 * says so rather than inventing a number.
 */
export function scoreSignals(s: LiveSignals): PageScores {
  const per = (n: number) => rate(n, s.sessions);
  const opt = (v: number | null, t: Threshold) => (v === null ? null : scoreMetric(v, t));

  const confusionish = mean([
    opt(per(s.deadClicks), SIGNAL_BUDGETS.deadClicksPerSession),
    opt(per(s.rageClicks), SIGNAL_BUDGETS.rageClicksPerSession),
  ]);

  return {
    // Needing help and stalling are what "I don't understand this" looks like.
    clarity: mean([
      opt(per(s.helpOpens), SIGNAL_BUDGETS.helpOpensPerSession),
      opt(per(s.stalls), SIGNAL_BUDGETS.stallsPerSession),
    ]),

    // Wrong clicks and bouncing between pages are what "I'm lost" looks like.
    friendliness: mean([
      confusionish,
      opt(per(s.backThrash), SIGNAL_BUDGETS.backThrashPerSession),
    ]),

    helpfulness: mean([
      opt(rate(s.tasksCompleted, s.tasksStarted), SIGNAL_BUDGETS.taskCompletionRate),
      opt(rate(s.bounces, s.sessions), SIGNAL_BUDGETS.bounceRate),
    ]),

    interactivity: mean([
      opt(rate(s.interactions, s.activeMinutes), SIGNAL_BUDGETS.interactionsPerActiveMinute),
      opt(rate(s.bounces, s.sessions), SIGNAL_BUDGETS.bounceRate),
    ]),

    // Guidance is judged by whether suggestions get taken, not whether they exist.
    guidance: mean([
      opt(rate(s.promptsTaken, s.promptsShown), SIGNAL_BUDGETS.promptTakeRate),
      opt(
        rate(s.tourCompletes, s.tourCompletes + s.tourSkips),
        SIGNAL_BUDGETS.tourCompletionRate,
      ),
    ]),

    accessibility: opt(rate(s.keyboardUse, s.interactions), { good: 0.05, poor: 0 }),

    // Layout is a property of the page, not of the visit. The crawl owns it.
    layout: null,

    feedback: confusionish,

    personalization: s.sessions > 0 ? bool(s.goalSet) : null,
  };
}

// ─── Site scoring ───────────────────────────────────────────────────────────

export interface CategoryResult {
  category: Category;
  /** 0–10, or null when neither the crawl nor behaviour could measure it. */
  score: number | null;
  fromPages: number | null;
  fromSignals: number | null;
  /** Routes dragging this category down, worst first. */
  worstRoutes: readonly string[];
}

export interface SiteScore {
  overall: number;
  level: Level;
  categories: readonly CategoryResult[];
  weakest: CategoryResult | null;
  recommendation: string;
  pages: readonly ScoredPage[];
  /** Share of categories backed by real behaviour, 0–1. */
  signalCoverage: number;
  pagesCrawled: number;
  pagesFailed: number;
  generatedAt: string;
}

/**
 * Behaviour is worth half of a category once there is enough of it. Below
 * MIN_SESSIONS the crawl carries the score alone — a single curious visitor
 * must not be able to move the grade.
 */
export const MIN_SESSIONS = 5;
const SIGNAL_WEIGHT = 0.5;

export function scoreSite(
  measurements: readonly PageMeasurement[],
  signals: LiveSignals = EMPTY_SIGNALS,
  generatedAt = "",
): SiteScore {
  const pages = measurements.map(scorePage);
  const signalScores = signals.sessions >= MIN_SESSIONS ? scoreSignals(signals) : null;

  const categories: CategoryResult[] = CATEGORIES.map((category) => {
    const fromPages = mean(pages.map((p) => p.scores[category.key]));
    const fromSignals = signalScores ? signalScores[category.key] : null;

    const score =
      fromPages !== null && fromSignals !== null
        ? fromPages * (1 - SIGNAL_WEIGHT) + fromSignals * SIGNAL_WEIGHT
        : (fromPages ?? fromSignals);

    const worstRoutes = pages
      .filter((p) => p.scores[category.key] !== null)
      .sort((a, b) => (a.scores[category.key] ?? 0) - (b.scores[category.key] ?? 0))
      .slice(0, 3)
      .map((p) => p.measurement.route);

    return { category, score, fromPages, fromSignals, worstRoutes };
  });

  const scored = Object.fromEntries(
    categories.map((c) => [c.category.key, c.score]),
  ) as PageScores;

  const measured = categories.filter((c) => c.score !== null);
  const weakest =
    measured.length === 0
      ? null
      : measured.reduce((a, b) => ((a.score ?? 10) <= (b.score ?? 10) ? a : b));

  const overall = weighted(scored);

  return {
    overall,
    level: levelFor(overall),
    categories,
    weakest,
    recommendation: weakest ? recommendFor(weakest) : "Nothing measurable yet — run the crawl.",
    pages,
    signalCoverage: categories.filter((c) => c.fromSignals !== null).length / CATEGORIES.length,
    pagesCrawled: measurements.length,
    pagesFailed: measurements.filter((m) => m.crawlError).length,
    generatedAt,
  };
}

// ─── Recommendations ────────────────────────────────────────────────────────

/**
 * The highest-impact fix per category. One fix, not a menu — the point of
 * naming a single weakest area is to make the next move obvious.
 */
export const FIXES: Record<CategoryKey, string> = {
  clarity:
    "Put one plain-English sentence at the top of every page saying what it is for, and wrap every unexplained term in a tooltip that links to the glossary.",
  friendliness:
    "Cut the route count a new person can reach from the nav. Anything they cannot act on today belongs behind Advanced.",
  helpfulness:
    "Give every page one thing to do. If a page only reports, add the action it should lead to.",
  interactivity:
    "Add a control that changes what the page shows — a filter, a range, a horizon, a symbol picker — and mark it with aria-pressed or role=tab so it is a control for everyone. Pages with nothing but links are posters.",
  guidance:
    "Show a Next best step on every page, chosen from what the person has already done.",
  accessibility:
    "Name every control, stop skipping heading levels, hold interactive targets to 40px, and make sure anything that looks clickable is a real button or link — not a div with an onClick, which no keyboard can reach.",
  layout:
    "Halve the words and the numbers on the densest pages. Move the third and fourth screenful behind a disclosure.",
  feedback:
    "Give every async panel all three states — loading, empty, error — and confirm every action that changes stored data.",
  personalization:
    "Metrics pages show a beginner exactly what they show a quant. Make the goal change what leads the page, the way the dashboard now does — demote, never delete.",
};

export function recommendFor(c: CategoryResult): string {
  const worst = c.worstRoutes.length ? ` Start with ${c.worstRoutes.join(", ")}.` : "";
  return `${FIXES[c.category.key]}${worst}`;
}

// ─── Jargon ─────────────────────────────────────────────────────────────────

/**
 * Terms that mean nothing to someone who has not read the docs. The crawler
 * flags any of these rendered without a tooltip or glossary link nearby.
 * Keep in sync with src/lib/plain.ts — anything explained there is fine to use.
 */
export const JARGON: readonly string[] = [
  "walk-forward",
  "z-score",
  "cointegration",
  "Bonferroni",
  "Sharpe",
  "drawdown",
  "regime",
  "conviction band",
  "breadth",
  "realized volatility",
  "implied vol",
  "IC",
  "quantile",
  "decile",
  "p-value",
  "hit rate",
  "calibration",
  "backtest",
  "pseudo-replication",
  "survivorship",
  "alpha",
  "beta",
  "basis",
  "skew",
  "kurtosis",
] as const;
