// "Risk first" (plan step 9): whether the HAR volatility forecast leads the
// member's /today page. Decided ONLY by the verdict string the live record
// (GET /api/vol-forecast/record) publishes for the registered HEADLINE cell,
// horizon 1 -- never by re-deriving a verdict from the numbers here. The verdict
// names are the pre-registration's own (internal/volprereg DecisionRule):
//   BEATS THE NULLS                              -> pass: the block leads /today
//   NO SKILL DEMONSTRATED | ESTIMATOR ARTIFACT   -> fail: said plainly, no promotion
//   INSUFFICIENT                                 -> progress strip (days of 60)
//   ACCRUING (floor met, grader not yet ruled)   -> progress strip, no claim
// Anything else, or a malformed payload, is "unavailable": no claim either way.
// Plain TS (no JSX) so node --test can run it.

export type RiskState = "pass" | "fail" | "insufficient" | "accruing" | "unavailable";

export interface RiskHorizon {
  horizon: number;
  distinctDays: number;
  verdict: string;
}

export interface RiskHeadline {
  state: RiskState;
  /** True only on pass: the block is the page's headline. */
  lead: boolean;
  text: string;
  minDays: number;
  horizons: RiskHorizon[];
  /**
   * The registered statistic, present only when the record carried it: the
   * headline cell's mean daily QLIKE loss differential, forecast minus
   * RiskMetrics EWMA (grade.headline.meanDiff). Not the pooled vsEwma beside
   * it, which averages rows rather than days and decides nothing.
   */
  meanDiff?: number;
}

export const PASS_VERDICT = "BEATS THE NULLS";
export const FAIL_VERDICTS = ["NO SKILL DEMONSTRATED", "ESTIMATOR ARTIFACT"];
const HEADLINE_HORIZON = 1;

const num = (v: unknown): number | undefined => (typeof v === "number" && Number.isFinite(v) ? v : undefined);
const horizonName = (h: number) => (h === 1 ? "next day" : h === 5 ? "next week" : `${h} sessions`);
const signed = (v: number) => `${v > 0 ? "+" : v < 0 ? "−" : ""}${Math.abs(v).toFixed(3)}`;

const UNAVAILABLE: RiskHeadline = {
  state: "unavailable",
  lead: false,
  text: "The live record of SignalDeck’s volatility forecast is not available right now; no claim is made either way.",
  minDays: 0,
  horizons: [],
};

export function riskHeadline(record: unknown): RiskHeadline {
  if (!record || typeof record !== "object") return UNAVAILABLE;
  const r = record as { minDistinctDays?: unknown; horizons?: unknown };
  const minDays = num(r.minDistinctDays);
  if (minDays === undefined || minDays <= 0 || !Array.isArray(r.horizons)) return UNAVAILABLE;

  const horizons: RiskHorizon[] = [];
  let head: Record<string, unknown> | undefined;
  for (const raw of r.horizons) {
    if (!raw || typeof raw !== "object") return UNAVAILABLE;
    const h = raw as Record<string, unknown>;
    const horizon = num(h.horizon);
    const distinctDays = num(h.distinctDays);
    if (horizon === undefined || distinctDays === undefined || typeof h.verdict !== "string") return UNAVAILABLE;
    horizons.push({ horizon, distinctDays, verdict: h.verdict });
    if (horizon === HEADLINE_HORIZON) head = h;
  }
  if (!head) return UNAVAILABLE;
  const verdict = head.verdict as string;
  const days = num(head.distinctDays) as number;
  const base = { minDays, horizons };

  if (verdict === PASS_VERDICT) {
    // Only the horizon-1 cell is graded and only it decides, and the cell is
    // the forecast against RiskMetrics EWMA alone: the copy names exactly that.
    const grade = head.grade as { headline?: { meanDiff?: unknown } } | null | undefined;
    const meanDiff = num(grade?.headline?.meanDiff);
    const nums =
      meanDiff !== undefined
        ? `: mean daily QLIKE loss difference ${signed(meanDiff)} (negative favours the forecast)`
        : "";
    return {
      ...base,
      state: "pass",
      lead: true,
      meanDiff,
      text:
        "SignalDeck’s next-day realized volatility forecast passed its pre-registered live test against " +
        `RiskMetrics EWMA${nums} over ${days} trading days. That is a test result over those days, ` +
        "not a guarantee of future accuracy.",
    };
  }
  if (FAIL_VERDICTS.includes(verdict)) {
    return {
      ...base,
      state: "fail",
      lead: false,
      text:
        `The live test of SignalDeck’s volatility forecast did not beat its pre-registered baselines ` +
        `(verdict: ${verdict.toLowerCase()}, after ${days} trading days). It is not promoted to this page’s headline.`,
    };
  }
  if (verdict === "INSUFFICIENT" || verdict === "ACCRUING") {
    const progress = horizons
      // The true count: past the floor it reads "64 trading days (floor 60)",
      // never a clamped "60 of 60".
      .map((h) =>
        h.distinctDays >= minDays
          ? `${h.distinctDays} trading days (floor ${minDays}), awaiting its grade (${horizonName(h.horizon)})`
          : `${h.distinctDays} of ${minDays} trading days (${horizonName(h.horizon)})`,
      )
      .join("; ");
    const lead =
      verdict === "ACCRUING"
        ? "SignalDeck’s most predictable forecast, volatility, has reached its live evidence floor and awaits its pre-registered grade: "
        : "SignalDeck’s most predictable forecast, volatility, is being graded live: ";
    return {
      ...base,
      state: verdict === "ACCRUING" ? "accruing" : "insufficient",
      lead: false,
      text: `${lead}${progress}. It becomes this page’s headline if it passes; if it fails, this says so.`,
    };
  }
  return UNAVAILABLE;
}
