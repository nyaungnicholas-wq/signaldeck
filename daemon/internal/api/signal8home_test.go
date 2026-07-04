// Signal8 wave — Stage 4: /api/tape, /api/movers, /api/calendar tests.
// Separate harness file so parallel edits never collide. t.TempDir store only.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/universe"
)

func newHomeServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) {
		c.PublicReads = true
		c.CryptoSymbol = "BTC/USD"
	})
	mux := http.NewServeMux()
	d.registerSignal8Home(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// seedDaily writes two daily closes (prev, last) ending at ts.
func seedDaily(t *testing.T, st *store.Store, id int64, prev, last float64, ts int64) {
	t.Helper()
	err := st.UpsertBars(context.Background(), []md.Bar{
		{SymbolID: id, TF: md.TF1d, Ts: ts - 86400, Open: prev, High: prev, Low: prev, Close: prev},
		{SymbolID: id, TF: md.TF1d, Ts: ts, Open: last, High: last, Low: last, Close: last},
	})
	if err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

type tapeResp struct {
	Items []struct {
		Symbol       string  `json:"symbol"`
		Kind         string  `json:"kind"`
		Market       string  `json:"market"`
		Price        float64 `json:"price"`
		DayChangePct float64 `json:"dayChangePct"`
		HasData      bool    `json:"hasData"`
		Note         string  `json:"note"`
	} `json:"items"`
	Count int    `json:"count"`
	Note  string `json:"note"`
}

func TestTapeEndpoint(t *testing.T) {
	srv, st := newHomeServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	spy, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "SPDR S&P 500 ETF")
	seedDaily(t, st, spy.ID, 100, 110, now) // +10%
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	seedDaily(t, st, btc.ID, 50000, 49000, now) // -2%
	_ = st.InsertMacro(ctx, "VIXCLS", now-2*86400, 20)
	_ = st.InsertMacro(ctx, "VIXCLS", now-86400, 25) // +25%

	var body tapeResp
	if code := s8Get(t, srv.URL+"/api/tape", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	// Full strip: every tape ETF + BTC + VIX, present even without data.
	if body.Count != len(universe.TapeETFs)+2 {
		t.Fatalf("count = %d, want %d", body.Count, len(universe.TapeETFs)+2)
	}
	byKey := map[string]int{}
	for i, it := range body.Items {
		byKey[it.Symbol] = i
	}

	s := body.Items[byKey["SPY"]]
	if !s.HasData || s.Price != 110 || s.DayChangePct < 9.9 || s.DayChangePct > 10.1 {
		t.Errorf("SPY item = %+v", s)
	}
	b := body.Items[byKey["BTC/USD"]]
	if !b.HasData || b.Kind != "crypto" || b.DayChangePct > -1.9 {
		t.Errorf("BTC item = %+v", b)
	}
	v := body.Items[byKey["VIX"]]
	if !v.HasData || v.Price != 25 || v.DayChangePct < 24.9 || !strings.Contains(v.Note, "FRED") {
		t.Errorf("VIX item = %+v", v)
	}
	// Registered-but-empty ETF is honestly present with hasData=false.
	if dia := body.Items[byKey["DIA"]]; dia.HasData {
		t.Errorf("DIA should have no data yet: %+v", dia)
	}
	if !strings.Contains(body.Note, "NOT live quotes") {
		t.Errorf("tape note = %q", body.Note)
	}
}

type moversResp struct {
	Gainers []struct {
		Symbol       string   `json:"symbol"`
		Price        float64  `json:"price"`
		DayChangePct float64  `json:"dayChangePct"`
		Mcap         *float64 `json:"mcap"`
	} `json:"gainers"`
	Losers []struct {
		Symbol       string   `json:"symbol"`
		DayChangePct float64  `json:"dayChangePct"`
		Mcap         *float64 `json:"mcap"`
	} `json:"losers"`
	UniverseN           int     `json:"universeN"`
	MinMcap             float64 `json:"minMcap"`
	UnknownMcapExcluded int     `json:"unknownMcapExcluded"`
	Note                string  `json:"note"`
	McapNote            string  `json:"mcapNote"`
}

func TestMoversEndpoint(t *testing.T) {
	srv, st := newHomeServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	aaa, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "Alpha Corp")
	seedDaily(t, st, aaa.ID, 100, 105, now) // +5%
	bbb, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "Beta Inc")
	seedDaily(t, st, bbb.ID, 100, 97, now) // -3%
	ccc, _ := st.UpsertSymbol(ctx, "CCC", md.Stocks, "Gamma Ltd")
	seedDaily(t, st, ccc.ID, 100, 101, now) // +1%
	// Tape ETF must be excluded from movers even with the biggest move.
	spy, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	seedDaily(t, st, spy.ID, 100, 120, now) // +20% — a basket, not a mover
	// Stale symbol (last bar 30d old) must be skipped.
	ddd, _ := st.UpsertSymbol(ctx, "DDD", md.Stocks, "Dead Co")
	seedDaily(t, st, ddd.ID, 100, 150, now-30*86400)

	// AAA has EDGAR shares → mcap = 2e9 shares × $105 = $210B.
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: aaa.ID, Metric: "SharesOutstanding", Value: 2e9, AsOf: now - 86400, FetchedAt: now,
	})

	var body moversResp
	if code := s8Get(t, srv.URL+"/api/movers?limit=2", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.UniverseN != 3 {
		t.Fatalf("universeN = %d, want 3 (SPY + stale DDD excluded)", body.UniverseN)
	}
	if len(body.Gainers) != 2 || body.Gainers[0].Symbol != "AAA" || body.Gainers[1].Symbol != "CCC" {
		t.Fatalf("gainers = %+v", body.Gainers)
	}
	if len(body.Losers) != 2 || body.Losers[0].Symbol != "BBB" {
		t.Fatalf("losers = %+v", body.Losers)
	}
	if body.Gainers[0].Mcap == nil || *body.Gainers[0].Mcap != 2e9*105 {
		t.Errorf("AAA mcap = %v, want 210e9", body.Gainers[0].Mcap)
	}
	if body.Gainers[1].Mcap != nil {
		t.Errorf("CCC mcap should be nil (EDGAR not covered): %v", *body.Gainers[1].Mcap)
	}
	if !strings.Contains(body.McapNote, "mcap unavailable") {
		t.Errorf("mcapNote = %q", body.McapNote)
	}

	// Min-mcap filter: only AAA has a known mcap ≥ $10B; the two unknown-mcap
	// symbols are excluded AND counted honestly.
	if code := s8Get(t, srv.URL+"/api/movers?minMcap=10000000000", &body); code != 200 {
		t.Fatalf("filter status = %d", code)
	}
	if body.UniverseN != 1 || len(body.Gainers) != 1 || body.Gainers[0].Symbol != "AAA" {
		t.Fatalf("filtered = %+v", body)
	}
	if body.UnknownMcapExcluded != 2 {
		t.Errorf("unknownMcapExcluded = %d, want 2", body.UnknownMcapExcluded)
	}
}

type calendarResp struct {
	Econ []struct {
		Series string  `json:"series"`
		Label  string  `json:"label"`
		Value  float64 `json:"value"`
		Ts     int64   `json:"ts"`
	} `json:"econ"`
	EconNote    string `json:"econNote"`
	EarningsEst []struct {
		Symbol       string `json:"symbol"`
		LastFilingTs int64  `json:"lastFilingTs"`
		EstTs        int64  `json:"estTs"`
		Estimate     bool   `json:"estimate"`
	} `json:"earningsEst"`
	EarningsNote string `json:"earningsNote"`
}

func TestCalendarEndpoint(t *testing.T) {
	srv, st := newHomeServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	_ = st.InsertMacro(ctx, "VIXCLS", now-86400, 18.5)
	_ = st.InsertMacro(ctx, "DGS10", now-86400, 4.2)

	// AAA filed 85d ago → estimated report ≈ +6d from now → IN window, EST.
	aaa, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "Alpha Corp")
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: aaa.ID, Metric: "LatestFilingDate",
		Value: float64(now - 85*86400), AsOf: now - 85*86400, FetchedAt: now,
	})
	// BBB filed 30d ago → estimate ~61d out → OUT of window.
	bbb, _ := st.UpsertSymbol(ctx, "BBB", md.Stocks, "Beta Inc")
	_ = st.UpsertFundamental(ctx, store.FundamentalRow{
		SymbolID: bbb.ID, Metric: "LatestFilingDate",
		Value: float64(now - 30*86400), AsOf: now - 30*86400, FetchedAt: now,
	})

	var body calendarResp
	if code := s8Get(t, srv.URL+"/api/calendar", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(body.Econ) != 2 {
		t.Fatalf("econ = %+v, want VIXCLS + DGS10", body.Econ)
	}
	if body.Econ[0].Series != "VIXCLS" || body.Econ[0].Value != 18.5 || body.Econ[0].Label == "" {
		t.Errorf("econ[0] = %+v", body.Econ[0])
	}
	if !strings.Contains(body.EconNote, "FRED") || !strings.Contains(body.EconNote, "no paid") {
		t.Errorf("econNote = %q", body.EconNote)
	}

	if len(body.EarningsEst) != 1 || body.EarningsEst[0].Symbol != "AAA" {
		t.Fatalf("earningsEst = %+v, want only AAA", body.EarningsEst)
	}
	est := body.EarningsEst[0]
	if !est.Estimate {
		t.Errorf("estimate flag must be true (honesty label)")
	}
	wantEst := (now - 85*86400) + 91*86400
	if est.EstTs != wantEst {
		t.Errorf("estTs = %d, want %d", est.EstTs, wantEst)
	}
	if !strings.Contains(body.EarningsNote, "ESTIMATE ONLY") {
		t.Errorf("earningsNote = %q", body.EarningsNote)
	}
}
