package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/congress"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// congressFixture loads a fixture from the congress package's testdata
// (single source of truth for mirror-shaped JSON).
func congressFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "ingest", "congress", "testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func openCongressStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "congress_p.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newCongressMirror serves the two fixtures; senateUp/houseUp=false simulate
// the dead-mirror 403 the real endpoints return today.
func newCongressMirror(t *testing.T, senateUp, houseUp bool) *congress.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/senate"):
			if !senateUp {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
				return
			}
			_, _ = w.Write(congressFixture(t, "senate_transactions.json"))
		case strings.HasPrefix(r.URL.Path, "/house"):
			if !houseUp {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
				return
			}
			_, _ = w.Write(congressFixture(t, "house_transactions.json"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := congress.New()
	c.SenateURL = srv.URL + "/senate.json"
	c.HouseURL = srv.URL + "/house.json"
	c.MinInterval = time.Millisecond
	return c
}

func TestCongressPollerIngestMapsAndDedups(t *testing.T) {
	st := openCongressStore(t)
	ctx := context.Background()
	aapl, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc.")
	_, _ = st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin") // must NOT match stock tickers

	w := &CongressPoller{St: st, Client: newCongressMirror(t, true, true)}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v (%s)", err, msg)
	}
	// Fixtures: senate 3 usable rows + house 2 usable rows = 5 new.
	rows, err := st.CongressTrades(ctx, "", "", "", 0)
	if err != nil || len(rows) != 5 {
		t.Fatalf("rows = %d err=%v", len(rows), err)
	}

	// Ticker mapping: AAPL (tracked) → symbol_id set; ZZTOP/NVDA/BA/TSLA
	// (untracked stocks) → NULL, raw ticker text preserved.
	bySym := map[string]store.CongressTradeRow{}
	for _, r := range rows {
		bySym[r.Symbol] = r
	}
	if r := bySym["AAPL"]; r.SymbolID == nil || *r.SymbolID != aapl.ID {
		t.Errorf("AAPL not mapped: %+v", r)
	}
	for _, sym := range []string{"ZZTOP", "NVDA", "BA", "TSLA"} {
		if r := bySym[sym]; r.SymbolID != nil {
			t.Errorf("%s should stay unmapped (honest NULL): %+v", sym, r)
		}
	}
	// Buy/sell vocabulary survived normalization.
	if bySym["AAPL"].TxType != "purchase" || bySym["ZZTOP"].TxType != "sale_full" {
		t.Errorf("txType mapping: %+v / %+v", bySym["AAPL"], bySym["ZZTOP"])
	}

	// Second run over the SAME cumulative dumps: zero new (hash dedup).
	msg, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(msg, "senate 0 new") || !strings.Contains(msg, "house 0 new") {
		t.Errorf("second run not idempotent: %q", msg)
	}
	if rows, _ = st.CongressTrades(ctx, "", "", "", 0); len(rows) != 5 {
		t.Errorf("rows after re-run = %d, want 5", len(rows))
	}

	// Status meta reflects a healthy pass.
	raw, _ := st.GetJSONRaw(ctx, "congress_mirror_status")
	if !strings.Contains(raw, `"ok":true`) {
		t.Errorf("status meta = %s", raw)
	}
}

func TestCongressPollerDeadMirrorsDegradeGracefully(t *testing.T) {
	st := openCongressStore(t)
	ctx := context.Background()

	w := &CongressPoller{St: st, Client: newCongressMirror(t, false, false)}
	msg, err := w.Run(ctx)
	// Dead mirrors must NEVER fail the fleet -- but they must not read as
	// SUCCESS either. This asserted err == nil, and the run was therefore filed
	// status=ok on every poll for seven days while congress_trades held zero
	// rows. The requirement is now stricter, not looser: the error must be
	// exactly ErrDegraded, so a plain failure here still fails this test.
	if !errors.Is(err, workers.ErrDegraded) {
		t.Fatalf("dead mirrors must report DEGRADED, not %v", err)
	}
	if !strings.Contains(msg, "unavailable") {
		t.Errorf("message should say the mirrors are down: %q", msg)
	}
	// No rows fabricated.
	if rows, _ := st.CongressTrades(ctx, "", "", "", 0); len(rows) != 0 {
		t.Errorf("fabricated rows: %+v", rows)
	}
	// One dq event per dead chamber.
	dq, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatalf("RecentDQ: %v", err)
	}
	mirrors := 0
	for _, ev := range dq {
		if ev.Kind == "congress_mirror_error" {
			mirrors++
		}
	}
	if mirrors != 2 {
		t.Errorf("dq mirror errors = %d, want 2 (senate + house)", mirrors)
	}
	// Honest status meta: both chambers marked down, with detail.
	raw, _ := st.GetJSONRaw(ctx, "congress_mirror_status")
	if !strings.Contains(raw, `"ok":false`) || !strings.Contains(raw, "403") {
		t.Errorf("status meta should record the outage: %s", raw)
	}
}

func TestCongressPollerPartialOutageStillIngests(t *testing.T) {
	st := openCongressStore(t)
	ctx := context.Background()

	// Senate dead, House alive: the run reports success and ingests house rows.
	w := &CongressPoller{St: st, Client: newCongressMirror(t, false, true)}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("partial outage must not fail: %v", err)
	}
	if !strings.Contains(msg, "house 2 new") {
		t.Errorf("house rows should land despite senate outage: %q", msg)
	}
	rows, _ := st.CongressTrades(ctx, "", "", "senate", 0)
	if len(rows) != 0 {
		t.Errorf("no senate rows expected, got %+v", rows)
	}
	rows, _ = st.CongressTrades(ctx, "", "", "house", 0)
	if len(rows) != 2 {
		t.Errorf("house rows = %d, want 2", len(rows))
	}
}
