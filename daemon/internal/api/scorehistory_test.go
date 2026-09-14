package api

import (
	"context"
	"net/http/httptest"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// /api/scores/history ends in writeJSON(w, scores) -- a bare row array that
// echoes neither the horizon nor the window it answered. That is what makes a
// silent substitution here worse than elsewhere: ?horizon=1x used to be coerced
// to 1d and served with nothing in the response saying a different question had
// been answered. An absent horizon still defaults; a supplied one that is not
// recognised is refused, because there is no nearest legal value to clamp to.
func TestScoreHistory_HorizonIsHonouredOrRefused(t *testing.T) {
	d, _ := newExportDeps(t)
	ctx := context.Background()
	if _, err := d.St.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple"); err != nil {
		t.Fatalf("seed symbol: %v", err)
	}

	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/scores/history?symbol=AAPL&market=stocks"+query, nil)
		rec := httptest.NewRecorder()
		d.scoreHistory(rec, r)
		return rec
	}

	for _, tc := range []struct {
		query string
		code  int
		why   string
	}{
		{"", 200, "an absent horizon must keep defaulting to 1d"},
		{"&horizon=1h", 200, "1h is one of the three real horizons"},
		{"&horizon=1d", 200, "1d is one of the three real horizons"},
		{"&horizon=1w", 200, "1w is one of the three real horizons"},
		{"&horizon=1x", 400, "an unrecognised horizon must be refused, not quietly served as 1d"},
		{"&horizon=1D", 400, "horizons are exact tokens; a near miss is still a different question"},
		{"&horizon=daily", 400, "a plausible-looking alias is not one of the three"},
	} {
		rec := get(tc.query)
		if rec.Code != tc.code {
			t.Fatalf("GET /api/scores/history%s = %d, want %d -- %s (body: %s)",
				tc.query, rec.Code, tc.code, tc.why, rec.Body.String())
		}
		if tc.code == 400 && rec.Body.Len() == 0 {
			t.Fatalf("GET /api/scores/history%s refused with an empty body; the caller needs to be "+
				"told which value was rejected and what is accepted", tc.query)
		}
	}

	// The window clamps rather than collapsing, and neither bound is reported in
	// the payload, so this is the only place the behaviour can be observed.
	if rec := get("&days=9999"); rec.Code != 200 {
		t.Fatalf("an over-max window must clamp and answer, not fail: got %d", rec.Code)
	}
}
