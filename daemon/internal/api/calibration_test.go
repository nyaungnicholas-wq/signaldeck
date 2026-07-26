// Regression for the 2026-07-26 hostile review, finding C3 (third defect):
// /api/calibration published a bare `brier: 0.302` and OMITTED the skill score.
// Against the live 56.0% base rate the constant forecast scores 0.246, so the
// published model is 23% WORSE than a constant (skill -0.226) — the opposite of
// what a small-looking Brier suggests. The same codebase computed brierSkill
// correctly on /api/trackrecord, so the omission read as selective rather than
// accidental. Brier and Brier skill now ship together or not at all.
package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// callCalibration invokes the handler directly (not through the SWR wrapper,
// whose cache is process-wide and would leak between tests).
func callCalibration(t *testing.T, d Deps, horizon string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/calibration?horizon="+horizon, nil)
	d.calibration(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return body
}

func TestCalibrationPayloadCarriesBrierSkill(t *testing.T) {
	ctx := context.Background()
	_, st, d := newTestServer(t, func(c *config.Config) {})

	sym, err := st.UpsertSymbol(ctx, "SKILL", md.Stocks, "Skill Fixture")
	if err != nil {
		t.Fatal(err)
	}
	// 100 resolved calls: 60 up, and a deliberately INVERTED forecast, so the
	// Brier score alone looks unremarkable while the skill score is negative.
	for i := 0; i < 100; i++ {
		up := i < 60
		prob, fwd := 0.8, -1.0
		if up {
			prob, fwd = 0.2, 1.0
		}
		ts := int64(1_700_000_000 + i*86400)
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
			RawProb: prob, CalProb: prob, NUsed: 3, Components: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, fwd); err != nil {
			t.Fatal(err)
		}
	}

	body := callCalibration(t, d, "1d")

	brier, ok := body["brier"].(float64)
	if !ok {
		t.Fatalf("brier missing or not a number: %#v", body["brier"])
	}
	skill, ok := body["brierSkill"].(float64)
	if !ok {
		t.Fatalf("brierSkill MISSING from the calibration payload — a bare Brier "+
			"score is not interpretable without its base-rate benchmark: %#v", body)
	}
	base, ok := body["baseRate"].(float64)
	if !ok {
		t.Fatalf("baseRate missing: %#v", body["baseRate"])
	}
	if math.Abs(base-0.6) > 1e-9 {
		t.Errorf("baseRate=%v want 0.6", base)
	}
	// brier = 0.64 (every call off by 0.8); reference = 0.6*0.4 = 0.24.
	if math.Abs(brier-0.64) > 1e-9 {
		t.Errorf("brier=%v want 0.64", brier)
	}
	if want := 1 - 0.64/0.24; math.Abs(skill-want) > 1e-9 {
		t.Errorf("brierSkill=%v want %v", skill, want)
	}
	if skill >= 0 {
		t.Errorf("an inverted forecast must publish NEGATIVE skill, got %v", skill)
	}
	if _, ok := body["brierNote"].(string); !ok {
		t.Errorf("brierNote missing — the skill score needs its plain-English verdict")
	}
}

// With no gradable history the skill score must be WITHHELD (null), never 0 —
// a skill of exactly 0 is the real verdict "as good as the base rate".
func TestCalibrationWithholdsUngradableSkill(t *testing.T) {
	_, _, d := newTestServer(t, func(c *config.Config) {})
	body := callCalibration(t, d, "1d")
	if v, present := body["brierSkill"]; !present {
		t.Fatal("brierSkill key must be present even when ungradable")
	} else if v != nil {
		t.Fatalf("brierSkill=%#v with no resolved history — must be null, never a number", v)
	}
	if v := body["baseRate"]; v != nil {
		t.Errorf("baseRate=%#v with no history — must be null", v)
	}
	if note, _ := body["brierNote"].(string); note == "" {
		t.Error("a withheld skill score must state its reason")
	}
}

// Degenerate outcomes (every call resolved up) give a zero-variance base-rate
// reference. Skill is undefined there and must be withheld, not reported as a
// finite number — the guard that stops "skill = 1 - brier/0" nonsense.
func TestCalibrationWithholdsSkillOnDegenerateOutcomes(t *testing.T) {
	ctx := context.Background()
	_, st, d := newTestServer(t, func(c *config.Config) {})
	sym, err := st.UpsertSymbol(ctx, "ALLUP", md.Stocks, "Degenerate Fixture")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		ts := int64(1_700_000_000 + i*86400)
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
			RawProb: 0.55, CalProb: 0.55, NUsed: 3, Components: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, 1.0); err != nil {
			t.Fatal(err)
		}
	}
	body := callCalibration(t, d, "1d")
	if v := body["brierSkill"]; v != nil {
		t.Fatalf("brierSkill=%#v against a zero-variance reference — must be null", v)
	}
}
