// Round-trip tests for the confluence-gate store layer: setups (latest-only
// upsert), forward-tracked outcomes (idempotent insert → resolve → scoreboard
// read), and day-deduped events with the id-cursor sweep.
package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestConfluenceSetups_UpsertLatestAndTop(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	btc, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	// Absent symbol: honest false, no error.
	if _, ok, err := st.LatestConfluenceSetup(ctx, aapl.ID); err != nil || ok {
		t.Fatalf("LatestConfluenceSetup absent = ok=%v, %v; want false, nil", ok, err)
	}

	if err := st.UpsertConfluenceSetup(ctx, ConfluenceSetup{
		SymbolID: aapl.ID, Ts: 100, Direction: 1, Agree: 2, Dissent: 1,
		Score: 0.3, IsSetup: false, Payload: `{"v":1}`,
	}); err != nil {
		t.Fatalf("upsert setup: %v", err)
	}
	// Re-upsert replaces in place (PK = symbol_id): the newer row wins.
	if err := st.UpsertConfluenceSetup(ctx, ConfluenceSetup{
		SymbolID: aapl.ID, Ts: 200, Direction: 1, Agree: 4, Dissent: 0,
		Score: 0.9, IsSetup: true, Payload: `{"v":2}`,
	}); err != nil {
		t.Fatalf("re-upsert setup: %v", err)
	}
	if err := st.UpsertConfluenceSetup(ctx, ConfluenceSetup{
		SymbolID: btc.ID, Ts: 150, Direction: -1, Agree: 3, Dissent: 1,
		Score: -0.5, IsSetup: false, Payload: `{}`,
	}); err != nil {
		t.Fatalf("upsert btc setup: %v", err)
	}

	got, ok, err := st.LatestConfluenceSetup(ctx, aapl.ID)
	if err != nil || !ok {
		t.Fatalf("LatestConfluenceSetup = ok=%v, %v", ok, err)
	}
	if got.Ts != 200 || !got.IsSetup || got.Agree != 4 || got.Payload != `{"v":2}` {
		t.Fatalf("upsert did not replace in place: %+v", got)
	}

	all, err := st.TopConfluenceSetups(ctx, "", 0, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("TopConfluenceSetups all = %d rows, %v; want 2", len(all), err)
	}
	// Flagged setups sort first, and the join resolves symbol+market.
	if all[0].Symbol != "AAPL" || !all[0].IsSetup || all[0].Market != string(md.Stocks) {
		t.Fatalf("ordering/join wrong: %+v", all[0])
	}
	onlyCrypto, err := st.TopConfluenceSetups(ctx, string(md.Crypto), 5, false)
	if err != nil || len(onlyCrypto) != 1 || onlyCrypto[0].Symbol != "BTC/USD" {
		t.Fatalf("market filter = %+v, %v", onlyCrypto, err)
	}
	onlySetups, err := st.TopConfluenceSetups(ctx, "", 5, true)
	if err != nil || len(onlySetups) != 1 || onlySetups[0].SymbolID != aapl.ID {
		t.Fatalf("onlySetups filter = %+v, %v", onlySetups, err)
	}
}

func TestConfluenceOutcomes_ForwardTrackAndResolve(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	o := ConfluenceOutcome{SymbolID: sym.ID, Ts: 1000, Horizon: "1d",
		Direction: 1, Agree: 3, EntryPx: 410.5}
	if err := st.InsertConfluenceOutcome(ctx, o); err != nil {
		t.Fatalf("insert outcome: %v", err)
	}
	// Idempotent on (symbol, ts, horizon): a re-run never double-inserts.
	if err := st.InsertConfluenceOutcome(ctx, o); err != nil {
		t.Fatalf("re-insert outcome: %v", err)
	}

	pending, err := st.UnresolvedConfluenceOutcomes(ctx, 2000, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("unresolved = %d rows, %v; want 1", len(pending), err)
	}
	if pending[0].EntryPx != 410.5 || pending[0].Horizon != "1d" {
		t.Fatalf("pending row mangled: %+v", pending[0])
	}
	// Maturity cutoff: nothing pending before the flag time.
	if p, _ := st.UnresolvedConfluenceOutcomes(ctx, 500, 10); len(p) != 0 {
		t.Fatal("maturity cutoff leaked an immature outcome")
	}

	if err := st.ResolveConfluenceOutcome(ctx, sym.ID, 1000, "1d", 0.021, true); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p, _ := st.UnresolvedConfluenceOutcomes(ctx, 2000, 10); len(p) != 0 {
		t.Fatal("resolved outcome still listed as unresolved")
	}
	res, err := st.ResolvedConfluenceOutcomes(ctx, 0)
	if err != nil || len(res) != 1 {
		t.Fatalf("resolved = %d rows, %v; want 1", len(res), err)
	}
	r := res[0]
	if r.Symbol != "MSFT" || r.FwdReturn != 0.021 || r.Win != 1 {
		t.Fatalf("resolved row mangled: %+v", r)
	}
}

func TestConfluenceEvents_DayDedupAndCursor(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "Tesla")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	if id, err := st.MaxConfluenceEventID(ctx); err != nil || id != 0 {
		t.Fatalf("MaxConfluenceEventID empty = %d, %v; want 0", id, err)
	}

	ev := ConfluenceEvent{SymbolID: sym.ID, Ts: 1000, Kind: "confluence_setup",
		Detail: "3 families agree", DayBucket: "2026-07-26"}
	fresh, err := st.InsertConfluenceEvent(ctx, ev)
	if err != nil || !fresh {
		t.Fatalf("first insert = fresh=%v, %v; want true", fresh, err)
	}
	// Same symbol+kind+day: deduped, reported as not-new.
	dup, err := st.InsertConfluenceEvent(ctx, ev)
	if err != nil || dup {
		t.Fatalf("dup insert = fresh=%v, %v; want false", dup, err)
	}

	maxID, err := st.MaxConfluenceEventID(ctx)
	if err != nil || maxID == 0 {
		t.Fatalf("MaxConfluenceEventID = %d, %v; want > 0", maxID, err)
	}
	rows, err := st.ConfluenceEventsAfterID(ctx, 0, 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("events after 0 = %d rows, %v; want 1", len(rows), err)
	}
	if rows[0].Symbol != "TSLA" || rows[0].Kind != "confluence_setup" {
		t.Fatalf("event row mangled: %+v", rows[0])
	}
	if rows, _ := st.ConfluenceEventsAfterID(ctx, maxID, 0, 10); len(rows) != 0 {
		t.Fatal("cursor advance leaked an already-swept event")
	}
}
