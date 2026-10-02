package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A label must not be frozen against a bar that is still forming. The resolver's
// only time check used to be `now < target`, and target is the next session's
// bar STAMP — a bar ingest creates at the open — so a pass during the session
// wrote the live price in as a close-to-close outcome and never revisited it.
// Measured on the live record: 37.8% of 1d labels were frozen mid-session and
// disagreed with the final close 9.7% of the time.
//
// The guard is "a later bar exists", so this test drives it by adding one.
func TestResolverWaitsForTheForwardSessionToSettle(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "SETTLE", md.Stocks, "")
	if err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}
	// D0 is the exchange-local midnight (04:00Z, EDT) of the day the graded window
	// opens, so every stamp below is inside the window the resolved-pair read is
	// restricted to (GradingEpochTS) and in the past, which the resolver requires.
	// Predictions are stamped 23h into D0: after D0 settles (22h,
	// md.DailyBarSettled), which settledBase requires of rows since 2026-09-08.
	const d0 = int64(store.GradingEpochTS) + 4*3600
	bar := func(ts int64, c float64) md.Bar {
		return md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: ts, Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	// D0 and D2 only. A prediction stamped inside D0 takes D0 as its base, so
	// its target is D1 and the forward bar is D2 — the NEWEST bar, i.e. the
	// session that has not settled yet.
	if err := st.UpsertBars(ctx, []md.Bar{bar(d0, 100), bar(d0+2*86400, 110)}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: d0 + 23*3600,
		RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: `{}`,
	}); err != nil {
		t.Fatalf("UpsertPrediction: %v", err)
	}

	r := &PredictionResolver{St: st}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ups, _, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 10); err != nil {
		t.Fatalf("ResolvedRawPredictionPairs: %v", err)
	} else if len(ups) != 0 {
		t.Fatalf("resolved %d rows against an unsettled forward bar; want 0", len(ups))
	}

	// The next session prints. D2 is now settled, so the label may be frozen.
	if err := st.UpsertBars(ctx, []md.Bar{bar(d0+3*86400, 111)}); err != nil {
		t.Fatalf("UpsertBars: %v", err)
	}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, ups, _, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 10)
	if err != nil {
		t.Fatalf("ResolvedRawPredictionPairs: %v", err)
	}
	if len(ups) != 1 {
		t.Fatalf("resolved %d rows once the forward session settled; want 1", len(ups))
	}
	if ups[0] != 1 {
		t.Fatalf("100 -> 110 must label up, got %v", ups[0])
	}
}

// Head-of-line blocking, the second time. The resolver read ONE oldest-first
// batch of 1500 and skipped, for good, rows whose forward bar sits more than
// three horizons past target (every pre-holiday Friday's 1d row) — so once
// 1500 such rows led the queue, nothing behind them was ever offered. Live
// from ~2026-09-10: the 1d head was 1500 stuck rows and 44k owed rows waited.
func TestResolverPagesPastRowsItSkips(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	const d0 = int64(store.GradingEpochTS) + 4*3600 // exchange midnight; see TestResolverWaitsForTheForwardSessionToSettle
	stuck, err := st.UpsertSymbol(ctx, "STUCK", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	live, err := st.UpsertSymbol(ctx, "LIVE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	bar := func(id, ts int64, c float64) md.Bar {
		return md.Bar{SymbolID: id, TF: md.TF1d, Ts: ts, Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	// STUCK's next bar is 5 days on: past the 3-horizon gap guard, so every
	// one of its rows is skipped on every pass — yet a forward bar exists, so
	// the store still offers them, oldest first.
	// LIVE is an ordinary resolvable row: base D0, forward D1, settled by D2.
	if err := st.UpsertBars(ctx, []md.Bar{
		bar(stuck.ID, d0, 100), bar(stuck.ID, d0+5*86400, 100),
		bar(live.ID, d0, 100), bar(live.ID, d0+86400, 110), bar(live.ID, d0+2*86400, 111),
	}); err != nil {
		t.Fatal(err)
	}
	for i := int64(0); i <= 1500; i++ { // one more than the old head batch of 1500
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: stuck.ID, Horizon: md.H1d, Ts: d0 + 23*3600 + i, // D0 settled; still before D1
			RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: live.ID, Horizon: md.H1d, Ts: d0 + 23*3600 + 1800, // newer than every stuck row
		RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	msg, err := (&PredictionResolver{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, ups, _, err := st.ResolvedRawPredictionPairs(ctx, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 {
		t.Fatalf("resolved %d rows (%s); want the one LIVE row behind 1501 skipped ones", len(ups), msg)
	}
}

// A HOLIDAY IS NOT A GAP. Friday 2026-09-04's next session is Tuesday 09-08
// (Labor Day between): from the slackened target that bar is 3d6h out, past the
// 3-horizon gap guard, so every pre-holiday Friday 1d row was skipped forever.
// The NYSE calendar admits it. A forward bar that skips a real session
// (Tuesday missing, next bar Wednesday) is a data gap and is still refused.
func TestResolverGradesAcrossAHolidayNotAGap(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	const fri, day = int64(1788494400), int64(86400) // 2026-09-04 00:00 ET
	if !marketcal.IsFullHoliday(time.Unix(fri+3*day, 0)) {
		t.Fatal("fixture: 2026-09-07 must be an NYSE holiday")
	}
	hol, err := st.UpsertSymbol(ctx, "HOLIDAY", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	gap, err := st.UpsertSymbol(ctx, "GAP", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	// Crypto trades every day, so it has no holidays: the same Fri/Tue bars are a
	// 4-day hole there and must stay refused.
	coin, err := st.UpsertSymbol(ctx, "HOLICOIN", md.Crypto, "")
	if err != nil {
		t.Fatal(err)
	}
	bar := func(id, ts int64, c float64) md.Bar {
		return md.Bar{SymbolID: id, TF: md.TF1d, Ts: ts, Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	// HOLIDAY: Fri, Tue (the next session), Wed settles Tue. 100 -> 110, up.
	// GAP: Fri, then Wed — Tuesday's session is missing — Thu settles Wed. Down.
	if err := st.UpsertBars(ctx, []md.Bar{
		bar(hol.ID, fri, 100), bar(hol.ID, fri+4*day, 110), bar(hol.ID, fri+5*day, 111),
		bar(gap.ID, fri, 100), bar(gap.ID, fri+5*day, 90), bar(gap.ID, fri+6*day, 89),
		bar(coin.ID, fri, 100), bar(coin.ID, fri+4*day, 110), bar(coin.ID, fri+5*day, 111),
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{hol.ID, gap.ID, coin.ID} {
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: id, Horizon: md.H1d, Ts: fri + 17*3600, // Friday 17:00 ET, after the close
			RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
	}

	msg, err := (&PredictionResolver{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Read the resolver's own output, not the graded view: the only NYSE holiday
	// a past-dated fixture can use (Labor Day) predates store.GradingEpoch, which
	// the grader's reads floor on. Resolution is what is under test here.
	rows, err := st.DB().QueryContext(ctx, `SELECT symbol_id, up FROM prediction_outcomes
		WHERE horizon = '1d' AND resolved_at IS NOT NULL ORDER BY symbol_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck
	var got [][2]int64
	for rows.Next() {
		var id, up int64
		if err := rows.Scan(&id, &up); err != nil {
			t.Fatal(err)
		}
		got = append(got, [2]int64{id, up})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != [2]int64{hol.ID, 1} {
		t.Fatalf("resolved (symbol, up) = %v (%s); want exactly the HOLIDAY row (%d), labeled up", got, msg, hol.ID)
	}
}

// The benchmark twin ("1d#pm", same symbol and ts) resolves in the SAME step as
// its ensemble row, not in a later pass behind the whole ensemble queue: that
// pass starved whenever a restart cut a long run short (2026-10-01, 39k 1d#pm
// rows unresolved, the prequential-majority (1d) row gone from the registry).
func TestResolverResolvesTheBenchmarkTwinWithItsRow(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	sym, err := st.UpsertSymbol(ctx, "TWIN", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	const d0 = int64(store.GradingEpochTS) + 4*3600 // exchange midnight
	bar := func(ts int64, c float64) md.Bar {
		return md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: ts, Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar(d0, 100), bar(d0+86400, 110), bar(d0+2*86400, 111)}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []md.Horizon{md.H1d, benchmarkHorizon(md.H1d)} {
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: h, Ts: d0 + 23*3600, RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := (&PredictionResolver{St: st}).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(msg, "(+1 benchmark twins)") {
		t.Fatalf("Run = %q; the 1d#pm twin must resolve with its ensemble row, not in its own later pass", msg)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(DISTINCT fwd_return) FROM prediction_outcomes
		WHERE symbol_id=? AND horizon IN ('1d','1d#pm') AND resolved_at IS NOT NULL`, sym.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d distinct labels across the ensemble row and its twin; want 1 (identical bars)", n)
	}
}
