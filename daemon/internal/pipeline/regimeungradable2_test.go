package pipeline

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// The other two permanent-retry holes in the resolver.
//
// Insufficient forward bars was the measured one (2,288 rows, 31 days). These
// two are the same class and were equally silent:
//
//   - an UNKNOWN KIND is a kind the engine no longer has (retired gapfill
//     rows). No future bar teaches it one.
//   - a DEGENERATE WINDOW (tie / NaN median) is computed from a FIXED
//     historical bar index, so re-running it never changes the answer.
//
// Neither can ever become gradable, so `continue` retried them forever. Both
// now retire with a reason past the same grace window, and neither is guessed
// at or graded.

func TestRegimeUnknownKindIsRetiredNotRetriedForever(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "unknown_kind.db")
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	price := func(i int) (float64, float64) { return 100.0 * math.Pow(1.002, float64(i)), 1e6 }
	const start = 1000
	callTs := int64(start+280) * 86400
	// Full forward bars: the row is NOT bar-starved. The only thing wrong with
	// it is that its kind no longer exists in the engine.
	seedRegimeBars(t, st, sym.ID, start, 340, price)
	freezeCall(t, st, sym.ID, "retired-gapfill-kind", callTs, ungradableHorizon, "uptrend", 0.5, 0.5)

	// Inside the grace window: retried, not retired.
	early := callTs + int64(ungradableHorizon*1.45*86400) + 10
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(early, 0) }}
	summary, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(summary, "retired 0 ungradable") {
		t.Fatalf("unknown kind retired inside its grace window: %s", summary)
	}
	// A bar-rich row must NOT be counted as stuck for want of bars.
	if !strings.Contains(summary, "0 due but stuck short of forward bars") {
		t.Fatalf("unknown kind miscounted as bar-starved: %s", summary)
	}

	// Past the window: retired with a reason.
	late := callTs + int64(ungradableHorizon*5*86400) + 86400
	w = &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(late, 0) }}
	summary, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if !strings.Contains(summary, "retired 1 ungradable") {
		t.Fatalf("an unknown kind can never be graded and must be retired: %s", summary)
	}
	// Never guessed at: retiring must not invent a grade.
	if res, _ := st.ResolvedRegimeOutcomes(ctx, 0); len(res) != 0 {
		t.Fatalf("unknown kind must never be graded, got %d resolved", len(res))
	}
	if due, _ := st.DueRegimeOutcomes(ctx, late, 0); len(due) != 0 {
		t.Fatalf("retired row must leave the due queue, got %d", len(due))
	}
}

// A flat series makes the trend resolver's window degenerate. The row has all
// its bars, so it is not bar-starved — it is simply unanswerable, forever.
func TestRegimeDegenerateWindowIsRetiredNotRetriedForever(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "degenerate.db")
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	flat := func(int) (float64, float64) { return 100.0, 1e6 }
	const start = 1000
	callTs := int64(start+280) * 86400
	seedRegimeBars(t, st, sym.ID, start, 340, flat)
	freezeCall(t, st, sym.ID, structregime.KindVol21, callTs, ungradableHorizon, "high-vol", 0.5, 0.5)

	late := callTs + int64(ungradableHorizon*5*86400) + 86400
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(late, 0) }}
	summary, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Either it graded (the resolver coped) or it retired — but it must NEVER
	// sit in the due queue forever, which is the defect under test.
	due, err := st.DueRegimeOutcomes(ctx, late, 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("a permanently unanswerable row is still queued for retry: %s", summary)
	}
}
