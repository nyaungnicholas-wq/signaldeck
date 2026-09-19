package pipeline

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// A due regime call whose symbol can never print the bars it needs must be
// RETIRED with a reason, not retried forever.
//
// Measured live 2026-08-27: regime-outcome-runner had reported `ok ...
// resolved 0` for 31 days. 2,288 rows were due, 2,267 of them on symbols the
// universe sweep had PRUNED, and ZERO had enough forward bars — an inactive
// symbol stops receiving daily bars, so "retried later" was never going to
// arrive. Left alone this is a survivorship hole: the structural record can
// only ever grade symbols that stayed in the universe, and the excluded ones
// are invisible rather than counted.
//
// The grace window must be wide enough that a merely-slow symbol is never
// retired early, which is what the first phase of each test below pins.

const ungradableHorizon = 21

// seedShortCall freezes one call at bar 280 with bars only up to the call, so
// it is due by the calendar and short of its forward window.
func seedShortCall(t *testing.T, name string) (ctx context.Context, st *store.Store, symID int64, callTs int64) {
	t.Helper()
	ctx = context.Background()
	st = newRegimeOutcomeStore(t, name)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	price := func(i int) (float64, float64) { return 100.0 * math.Pow(1.002, float64(i)), 1e6 }
	const start = 1000
	callTs = int64(start+280) * 86400
	seedRegimeBars(t, st, sym.ID, start, 281, price)
	freezeCall(t, st, sym.ID, structregime.KindTrend21, callTs, ungradableHorizon, "uptrend", 0.91, 0.972)
	return ctx, st, sym.ID, callTs
}

func TestRegimeDueRowInsideGraceWindowIsNotRetired(t *testing.T) {
	ctx, st, _, callTs := seedShortCall(t, "grace_inside.db")

	// Due by the calendar (1.45x) but well inside the 3x grace window.
	clock := callTs + int64(ungradableHorizon*1.45*86400) + 10
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(clock, 0) }}
	summary, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(summary, "retired 1 ungradable") {
		t.Fatalf("retired a row that is still inside its grace window: %s", summary)
	}
	due, err := st.DueRegimeOutcomes(ctx, clock, 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("row must stay in the due queue while later can still arrive, got %d", len(due))
	}
}

func TestRegimeDueRowPastGraceWindowIsRetiredAndCounted(t *testing.T) {
	ctx, st, _, callTs := seedShortCall(t, "grace_past.db")

	// Past the window with the bars still absent: no future bar rescues it.
	//
	// A LITERAL 5x, deliberately NOT regimeAbandonMultiple. Deriving this clock
	// from the constant made the test self-referential: widening the constant
	// moved the clock with it, so a mutation to 100000 (never retire anything)
	// still PASSED. A test that cannot fail when the value it guards changes is
	// not guarding it.
	clock := callTs + int64(ungradableHorizon*5*86400) + 86400
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(clock, 0) }}
	summary, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(summary, "retired 1 ungradable") {
		t.Fatalf("a permanently unresolvable row must be retired AND COUNTED; summary was: %s", summary)
	}
	due, err := st.DueRegimeOutcomes(ctx, clock, 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("retired row must leave the due queue, still got %d", len(due))
	}
	// It is retired, never graded: an ungradable row must not become evidence.
	res, _ := st.ResolvedRegimeOutcomes(ctx, 0)
	if len(res) != 0 {
		t.Fatalf("a retired row must NOT be graded, got %d resolved", len(res))
	}
	// Idempotent: a second pass neither re-retires nor re-counts it.
	summary2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if !strings.Contains(summary2, "retired 0 ungradable") {
		t.Fatalf("retirement must be idempotent, second pass said: %s", summary2)
	}
}

// The grace window must not cost a real grade: a slow symbol whose bars arrive
// before the window closes still resolves normally.
func TestRegimeSlowSymbolStillGradesInsideGraceWindow(t *testing.T) {
	ctx, st, symID, callTs := seedShortCall(t, "grace_slow.db")

	price := func(i int) (float64, float64) { return 100.0 * math.Pow(1.002, float64(i)), 1e6 }
	const start = 1000
	seedRegimeBars(t, st, symID, start, 310, price) // forward bars finally print

	clock := callTs + int64(ungradableHorizon*1.45*86400) + 10
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(clock, 0) }}
	summary, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res, _ := st.ResolvedRegimeOutcomes(ctx, 0)
	if len(res) != 1 {
		t.Fatalf("a slow symbol whose bars arrived must still grade, got %d resolved (%s)", len(res), summary)
	}
	if strings.Contains(summary, "retired 1 ungradable") {
		t.Fatalf("graded row must not also be retired: %s", summary)
	}
}

// The stuck count must be visible on the very first pass, long before anything
// is old enough to retire. That is the half of E22 the grace window does NOT
// fix: a wide window defers RETIREMENT, and retirement was never the complaint --
// silence was. A pass that grades nothing because 2,288 rows lack forward bars
// must say so, not print a bare "resolved 0".
func TestRegimeStuckRowsAreReportedBeforeTheyAreRetirable(t *testing.T) {
	ctx, st, _, callTs := seedShortCall(t, "stuck_visible.db")

	// Due by the calendar, nowhere near the 3x retirement threshold.
	clock := callTs + int64(ungradableHorizon*1.45*86400) + 10
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(clock, 0) }}
	summary, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(summary, "1 due but stuck short of forward bars") {
		t.Fatalf("a pass that graded nothing must SAY why; summary was: %s", summary)
	}
	if !strings.Contains(summary, "retired 0 ungradable") {
		t.Fatalf("nothing may be retired this early: %s", summary)
	}
}

// A pass with nothing stuck must not cry wolf.
func TestRegimeStuckCountIsZeroWhenEverythingGrades(t *testing.T) {
	ctx, st, symID, callTs := seedShortCall(t, "stuck_none.db")
	price := func(i int) (float64, float64) { return 100.0 * math.Pow(1.002, float64(i)), 1e6 }
	seedRegimeBars(t, st, symID, 1000, 310, price)

	clock := callTs + int64(ungradableHorizon*1.45*86400) + 10
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(clock, 0) }}
	summary, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(summary, "0 due but stuck short of forward bars") {
		t.Fatalf("nothing was stuck; summary must say 0: %s", summary)
	}
}
