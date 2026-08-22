package pipeline

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ONE PRICE OBSERVATION MUST NOT BECOME THREE INDEPENDENT BETS.
//
// Outcomes were bucketed by UTC calendar day while the scorer ran every 30
// minutes, weekends included. A setup that persisted over a weekend opened three
// rows — Friday, Saturday, Sunday — and the resolver graded all three from the
// SAME pair of bars, because both its entry read (BarAtOrBefore) and its forward
// read (BarAtOrAfter) fall back to the nearest bar in each direction and there is
// no bar between Friday and Monday.
//
// Measured on the live table before this fix: RNWWW carried entry_px 0.0015 and
// fwd_return +0.9333 on 2026-07-17, 07-18 AND 07-19 — the identical Friday-close
// to Monday-close move, published as three independent bets, each entering the
// mean of a money number.
//
// Two guards close it, and this file pins both:
//
//	the scorer opens no outcome on a non-trading day;
//	the resolver refuses an entry bar from outside the bucket day.
//
// MUTATION CHECK. Delete the `if entry.Ts < o.Ts { continue }` guard in
// ConfluenceResolver.Run and TestConfluenceResolve_RefusesAnEntryBarFromAnEarlierDay
// fails with the weekend row graded at +93.33%. Replace `tradingDay` in
// ConfluenceScorer.Run with the constant true and
// TestConfluenceScorer_OpensNoBetOnANonTradingDay fails with an outcome row on a
// Saturday.

// satBucket/sunBucket/friBucket are three consecutive UTC day buckets around a
// real weekend: 2026-07-17 (Friday), 07-18, 07-19. These are the exact days the
// live RNWWW duplication occupied.
const (
	friBucket = int64(1784246400) // 2026-07-17 00:00 UTC
	satBucket = int64(1784332800) // 2026-07-18 00:00 UTC
	sunBucket = int64(1784419200) // 2026-07-19 00:00 UTC
	monBar    = int64(1784520000) // 2026-07-20 04:00 UTC — the next real session
	friBar    = int64(1784260800) // 2026-07-17 04:00 UTC — the last real session
)

// seedRNWWW writes the two real sessions the weekend sits between: a Friday bar
// closing at 0.0015 and a Monday bar closing at 0.0029 (+93.33%).
func seedRNWWW(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "RNWWW", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	bars := []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: friBar, Open: 0.0014, High: 0.0025, Low: 0.0010, Close: 0.0015, Volume: 54019},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: monBar, Open: 0.0018, High: 0.0030, Low: 0.0011, Close: 0.0029, Volume: 19558},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
	return sym.ID
}

// TestConfluenceResolve_RefusesAnEntryBarFromAnEarlierDay is the resolver half.
// The Saturday bucket has no bar of its own; grading it would reach back to
// Friday's and republish Friday's move.
func TestConfluenceResolve_RefusesAnEntryBarFromAnEarlierDay(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	id := seedRNWWW(t, st)

	for _, bucket := range []int64{friBucket, satBucket, sunBucket} {
		if err := st.InsertConfluenceOutcome(ctx, store.ConfluenceOutcome{
			SymbolID: id, Ts: bucket, Horizon: confluenceHorizon,
			Direction: -1, Agree: 3, EntryPx: 0.0015,
		}); err != nil {
			t.Fatalf("insert %d: %v", bucket, err)
		}
	}

	if _, err := (&ConfluenceResolver{St: st}).Run(ctx); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	rows, err := st.ResolvedConfluenceOutcomes(ctx, 100)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	graded := map[int64]store.ConfluenceOutcome{}
	for _, r := range rows {
		if r.SymbolID == id {
			graded[r.Ts] = r
		}
	}
	if len(graded) != 1 {
		t.Fatalf("graded %d of 3 buckets, want exactly 1 (the Friday one). "+
			"Grading a weekend bucket republishes Friday's move as a separate bet: %v", len(graded), bucketsOf(graded))
	}
	fri, ok := graded[friBucket]
	if !ok {
		t.Fatalf("the graded bucket is not the Friday one: %v", bucketsOf(graded))
	}
	if fri.EntryTs != friBar {
		t.Fatalf("entry_ts = %d, want the Friday bar %d — the row must record WHICH bar its entry leg came from", fri.EntryTs, friBar)
	}
	// The move itself is genuine and must survive: 0.0029/0.0015 - 1.
	if fri.FwdReturn < 0.93 || fri.FwdReturn > 0.94 {
		t.Fatalf("fwd_return = %.4f, want ~0.9333 — the real Friday-to-Monday move must NOT be clipped away", fri.FwdReturn)
	}
}

// TestConfluenceScorer_OpensNoBetOnANonTradingDay is the scorer half: the
// duplicate rows must never be written in the first place.
func TestConfluenceScorer_OpensNoBetOnANonTradingDay(t *testing.T) {
	if IsConfluenceBettableDay(satBucket+12*3600, md.Stocks) {
		t.Fatal("2026-07-18 is a Saturday; the scorer must not open a STOCK bet on it")
	}
	if IsConfluenceBettableDay(sunBucket+12*3600, md.Stocks) {
		t.Fatal("2026-07-19 is a Sunday; the scorer must not open a STOCK bet on it")
	}
	if !IsConfluenceBettableDay(friBucket+18*3600, md.Stocks) {
		t.Fatal("2026-07-17 is a Friday session; the scorer must open bets on it")
	}
	// A full NYSE closure on a weekday is the case a weekday-only check misses:
	// 2026-07-03 is the observed Independence Day holiday.
	if IsConfluenceBettableDay(1783123200+12*3600, md.Stocks) {
		t.Fatal("2026-07-03 is an observed NYSE holiday; the scorer must not open a STOCK bet on it")
	}
}

// CRYPTO TRADES ON THE DAYS THE NYSE DOES NOT, and gating it on the NYSE
// calendar would silently stop a 24/7 book two days in seven.
//
// This was a real regression, caught by reading the live table rather than by a
// test: BTC/USD and its peers print a daily bar on all 25 weekend days of a
// 90-day window, and the record already held 11 weekend crypto setups with 9 of
// them graded. Every one would have stopped being opened.
//
// MUTATION CHECK, verified: remove the md.Crypto branch from
// IsConfluenceBettableDay and this test fails on both weekend days.
func TestConfluenceScorer_CryptoIsBettableEveryDay(t *testing.T) {
	for _, ts := range []int64{satBucket + 12*3600, sunBucket + 12*3600, friBucket + 18*3600} {
		if !IsConfluenceBettableDay(ts, md.Crypto) {
			t.Fatalf("crypto refused at %s: it trades every calendar day and prints a bar on every "+
				"one, so the NYSE calendar must not gate it",
				time.Unix(ts, 0).UTC().Format("2006-01-02 Mon"))
		}
	}
	// The NYSE holiday too: crypto does not observe Independence Day.
	if !IsConfluenceBettableDay(1783123200+12*3600, md.Crypto) {
		t.Fatal("crypto refused on an NYSE holiday; it does not observe one")
	}
}

// TestConfluenceEpisodes_CollapseAPersistentSetup pins the episode key: a setup
// that persists across consecutive sessions is ONE bet held, not one new bet per
// session. AXTI carried the identical -85.66% short on 2026-07-31 and 08-01 and
// NXGLW on 08-15 and 08-16.
//
// MUTATION CHECK: widen ConfluenceEpisodeGapSecs to 0 and the four consecutive
// days below become four episodes, failing the first assertion.
func TestConfluenceEpisodes_CollapseAPersistentSetup(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AXTI", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	const day = int64(86400)
	base := int64(20300) * day
	// Four consecutive days short, then a five-day lapse, then two more days
	// short: two episodes. The single long day in between is its own episode
	// because direction is part of the key.
	type row struct {
		off int64
		dir int
	}
	for _, r := range []row{
		{0, -1}, {1, -1}, {2, -1}, {3, -1},
		{2, 1}, // same day, opposite direction — a different episode
		{9, -1}, {10, -1},
	} {
		if err := st.InsertConfluenceOutcome(ctx, store.ConfluenceOutcome{
			SymbolID: sym.ID, Ts: base + r.off*day, Horizon: confluenceHorizon,
			Direction: r.dir, Agree: 3, EntryPx: 36.97,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// The insert path must assign episodes on its own.
	assertEpisodes(t, st, sym.ID, base, day, map[int64]int64{
		base: base, base + day: base, base + 2*day: base, base + 3*day: base,
		base + 9*day: base + 9*day, base + 10*day: base + 9*day,
	})

	// The backfill must reproduce the identical assignment from the stored rows
	// alone — it is a re-derivation, not a second opinion.
	if _, err := st.BackfillConfluenceEpisodes(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	assertEpisodes(t, st, sym.ID, base, day, map[int64]int64{
		base: base, base + day: base, base + 2*day: base, base + 3*day: base,
		base + 9*day: base + 9*day, base + 10*day: base + 9*day,
	})
}

// assertEpisodes checks episode_ts for the SHORT rows of one symbol.
func assertEpisodes(t *testing.T, st *store.Store, symbolID, base, day int64, want map[int64]int64) {
	t.Helper()
	rows, err := st.ConfluenceOutcomesForSymbol(context.Background(), symbolID, confluenceHorizon)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := map[int64]int64{}
	for _, r := range rows {
		if r.Direction < 0 {
			got[r.Ts] = r.EpisodeTs
		}
	}
	if len(got) != len(want) {
		t.Fatalf("short rows = %d, want %d", len(got), len(want))
	}
	for ts, w := range want {
		if got[ts] != w {
			t.Fatalf("episode_ts at day +%d = %d (want %d, i.e. day +%d). "+
				"A persistent setup must stay ONE episode: without this the same sustained "+
				"move enters the published mean once per calendar day.",
				(ts-base)/day, got[ts], w, (w-base)/day)
		}
	}
}

func bucketsOf(m map[int64]store.ConfluenceOutcome) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
