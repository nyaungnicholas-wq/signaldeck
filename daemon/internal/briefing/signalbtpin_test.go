package briefing

// STAGE 2 — weekly signal-backtest pin tests: the worker stores the graded
// snapshot under both meta keys, writes one honest insight, dedups within the
// week, and holds before the Sunday gate hour.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signalbt"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func TestSignalBTPinWorkerEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ny := NYLoc()
	now := time.Date(2026, 7, 5, 18, 30, 0, 0, ny) // Sunday 6:30pm ET

	// One resolved 1d observation in the feature store: two daily bars and a
	// resolved prediction at the first (fwd = 101/100-1).
	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	day := int64(86400)
	predTs := now.Add(-72 * time.Hour).Unix()
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: aapl.ID, TF: md.TF1d, Ts: predTs, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: aapl.ID, TF: md.TF1d, Ts: predTs + day, Open: 101, High: 101, Low: 101, Close: 101},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: aapl.ID, Horizon: md.H1d, Ts: predTs,
		RawProb: 0.7, CalProb: 0.7, NUsed: 3, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolvePrediction(ctx, aapl.ID, md.H1d, predTs, 0.01); err != nil {
		t.Fatal(err)
	}

	w := &SignalBTPinWorker{St: st, Loc: ny, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "2026-07-05") {
		t.Errorf("detail = %q", detail)
	}

	// Both meta keys hold the same parseable snapshot.
	for _, key := range []string{signalbt.MetaKeyLatest, signalbt.MetaKeyWeekly("2026-07-05")} {
		raw, err := st.GetMeta(ctx, key)
		if err != nil || raw == "" {
			t.Fatalf("meta %q missing (err=%v)", key, err)
		}
		var p signalbt.Pinned
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("meta %q unparseable: %v", key, err)
		}
		if p.DayKey != "2026-07-05" || p.ComputedTs != now.Unix() {
			t.Fatalf("meta %q envelope = %+v", key, p)
		}
		res, ok := p.Results["1d"]
		if !ok {
			t.Fatalf("meta %q missing the 1d result", key)
		}
		if res.RawN != 1 || res.IndependentN != 1 || !res.Gated {
			t.Fatalf("1d result = rawN %d indepN %d gated %v — want 1/1/gated", res.RawN, res.IndependentN, res.Gated)
		}
		if res.Live {
			t.Fatal("a pinned replay must never claim to be live")
		}
		if _, ok := p.Results["1w"]; !ok {
			t.Fatalf("meta %q missing the 1w result (0 obs is still an honest result)", key)
		}
	}

	// One honest insight: gated truth stated, labeled backtested.
	ins, err := st.InsightsByKind(ctx, SignalBTKind, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 1 {
		t.Fatalf("insights stored = %d want 1", len(ins))
	}
	body := ins[0].Body
	for _, want := range []string{
		"1d: GATED — 1 of 30",
		"BACKTESTED",
		"not a forecast",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}

	// Same-week rerun: dedup → waiting, still one insight, snapshot unchanged.
	detail, err = w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "waiting") {
		t.Errorf("rerun detail = %q want waiting", detail)
	}
	if ins, _ = st.InsightsByKind(ctx, SignalBTKind, 5); len(ins) != 1 {
		t.Fatalf("same-week rerun duplicated the pin insight: %d", len(ins))
	}

	// Next Sunday: a fresh pin lands under its own week key AND replaces latest.
	next := time.Date(2026, 7, 12, 18, 5, 0, 0, ny)
	w.Now = func() time.Time { return next }
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.GetMeta(ctx, signalbt.MetaKeyLatest)
	var latest signalbt.Pinned
	if err := json.Unmarshal([]byte(raw), &latest); err != nil || latest.DayKey != "2026-07-12" {
		t.Fatalf("latest not replaced: %q err=%v", latest.DayKey, err)
	}
	if raw, _ := st.GetMeta(ctx, signalbt.MetaKeyWeekly("2026-07-05")); raw == "" {
		t.Fatal("the immutable per-week record for 2026-07-05 must survive")
	}
}

func TestSignalBTPinWorkerHoldsBeforeHour(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	ny := NYLoc()
	w := &SignalBTPinWorker{St: st, Loc: ny, Now: func() time.Time {
		return time.Date(2026, 7, 5, 17, 30, 0, 0, ny) // after the report hour, before the pin hour
	}}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "waiting") {
		t.Errorf("detail = %q want waiting", detail)
	}
	if raw, _ := st.GetMeta(ctx, signalbt.MetaKeyLatest); raw != "" {
		t.Fatal("pin stored before the Sunday gate hour")
	}
}

// TestDownsampleEquity pins the storage decimation contract: first/last marks
// (and therefore total returns) survive, short curves pass through untouched.
func TestDownsampleEquity(t *testing.T) {
	pts := make([]signalbt.EquityPoint, 100)
	for i := range pts {
		pts[i] = signalbt.EquityPoint{Ts: int64(i), Strategy: 1 + float64(i)/100}
	}
	out, ds := signalbt.DownsampleEquity(pts, 10)
	if !ds || len(out) != 10 {
		t.Fatalf("downsampled = %d points (ds=%v) want 10", len(out), ds)
	}
	if out[0].Ts != 0 || out[len(out)-1].Ts != 99 {
		t.Fatalf("first/last marks not preserved: %v..%v", out[0].Ts, out[len(out)-1].Ts)
	}
	same, ds := signalbt.DownsampleEquity(pts[:5], 10)
	if ds || len(same) != 5 {
		t.Fatalf("short curve must pass through untouched (%d, ds=%v)", len(same), ds)
	}
}
