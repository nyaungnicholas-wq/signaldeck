package pipeline

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// naiveLabel's second return separates a DATA GAP from a DEGENERATE statistic.
// Both leave the row unfrozen, but only the gap is a coverage failure, and
// conflating them kept regime-outcome-runner permanently red: 3 of 1151 calls
// per pass sat exactly on their own trailing median, which is not something an
// operator can fix and not evidence of missing data.
func TestNaiveLabelSeparatesDataGapFromDegenerateNull(t *testing.T) {
	w := &RegimeOutcomeWorker{}
	call := store.RegimeCall{SymbolID: 1, Kind: structregime.KindTrend21, Ts: 1000}

	t.Run("no series at all is a data gap", func(t *testing.T) {
		lbl, degenerate := w.naiveLabel(nil, call)
		if lbl != "" || degenerate {
			t.Fatalf("naiveLabel(nil) = (%q, %v), want (empty, false) — a missing series is a gap", lbl, degenerate)
		}
	})

	t.Run("no bar at or before the call is a data gap", func(t *testing.T) {
		sr := &series{ts: []int64{2000, 3000}, closes: []float64{10, 11}, vols: []float64{1, 1}}
		lbl, degenerate := w.naiveLabel(sr, call) // call.Ts=1000 predates every bar
		if lbl != "" || degenerate {
			t.Fatalf("naiveLabel = (%q, %v), want (empty, false) — no usable bar is a gap", lbl, degenerate)
		}
	})

	t.Run("adequate inputs with an undefined statistic is an abstention", func(t *testing.T) {
		// A flat series: the 200-bar SMA equals the close, so NaiveTrendAt's
		// d == 0 branch fires. Inputs are fully adequate — the null simply has
		// no direction.
		n := 400
		sr := &series{}
		for i := 0; i < n; i++ {
			sr.ts = append(sr.ts, int64(i)*86400)
			sr.closes = append(sr.closes, 100)
			sr.vols = append(sr.vols, 1000)
		}
		c := store.RegimeCall{SymbolID: 1, Kind: structregime.KindTrend21, Ts: int64(n-1) * 86400}
		lbl, degenerate := w.naiveLabel(sr, c)
		if lbl != "" {
			t.Fatalf("a flat series produced label %q; expected no direction", lbl)
		}
		if !degenerate {
			t.Fatal("a tied null was reported as a DATA GAP; it must be an abstention, " +
				"or the unmatched-null refusal fires forever on something nobody can fix")
		}
	})

	t.Run("an unknown kind is not counted as a closable gap", func(t *testing.T) {
		sr := &series{ts: []int64{1000}, closes: []float64{10}, vols: []float64{1}}
		lbl, degenerate := w.naiveLabel(sr, store.RegimeCall{SymbolID: 1, Kind: "retired-kind", Ts: 1000})
		if lbl != "" || degenerate {
			t.Fatalf("unknown kind = (%q, %v), want (empty, false)", lbl, degenerate)
		}
	})
}
