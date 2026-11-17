package splitfix

// HeldForReview reports whether a resolver should wait before grading a window
// of daily bars (ascending): the window carries a split-shaped one-day move
// that the split-repair worker has not examined since it appeared.
//
// WHY (audit 2026-11-10). The resolvers grade raw closes. A split the provider
// had not yet adjusted reads as a 50-90% move and is graded as one. Bars are
// fetched split-adjusted and the split-repair worker re-fetches any series it
// flags, then either repairs it or records the move as real. The guard only
// waits for that verdict: once the worker has looked (lastRepair after the
// move), the window is graded on whatever bars the store then holds, by the
// same arithmetic as before. Nothing about the graded quantity changes.
//
// hasRepair/lastRepair are store.LastSplitRepair for the symbol.
func HeldForReview(window []Bar, lastRepair int64, hasRepair bool) bool {
	rep := Detect(window)
	if len(rep.Suspects) == 0 {
		return false
	}
	var newest int64
	for _, s := range rep.Suspects {
		if s.Ts > newest {
			newest = s.Ts
		}
	}
	// A review stamped before the suspect bar finished forming (its stamp is
	// the session's midnight) cannot have seen it.
	return !hasRepair || lastRepair <= newest+86400
}

// MaxHoldSecs bounds HeldForReview's wait. The worker examines only active
// symbols with a recent flag and needs a market-data client; a row must not
// wait forever on a review that will never come (that would quietly drop
// exactly the large moves from the record).
const MaxHoldSecs = 10 * 86400
