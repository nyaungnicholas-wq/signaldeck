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
const (
	calibrationScopeNote = "SCOPE: this is the PREQUENTIAL record - every probability was frozen at prediction time and graded forward, over all resolved pairs. /api/track-record publishes the same underlying record behind an independent-observation AND distinct-day gate, so its independentN, winRate, baseRate and brierSkill are all legitimately DIFFERENT from these - a smaller, day-declustered sample, not a contradiction and not a bug. Compare verdicts, not figures: both scopes put the win rate at or below the naive baseline and the Brier skill below zero, i.e. no measured probabilistic skill on either reading."
	trackRecordScopeNote = "SCOPE: this is the live out-of-sample record behind both gates (independent observations AND distinct days), so it is a smaller and day-declustered sample. /api/calibration publishes the PREQUENTIAL record over all resolved pairs; its independentN, winRate, baseRate and brierSkill are all legitimately DIFFERENT from these - a wider ungated sample, not a contradiction and not a bug. Compare verdicts, not figures: both scopes put the win rate at or below the naive baseline and the Brier skill below zero, i.e. no measured probabilistic skill on either reading."
)
