package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// prevSessionTs is the ET-midnight stamp of the last trading session before
// now's session date: always inside the journal's recency window
// (memberjournal.PickableSince), and never a call's entry session, so a
// call made now can still be withdrawn.
func prevSessionTs(now time.Time) int64 {
	d := marketcal.SessionDate(now)
	for {
		d = d.AddDate(0, 0, -1)
		if marketcal.IsTradingDay(d) {
			return d.Unix()
		}
	}
}

// TestJournalPickerOffersWhatTheJournalAccepts: the journal's picker (GET
// /api/journal/symbols) and POST /api/journal share one store predicate
// (JournalSymbols / JournalSymbolByTicker), so a member can pick the ETFs the
// daemon has daily bars for, which the SEC directory behind /api/companies
// never lists, and the server accepts exactly what the picker offers. A tape
// that stopped (no daily bar in the last ~10 sessions: a delisted ticker) is
// neither offered nor accepted.
func TestJournalPickerOffersWhatTheJournalAccepts(t *testing.T) {
	ctx := t.Context()
	srv, st, mb := startPublished(t, nil, true, func(d Deps) http.Handler {
		lim := newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst)
		return d.httpServerWith(d.routes(lim), lim).Handler
	})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	recent := prevSessionTs(time.Now())
	const staleTs = 1_699_920_000 // 2023: far outside the ~10-session window
	bar := func(s md.Symbol, tf md.Timeframe, ts int64) {
		must(st.UpsertBars(ctx, []md.Bar{{SymbolID: s.ID, TF: tf, Ts: ts, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}}))
	}
	sym := func(ticker, name string, tfs ...md.Timeframe) md.Symbol {
		s, err := st.UpsertSymbol(ctx, ticker, md.Stocks, name)
		must(err)
		for _, tf := range tfs {
			bar(s, tf, recent)
		}
		return s
	}
	bar(sym("STALE", "Stale Tape Inc"), md.TF1d, staleTs) // tape stopped: a delisted ticker
	sym("SPY", "SPDR S&P 500 ETF Trust", md.TF1d) // an ETF: no SEC directory row
	sym("AAPL", "AAPL", md.TF1d)
	sym("NOBAR", "No Bars Corp")
	sym("HOURLY", "Hourly Only Inc", md.TF1h)
	must(st.SetSymbolActive(ctx, sym("GONE", "Gone Inc", md.TF1d).ID, false))
	must(st.UpsertCompanies(ctx, []store.CompanyRow{{CIK: 320193, Ticker: "AAPL", Name: "Apple Inc."},
		{CIK: 1, Ticker: "NOBAR", Name: "No Bars Corp"}}))

	if code, _ := getAs(t, newClient(t), srv.URL+"/api/journal/symbols?q=spy"); code != http.StatusUnauthorized {
		t.Errorf("anonymous picker: %d, want 401", code)
	}
	mira := signupVerified(t, srv, mb, "mira", "mira@gmail.com")
	nils := signupVerified(t, srv, mb, "nils", "nils@gmail.com")

	// Why the picker moved: the SEC directory has no ETFs.
	if code, body := getAs(t, mira, srv.URL+"/api/companies?q=SPY"); code != 200 || strings.Contains(body, `"ticker":"SPY"`) {
		t.Errorf("/api/companies?q=SPY: %d %.300s, want 200 without SPY", code, body)
	}
	type pick struct{ Symbol, Name string }
	picker := func(q string) []pick {
		t.Helper()
		code, body := getAs(t, mira, srv.URL+"/api/journal/symbols?q="+q)
		if code != 200 || strings.Contains(body, `"close"`) || strings.Contains(body, `"price"`) {
			t.Fatalf("picker %q: %d %.300s", q, code, body)
		}
		var v struct {
			Symbols []pick `json:"symbols"`
		}
		must(json.Unmarshal([]byte(body), &v))
		if v.Symbols == nil {
			t.Fatalf("picker %q: symbols is null, want a list: %s", q, body)
		}
		return v.Symbols
	}
	if got := picker("spy"); len(got) == 0 || got[0] != (pick{"SPY", "SPDR S&P 500 ETF Trust"}) {
		t.Errorf("picker spy: %+v, want SPY first", got)
	}
	if got := picker("apple"); len(got) != 1 || got[0] != (pick{"AAPL", "Apple Inc."}) {
		t.Errorf("picker apple: %+v, want only AAPL as Apple Inc.", got)
	}
	for _, q := range []string{"nobar", "hourly", "gone", "stale"} {
		if got := picker(q); len(got) != 0 {
			t.Errorf("picker %s: %+v, want nothing (no recent daily bar, or inactive)", q, got)
		}
	}

	call := func(c *http.Client, symbol, dir string, horizon int) (int, string) {
		t.Helper()
		resp := postJSON(t, c, srv.URL+"/api/journal",
			map[string]any{"symbol": symbol, "market": "stocks", "call": dir, "horizon": horizon, "note": ""})
		return resp.StatusCode, drain(t, resp)
	}
	if code, body := call(mira, "spy", "up", 5); code != 200 {
		t.Errorf("a call on the ETF the picker offered: %d %.300s", code, body)
	}
	for _, s := range []string{"NOBAR", "HOURLY", "GONE", "STALE", "ZZZZ"} {
		if code, body := call(mira, s, "up", 5); code != 404 || !strings.Contains(body, "unknown symbol") {
			t.Errorf("a call on %s: %d %.300s, want 404 unknown symbol", s, code, body)
		}
	}
	// Everything the picker offers is accepted (a second member: mira's
	// daily cap is not what is being tested).
	offered := map[string]bool{}
	for _, q := range []string{"a", "s", "n", "h", "g"} {
		for _, p := range picker(q) {
			offered[p.Symbol] = true
		}
	}
	if len(offered) != 2 {
		t.Errorf("picker offered %v over the sweep, want SPY and AAPL", offered)
	}
	for s := range offered {
		if code, body := call(nils, s, "down", 1); code != 200 {
			t.Errorf("the picker offered %s but the journal refused it: %d %.300s", s, code, body)
		}
	}
}
