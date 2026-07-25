// ═══ SURVIVORSHIP LABELS (#20, appended wave) ════════════════════════════════
//
// The prediction ledger levies a survivorship PENALTY internally; this file
// propagates the LABEL to every user surface whose statistics rest on the
// currently-tracked-universe bars table — today, all of them. One shared
// helper so the wording can never drift between endpoints.
package api

// survivorshipNote is the honest disclosure, verbatim on every exposed payload.
const survivorshipNote = "universe is currently-tracked symbols; delisted names absent — long-side persistence and mean-reversion stats are inflated by an unmeasurable amount"

// survivorshipBlock returns the {"exposed": bool, "note": "..."} block carried
// by /api/signal-backtest, /api/backtest, /api/track-record (regimes +
// predictions) and /api/regimes. exposed=true wherever the stat rests on the
// currently-tracked-universe bars table — which is every one of those surfaces
// today.
func survivorshipBlock() map[string]any {
	return map[string]any{
		"exposed": true,
		"note":    survivorshipNote,
	}
}
