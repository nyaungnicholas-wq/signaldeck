package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Crypto was paused on 2026-10-21 (no licensed crypto data source). Every
// handler that starts a NEW order or analysis must refuse market=crypto for
// every caller with registerSymbol's wording, even though BTC/USD is still a
// known symbol with history. A stock request must not trip the same guard.
func TestCryptoPausedRefusesNewOrdersAndAnalyses(t *testing.T) {
	d, _ := newExportDeps(t)
	if _, err := d.St.UpsertSymbol(context.Background(), "BTC/USD", md.Crypto, "Bitcoin"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		h    http.HandlerFunc
		body string
	}{
		{"paper order", d.paperOrder, `{"symbol":"BTC/USD","market":"crypto","side":"buy","qty":1}`},
		{"backtest", d.backtestRun, `{"symbol":"BTC/USD","market":"crypto","text":"buy when rsi < 30"}`},
		{"risk", d.risk, `{"holdings":[{"symbol":"AAPL","market":"stocks","weight":0.5},{"symbol":"BTC/USD","market":"crypto","weight":0.5}]}`},
		{"portfolio add", d.portfolioAdd, `{"symbol":"BTC/USD","market":"crypto","qty":1}`},
		{"stress run", d.stressRun, `{"scenarios":["flash_crash"],"symbols":["BTC/USD"],"market":"crypto"}`},
	}
	for _, c := range cases {
		r := withUser(httptest.NewRequest("POST", "/", strings.NewReader(c.body)), 7)
		rec := httptest.NewRecorder()
		c.h(rec, r)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), cryptoPaused) {
			t.Errorf("%s: got %d %q, want 422 %q", c.name, rec.Code, strings.TrimSpace(rec.Body.String()), cryptoPaused)
		}
	}

	r := withUser(httptest.NewRequest("POST", "/", strings.NewReader(`{"symbol":"AAPL","market":"stocks","side":"buy","qty":1}`)), 7)
	rec := httptest.NewRecorder()
	d.paperOrder(rec, r)
	if strings.Contains(rec.Body.String(), cryptoPaused) {
		t.Fatalf("a stock order was refused as crypto: %d %q", rec.Code, rec.Body.String())
	}
}
