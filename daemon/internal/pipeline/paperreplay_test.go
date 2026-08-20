package pipeline

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// replayBars is deliberately larger than buildReturnForecast's minimum of
// volWindow(200) + volShort(20) + span + distribution.MinSample(60) = 281.
//
// This matters more than it looks: a replay RE-FITS the return distribution from
// as-of bars rather than reading the stored table, and the EV gate REQUIRES a
// distribution. A shorter fixture therefore produces no forecast, no entry can
// ever open, and every assertion below passes for the wrong reason — which is
// exactly how the first version of this file passed with the as-of bound removed.
const replayBars = 700

// replayFixture seeds enough sessions for a distribution to fit, with real
// variance (a flat series has zero volatility and fits nothing), and derives the
// point-in-time universe from those bars.
func replayFixture(t *testing.T) (*PaperTrader, int64) {
	t.Helper()
	// These tests exercise the AS-OF PLUMBING, not the EV model — internal/ev has
	// its own tests for the gate. A synthetic price series does not produce a
	// distribution whose net EV clears the production floor after impact costs, so
	// the floor is relaxed here to let a decision through and let the assertions be
	// about which SIGNAL was read. Everything else runs at production settings.
	t.Setenv("SIGNALDECK_EV_MIN_NET_EV", "-0.05")
	t.Setenv("SIGNALDECK_EV_MAX_TAIL_P90", "0.99")
	st := openStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Seeded directly rather than via seedDailyPx, because the fixture needs
	// control of VOLUME: the EV gate charges square-root-law market impact against
	// the name's ADV, and at seedDailyPx's 1,000 shares the impact alone sinks net
	// EV below the floor no matter how strong the signal. Deep liquidity here makes
	// the test measure the as-of bound rather than the cost model.
	bars := make([]md.Bar, 0, replayBars)
	for d := 1; d <= replayBars; d++ {
		// Deterministic and non-degenerate: a real upward drift so the fitted
		// distribution carries a tradable edge, plus a sine so returns have the
		// variance the vol windows need.
		px := 100 * (1 + 0.02*math.Sin(float64(d)/6) + 0.0015*float64(d))
		bars = append(bars, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: int64(d) * 86400,
			Open: px, High: px * 1.004, Low: px * 0.996, Close: px, Volume: 10_000_000,
		})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
	if _, err := st.RebuildUniverseMembership(ctx); err != nil {
		t.Fatalf("membership: %v", err)
	}
	return &PaperTrader{St: st}, sym.ID
}

// THE POINT OF THE WHOLE THING. A replayed bar must decide on the signal that
// existed BEFORE it, never the newest row in the table.
//
// The damage from an unbounded read is not a phantom future fill — the
// `fillBar.Ts > asof` guard already blocks that. It is that the candidate is
// SILENTLY SKIPPED: LatestPrediction hands back a prediction stamped after the
// bar, its fill anchor (BarAtOrAfter(pred.Ts+1)) lands in the future, the guard
// drops it, and the reconstruction comes out EMPTY while looking like a complete
// run. So this asserts a position IS opened from the in-window signal, with a
// newer one sitting in the table to be ignored.
func TestReplay_UsesTheSignalThatExistedAtTheBarNotTheNewest(t *testing.T) {
	w, symID := replayFixture(t)
	ctx := context.Background()

	const entryBar = replayBars - 20 // the bar the fill should land on

	// The signal that existed just before the bar: strong long.
	seedPrediction(t, w.St, symID, md.H1d, int64(entryBar-1)*86400, 0.95)
	// A NEWER one, after the replay window. An unbounded read returns this, its
	// fill anchor lands past asof, and the candidate is dropped.
	seedPrediction(t, w.St, symID, md.H1d, int64(entryBar+10)*86400, 0.95)

	rep, err := w.ReplayRange(ctx, int64(entryBar)*86400, int64(entryBar+2)*86400, ReplayConfig{
		StrategySuffix:   "-replay",
		MaxPredictionAge: 30 * 86400,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if rep.Bars != 3 {
		t.Fatalf("stepped %d session(s), want 3 — one pass per bar", rep.Bars)
	}

	// Assert on the TRADE LOG, not the end state: flagship-1d has a one-bar
	// horizon, so a position opened at the first replayed bar is closed by the
	// expiry barrier on the next one and the book is flat again by the end.
	trades, err := w.St.PaperTrades(ctx, "flagship-1d-replay", 50)
	if err != nil {
		t.Fatalf("trades: %v", err)
	}
	buys := 0
	for _, tr := range trades {
		if tr.Side == "buy" {
			buys++
		}
		// Every reconstructed fill must land inside the window it replayed.
		if tr.Ts < int64(entryBar)*86400 || tr.Ts > int64(entryBar+2)*86400 {
			t.Fatalf("reconstructed fill at ts=%d is outside the replayed window [%d, %d]",
				tr.Ts, int64(entryBar)*86400, int64(entryBar+2)*86400)
		}
	}
	if buys == 0 {
		if ds, derr := w.St.EVDecisions(ctx, "", "", 20); derr == nil {
			for _, d := range ds {
				net := "nil"
				if d.NetEV != nil {
					net = fmt.Sprintf("%.6f", *d.NetEV)
				}
				t.Logf("ledger: %s %s netEV=%s rank=%d/%d", d.Decision, d.Reason, net, d.Rank, d.RankOf)
			}
			if len(ds) == 0 {
				t.Log("ledger: EMPTY — the candidate never reached the EV gate")
			}
		}
		t.Fatal("the reconstruction bought nothing: it read the NEWEST prediction " +
			"(stamped after the replay window), whose fill anchor lands past asof and is " +
			"discarded — so the replay silently produces an empty book instead of the " +
			"decision the bar actually supported")
	}

	// A reconstruction must never touch the book that actually ran.
	if tr, eq, pos, _ := w.St.CountPaperRows(ctx, "flagship-1d"); tr+eq+pos != 0 {
		t.Fatalf("the reconstruction wrote into the LIVE book: %d trade(s), %d mark(s), %d position(s)", tr, eq, pos)
	}
}

// Staleness is bounded as well as as-of-ness. On the FIRST bar of a replay the
// cursor is 0, so the fill-window lower bound does not bind — only the age bound
// stands between the reconstruction and a fill back-dated by a year.
func TestReplay_RefusesAStaleSignal(t *testing.T) {
	w, symID := replayFixture(t)
	ctx := context.Background()

	// The only signal is from session 2; the replay starts 300 sessions later.
	seedPrediction(t, w.St, symID, md.H1d, 2*86400, 0.95)

	if _, err := w.ReplayRange(ctx, 302*86400, 304*86400, ReplayConfig{
		StrategySuffix:   "-stale",
		MaxPredictionAge: 2 * 86400,
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, held, _ := w.St.PaperPosition(ctx, "flagship-1d-stale", symID); held {
		t.Fatal("acted on a signal 300 sessions old: as-of-ness alone is not freshness, " +
			"and on the first replayed bar the cursor bound does not yet bind")
	}
}

// A reconstruction must refuse before it writes anything, not partway through: a
// partial replay is worse than none, because it looks like a complete curve.
func TestReplay_RefusesBeforeWritingAnything(t *testing.T) {
	w, symID := replayFixture(t)
	ctx := context.Background()
	seedPrediction(t, w.St, symID, md.H1d, int64(replayBars-21)*86400, 0.95)

	t.Run("empty suffix would overwrite the live book", func(t *testing.T) {
		_, err := w.ReplayRange(ctx, 300*86400, 302*86400, ReplayConfig{})
		if err == nil || !strings.Contains(err.Error(), "StrategySuffix is empty") {
			t.Fatalf("want a refusal naming the empty suffix, got %v", err)
		}
	})

	t.Run("window outside the universe record", func(t *testing.T) {
		_, err := w.ReplayRange(ctx, 300*86400, int64(replayBars+400)*86400, ReplayConfig{StrategySuffix: "-x"})
		if err == nil || !strings.Contains(err.Error(), "universe_membership covers") {
			t.Fatalf("want a refusal naming the coverage gap, got %v", err)
		}
	})

	t.Run("destination already holds rows", func(t *testing.T) {
		cfg := ReplayConfig{StrategySuffix: "-again", MaxPredictionAge: 30 * 86400}
		if _, err := w.ReplayRange(ctx, 300*86400, 302*86400, cfg); err != nil {
			t.Fatalf("first replay: %v", err)
		}
		_, err := w.ReplayRange(ctx, 300*86400, 302*86400, cfg)
		if err == nil || !strings.Contains(err.Error(), "already holds") {
			t.Fatalf("want a refusal to append to a previous run, got %v", err)
		}
	})
}
