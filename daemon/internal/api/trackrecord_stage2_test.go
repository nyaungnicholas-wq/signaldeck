package api

// STAGE 2 — GATE COUNTDOWN tests: the pure countdown math (threshold /
// remaining / estimate) and the /api/track-record payload's gate block from a
// seeded temp store.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestTrackGateCountdownMath pins the pure countdown math: the estimate exists
// only when the last window actually accrued independent symbol-days, is 0
// when the gate is already clear, and is nil (honest unknown) otherwise.
func TestTrackGateCountdownMath(t *testing.T) {
	cases := []struct {
		name                  string
		indepN, threshold     int
		newIndep, tradingDays int
		wantRemaining         int
		wantEst               any // nil = unknown; int otherwise
	}{
		{"one-per-day", 20, 30, 5, 5, 10, 10},
		{"fast-accrual-ceils", 20, 30, 12, 5, 10, 5},  // 10 / 2.4 = 4.17 → 5
		{"no-accrual-unknown", 20, 30, 0, 5, 10, nil}, // nothing to extrapolate
		{"already-clear", 31, 30, 0, 5, 0, 0},         // ungated → 0
		{"exactly-at-gate", 30, 30, 3, 5, 0, 0},
		{"degenerate-no-trading-days", 20, 30, 4, 0, 10, nil},
	}
	for _, c := range cases {
		got := trackGateCountdown(c.indepN, c.threshold, c.newIndep, c.tradingDays)
		if got["threshold"] != c.threshold {
			t.Errorf("%s: threshold = %v want %v", c.name, got["threshold"], c.threshold)
		}
		if got["remaining"] != c.wantRemaining {
			t.Errorf("%s: remaining = %v want %v", c.name, got["remaining"], c.wantRemaining)
		}
		est := got["estDaysToUngate"]
		if c.wantEst == nil {
			if est != nil {
				t.Errorf("%s: estDaysToUngate = %v want nil", c.name, est)
			}
		} else if est != c.wantEst {
			t.Errorf("%s: estDaysToUngate = %v want %v", c.name, est, c.wantEst)
		}
		if got["estimate"] != true {
			t.Errorf("%s: the countdown must be labeled an estimate", c.name)
		}
	}
}

// TestTradingDaysInLastN sanity-checks the trading-day counter: any 7-day
// window holds at least 3 and at most 5 NYSE trading days.
func TestTradingDaysInLastN(t *testing.T) {
	for _, when := range []time.Time{
		time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC),   // regular week (after Jul 4)
		time.Date(2026, 12, 28, 12, 0, 0, 0, time.UTC), // holiday cluster
	} {
		n := tradingDaysInLastN(when, 7)
		if n < 3 || n > 5 {
			t.Errorf("tradingDaysInLastN(%v, 7) = %d, want 3..5", when, n)
		}
	}
}

// trackGateBody is the decoded gate block of /api/track-record.
type trackGateBody struct {
	IndependentN int  `json:"independentN"`
	Gated        bool `json:"gated"`
	Gate         struct {
		Threshold       int      `json:"threshold"`
		Remaining       int      `json:"remaining"`
		EstDaysToUngate *float64 `json:"estDaysToUngate"`
		Estimate        bool     `json:"estimate"`
		EstBasis        string   `json:"estBasis"`
		Accrual7d       struct {
			IndependentNew int     `json:"independentNew"`
			TradingDays    int     `json:"tradingDays"`
			PerTradingDay  float64 `json:"perTradingDay"`
		} `json:"accrual7d"`
		FirstResolveEta     map[string]*int64 `json:"firstResolveEta"`
		FirstResolveEtaNote string            `json:"firstResolveEtaNote"`
	} `json:"gate"`
}

// TestTrackRecordGatePayload seeds one fresh resolution (1d) plus one still-open
// 1w prediction and checks the countdown block: threshold/remaining, a non-nil
// labeled estimate (something accrued this week), a first-resolution ETA for
// the zero-resolved 1w horizon, and none for the already-resolving 1d.
func TestTrackRecordGatePayload(t *testing.T) {
	srv, st := newTrackRecordServer(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	// One resolved 1d outcome (ResolvePrediction stamps resolved_at = now, so
	// it counts as this week's accrual).
	predTs := time.Now().Add(-48 * time.Hour).Unix()
	seedResolvedPrediction(t, st, sym.ID, md.H1d, predTs, 0.7, 0.01)
	// One still-open 1w prediction → the 1w horizon (0 resolved) gets an ETA.
	openTs := time.Now().Add(-24 * time.Hour).Unix()
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1w, Ts: openTs,
		RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/track-record?horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var body trackGateBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if !body.Gated || body.IndependentN != 1 {
		t.Fatalf("independentN=%d gated=%v — want 1, gated", body.IndependentN, body.Gated)
	}
	g := body.Gate
	if g.Threshold != 30 || g.Remaining != 29 {
		t.Fatalf("threshold/remaining = %d/%d want 30/29", g.Threshold, g.Remaining)
	}
	if !g.Estimate || g.EstBasis == "" {
		t.Fatalf("countdown must be labeled an estimate with its basis (estimate=%v basis=%q)", g.Estimate, g.EstBasis)
	}
	if g.Accrual7d.IndependentNew != 1 {
		t.Fatalf("accrual7d.independentNew = %d want 1", g.Accrual7d.IndependentNew)
	}
	if g.EstDaysToUngate == nil || *g.EstDaysToUngate <= 0 {
		t.Fatalf("estDaysToUngate = %v — accrued this week, so a positive estimate is due", g.EstDaysToUngate)
	}
	// 1w: zero resolved + one open prediction → ETA = openTs + 7 days.
	eta := g.FirstResolveEta["1w"]
	if eta == nil || *eta != openTs+7*86400 {
		t.Fatalf("firstResolveEta[1w] = %v want %d", eta, openTs+7*86400)
	}
	// 1d already has a resolution → no first-resolution ETA offered.
	if g.FirstResolveEta["1d"] != nil {
		t.Fatalf("firstResolveEta[1d] = %v want null (already resolving)", *g.FirstResolveEta["1d"])
	}
	if g.FirstResolveEtaNote == "" {
		t.Fatal("firstResolveEta must carry its estimate label")
	}
}
