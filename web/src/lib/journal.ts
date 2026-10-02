// Member call journal (daemon plan step 8): pure helpers the /journal page
// renders, kept here so node --test can pin them (journal.test.mjs).
// The daemon never sends prices or returns for a call (licence), only its
// grade, so nothing here formats one.

export const JOURNAL_FOOTER =
  "Your calls, graded by the same rules SignalDeck grades itself with. Prices are not shown: the market data is licensed. Not financial advice.";

export const JOURNAL_IMMUTABLE =
  "Calls cannot be edited or deleted once made. You can withdraw one only before its entry session starts trading.";

/** Horizons in trading sessions, as the daemon accepts them. */
export const HORIZONS: ReadonlyArray<{ value: 1 | 5 | 21; label: string }> = [
  { value: 1, label: "1 day" },
  { value: 5, label: "1 week" },
  { value: 21, label: "1 month" },
];

export type CallDirection = "up" | "down";
export type CallStatus = "open" | "resolved" | "void" | "withdrawn";

export interface JournalCall {
  id: number;
  symbol: string;
  market: string;
  call: CallDirection;
  horizon: number;
  note: string;
  createdTs: number;
  status: CallStatus;
  outcome?: "hit" | "miss";
  /** ET date of the close that grades (or graded) the call. */
  resolveDate: string;
  canWithdraw: boolean;
}

export interface JournalStats {
  resolved: number;
  /** Distinct entry sessions among resolved calls: the independent units the
   *  interval and the minN floor count (calls made the same day share one
   *  market move). */
  callDays: number;
  hits: number;
  misses: number;
  open: number;
  void: number;
  withdrawn: number;
  /** null while withheld (fewer than minN call days). */
  hitRate: number | null;
  ciLow: number | null;
  ciHigh: number | null;
  baselineUpRate: number | null;
  withheld: boolean;
  minN: number;
}

/** One symbol the journal's picker offers (GET /api/journal/symbols). */
export interface JournalPick {
  symbol: string;
  name: string;
}

export interface Journal {
  calls: JournalCall[];
  stats: JournalStats;
  caps: { todayLeft: number; openLeft: number };
}

export function horizonLabel(h: number): string {
  return HORIZONS.find((x) => x.value === h)?.label ?? `${h} sessions`;
}

const pct = (v: number) => `${Math.round(v * 100)}%`;

/** The stats headline. Below the floor it says so and shows no rate, the
 *  platform's own withholding rule; above it, the rate with its interval and
 *  the always-up baseline over the same calls. */
export function statsHeadline(s: JournalStats): string {
  if (s.withheld || s.hitRate == null || s.ciLow == null || s.ciHigh == null) {
    return `Not enough independent call days yet (${s.callDays}/${s.minN}; ${s.resolved} resolved calls)`;
  }
  const base = s.baselineUpRate == null ? "" : `; "always up" over the same calls: ${pct(s.baselineUpRate)}`;
  return `Hit rate ${pct(s.hitRate)} (95% interval ${pct(s.ciLow)}–${pct(s.ciHigh)}, counting each of ${s.callDays} call days once) over ${s.resolved} resolved calls${base}`;
}

/** Whether the member's record clears the always-up baseline, or null when
 *  it cannot be said: withheld, or the baseline sits inside the interval. */
export function beatsDrift(s: JournalStats): boolean | null {
  if (s.withheld || s.ciLow == null || s.ciHigh == null || s.baselineUpRate == null) return null;
  if (s.ciLow > s.baselineUpRate) return true;
  if (s.ciHigh < s.baselineUpRate) return false;
  return null;
}

export function statusLabel(c: JournalCall): string {
  switch (c.status) {
    case "resolved":
      return c.outcome === "hit" ? "Hit" : "Miss";
    case "void":
      return "Void";
    case "withdrawn":
      return "Withdrawn";
    default:
      return "Open";
  }
}
