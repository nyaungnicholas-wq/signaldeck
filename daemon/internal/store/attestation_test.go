package store

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// EVIDENCE INTEGRITY ON THE PREDICTION WRITE PATH.
//
// internal/pipeline/predict.go used to write the prediction and its chain
// attestation separately, with the ledger half explicitly best-effort: "a
// ledger failure logs + records a dq event but MUST NOT fail the prediction
// (the prediction is already durably written above)".
//
// That is right about durability and wrong about evidence. UpsertPrediction
// also seeds prediction_outcomes, which predict.go itself calls "the population
// every grader reads" -- so a forecast whose attestation failed was later
// graded as though it had been committed to the chain before its outcome
// existed. Precommitment is the claim the entire project rests on, and that
// path could not support it. Measured on the live database 2026-09-13: 30
// served predictions of 498,523 since the ledger epoch have no chain entry, and
// the only thing watching was a bounded alarm in a health check, not a gate.
//
// The invariant these tests pin is one sentence: A ROW THAT CAN BE GRADED HAS
// AN ATTESTATION. Not "usually", and not "unless the append failed".

func mustSymbol(t *testing.T, st *Store, sym string) int64 {
	t.Helper()
	s, err := st.UpsertSymbol(context.Background(), sym, md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	return s.ID
}

func counts(t *testing.T, st *Store, symbolID int64) (preds, outcomes, ledger int) {
	t.Helper()
	ctx := context.Background()
	row := st.DB().QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM predictions        WHERE symbol_id=?),
		        (SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=?),
		        (SELECT COUNT(*) FROM prediction_ledger   WHERE symbol_id=?)`,
		symbolID, symbolID, symbolID)
	if err := row.Scan(&preds, &outcomes, &ledger); err != nil {
		t.Fatalf("counts: %v", err)
	}
	return
}

func samplePrediction(symbolID int64, ts int64) Prediction {
	return Prediction{
		SymbolID: symbolID, Horizon: md.H1d, Ts: ts,
		RawProb: 0.61, CalProb: 0.58, NUsed: 3,
		Components: "{}", Weights: "{}", Basis: "test",
	}
}

func sampleEntry(symbolID int64, ts int64) LedgerEntry {
	return LedgerEntry{
		PredictedAt: time.Now().Unix(), SymbolID: symbolID, Horizon: md.H1d,
		BarTs: ts, RawProb: 0.61, CalProb: 0.58,
		FeatureHash: "featurehash", ModelVersion: 1,
	}
}

// TestAttested_WritesAllThreeTogether is the control. Without it, a function
// that wrote nothing at all would satisfy every failure test below.
func TestAttested_WritesAllThreeTogether(t *testing.T) {
	st := openTemp(t)
	id := mustSymbol(t, st, "ZZATT")
	ts := time.Now().Unix()

	entry, err := st.UpsertPredictionAttested(context.Background(),
		samplePrediction(id, ts), sampleEntry(id, ts))
	if err != nil {
		t.Fatalf("attested write: %v", err)
	}
	if entry.Seq == 0 || entry.EntryHash == "" {
		t.Fatalf("ledger entry came back unassigned: %+v", entry)
	}
	p, o, l := counts(t, st, id)
	if p != 1 || o != 1 || l != 1 {
		t.Fatalf("want 1/1/1 (prediction/outcome/ledger), got %d/%d/%d", p, o, l)
	}
}

// TestAttested_LedgerFailureLeavesNothingGradable is the whole point.
//
// The append is made to fail by removing the table it writes to. What must NOT
// survive is the prediction_outcomes row: that is the one that makes a forecast
// eligible to be graded as precommitted.
func TestAttested_LedgerFailureLeavesNothingGradable(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	id := mustSymbol(t, st, "ZZATT")
	ts := time.Now().Unix()

	if _, err := st.DB().ExecContext(ctx, `DROP TABLE prediction_ledger`); err != nil {
		t.Fatalf("could not induce the append failure: %v", err)
	}

	if _, err := st.UpsertPredictionAttested(ctx, samplePrediction(id, ts), sampleEntry(id, ts)); err == nil {
		t.Fatal("the append failed but the write reported success")
	}

	p, o := countsNoLedger(t, st, id)
	if o != 0 {
		t.Fatalf("a forecast became GRADABLE with no attestation: %d outcome row(s). "+
			"This is the precommitment claim failing silently, which is the defect "+
			"this function was written to make impossible", o)
	}
	if p != 0 {
		t.Fatalf("the prediction survived a rolled-back attestation: %d row(s). "+
			"It would be served as a forecast with nothing on the chain behind it", p)
	}
}

func countsNoLedger(t *testing.T, st *Store, symbolID int64) (preds, outcomes int) {
	t.Helper()
	row := st.DB().QueryRowContext(context.Background(),
		`SELECT (SELECT COUNT(*) FROM predictions         WHERE symbol_id=?),
		        (SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=?)`,
		symbolID, symbolID)
	if err := row.Scan(&preds, &outcomes); err != nil {
		t.Fatalf("counts: %v", err)
	}
	return
}

// TestAttested_ChainStaysLinearAcrossTheAtomicPath. The chain link is now
// reachable by two callers (AppendLedger and UpsertPredictionAttested) through
// one shared helper. If they ever diverge, the chain stops verifying -- so the
// interleaving is walked end to end rather than assumed.
func TestAttested_ChainStaysLinearAcrossTheAtomicPath(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	id := mustSymbol(t, st, "ZZATT")
	base := time.Now().Unix()

	for i := 0; i < 5; i++ {
		ts := base + int64(i)*60
		if _, err := st.UpsertPredictionAttested(ctx, samplePrediction(id, ts), sampleEntry(id, ts)); err != nil {
			t.Fatalf("attested write %d: %v", i, err)
		}
		// Alternate with the standalone appender so both paths are in the chain.
		if _, err := st.AppendLedger(ctx, sampleEntry(id, ts+30)); err != nil {
			t.Fatalf("plain append %d: %v", i, err)
		}
	}

	v, err := st.VerifyLedger(ctx)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !v.Intact {
		t.Fatalf("the chain BROKE at seq %v after interleaving both append paths -- "+
			"the two callers are not computing the same link", v.BrokenAtSeq)
	}
	if v.Count != 10 {
		t.Fatalf("expected 10 chain entries, walked %d", v.Count)
	}
}

// TestAttested_RepeatIsIdempotentForEligibility. The runner can legitimately
// re-run for the same bar. The prediction is REPLACEd and the outcome row is
// INSERT OR IGNOREd, so eligibility must not multiply -- an inflated population
// is an inflated n, and n is what every interval on the site is computed from.
func TestAttested_RepeatIsIdempotentForEligibility(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	id := mustSymbol(t, st, "ZZATT")
	ts := time.Now().Unix()

	for i := 0; i < 3; i++ {
		if _, err := st.UpsertPredictionAttested(ctx, samplePrediction(id, ts), sampleEntry(id, ts)); err != nil {
			t.Fatalf("attested write %d: %v", i, err)
		}
	}
	p, o, l := counts(t, st, id)
	if p != 1 {
		t.Fatalf("prediction rows = %d, want 1 (INSERT OR REPLACE on the same key)", p)
	}
	if o != 1 {
		t.Fatalf("eligibility rows = %d, want 1. A retry inflated the graded population", o)
	}
	// The ledger is append-only by design: a re-run legitimately records that it
	// happened again. That is a different statement from eligibility and must
	// NOT be deduplicated.
	if l != 3 {
		t.Fatalf("ledger rows = %d, want 3: the chain is append-only and records every attestation", l)
	}
}

// TestAttested_LeglessRowIsNeverEligible. n_used == 0 means no leg was admitted,
// so the row is evidence rather than a forecast. It still gets attested -- what
// was computed is a fact -- but it must not enter the graded population, for the
// reason UpsertPrediction already records: a legless 0.5 lands on one side of
// the threshold and scores as a confident directional call.
func TestAttested_LeglessRowIsNeverEligible(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	id := mustSymbol(t, st, "ZZATT")
	ts := time.Now().Unix()

	p := samplePrediction(id, ts)
	p.NUsed = 0
	if _, err := st.UpsertPredictionAttested(ctx, p, sampleEntry(id, ts)); err != nil {
		t.Fatalf("attested write: %v", err)
	}
	pn, on, ln := counts(t, st, id)
	if pn != 1 || ln != 1 {
		t.Fatalf("want the evidence row and its attestation, got %d/%d", pn, ln)
	}
	if on != 0 {
		t.Fatalf("a legless row became gradable: %d outcome row(s)", on)
	}
}
