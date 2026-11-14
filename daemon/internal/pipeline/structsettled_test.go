package pipeline

import (
	"math"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Three daily stock bars at 05:00Z on consecutive days. A stock daily bar
// settles 22 hours after its stamp (md.DailyBarSettled).
func settledFixture() (ts []int64, d0, d1, d2 int64) {
	d0 = int64(1_800_000_000)
	d1, d2 = d0+86400, d0+2*86400
	return []int64{d0, d1, d2}, d0, d1, d2
}

func withCutover(t *testing.T, v int64) {
	t.Helper()
	old := structSettledSinceTs
	structSettledSinceTs = v
	t.Cleanup(func() { structSettledSinceTs = old })
}

// The shipped cut-over is the registered one, and nothing else.
func TestStructSettled_CutoverIsTheRegisteredEpoch(t *testing.T) {
	if structSettledSinceTs != store.StructSettledEpochTS {
		t.Fatalf("cut-over %d is not the registered epoch %d", structSettledSinceTs, store.StructSettledEpochTS)
	}
	if got := time.Unix(store.StructSettledEpochTS, 0).UTC().Format(time.RFC3339); got != "2026-11-16T05:00:00Z" {
		t.Fatalf("registered epoch is %s, want 2026-11-16T05:00:00Z (Monday 00:00 New York)", got)
	}
}

// Before the cut-over every helper returns what the old code computed.
func TestStructSettled_BeforeCutoverChangesNothing(t *testing.T) {
	withCutover(t, math.MaxInt64)
	ts, _, _, d2 := settledFixture()
	mid := d2 + 10*3600 // ten hours into the newest bar: still forming
	if got := structCallIndex(md.Stocks, ts, mid); got != 2 {
		t.Errorf("dormant call index = %d, want 2 (the bar at or before the call)", got)
	}
	if got := structUsableLen(md.Stocks, ts, mid, mid); got != 3 {
		t.Errorf("dormant usable length = %d, want 3", got)
	}
	bars := []md.Bar{{Ts: ts[0]}, {Ts: ts[1]}, {Ts: ts[2]}}
	if got := structSettledBars(md.Stocks, bars, mid); len(got) != 3 {
		t.Errorf("dormant predictor input kept %d bars, want 3", len(got))
	}
}

// Live: a mid-session call is built from, and frozen against, the last SETTLED
// bar, and a forward bar that is still forming cannot resolve it.
func TestStructSettled_AfterCutoverUsesSettledBarsOnly(t *testing.T) {
	ts, d0, _, d2 := settledFixture()
	withCutover(t, d0)
	mid := d2 + 10*3600
	late := d2 + 23*3600 // the newest bar has settled

	bars := []md.Bar{{Ts: ts[0]}, {Ts: ts[1]}, {Ts: ts[2]}}
	if got := structSettledBars(md.Stocks, bars, mid); len(got) != 2 {
		t.Errorf("mid-session predictor input kept %d bars, want 2 (forming bar dropped)", len(got))
	}
	if got := structSettledBars(md.Stocks, bars, late); len(got) != 3 {
		t.Errorf("after settlement the predictor input kept %d bars, want 3", len(got))
	}
	if got := structCallIndex(md.Stocks, ts, mid); got != 1 {
		t.Errorf("mid-session call index = %d, want 1 (last settled bar)", got)
	}
	if got := structCallIndex(md.Stocks, ts, late); got != 2 {
		t.Errorf("post-settlement call index = %d, want 2", got)
	}
	if got := structUsableLen(md.Stocks, ts, mid, mid); got != 2 {
		t.Errorf("usable length while the newest bar forms = %d, want 2", got)
	}
	if got := structUsableLen(md.Stocks, ts, mid, late); got != 3 {
		t.Errorf("usable length once it settles = %d, want 3", got)
	}
}

// A call frozen BEFORE the cut-over keeps its old rule after the cut-over.
func TestStructSettled_OldRowsKeepTheirRule(t *testing.T) {
	ts, _, d1, d2 := settledFixture()
	withCutover(t, d2)  // cut-over lands on the third day
	old := d1 + 10*3600 // a mid-session call on the second day, before it
	if got := structCallIndex(md.Stocks, ts[:2], old); got != 1 {
		t.Errorf("pre-cut-over call index = %d, want 1 (its own forming bar, as frozen)", got)
	}
	now := d2 + 10*3600
	if got := structUsableLen(md.Stocks, ts, old, now); got != 3 {
		t.Errorf("pre-cut-over row usable length = %d, want 3 (old rule)", got)
	}
}
