package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSymbolFromQuery_UnknownSymbolLeaksNoSQLInternals pins the fix for the
// raw database/sql strings that reached clients from 35 GET routes.
//
// symbolFromQuery returned store.GetSymbol's error unmodified, and GetSymbol
// hands back database/sql's own sentinel. Every one of its 36 callers but one
// answers httpErr(w, 404, err.Error()), so a request for an absent symbol
// published `{"error":"sql: no rows in result set"}` -- observed live on
// GET /api/ledger?symbol=BTC before this change. On the published surface
// /api/ledger is anonymous, so that named the storage engine and the access
// pattern to anyone who asked.
//
// Asserted on /api/ledger because it is the route the leak was actually
// observed on; the guard lives in the shared helper, so the other 34 are
// covered by the same change.
func TestSymbolFromQuery_UnknownSymbolLeaksNoSQLInternals(t *testing.T) {
	srv, _ := newLedgerServer(t, nil)

	res := ledgerGet(t, srv, "/api/ledger?symbol=NOSUCHTICKER&market=stocks&horizon=1d")
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %v, want %v", res.StatusCode, http.StatusNotFound)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The specific string that was shipping, and the general shape of it.
	for _, leak := range []string{"sql:", "no rows in result set", "database/sql", "sqlite"} {
		if strings.Contains(strings.ToLower(body.Error), strings.ToLower(leak)) {
			t.Fatalf("error names internals (%q): %q", leak, body.Error)
		}
	}
	// And it must still ANSWER the question the caller asked. A guard that
	// replaced the leak with an empty or generic string would pass the checks
	// above while making the endpoint less usable than the bug.
	if !strings.Contains(body.Error, "NOSUCHTICKER") || !strings.Contains(body.Error, "unknown symbol") {
		t.Fatalf("error should name the unknown symbol, got %q", body.Error)
	}
}

// TestSymbolFromQuery_MissingParamsStillExplainThemselves guards the other
// branch: a malformed request predates the store lookup and its message was
// always safe, so the new switch must not have swallowed it.
func TestSymbolFromQuery_MissingParamsStillExplainThemselves(t *testing.T) {
	srv, _ := newLedgerServer(t, nil)

	res := ledgerGet(t, srv, "/api/ledger?symbol=&market=")
	defer res.Body.Close() //nolint:errcheck
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(body.Error, "need symbol=") {
		t.Fatalf("got = %q, want the parameter-usage message", body.Error)
	}
}
