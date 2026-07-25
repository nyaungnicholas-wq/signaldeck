package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newLedgerStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func ledgerHypByID(t *testing.T, st *store.Store, id string) rl.Hypothesis {
	t.Helper()
	hyps, err := st.LedgerHypotheses(context.Background())
	if err != nil {
		t.Fatalf("hyps: %v", err)
	}
	for _, h := range hyps {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("hypothesis %s not found", id)
	return rl.Hypothesis{}
}

// The seed writes the Pressure chapter once, computes honest posteriors from
// the transcribed observations, and never re-runs.
func TestResearchLedgerSeed(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)
	w := NewResearchLedgerWorker(st)
	w.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }

	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "seeded") {
		t.Errorf("first run message %q lacks 'seeded'", msg)
	}

	// H001 (pressure predicts direction): two decisive-against experiments from
	// prior 0.5 must floor the posterior — rejected.
	h1 := ledgerHypByID(t, st, "H001")
	if h1.Posterior != rl.MinPosterior || h1.Status != rl.StatusRejected {
		t.Errorf("H001 = (%.4f, %s), want (%.2f, rejected)", h1.Posterior, h1.Status, rl.MinPosterior)
	}
	if h1.Replications != 0 || h1.Contradictions != 0 {
		t.Errorf("H001 counters = (%d,%d), want (0,0) — transcribed manual rows are neither replications nor contradictions", h1.Replications, h1.Contradictions)
	}

	// H005 (contamination): two decisive-for below-band experiments cap the
	// posterior at 0.97 — but single-regime evidence caps status at tentative.
	h5 := ledgerHypByID(t, st, "H005")
	if h5.Posterior != rl.MaxPosterior || h5.Status != rl.StatusTentative {
		t.Errorf("H005 = (%.4f, %s), want (%.2f, tentative — regime gate)", h5.Posterior, h5.Status, rl.MaxPosterior)
	}

	// H002 (open discovery): NO transcribed evidence — sits at its prior until
	// the grader earns data.
	h2 := ledgerHypByID(t, st, "H002")
	if h2.Posterior != h2.Prior || h2.Prior != 0.25 {
		t.Errorf("H002 posterior = %.4f, want prior 0.25 (no evidence yet)", h2.Posterior)
	}

	// Second run same day: gated.
	msg, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if msg != "already ran today" {
		t.Errorf("second run message = %q", msg)
	}

	// Next day: seed must NOT re-run (guarded), hypotheses unchanged.
	w.Now = func() time.Time { return time.Unix(1_800_000_000+86_400, 0) }
	msg, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if strings.Contains(msg, "seeded") {
		t.Errorf("seed re-ran: %q", msg)
	}
}

// seedLedgerLabeled writes one labeled 1w example (prediction + features +
// resolved outcome) so it joins into LabeledFeaturesSince.
func seedLedgerLabeled(t *testing.T, st *store.Store, sym md.Symbol, ts int64, vec map[string]float64, fwd float64) {
	t.Helper()
	ctx := context.Background()
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1w, Ts: ts, RawProb: 0.5, CalProb: 0.5, NUsed: 40, Components: "{}",
	}); err != nil {
		t.Fatalf("upsert pred: %v", err)
	}
	if err := st.InsertFeatures(ctx, sym.ID, md.H1w, ts, 9, vec); err != nil {
		t.Fatalf("insert features: %v", err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1w, ts, fwd); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

// The H002 grader turns fresh labeled rows into evidence: a 65%-accurate
// inverse call over a balanced sample must RAISE the posterior above the
// prior, write the experiment + 3 attack rows, and count one replication.
func TestResearchLedgerGraderUpdatesPosterior(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)

	// 100 obs: 10 symbols × 10 calendar weeks, balanced up/down, inverse call
	// correct 65% of the time. pressure<0 predicts up.
	syms := make([]md.Symbol, 10)
	for i := range syms {
		s, err := st.UpsertSymbol(ctx, "T"+string(rune('A'+i)), md.Stocks, "")
		if err != nil {
			t.Fatalf("sym: %v", err)
		}
		syms[i] = s
	}
	base := int64(1_000_000)
	for i := 0; i < 100; i++ {
		week, sIdx := i/10, i%10
		up := sIdx%2 == 0 // 5 up / 5 down per week -> folded baseline exactly 0.5
		// Weeks 0-7: 7/10 correct calls (beats 0.5) -> winning week trials.
		// Weeks 8-9: 4/10 correct -> losing trials. k=8 of n=10 weeks.
		win := sIdx < 7
		if week >= 8 {
			win = sIdx < 4
		}
		pressure := 0.5 // predicts down
		if up == win {  // pressure<0 (predict up) exactly when the call should equal `up`
			pressure = -0.5
		}
		fwd := -0.01
		if up {
			fwd = 0.01
		}
		ts := base + int64(week)*weekSecs + int64(sIdx)
		seedLedgerLabeled(t, st, syms[sIdx], ts, map[string]float64{
			"pressure_score": pressure, "comp_rsi": 0.005, // below H008's 0.02 trigger
		}, fwd)
	}

	w := NewResearchLedgerWorker(st)
	w.MinNewObs = 50
	w.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "1 new grades") {
		t.Errorf("message %q, want exactly 1 new grade (H002 only — H008's trigger unmet)", msg)
	}

	h2 := ledgerHypByID(t, st, "H002")
	if h2.Posterior <= h2.Prior {
		t.Errorf("H002 posterior %.4f did not rise above prior %.2f on 65%% accuracy", h2.Posterior, h2.Prior)
	}
	if h2.Replications != 0 {
		t.Errorf("H002 replications = %d, want 0 — the in-sample discovery grade is not a replication", h2.Replications)
	}

	ev, err := st.LedgerEvidence(ctx, "H002")
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	var exp, attacks int
	for _, e := range ev {
		switch e.Kind {
		case rl.KindExperiment:
			exp++
			if e.K != 8 || e.N != 10 {
				t.Errorf("experiment graded %d/%d week trials, want 8/10", e.K, e.N)
			}
			if !strings.Contains(e.Note, "IN-SAMPLE discovery window") {
				t.Errorf("discovery grade note lacks the in-sample caveat: %q", e.Note)
			}
			if e.BF > rl.DiscoveryMaxBF {
				t.Errorf("discovery grade BF %.2f exceeds DiscoveryMaxBF %.1f", e.BF, rl.DiscoveryMaxBF)
			}
		case rl.KindAttack:
			attacks++
		}
	}
	if exp != 1 || attacks != 3 {
		t.Errorf("evidence = %d experiments + %d attacks, want 1 + 3", exp, attacks)
	}

	// Next day, NO new rows: the grader must write nothing (cursor discipline)
	// and the posterior must stand exactly.
	before := h2.Posterior
	w.Now = func() time.Time { return time.Unix(1_800_000_000+86_400, 0) }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	h2b := ledgerHypByID(t, st, "H002")
	if h2b.Posterior != before {
		t.Errorf("posterior moved with no new data: %.6f -> %.6f", before, h2b.Posterior)
	}
	ev2, _ := st.LedgerEvidence(ctx, "H002")
	if len(ev2) != len(ev) {
		t.Errorf("evidence grew with no new data: %d -> %d rows", len(ev), len(ev2))
	}
}

// An anti-predictive fresh window must LOWER the posterior and count a
// contradiction — beliefs move in both directions.
func TestResearchLedgerContradictionLowersPosterior(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)
	syms := make([]md.Symbol, 10)
	for i := range syms {
		s, _ := st.UpsertSymbol(ctx, "U"+string(rune('A'+i)), md.Stocks, "")
		syms[i] = s
	}
	base := int64(1_000_000)
	for i := 0; i < 100; i++ {
		week, sIdx := i/10, i%10
		up := sIdx%2 == 0
		win := sIdx < 3 // 3/10 correct every week — every week trial loses
		pressure := 0.5
		if up == win {
			pressure = -0.5
		}
		fwd := -0.01
		if up {
			fwd = 0.01
		}
		ts := base + int64(week)*weekSecs + int64(sIdx)
		seedLedgerLabeled(t, st, syms[sIdx], ts, map[string]float64{
			"pressure_score": pressure, "comp_rsi": 0.005,
		}, fwd)
	}
	w := NewResearchLedgerWorker(st)
	w.MinNewObs = 50
	w.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	h2 := ledgerHypByID(t, st, "H002")
	if h2.Posterior >= h2.Prior {
		t.Errorf("posterior %.4f did not fall below prior %.2f on 35%% accuracy", h2.Posterior, h2.Prior)
	}
	if h2.Contradictions != 1 {
		t.Errorf("contradictions = %d, want 1", h2.Contradictions)
	}
}
