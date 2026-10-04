package publication

// SD-30 WITHHOLDING (2026-10-02). The 1d/1w directional label is mostly known
// when the call is issued: it is measured from the settled base close, and the
// row the grader keeps is the latest one per base bar, issued after the
// forward session has already closed. Measured read-only on the live record
// since 2026-09-25, the outcome matches the move already visible at issue on
// 93.8% of graded stock 1d rows and 78.6% of crypto ones; 1w shows the same
// leak (audits/2026-09-30-signaldeckfix-ledger.md SD-30). A corrected label is
// a preregistration decision that has not been made, so until it is, every
// public or member figure measuring the directional call against that label
// (accuracy, hit rate, skill, Brier, verdict) is withheld with this reason.
//
// Grading, resolution, storage, model-health and the prereg chain are NOT
// touched: this only decides what is published. tools/live_accuracy.py reads
// the two declarations below from this file, so the Go surfaces and the
// generated documents flip together.
//
// To republish: set SD30Withheld = false. That is the whole reversal.
var SD30Withheld = true

const SD30Reason = "withheld: label partly realised at issue (SD-30); a corrected label grades from 2026-10-04 and figures return only after that window clears its evidence floors"

// DirectionalWithheld reports whether directional figures at horizon h are
// withheld, and the reason to publish in their place.
func DirectionalWithheld(h string) (string, bool) {
	if SD30Withheld && (h == "1d" || h == "1w") {
		return SD30Reason, true
	}
	return "", false
}
