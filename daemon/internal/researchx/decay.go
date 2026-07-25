package researchx

import (
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

// backtestKind is the historical-backfill evidence kind — a stable DB
// contract value, referenced by literal so this package depends only on
// long-stable researchledger symbols.
const backtestKind = "backtest"

// staleAfterSecs: a hypothesis nobody has graded in 60 days has stopped being
// tested.
const staleAfterSecs int64 = 60 * 86400

// DecayReport tracks how a hypothesis's posterior has moved relative to its
// historical peak, and whether it is still being tested at all.
type DecayReport struct {
	Peak        float64
	PeakTs      int64
	Current     float64
	LastGradeTs int64 // newest experiment/replication/backtest ts; 0 = never graded
	// EdgeWeakening: Current sits >0.15 below Peak — only meaningful once the
	// belief actually peaked above coin-flip (Peak > 0.5).
	EdgeWeakening bool
	// Stale: nowTs-LastGradeTs > 60d; a never-graded hypothesis is stale by
	// definition.
	Stale bool
}

// Decay replays the evidence chain prefix by prefix (each prefix through
// rl.EffectiveChain, exactly as the ledger would have reported it at the
// time) to find the posterior peak. chain must be in insertion (ts) order —
// the order the store returns it. The peak starts at the clamped prior with
// PeakTs=0: a belief that only ever lost ground peaked at birth.
func Decay(prior float64, chain []rl.Evidence, nowTs int64) DecayReport {
	base := rl.Posterior(prior, nil)
	rep := DecayReport{Peak: base, Current: base}
	for i, e := range chain {
		p := rl.Posterior(prior, rl.EffectiveChain(chain[:i+1]))
		if p > rep.Peak {
			rep.Peak, rep.PeakTs = p, e.Ts
		}
		rep.Current = p
		switch e.Kind {
		case rl.KindExperiment, rl.KindReplication, backtestKind:
			if e.Ts > rep.LastGradeTs {
				rep.LastGradeTs = e.Ts
			}
		}
	}
	rep.EdgeWeakening = rep.Peak > 0.5 && rep.Current < rep.Peak-0.15
	rep.Stale = rep.LastGradeTs == 0 || nowTs-rep.LastGradeTs > staleAfterSecs
	return rep
}
