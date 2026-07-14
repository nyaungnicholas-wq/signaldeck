// SIGNALS-hub overhaul: /api/composite + /api/composite/top endpoint tests
// (separate harness file so parallel edits never collide).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newCompositeServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerComposite(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// seedCompositeRow stores one composite row with a REAL engine payload so the
// handler decodes exactly what the worker writes.
func seedCompositeRow(t *testing.T, st *store.Store, ctx context.Context, symbolID, ts int64, score int, pct, cal float64) {
	t.Helper()
	c := ensemble.Components{PressureScore: (cal - 0.5) * 4}
	payload := composite.Payload{
		Horizon: "1d",
		Edge:    cal - 0.5,
		RawProb: cal,
		CalProb: cal,
		NUsed:   1,
		PredTs:  ts - 60,
		Factors: composite.BuildFactors(composite.Inputs{Components: c}),
		Ledger:  composite.BuildLedger(c, cal, cal),
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCompositeScore(ctx, store.CompositeScore{
		SymbolID: symbolID, Ts: ts, Horizon: "1d",
		Score: score, CurvePct: pct, Edge: cal - 0.5, Payload: string(blob),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompositeDetailEndpoint(t *testing.T) {
	srv, st := newCompositeServer(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	_ = aapl

	// No row yet: honest absence with the gate explained, never a 404.
	var absent struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if code := s8Get(t, srv.URL+"/api/composite?symbol=NVDA&market=stocks", &absent); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if absent.Available || !strings.Contains(absent.Reason, "forced-curve gate") {
		t.Fatalf("absence payload = %+v", absent)
	}

	now := time.Now().Unix()
	seedCompositeRow(t, st, ctx, nvda.ID, now-3600, 5, 50, 0.52)
	seedCompositeRow(t, st, ctx, nvda.ID, now, 8, 78.3, 0.563)

	var body struct {
		Available  bool               `json:"available"`
		Symbol     string             `json:"symbol"`
		Horizon    string             `json:"horizon"`
		Ts         int64              `json:"ts"`
		Score      int                `json:"score"`
		CurvePct   float64            `json:"curvePct"`
		Edge       float64            `json:"edge"`
		EdgeLine   string             `json:"edgeLine"`
		CalProb    float64            `json:"calProb"`
		Factors    []composite.Factor `json:"factors"`
		Ledger     composite.Ledger   `json:"ledger"`
		CurveNote  string             `json:"curveNote"`
		TrackLabel string             `json:"trackLabel"`
	}
	if code := s8Get(t, srv.URL+"/api/composite?symbol=NVDA&market=stocks", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if !body.Available || body.Score != 8 || body.Ts != now || body.CurvePct != 78.3 {
		t.Fatalf("latest row: %+v", body)
	}
	if len(body.Factors) != 13 {
		t.Fatalf("factor tiles = %d, want 13", len(body.Factors))
	}
	if len(body.Ledger.Entries) == 0 || body.Ledger.Method == "" {
		t.Fatalf("ledger = %+v", body.Ledger)
	}
	// Danelfin-style edge framing with real numbers.
	if !strings.Contains(body.EdgeLine, "56.3%") || !strings.Contains(body.EdgeLine, "+6.3pp") {
		t.Fatalf("edgeLine = %q", body.EdgeLine)
	}
	if !strings.Contains(body.CurveNote, "not a probability") ||
		!strings.Contains(body.TrackLabel, "not a live track record") {
		t.Fatalf("honesty labels: curveNote=%q trackLabel=%q", body.CurveNote, body.TrackLabel)
	}

	// Unknown symbol → 404.
	if code := s8Get(t, srv.URL+"/api/composite?symbol=ZZZZ&market=stocks", &absent); code != 404 {
		t.Fatalf("unknown symbol status = %d", code)
	}
}

func TestCompositeTopEndpoint(t *testing.T) {
	srv, st := newCompositeServer(t)
	ctx := context.Background()
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")

	startOfToday := time.Now().UTC().Truncate(24 * time.Hour).Unix()
	yesterday := startOfToday - 12*3600
	today := startOfToday + 3600

	// Yesterday: AAPL #1, NVDA #2. Today: NVDA #1, AAPL #2, BTC new.
	seedCompositeRow(t, st, ctx, aapl.ID, yesterday, 9, 90, 0.56)
	seedCompositeRow(t, st, ctx, nvda.ID, yesterday, 5, 50, 0.51)
	seedCompositeRow(t, st, ctx, nvda.ID, today, 10, 98, 0.58)
	seedCompositeRow(t, st, ctx, aapl.ID, today, 6, 60, 0.52)
	seedCompositeRow(t, st, ctx, btc.ID, today, 7, 70, 0.53)

	var body struct {
		Rows []struct {
			Symbol     string `json:"symbol"`
			Score      int    `json:"score"`
			Rank       int    `json:"rank"`
			PrevRank   *int   `json:"prevRank"`
			RankChange *int   `json:"rankChange"`
		} `json:"rows"`
		N         int    `json:"n"`
		Total     int    `json:"total"`
		CurveNote string `json:"curveNote"`
		RankNote  string `json:"rankNote"`
	}
	if code := s8Get(t, srv.URL+"/api/composite/top", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.N != 3 || body.Total != 3 {
		t.Fatalf("counts = %+v", body)
	}
	if body.Rows[0].Symbol != "NVDA" || body.Rows[0].Rank != 1 ||
		body.Rows[1].Symbol != "BTC/USD" || body.Rows[2].Symbol != "AAPL" {
		t.Fatalf("order = %+v", body.Rows)
	}
	// NVDA moved #2 -> #1 (+1); AAPL #1 -> #3 (-2); BTC has NO previous rank.
	nv, ap, bc := body.Rows[0], body.Rows[2], body.Rows[1]
	if nv.PrevRank == nil || *nv.PrevRank != 2 || nv.RankChange == nil || *nv.RankChange != 1 {
		t.Fatalf("NVDA rank change = %+v", nv)
	}
	if ap.PrevRank == nil || *ap.PrevRank != 1 || ap.RankChange == nil || *ap.RankChange != -2 {
		t.Fatalf("AAPL rank change = %+v", ap)
	}
	if bc.PrevRank != nil || bc.RankChange != nil {
		t.Fatalf("BTC must carry null prev rank (absent yesterday): %+v", bc)
	}
	if !strings.Contains(body.RankNote, "never a fabricated change") {
		t.Fatalf("rankNote = %q", body.RankNote)
	}

	// Market filter + limit: ranks come from the FULL set, then truncate.
	if code := s8Get(t, srv.URL+"/api/composite/top?market=stocks&limit=1", &body); code != 200 {
		t.Fatalf("filtered status = %d", code)
	}
	if body.N != 1 || body.Total != 2 || body.Rows[0].Symbol != "NVDA" || body.Rows[0].Rank != 1 {
		t.Fatalf("filtered = %+v", body)
	}
}

// TestShortVolCaveatVerbatim ties the composite tile's caveat to the
// /api/shorts one byte-for-byte — the verbatim-caveat contract.
func TestShortVolCaveatVerbatim(t *testing.T) {
	if composite.ShortVolCaveat != shortsCaveat {
		t.Fatalf("composite.ShortVolCaveat drifted from api/shorts.go:\n%q\n%q",
			composite.ShortVolCaveat, shortsCaveat)
	}
}
