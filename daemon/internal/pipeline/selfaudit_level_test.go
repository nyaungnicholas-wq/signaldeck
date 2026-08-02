package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The self-audit compares every metric only to its OWN PRIOR VALUE, so it
// detects drift and never level. A model that has been at chance since the day
// it was born never changes, therefore never flags.
//
// Measured on the live system 2026-08-02: calibration:1d reported
// status "ok" at reliability 0.4982 — mean|cal_prob−realized| of 0.4982 is what
// a useless constant p=0.5 scores — because it had moved only +0.0007 against a
// drift threshold of 0.020. The system could not tell itself it was at chance.
//
// This pins the level check. It is emitted as its OWN metric rather than
// overloading calibration:<h>'s status, so the drift verdict stays readable and
// no existing assertion changes meaning.
func TestSelfAuditFlagsCalibrationAtChance(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	// 40 independent resolved 1d predictions, every one of them called at
	// cal_prob 0.50 — the definition of no information. Half resolve up, half
	// down, so reliability lands at 0.50 and bias stays ~0.
	predTs := time.Now().Add(-10 * 24 * time.Hour).Unix()
	for i := 0; i < 40; i++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("CHANCE%d", i), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: predTs,
			RawProb: 0.50, CalProb: 0.50, NUsed: 3,
		}); err != nil {
			t.Fatal(err)
		}
		fwd := -0.01
		if i < 20 {
			fwd = 0.01
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, predTs, fwd); err != nil {
			t.Fatal(err)
		}
	}

	// A prior at the SAME level. Drift is therefore exactly zero: the old
	// machinery has nothing to say, which is precisely the blind spot.
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{
		Ts: predTs, Metric: "calibration_level:1d", Value: 0.50, Status: "at_chance", Detail: "prior",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&SelfAuditor{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	got := latestAudit(t, st)
	row, ok := got["calibration_level:1d"]
	if !ok {
		t.Fatalf("no calibration_level:1d finding emitted; got metrics %v", keysOf(got))
	}
	if row.Status != "at_chance" {
		t.Errorf("calibration_level:1d status = %q, want %q (reliability %.4f is a coin flip)",
			row.Status, "at_chance", row.Value)
	}
	// The detail must state the number, not just the verdict.
	if !strings.Contains(row.Detail, "0.5") {
		t.Errorf("detail %q does not quote the measured reliability", row.Detail)
	}

	// The drift check must be UNCHANGED and still present.
	if _, ok := got["calibration:1d"]; !ok {
		t.Error("calibration:1d drift finding disappeared; the level check must be additive")
	}
}

// A genuinely informative model must NOT trip the level flag.
func TestSelfAuditDoesNotFlagInformativeCalibration(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	// 40 confident calls that are mostly RIGHT: cal_prob 0.90, 36 of 40 resolve
	// up. Reliability ~0.14 — far from chance.
	predTs := time.Now().Add(-10 * 24 * time.Hour).Unix()
	for i := 0; i < 40; i++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("GOOD%d", i), md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: predTs,
			RawProb: 0.90, CalProb: 0.90, NUsed: 3,
		}); err != nil {
			t.Fatal(err)
		}
		fwd := -0.01
		if i < 36 {
			fwd = 0.01
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, predTs, fwd); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := (&SelfAuditor{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	if row, ok := latestAudit(t, st)["calibration_level:1d"]; ok && row.Status == "at_chance" {
		t.Errorf("informative model wrongly flagged at_chance (reliability %.4f)", row.Value)
	}
}

func keysOf(m map[string]store.SelfAuditRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
