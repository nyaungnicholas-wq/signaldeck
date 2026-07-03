package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// openTemp opens a fresh store on a temp-file SQLite DB.
func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestOpen_SchemaIdempotent: opening (and thus applying the schema +
// migrations) twice against the same file must not error.
func TestOpen_SchemaIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idem.db")
	st1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	ctx := context.Background()
	if _, err := st1.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (schema re-apply): %v", err)
	}
	defer st2.Close() //nolint:errcheck
	sym, err := st2.GetSymbol(ctx, "AAPL", md.Stocks)
	if err != nil || sym.Symbol != "AAPL" {
		t.Fatalf("data lost across reopen: %v %+v", err, sym)
	}
}

func TestSymbols_UpsertAndList(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if a.ID == 0 || !a.Active || a.Name != "Apple" {
		t.Fatalf("bad symbol: %+v", a)
	}
	// Re-upsert with empty name keeps old name and same id.
	a2, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if a2.ID != a.ID || a2.Name != "Apple" {
		t.Fatalf("upsert not idempotent: %+v vs %+v", a2, a)
	}

	b, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("upsert crypto: %v", err)
	}
	if err := st.SetSymbolActive(ctx, b.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	all, err := st.ListSymbols(ctx, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("list all: %v, n=%d", err, len(all))
	}
	active, err := st.ListSymbols(ctx, true)
	if err != nil || len(active) != 1 || active[0].Symbol != "AAPL" {
		t.Fatalf("list active: %v, %+v", err, active)
	}
	// Re-upsert reactivates.
	if _, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, ""); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if s, err := st.GetSymbolByID(ctx, b.ID); err != nil || !s.Active {
		t.Fatalf("expected reactivated: %v %+v", err, s)
	}
}

func TestBars_UpsertLastBarsRoundtrip(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")

	// Insert out of order; LastBars must return ascending by ts.
	bars := []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 300, Open: 3, High: 3.5, Low: 2.5, Close: 3.2, Volume: 30},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 100, Open: 1, High: 1.5, Low: 0.5, Close: 1.2, Volume: 10},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 200, Open: 2, High: 2.5, Low: 1.5, Close: 2.2, Volume: 20},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}
	// Idempotent re-write with an update on one row.
	bars[1].Close = 9.9
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("re-upsert bars: %v", err)
	}

	got, err := st.LastBars(ctx, sym.ID, md.TF1d, 10)
	if err != nil {
		t.Fatalf("last bars: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 bars, got %d", len(got))
	}
	for i, wantTs := range []int64{100, 200, 300} {
		if got[i].Ts != wantTs {
			t.Fatalf("bar %d ts=%d want %d (ordering)", i, got[i].Ts, wantTs)
		}
	}
	if got[0].Close != 9.9 {
		t.Fatalf("replace semantics: close=%v want 9.9", got[0].Close)
	}
	// n smaller than available: newest n, still ascending.
	got2, err := st.LastBars(ctx, sym.ID, md.TF1d, 2)
	if err != nil || len(got2) != 2 || got2[0].Ts != 200 || got2[1].Ts != 300 {
		t.Fatalf("last 2 bars wrong: %v %+v", err, got2)
	}
	// Empty upsert is a no-op.
	if err := st.UpsertBars(ctx, nil); err != nil {
		t.Fatalf("empty upsert: %v", err)
	}
}

func TestScores_InsertSeedsOutcomeAndResolve(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")

	sc := md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 1000, Score: 0.42,
		Components: []md.ScoreComponent{{Name: "rsi", Value: 55, Norm: 0.1, Weight: 1, Contrib: 0.1, Note: "n"}},
	}
	if err := st.InsertScore(ctx, sc); err != nil {
		t.Fatalf("insert score: %v", err)
	}

	got, ok, err := st.LatestScore(ctx, sym.ID, md.H1d)
	if err != nil || !ok || got.Score != 0.42 || len(got.Components) != 1 {
		t.Fatalf("latest score: %v ok=%v %+v", err, ok, got)
	}

	// The outcome row must have been seeded, unresolved.
	pend, err := st.UnresolvedOutcomesByHorizon(ctx, md.H1d, 2000, 10)
	if err != nil || len(pend) != 1 || pend[0].Ts != 1000 || pend[0].Score != 0.42 {
		t.Fatalf("seeded outcome: %v %+v", err, pend)
	}

	if err := st.ResolveOutcome(ctx, sym.ID, md.H1d, 1000, 0.017); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	pend, err = st.UnresolvedOutcomesByHorizon(ctx, md.H1d, 2000, 10)
	if err != nil || len(pend) != 0 {
		t.Fatalf("still pending after resolve: %v %+v", err, pend)
	}
	res, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
	if err != nil || len(res) != 1 || res[0].FwdReturn == nil || *res[0].FwdReturn != 0.017 {
		t.Fatalf("resolved outcomes: %v %+v", err, res)
	}
}

func TestMeta_SetGet(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	if v, err := st.GetMeta(ctx, "missing"); err != nil || v != "" {
		t.Fatalf("missing key: %q %v", v, err)
	}
	if err := st.SetMeta(ctx, "k", "v1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := st.SetMeta(ctx, "k", "v2"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if v, err := st.GetMeta(ctx, "k"); err != nil || v != "v2" {
		t.Fatalf("get: %q %v", v, err)
	}
}

func TestIncrAndGetSpend_AtomicAndConcurrent(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if n, err := st.IncrAndGetSpend(ctx, "2026-07-01"); err != nil || n != 1 {
		t.Fatalf("first incr: n=%d err=%v", n, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := st.IncrAndGetSpend(ctx, "2026-07-01"); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent incr: %v", err)
	}
	// 1 initial + 200 concurrent = 201 on the next call = 202.
	if n, err := st.IncrAndGetSpend(ctx, "2026-07-01"); err != nil || n != 202 {
		t.Fatalf("counter not atomic: n=%d (want 202) err=%v", n, err)
	}

	// Day rollover: new key starts at 1 and old day keys are swept.
	if n, err := st.IncrAndGetSpend(ctx, "2026-07-02"); err != nil || n != 1 {
		t.Fatalf("rollover: n=%d err=%v", n, err)
	}
	if v, err := st.GetMeta(ctx, "llm_spend:2026-07-01"); err != nil || v != "" {
		t.Fatalf("old day key not pruned: %q %v", v, err)
	}
	if n, err := st.IncrAndGetSpend(ctx, "2026-07-02"); err != nil || n != 2 {
		t.Fatalf("new day isolation: n=%d err=%v", n, err)
	}
}

func TestUsersSessionsWatchlist(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	uid, err := st.CreateUser(ctx, "alice", "hash", true)
	if err != nil || uid == 0 {
		t.Fatalf("create user: %v id=%d", err, uid)
	}
	if _, err := st.CreateUser(ctx, "alice", "hash2", false); err == nil {
		t.Fatalf("duplicate username must fail")
	}
	u, ok, err := st.GetUserByName(ctx, "alice")
	if err != nil || !ok || u.ID != uid || !u.IsAdmin {
		t.Fatalf("get by name: %v %v %+v", err, ok, u)
	}
	if _, ok, _ := st.GetUserByName(ctx, "nobody"); ok {
		t.Fatalf("phantom user")
	}
	if n, _ := st.CountUsers(ctx); n != 1 {
		t.Fatalf("count=%d", n)
	}
	if id, _ := st.AdminUserID(ctx); id != uid {
		t.Fatalf("admin id=%d want %d", id, uid)
	}

	// Sessions: valid vs expired.
	now := time.Now().Unix()
	if err := st.CreateSession(ctx, "tok-live", uid, now+3600); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := st.CreateSession(ctx, "tok-dead", uid, now-10); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if got, ok, err := st.SessionUser(ctx, "tok-live"); err != nil || !ok || got != uid {
		t.Fatalf("live session: %v %v %d", err, ok, got)
	}
	if _, ok, err := st.SessionUser(ctx, "tok-dead"); err != nil || ok {
		t.Fatalf("expired session accepted: %v", err)
	}
	if err := st.PruneSessions(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if err := st.DeleteSession(ctx, "tok-live"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.SessionUser(ctx, "tok-live"); ok {
		t.Fatalf("deleted session accepted")
	}

	// Watchlist add/remove + watcher count.
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err := st.AddUserSymbol(ctx, uid, sym.ID); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := st.AddUserSymbol(ctx, uid, sym.ID); err != nil {
		t.Fatalf("add idempotent: %v", err)
	}
	if n, _ := st.SymbolWatcherCount(ctx, sym.ID); n != 1 {
		t.Fatalf("watchers=%d want 1", n)
	}
	if list, err := st.ListUserSymbols(ctx, uid); err != nil || len(list) != 1 || list[0].Symbol != "AAPL" {
		t.Fatalf("list user symbols: %v %+v", err, list)
	}
	if err := st.RemoveUserSymbol(ctx, uid, sym.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n, _ := st.SymbolWatcherCount(ctx, sym.ID); n != 0 {
		t.Fatalf("watchers=%d want 0", n)
	}
}

func TestAdoptActiveSymbols(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	b, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	inactive, _ := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "")
	_ = st.SetSymbolActive(ctx, inactive.ID, false)

	// An unowned (legacy) position.
	pid, err := st.InsertPosition(ctx, Position{SymbolID: a.ID, Qty: 1, EntryPrice: 100, EntryTs: 1})
	if err != nil {
		t.Fatalf("insert position: %v", err)
	}

	uid, _ := st.CreateUser(ctx, "local", "h", true)
	if err := st.AdoptActiveSymbols(ctx, uid); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	list, err := st.ListUserSymbols(ctx, uid)
	if err != nil || len(list) != 2 {
		t.Fatalf("adopted watchlist: %v %+v", err, list)
	}
	seen := map[int64]bool{}
	for _, s := range list {
		seen[s.ID] = true
	}
	if !seen[a.ID] || !seen[b.ID] || seen[inactive.ID] {
		t.Fatalf("wrong adoption set: %+v", list)
	}
	// Position was backfilled to the user.
	pos, err := st.Positions(ctx, uid, true)
	if err != nil || len(pos) != 1 || pos[0].ID != pid {
		t.Fatalf("position not adopted: %v %+v", err, pos)
	}
}

func TestClosePosition_Ownership(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	owner, _ := st.CreateUser(ctx, "owner", "h", false)
	other, _ := st.CreateUser(ctx, "other", "h", false)

	pid, err := st.InsertPosition(ctx, Position{SymbolID: sym.ID, UserID: owner, Qty: 2, EntryPrice: 10, EntryTs: 1})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Wrong user: must not close.
	ok, err := st.ClosePosition(ctx, pid, other, 12)
	if err != nil || ok {
		t.Fatalf("cross-user close succeeded: ok=%v err=%v", ok, err)
	}
	pos, _ := st.Positions(ctx, owner, true)
	if len(pos) != 1 || !pos[0].Open {
		t.Fatalf("position mutated by wrong user: %+v", pos)
	}
	// Right user closes.
	ok, err = st.ClosePosition(ctx, pid, owner, 12)
	if err != nil || !ok {
		t.Fatalf("owner close: ok=%v err=%v", ok, err)
	}
	pos, _ = st.Positions(ctx, owner, false)
	if len(pos) != 1 || pos[0].Open || pos[0].ExitPrice == nil || *pos[0].ExitPrice != 12 {
		t.Fatalf("close not recorded: %+v", pos)
	}
}

// TestConcurrentWrites_NoBusy hammers the write path from many goroutines:
// the single-connection write handle must serialize them without SQLITE_BUSY.
func TestConcurrentWrites_NoBusy(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")

	const goroutines = 12
	const iters = 25
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iters)
	for g := 0; g < goroutines; g++ {
		g := g
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				switch g % 3 {
				case 0:
					b := md.Bar{SymbolID: sym.ID, TF: md.TF1m, Ts: int64(g*1000 + i), Open: 1, High: 2, Low: 0.5, Close: 1.5, Volume: 9}
					if err := st.UpsertBars(ctx, []md.Bar{b}); err != nil {
						errs <- err
					}
				case 1:
					sc := md.Score{SymbolID: sym.ID, Horizon: md.H1h, Ts: int64(g*1000 + i), Score: 0.1}
					if err := st.InsertScore(ctx, sc); err != nil {
						errs <- err
					}
				default:
					if err := st.SetMeta(ctx, fmt.Sprintf("k%d", g), fmt.Sprintf("%d", i)); err != nil {
						errs <- err
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write error (SQLITE_BUSY?): %v", err)
	}
}
