// The daemon's evidence caveat on regime rows (structregime.go evidenceCaveat):
// what a historicalAccuracy IS (a backtest lookup, not a live measurement) and
// when it can first be graded. Every regime row ships it and it must be shown
// VERBATIM beside the accuracy it qualifies; a paraphrased caveat is a broken
// caveat. Until 2026-10-02 no member surface rendered it at all.

/** The distinct caveats carried by `rows`, exactly as served and in row order;
 *  rows without one contribute nothing. One per surface, not one per row: the
 *  text is the same on every row today, but if it ever differs, each version
 *  is shown. */
export function regimeCaveats(rows: ReadonlyArray<{ evidenceCaveat?: string }>): string[] {
  const out: string[] = [];
  for (const r of rows) {
    const c = r.evidenceCaveat;
    if (c && !out.includes(c)) out.push(c);
  }
  return out;
}
