package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// /api/paper published an equity curve and a Sharpe with no statement about
// whether the fills under them still reconcile against the bars they were
// priced from. On the live database 21 of 44 did not (max 90.3 bps): `bars` is
// written INSERT OR REPLACE, so a provider revision rewrites the reference
// price of a past fill and the trade log silently stops being re-derivable.
// The route must now carry that verdict.

// seedPaperFill writes one daily bar and one paper fill at that bar's ts, with
// fillPx as the recorded price. A fillPx that differs from open is exactly the
// live defect.
func seedPaperFill(t *testing.T, st *store.Store, symbol string, ts int64, open, fillPx float64) {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, symbol, md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: sym.ID, TF: md.TF1d, Ts: ts,
		Open: open, High: open * 1.02, Low: open * 0.98, Close: open, Volume: 100_000,
	}}); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}
	if _, err := st.InitPaperBook(ctx, "flagship-1d", 100_000, ts-86400); err != nil {
		t.Fatalf("init book: %v", err)
	}
	applied, err := st.ApplyPaperStep(ctx, store.PaperApply{
		Strategy: "flagship-1d", BarTs: ts, NewCash: 90_000,
		Trades: []store.PaperTrade{{
			Strategy: "flagship-1d", SymbolID: sym.ID, Side: "buy",
			Qty: 10, Px: fillPx, Cost: 1, Ts: ts, Reason: "test",
		}},
		EquityTs: ts, EquityCash: 90_000, EquityPositionsValue: 10_000, EquityValue: 100_000,
	})
	if err != nil || !applied {
		t.Fatalf("apply paper step: applied=%v err=%v", applied, err)
	}
}

func fetchPaper(t *testing.T, srv *httptest.Server) map[string]any {
	t.Helper()
	resp, err := newClient(t).Get(srv.URL + "/api/paper?strategy=flagship-1d")
	if err != nil {
		t.Fatalf("GET /api/paper: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// TestPaperAPI_FlagsFillsThatDoNotReproduce is the live case, in miniature: a
// fill recorded at 103.92 against a stored open of 102.99 (+90.3 bps, the worst
// live discrepancy). The payload must say the book is not verified and how far
// off it is.
func TestPaperAPI_FlagsFillsThatDoNotReproduce(t *testing.T) {
	srv, st := newPaperServer(t, nil)
	seedPaperFill(t, st, "ACGL", 1783396800, 102.99, 103.92)

	body := fetchPaper(t, srv)
	if v, _ := body["verified"].(bool); v {
		t.Error("verified=true for a fill that does not reproduce from its bar")
	}
	raw, err := json.Marshal(body["fillFidelity"])
	if err != nil {
		t.Fatalf("marshal fillFidelity: %v", err)
	}
	var f papertrade.Fidelity
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode fillFidelity: %v", err)
	}
	if f.Fills != 1 || f.Mismatched != 1 || f.Matched != 0 {
		t.Errorf("counts = fills %d matched %d mismatched %d; want 1/0/1", f.Fills, f.Matched, f.Mismatched)
	}
	if f.MaxAbsBps < 90 || f.MaxAbsBps > 91 {
		t.Errorf("MaxAbsBps=%.2f, want ~90.3", f.MaxAbsBps)
	}
	if f.Reason == "" {
		t.Error("an unverified book must carry a reason a reader can act on")
	}
}

// TestPaperAPI_VerifiesAReconcilingLog: the fix's other half — a fill written
// AT the bar open reconciles, and the route says so rather than staying silent.
func TestPaperAPI_VerifiesAReconcilingLog(t *testing.T) {
	srv, st := newPaperServer(t, nil)
	seedPaperFill(t, st, "AAPL", 1784865600, 321.79, 321.79)

	body := fetchPaper(t, srv)
	if v, _ := body["verified"].(bool); !v {
		t.Fatalf("verified=false for a log whose fill equals its bar open: %v", body["fillFidelity"])
	}
}

// An empty book must not report itself verified: "no fills yet" is not "every
// fill checks out", and a fresh book claiming a clean audit is the flattering
// version of the same silence.
func TestPaperAPI_EmptyBookIsNotVerified(t *testing.T) {
	srv, _ := newPaperServer(t, nil)
	body := fetchPaper(t, srv)
	if v, _ := body["verified"].(bool); v {
		t.Error("verified=true for a book with no fills")
	}
}
