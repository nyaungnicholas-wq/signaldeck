package api

// The live record is published on TWO surfaces, /api/calibration and
// /api/track-record, and they do not report the same numbers. On 2026-08-02
// they differed on every headline figure:
//
//	                   /api/calibration   /api/track-record
//	independent N     14,699             11,216
//	win rate          48.47%             50.01%
//	base rate         0.4837             0.4424
//	Brier skill       -0.0512            -0.1378
//
// Neither number is wrong. They are scoped differently: calibration grades the
// PREQUENTIAL record (every published probability frozen at prediction time and
// graded forward), while track-record grades live out-of-sample predictions
// behind an independent-observation AND distinct-day gate, so it deliberately
// sees a smaller, day-declustered sample.
// Since 2026-10-02 (CAL-N) calibration is day-declustered too: one pair per
// (symbol, settled trading day) over the grader's own population, behind the
// same two gates. What still differs is the population, as the notes say.
// The 2026-08-02 re-audit (finding C-2) recorded this as a real reporting
// defect anyway, and it is: a reader comparing the two surfaces could not
// reconcile them, because neither payload referenced the other or stated the
// scope difference. Two irreconcilable "live records" read as one of them being
// broken, which invites dismissing whichever number is less welcome.
// These notes are the fix, and they are deliberately a pair defined side by
// side: the failure mode is the two surfaces drifting apart in what they claim
// about each other, so an editor changing one sees the other on the same
// screen. No figure is altered by either note; the numbers were never the
// problem.
// Both scopes agree on the conclusion, which is the part that matters and the
// part each note ends on.
// calibrationBinMinN is the floor below which a calibration bin's MeanActual is
// not comparable evidence. 30 is not arbitrary: at n=30 the binomial standard
// error on a proportion is 0.5/sqrt(30) ≈ 9.1 percentage points, already wider
// than the miscalibration the reliability curve exists to reveal. Below it the
// bin is noise wearing the same shape as a 3,469-sample cell.
//
// It gates READING, never fitting — ensemble.MinCalibrationPairs governs
// whether the map is fit at all, and that gate is separate and holding.
const calibrationBinMinN = 30

const (
	calibrationScopeNote = "SCOPE: this is the PREQUENTIAL record - every probability was frozen at prediction time and graded forward - over the accuracy grader's own population: one observation per (symbol, settled trading day) in the graded window, after the settlement-quarantine and stale-feed exclusions, behind an independent-observation AND distinct-day gate. /api/track-record publishes the same underlying record behind the same two gates but without those two exclusions, so its independentN, winRate, baseRate and brierSkill are all legitimately DIFFERENT from these - a slightly different population, not a contradiction and not a bug. Compare verdicts, not figures: while its gate holds a scope publishes no figure at all, and neither has published a Brier skill above zero, i.e. no measured probabilistic skill on either reading."
	trackRecordScopeNote = "SCOPE: this is the live out-of-sample record behind both gates (independent observations AND distinct days): one observation per (symbol, settled move) in the graded window. /api/calibration publishes the PREQUENTIAL record behind the same two gates over the accuracy grader's own population, which also drops settlement-quarantined and stale-feed rows, so it is a subset of this one; its independentN, winRate, baseRate and brierSkill are all legitimately DIFFERENT from these - a slightly different population, not a contradiction and not a bug. Compare verdicts, not figures: while its gate holds a scope publishes no figure at all, and neither has published a Brier skill above zero, i.e. no measured probabilistic skill on either reading."
)
