package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// newLedgerServer wires the Stage-3 ledger routes behind the real middleware.
func newLedgerServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, mutate)
	mux := http.NewServeMux()
	d.registerLedger(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

// TestLedgerVerifyEndpoint: after appending a small chain, /api/ledger/verify
// reports intact + the correct count and head; after tampering a row it reports
// intact=false with the broken seq.
func TestLedgerVerifyEndpoint(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	var head string
	for i := 0; i < 5; i++ {
		e, err := st.AppendLedger(ctx, store.LedgerEntry{
			PredictedAt: int64(1000 + i), SymbolID: sym.ID, Horizon: md.H1d, BarTs: int64(i),
			RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		head = e.EntryHash
	}

	res, err := newClient(t).Get(srv.URL + "/api/ledger/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (public read default)", res.StatusCode)
	}
	var body struct {
		Intact      bool   `json:"intact"`
		Count       int64  `json:"count"`
		Head        string `json:"head"`
		BrokenAtSeq *int64 `json:"brokenAtSeq"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Intact || body.Count != 5 || body.Head != head || body.BrokenAtSeq != nil {
		t.Fatalf("intact chain response wrong: %+v (want head %q)", body, head)
	}

	// Tamper with seq 3 (payload mutated, stored hashes untouched), then
	// re-verify with ?full=1 → intact=false at seq 3. The full walk is the
	// deliberate audit path: this tamper preserves every stored hash and the
	// checkpoint anchor, so the incremental default cannot see it — the
	// endpoint discloses exactly that in verifiedNote (cold-load precompute
	// wave; see internal/store/ledgercache.go).
	if _, err := st.DB().ExecContext(ctx, // read pool can exec; a single connection
		`UPDATE prediction_ledger SET raw_prob=raw_prob+1 WHERE seq=3`); err != nil {
		t.Fatal(err)
	}
	res2, err := newClient(t).Get(srv.URL + "/api/ledger/verify?full=1")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close() //nolint:errcheck
	var body2 struct {
		Intact      bool   `json:"intact"`
		BrokenAtSeq *int64 `json:"brokenAtSeq"`
	}
	if err := json.NewDecoder(res2.Body).Decode(&body2); err != nil {
		t.Fatal(err)
	}
	if body2.Intact {
		t.Fatal("verify endpoint reported intact after tamper")
	}
	if body2.BrokenAtSeq == nil || *body2.BrokenAtSeq != 3 {
		t.Fatalf("brokenAtSeq = %v, want 3", body2.BrokenAtSeq)
	}
}

// TestLedgerListEndpoint: /api/ledger returns the committed entries for one
// symbol+horizon, newest first, scoped and limit-honoring.
func TestLedgerListEndpoint(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "Tesla")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.AppendLedger(ctx, store.LedgerEntry{
			PredictedAt: int64(100 + i), SymbolID: sym.ID, Horizon: md.H1d, BarTs: int64(100 + i),
			RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A 1w entry and another-symbol entry that must NOT appear in the 1d query.
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{
		PredictedAt: 200, SymbolID: sym.ID, Horizon: md.H1w, BarTs: 200,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{
		PredictedAt: 300, SymbolID: other.ID, Horizon: md.H1d, BarTs: 300,
		RawProb: 0.5, CalProb: 0.5, FeatureHash: "fh", ModelVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := newClient(t).Get(srv.URL + "/api/ledger?symbol=AAPL&market=stocks&horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Symbol  string              `json:"symbol"`
		Horizon string              `json:"horizon"`
		Count   int                 `json:"count"`
		Entries []store.LedgerEntry `json:"entries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Symbol != "AAPL" || body.Horizon != "1d" {
		t.Errorf("scope wrong: %+v", body)
	}
	if body.Count != 3 || len(body.Entries) != 3 {
		t.Fatalf("count = %d / %d entries, want 3 (1d, AAPL only)", body.Count, len(body.Entries))
	}
	// Newest first.
	if body.Entries[0].BarTs != 102 || body.Entries[2].BarTs != 100 {
		t.Errorf("order wrong: %d..%d, want 102..100", body.Entries[0].BarTs, body.Entries[2].BarTs)
	}
	// Each entry carries its chain fields.
	if body.Entries[0].EntryHash == "" || body.Entries[0].Seq == 0 {
		t.Errorf("entry missing chain fields: %+v", body.Entries[0])
	}

	// Unknown symbol → 404.
	res404, err := newClient(t).Get(srv.URL + "/api/ledger?symbol=NOPE&market=stocks")
	if err != nil {
		t.Fatal(err)
	}
	defer res404.Body.Close() //nolint:errcheck
	if res404.StatusCode != 404 {
		t.Errorf("unknown symbol status = %d, want 404", res404.StatusCode)
	}
}
