package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func adTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "adlive.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedADHyp registers one AD-* hypothesis with a machine-readable spec, its
// capped in-sample discovery experiment covering [winFrom, winTo], and an
// explicit status.
func seedADHyp(t *testing.T, st *store.Store, id, status string, winFrom, winTo int64) {
	t.Helper()
	ctx := context.Background()
	spec, _ := json.Marshal(researchx.Rule{Call: "long"})
	h := rl.Hypothesis{
		ID: id, Family: "auto", Horizon: "1w", Statement: "test AD " + id,
		Prior: 0.15, MaxEdge: 0.20, Spec: string(spec),
	}
	h.Posterior = h.Prior
	h.Status = status
	h.Regimes = 1
	if err := st.UpsertLedgerHypothesis(ctx, h, 1); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	if err := st.InsertLedgerEvidence(ctx, rl.Evidence{
		HypID: id, Ts: 1, Kind: rl.KindExperiment, K: 5, N: 10, P0: 0.5, BF: 2,
		Note: "discovery in-sample", WindowFrom: winFrom, WindowTo: winTo,
	}); err != nil {
		t.Fatalf("seed evidence %s: %v", id, err)
	}
}

// adObs builds `weeks` calendar-week clusters of `perWeek` obs starting at
// startTs (alternating up/down so the folded baseline is well-defined).
func adObs(startTs int64, weeks, perWeek int) []researchx.Obs {
	var out []researchx.Obs
	for wk := 0; wk < weeks; wk++ {
		ts := startTs + int64(wk)*histfeat.WeekSecs
		for i := 0; i < perWeek; i++ {
			out = append(out, researchx.Obs{
				SymbolID: int64(i + 1), Week: ts / histfeat.WeekSecs, Ts: ts,
				Vec: map[string]float64{"pressure_score": -1},
				Up:  i%2 == 0, FwdRet: 0.01, Era: "test",
			})
		}
	}
	return out
}

func countReplications(t *testing.T, st *store.Store, hypID string) int {
	t.Helper()
	ev, err := st.LedgerEvidence(context.Background(), hypID)
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	n := 0
	for _, e := range ev {
		if e.Kind == rl.KindReplication {
			n++
		}
	}
	return n
}

// A spec'd AD hypothesis accrues a fresh-window replication ONLY from weeks
// strictly beyond its window cursor (+2wk clearance); a second pass with no
// newer weeks appends nothing.
func TestADReplicationFreshWeeksOnly(t *testing.T) {
	st := adTestStore(t)
	w := &ResearchEngineWorker{St: st}
	discEnd := int64(100) * histfeat.WeekSecs
	seedADHyp(t, st, "AD-fresh001", rl.StatusTentative, histfeat.WeekSecs, discEnd)

	// 3 stale weeks INSIDE the clearance + 6 fresh weeks beyond it.
	stale := adObs(discEnd, 3, 12) // < discEnd+2wk for the first cluster
	fresh := adObs(discEnd+2*histfeat.WeekSecs, 6, 12)
	obs := append(stale[:12], fresh...) // first stale week only (ts == discEnd)

	if n := w.adReplicate(context.Background(), obs, 1000, 3, 4); n != 1 {
		t.Fatalf("want 1 AD grade, got %d", n)
	}
	if n := countReplications(t, st, "AD-fresh001"); n != 1 {
		t.Fatalf("want 1 replication row, got %d", n)
	}
	// The graded window must start at/after the clearance boundary — the stale
	// week (ts == discEnd) must not be inside it.
	ev, _ := st.LedgerEvidence(context.Background(), "AD-fresh001")
	for _, e := range ev {
		if e.Kind == rl.KindReplication {
			if e.WindowFrom < discEnd+2*histfeat.WeekSecs {
				t.Fatalf("replication window_from %d dips before the clearance boundary %d",
					e.WindowFrom, discEnd+2*histfeat.WeekSecs)
			}
		}
	}

	// Second pass over the SAME obs: the cursor advanced past them — no row.
	if n := w.adReplicate(context.Background(), obs, 2000, 3, 4); n != 0 {
		t.Fatalf("re-pass over consumed weeks must grade nothing, got %d", n)
	}
	if n := countReplications(t, st, "AD-fresh001"); n != 1 {
		t.Fatalf("cursor must be respected — still want 1 replication row, got %d", n)
	}
}

// Rejected specs are skipped entirely.
func TestADReplicationSkipsRejected(t *testing.T) {
	st := adTestStore(t)
	w := &ResearchEngineWorker{St: st}
	discEnd := int64(100) * histfeat.WeekSecs
	seedADHyp(t, st, "AD-reject01", rl.StatusRejected, histfeat.WeekSecs, discEnd)
	obs := adObs(discEnd+2*histfeat.WeekSecs, 6, 12)

	if n := w.adReplicate(context.Background(), obs, 1000, 3, 4); n != 0 {
		t.Fatalf("rejected hypothesis must not be graded, got %d", n)
	}
	if n := countReplications(t, st, "AD-reject01"); n != 0 {
		t.Fatalf("rejected hypothesis accrued evidence: %d rows", n)
	}
}

// The per-pass cap: five eligible AD hypotheses, only four grades land.
func TestADReplicationCapRespected(t *testing.T) {
	st := adTestStore(t)
	w := &ResearchEngineWorker{St: st}
	discEnd := int64(100) * histfeat.WeekSecs
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = fmt.Sprintf("AD-cap%05d", i)
		seedADHyp(t, st, ids[i], rl.StatusTentative, histfeat.WeekSecs, discEnd)
	}
	obs := adObs(discEnd+2*histfeat.WeekSecs, 6, 12)

	if n := w.adReplicate(context.Background(), obs, 1000, 3, 4); n != adMaxGradesPerPass {
		t.Fatalf("want the cap %d, got %d", adMaxGradesPerPass, n)
	}
	total := 0
	for _, id := range ids {
		total += countReplications(t, st, id)
	}
	if total != adMaxGradesPerPass {
		t.Fatalf("want %d replication rows across the fleet, got %d", adMaxGradesPerPass, total)
	}
}
