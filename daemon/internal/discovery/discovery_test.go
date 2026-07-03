package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── pure budget math ─────────────────────────────────────────────────────

func TestAutoAddBudget(t *testing.T) {
	cases := []struct {
		name                                     string
		active, cap, addsToday, dailyLimit, want int
	}{
		{"plenty of room", 5, 30, 0, 2, 2},
		{"cap tighter than day", 29, 30, 0, 2, 1},
		{"at cap", 30, 30, 0, 2, 0},
		{"over cap", 31, 30, 0, 2, 0},
		{"daily limit reached", 5, 30, 2, 2, 0},
		{"one add left today", 5, 30, 1, 2, 1},
		{"both exhausted", 30, 30, 2, 2, 0},
	}
	for _, c := range cases {
		if got := AutoAddBudget(c.active, c.cap, c.addsToday, c.dailyLimit); got != c.want {
			t.Errorf("%s: AutoAddBudget(%d,%d,%d,%d)=%d want %d",
				c.name, c.active, c.cap, c.addsToday, c.dailyLimit, got, c.want)
		}
	}
}

func TestPickAutoAdds(t *testing.T) {
	cands := []store.Candidate{
		{Symbol: "ONE_SWEEP", Status: "new", SeenCount: 1, DollarVol: 9e9},   // flashy spike: excluded
		{Symbol: "BIG", Status: "new", SeenCount: 3, DollarVol: 5e9},
		{Symbol: "MID", Status: "new", SeenCount: 2, DollarVol: 2e9},
		{Symbol: "SMALL", Status: "new", SeenCount: 4, DollarVol: 1e8},
		{Symbol: "DISMISSED", Status: "dismissed", SeenCount: 9, DollarVol: 8e9}, // excluded
		{Symbol: "ADDED", Status: "added", SeenCount: 9, DollarVol: 7e9},         // excluded
	}

	// seen>=2 rule + dollar-vol ranking + budget cap.
	picks := PickAutoAdds(cands, 2, MinSweeps)
	if len(picks) != 2 || picks[0].Symbol != "BIG" || picks[1].Symbol != "MID" {
		t.Fatalf("picks: %+v", picks)
	}

	// Budget zero (cap reached) → nothing, no matter how good the candidates.
	if got := PickAutoAdds(cands, 0, MinSweeps); len(got) != 0 {
		t.Fatalf("cap-reached picks: %+v", got)
	}
	if got := PickAutoAdds(cands, -1, MinSweeps); len(got) != 0 {
		t.Fatalf("negative budget picks: %+v", got)
	}

	// Budget larger than eligible set → all eligible, still ranked.
	all := PickAutoAdds(cands, 10, MinSweeps)
	if len(all) != 3 || all[0].Symbol != "BIG" || all[1].Symbol != "MID" || all[2].Symbol != "SMALL" {
		t.Fatalf("all-eligible picks: %+v", all)
	}
}

func TestFmtDollarVol(t *testing.T) {
	for v, want := range map[float64]string{
		1.23e9: "$1.2B",
		3.4e8:  "$340M",
		9.5e3:  "$9.5K",
		12:     "$12",
		2.1e12: "$2.1T",
	} {
		if got := FmtDollarVol(v); got != want {
			t.Errorf("FmtDollarVol(%g)=%q want %q", v, got, want)
		}
	}
}

// ── worker with a fake Alpaca screener server ────────────────────────────

type fakeAlpaca struct {
	actives       []ActiveRow
	gainers       []MoverRow
	losers        []MoverRow
	prices        map[string]float64
	activesStatus int // 0 → 200
	moversStatus  int
}

func (f *fakeAlpaca) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1beta1/screener/stocks/most-actives", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("APCA-API-KEY-ID") == "" {
			http.Error(w, "no auth", 403)
			return
		}
		if r.URL.Query().Get("by") != "volume" {
			http.Error(w, "want by=volume", 400)
			return
		}
		if f.activesStatus != 0 {
			http.Error(w, "nope", f.activesStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"most_actives": f.actives})
	})
	mux.HandleFunc("GET /v1beta1/screener/stocks/movers", func(w http.ResponseWriter, r *http.Request) {
		if f.moversStatus != 0 {
			http.Error(w, "nope", f.moversStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"gainers": f.gainers, "losers": f.losers})
	})
	mux.HandleFunc("GET /v2/stocks/trades/latest", func(w http.ResponseWriter, r *http.Request) {
		trades := map[string]any{}
		for _, sym := range strings.Split(r.URL.Query().Get("symbols"), ",") {
			if p, ok := f.prices[sym]; ok {
				trades[sym] = map[string]float64{"p": p}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"trades": trades})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "disc.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newWorker wires a Worker at the fake server with a Subscribe that upserts
// straight into the store (the real subscribe path minus Alpaca validation).
// Like the real subscribe, promotion puts a stock in the STREAMED hot set
// (stream=1), which is what the stream-cap budget counts.
func newWorker(t *testing.T, st *store.Store, f *fakeAlpaca) *Worker {
	t.Helper()
	srv := f.server(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	return &Worker{
		St:     st,
		Client: &Client{Key: "k", Secret: "s", Base: srv.URL, HTTP: srv.Client()},
		Subscribe: func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
			sym, err := st.UpsertSymbol(ctx, symbol, market, "")
			if err == nil && market == md.Stocks {
				_ = st.SetSymbolStream(ctx, sym.ID, true)
				sym.Stream = true
			}
			return sym, err
		},
		NowFn: func() time.Time { return now },
	}
}

func TestWorkerSkipsWithoutClient(t *testing.T) {
	w := &Worker{St: testStore(t)}
	detail, err := w.Run(context.Background())
	if err != nil || !strings.Contains(detail, "skipped") {
		t.Fatalf("no-key run: %q, %v", detail, err)
	}
}

func TestWorkerNameAndInterval(t *testing.T) {
	w := &Worker{}
	if w.Name() != "universe-discovery" || w.Interval() != 6*time.Hour {
		t.Fatalf("worker spec: %s %v", w.Name(), w.Interval())
	}
}

func TestTwoSweepsPromoteACandidate(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, err := st.CreateUser(ctx, "admin", "hash", true); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	f := &fakeAlpaca{
		actives: []ActiveRow{{Symbol: "COIN", Volume: 4e6}, {Symbol: "PLTR", Volume: 1e6}},
		gainers: []MoverRow{{Symbol: "COIN", PercentChange: 6.5, Price: 300}},
		prices:  map[string]float64{"COIN": 300, "PLTR": 25},
	}
	w := newWorker(t, st, f)

	// Sweep 1: candidates recorded, nothing auto-added (seen only once).
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 1: %v (%s)", err, detail)
	}
	if !strings.Contains(detail, "auto-added 0") {
		t.Fatalf("sweep 1 detail: %q", detail)
	}
	cands, _ := st.Candidates(ctx, "new")
	if len(cands) != 2 {
		t.Fatalf("candidates after sweep 1: %+v", cands)
	}
	// COIN dollar vol = 4e6 shares * $300 = $1.2B; pct from movers.
	if c := cands[0]; c.Symbol != "COIN" || c.DollarVol != 1.2e9 || c.PctChange != 6.5 {
		t.Fatalf("COIN row: %+v", c)
	}

	// Sweep 2: seen twice → best candidates auto-added under the daily limit.
	detail, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !strings.Contains(detail, "auto-added 2") {
		t.Fatalf("sweep 2 detail: %q", detail)
	}
	for _, sym := range []string{"COIN", "PLTR"} {
		s, err := st.GetSymbol(ctx, sym, md.Stocks)
		if err != nil || !s.Active {
			t.Fatalf("%s not active after auto-add: %+v %v", sym, s, err)
		}
		// Auto-added onto the admin watchlist.
		n, _ := st.SymbolWatcherCount(ctx, s.ID)
		if n != 1 {
			t.Fatalf("%s watchers: %d want 1 (admin)", sym, n)
		}
	}
	added, _ := st.Candidates(ctx, "added")
	if len(added) != 2 {
		t.Fatalf("added candidates: %+v", added)
	}
	// Insight written explaining why.
	ins, _ := st.RecentInsights(ctx, 0, 10)
	if len(ins) != 2 || !strings.Contains(ins[0].Body, "universe-discovery added") ||
		!strings.Contains(ins[0].Body, "sweeps") {
		t.Fatalf("insights: %+v", ins)
	}
	// Daily counter persisted.
	if n := AutoAddsToday(ctx, st, w.now()); n != 2 {
		t.Fatalf("autoAddsToday: %d want 2", n)
	}

	// Sweep 3: daily limit (2) exhausted → no more adds today, even though
	// candidates keep accruing sightings.
	f.actives = append(f.actives, ActiveRow{Symbol: "HOOD", Volume: 9e6})
	f.prices["HOOD"] = 100
	if _, err = w.Run(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if _, err = w.Run(ctx); err != nil { // HOOD now seen twice
		t.Fatalf("run 4: %v", err)
	}
	if s, err := st.GetSymbol(ctx, "HOOD", md.Stocks); err == nil && s.Active {
		t.Fatalf("HOOD auto-added past the daily limit")
	}

	// Next day the budget refreshes and HOOD (seen >= 2, ranked by $vol) lands.
	w.NowFn = func() time.Time { return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC) }
	if _, err = w.Run(ctx); err != nil {
		t.Fatalf("run next day: %v", err)
	}
	if s, err := st.GetSymbol(ctx, "HOOD", md.Stocks); err != nil || !s.Active {
		t.Fatalf("HOOD not auto-added next day: %v", err)
	}
}

func TestAutoAddRespectsSymbolCap(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	// Fill the STREAMED hot set to one below a tiny cap (the cap governs the
	// streamed set, so the seed must be stream=1).
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.SetSymbolStream(ctx, spy.ID, true); err != nil {
		t.Fatalf("seed stream flag: %v", err)
	}
	f := &fakeAlpaca{
		actives: []ActiveRow{{Symbol: "AAA", Volume: 1e6}, {Symbol: "BBB", Volume: 2e6}},
		prices:  map[string]float64{"AAA": 10, "BBB": 10},
	}
	w := newWorker(t, st, f)
	w.Cap = 2 // 1 streamed → room for exactly one more

	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	// Only ONE add fits under the cap, and it must be the higher dollar-vol one.
	if !strings.Contains(detail, "auto-added 1") {
		t.Fatalf("sweep 2 detail: %q", detail)
	}
	if s, err := st.GetSymbol(ctx, "BBB", md.Stocks); err != nil || !s.Active {
		t.Fatalf("BBB (higher $vol) not added: %v", err)
	}
	if s, err := st.GetSymbol(ctx, "AAA", md.Stocks); err == nil && s.Active {
		t.Fatalf("AAA added past the cap")
	}
	if n, _ := st.StreamedSymbolCount(ctx); n != 2 {
		t.Fatalf("streamed count: %d want 2 (== cap)", n)
	}
}

func TestSweepSkipsAlreadyActiveSymbols(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f := &fakeAlpaca{
		actives: []ActiveRow{{Symbol: "NVDA", Volume: 9e7}, {Symbol: "AMD", Volume: 3e7}},
		prices:  map[string]float64{"NVDA": 150, "AMD": 120},
	}
	w := newWorker(t, st, f)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	cands, _ := st.Candidates(ctx, "")
	if len(cands) != 1 || cands[0].Symbol != "AMD" {
		t.Fatalf("active symbol leaked into candidates: %+v", cands)
	}
}

func TestSweepFallsBackWhenOneEndpoint404s(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	// most-actives 404s → movers-only sweep still records candidates.
	f := &fakeAlpaca{
		activesStatus: 404,
		gainers:       []MoverRow{{Symbol: "GME", PercentChange: 22, Price: 40}},
		losers:        []MoverRow{{Symbol: "BBBY", PercentChange: -18, Price: 2}},
	}
	w := newWorker(t, st, f)
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("movers-only run: %v", err)
	}
	cands, _ := st.Candidates(ctx, "new")
	if len(cands) != 2 {
		t.Fatalf("movers-only candidates: %+v", cands)
	}
	for _, c := range cands {
		if c.Symbol == "BBBY" && c.PctChange != -18 {
			t.Fatalf("loser pct not recorded: %+v", c)
		}
	}

	// Both endpoints down → the sweep errors visibly (no silent no-op).
	f.moversStatus = 500
	if _, err := w.Run(ctx); err == nil {
		t.Fatalf("both-down run must fail")
	}
}

func TestSubscribeFailureLeavesCandidateNew(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	f := &fakeAlpaca{
		actives: []ActiveRow{{Symbol: "JUNK", Volume: 1e6}},
		prices:  map[string]float64{"JUNK": 5},
	}
	w := newWorker(t, st, f)
	w.Subscribe = func(ctx context.Context, symbol string, market md.Market) (md.Symbol, error) {
		return md.Symbol{}, fmt.Errorf("%q is not an active US equity on Alpaca", symbol)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	detail, err := w.Run(ctx) // seen twice → eligible, but subscribe rejects
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !strings.Contains(detail, "auto-added 0") {
		t.Fatalf("detail: %q", detail)
	}
	cands, _ := st.Candidates(ctx, "new")
	if len(cands) != 1 || cands[0].Symbol != "JUNK" {
		t.Fatalf("failed add should stay 'new': %+v", cands)
	}
	// The failed attempt must not burn the daily budget.
	if n := AutoAddsToday(ctx, st, w.now()); n != 0 {
		t.Fatalf("failed add burned budget: %d", n)
	}
}

func TestSymbolCapEnv(t *testing.T) {
	t.Setenv("SIGNALDECK_SYMBOL_CAP", "")
	if got := SymbolCap(); got != DefaultSymbolCap {
		t.Fatalf("default cap: %d", got)
	}
	t.Setenv("SIGNALDECK_SYMBOL_CAP", "45")
	if got := SymbolCap(); got != 45 {
		t.Fatalf("env cap: %d", got)
	}
	t.Setenv("SIGNALDECK_SYMBOL_CAP", "junk")
	if got := SymbolCap(); got != DefaultSymbolCap {
		t.Fatalf("malformed cap: %d", got)
	}
	t.Setenv("SIGNALDECK_SYMBOL_CAP", "-3")
	if got := SymbolCap(); got != DefaultSymbolCap {
		t.Fatalf("negative cap: %d", got)
	}
}
