package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ledgerListBody is the /api/ledger payload.
type ledgerListBody struct {
	Count          int  `json:"count"`
	Limit          int  `json:"limit"`
	Truncated      bool `json:"truncated"`
	RequestedLimit int  `json:"requestedLimit"`
}

// TestLedger_LimitIsClamped covers the last unbounded ?limit= in the package.
//
// /api/ledger is in publicRoutes, so on a published deployment this parameter
// is anonymous and attacker-controlled. It went straight into a SQLite LIMIT
// with no ceiling: measured against the live daemon, ?limit=100000 returned a
// symbol's entire 2,301-entry history in one response, and that number only
// grows. Every other limit in internal/api already clamps -- ledgerAnchors
// even states the rule in its own doc comment -- so this route was the
// exception rather than the pattern.
func TestLedger_LimitIsClamped(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("symbol: %v", err)
	}
	// One more than the ceiling, so a clamp that is off by one is visible.
	appendLedgerRows(t, st, sym.ID, maxLedgerPerRequest+1, 0.5)

	res := ledgerGet(t, srv, "/api/ledger?symbol=AAPL&market=stocks&horizon=1d&limit=100000")
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %v, want 200", res.StatusCode)
	}
	var got ledgerListBody
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Count != maxLedgerPerRequest {
		t.Fatalf("count = %v, want %v", got.Count, maxLedgerPerRequest)
	}
	if got.Limit != maxLedgerPerRequest {
		t.Fatalf("limit = %v, want %v", got.Limit, maxLedgerPerRequest)
	}
	// The clamp must be VISIBLE. This endpoint exists to be audited; an
	// auditor who asked for the whole chain and silently got a prefix would
	// compute a head hash that disagrees with the published one, with nothing
	// in the response to explain the disagreement.
	if !got.Truncated {
		t.Fatalf("truncated = false, want true when the clamp bit")
	}
	if got.RequestedLimit != 100000 {
		t.Fatalf("requestedLimit = %v, want %v", got.RequestedLimit, 100000)
	}
}

// TestLedger_UnclampedRequestIsNotFlaggedTruncated pins the other side: a
// request the ceiling did not touch must NOT claim truncation, or the flag
// stops meaning anything and an auditor learns to ignore it.
func TestLedger_UnclampedRequestIsNotFlaggedTruncated(t *testing.T) {
	srv, st := newLedgerServer(t, nil)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	if err != nil {
		t.Fatalf("symbol: %v", err)
	}
	appendLedgerRows(t, st, sym.ID, 5, 0.5)

	res := ledgerGet(t, srv, "/api/ledger?symbol=MSFT&market=stocks&horizon=1d&limit=10")
	defer res.Body.Close() //nolint:errcheck
	var got ledgerListBody
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Count != 5 {
		t.Fatalf("count = %v, want 5", got.Count)
	}
	if got.Limit != 10 {
		t.Fatalf("limit = %v, want 10", got.Limit)
	}
	if got.Truncated {
		t.Fatalf("truncated = true on a request the ceiling never touched")
	}
}
