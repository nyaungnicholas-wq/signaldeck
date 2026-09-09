package pipeline

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func nyLoc(t *testing.T) *time.Location {
	t.Helper()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return ny
}

func dayKey(d time.Time) int64 {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400
}

// fullCounts marks every NYSE trading day in [from, to] as fully covered.
func fullCounts(from, to time.Time) map[int64]int {
	counts := map[int64]int{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if marketcal.IsTradingDay(d) {
			counts[dayKey(d)] = 390
		}
	}
	return counts
}

func TestGapSessions(t *testing.T) {
	ny := nyLoc(t)
	now := time.Date(2026, time.September, 9, 10, 0, 0, 0, ny)
	from, to := time.Date(2026, time.August, 11, 0, 0, 0, 0, ny), time.Date(2026, time.September, 8, 0, 0, 0, 0, ny)

	counts := fullCounts(from, to)
	counts[dayKey(time.Date(2026, time.September, 4, 0, 0, 0, 0, ny))] = 120
	delete(counts, dayKey(time.Date(2026, time.September, 8, 0, 0, 0, 0, ny)))
	got := gapSessions(counts, now, 30)
	want := []string{"2026-09-04", "2026-09-08"}
	if len(got) != len(want) {
		t.Fatalf("gaps = %v, want %v", got, want)
	}
	for i, d := range got {
		if d.Format("2006-01-02") != want[i] {
			t.Errorf("gaps[%d] = %s, want %s", i, d.Format("2006-01-02"), want[i])
		}
	}

	if g := gapSessions(fullCounts(from, to), now, 30); len(g) != 0 {
		t.Errorf("fully covered window reported gaps: %v", g)
	}
	if g := gapSessions(map[int64]int{}, now, 1); len(g) != 0 {
		t.Errorf("retention 1 must report nothing, got %v", g)
	}

	// The day after Thanksgiving is a half day: 200 bars is complete, 100 is not.
	nowNov := time.Date(2026, time.November, 30, 10, 0, 0, 0, ny)
	half := time.Date(2026, time.November, 27, 0, 0, 0, 0, ny)
	if !marketcal.IsHalfDay(half) {
		t.Fatalf("%s is expected to be a half day", half.Format("2006-01-02"))
	}
	nov := fullCounts(nowNov.AddDate(0, 0, -9), nowNov.AddDate(0, 0, -1))
	nov[dayKey(half)] = 200
	if g := gapSessions(nov, nowNov, 10); len(g) != 0 {
		t.Errorf("200 bars on a half day is complete, got gaps %v", g)
	}
	nov[dayKey(half)] = 100
	if g := gapSessions(nov, nowNov, 10); len(g) != 1 || g[0].Format("2006-01-02") != "2026-11-27" {
		t.Errorf("100 bars on a half day must be a gap, got %v", g)
	}
}

// sessionBars returns 320 one-minute bars from 09:30 New York on day d.
func sessionBars(id int64, d time.Time) []md.Bar {
	start := time.Date(d.Year(), d.Month(), d.Day(), 9, 30, 0, 0, d.Location()).Unix()
	out := make([]md.Bar, 0, 320)
	for i := int64(0); i < 320; i++ {
		out = append(out, md.Bar{SymbolID: id, TF: md.TF1m, Ts: start + i*60, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1})
	}
	return out
}

func dailyBars(id int64, now time.Time) []md.Bar {
	out := make([]md.Bar, 0, 200)
	for i := 1; i <= 200; i++ {
		out = append(out, md.Bar{SymbolID: id, TF: md.TF1d, Ts: now.AddDate(0, 0, -i).Unix(), Open: 1, High: 1, Low: 1, Close: 1})
	}
	return out
}

// gapFixture seeds GAP (streamed, one session missing), FULL (streamed, complete)
// and DAILY (not streamed) inside a 10-day retention window and returns the store.
func gapFixture(t *testing.T) (*store.Store, md.Symbol) {
	t.Helper()
	ctx := context.Background()
	ny := nyLoc(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "gap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().In(ny)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ny)
	var sessions []time.Time
	for d := today.AddDate(0, 0, -9); d.Before(today); d = d.AddDate(0, 0, 1) {
		if marketcal.IsTradingDay(d) {
			sessions = append(sessions, d)
		}
	}
	if len(sessions) < 2 {
		t.Fatalf("need at least two sessions in the window, got %d", len(sessions))
	}

	mk := func(name string, stream bool) md.Symbol {
		s, err := st.UpsertSymbol(ctx, name, md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetSymbolStream(ctx, s.ID, stream); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertBars(ctx, dailyBars(s.ID, now)); err != nil {
			t.Fatal(err)
		}
		return s
	}
	gap, full, daily := mk("GAP", true), mk("FULL", true), mk("DAILY", false)

	var gapBars, fullBars []md.Bar
	for i, d := range sessions {
		fullBars = append(fullBars, sessionBars(full.ID, d)...)
		if i != len(sessions)-1 { // GAP misses the most recent session before today
			gapBars = append(gapBars, sessionBars(gap.ID, d)...)
		}
	}
	if err := st.UpsertBars(ctx, gapBars); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, fullBars); err != nil {
		t.Fatal(err)
	}
	// DAILY keeps its 1m count over the old under-coverage floor, outside the window.
	if err := st.UpsertBars(ctx, sessionBars(daily.ID, today.AddDate(0, 0, -40))); err != nil {
		t.Fatal(err)
	}
	return st, gap
}

func drain(bf *Backfiller) []md.Symbol {
	var out []md.Symbol
	for {
		select {
		case s := <-bf.queue:
			out = append(out, s)
		default:
			return out
		}
	}
}

func TestBackfillReconcilerGapFillsStreamedSymbols(t *testing.T) {
	t.Setenv("SIGNALDECK_1M_RETENTION_D", "10")
	t.Setenv("SIGNALDECK_BUDGET_DB_MB", "6144")
	t.Setenv("SIGNALDECK_1M_GAPFILL", "")
	ctx := context.Background()
	st, gap := gapFixture(t)
	bf := NewBackfiller(st, nil, nil)
	r := &BackfillReconciler{St: st, BF: bf}

	detail, err := r.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := drain(bf)
	if len(got) != 1 || got[0].ID != gap.ID {
		t.Fatalf("enqueued %v, want exactly GAP (id %d); detail %q", got, gap.ID, detail)
	}
	if !strings.Contains(detail, "gap-filled 1 streamed") || !strings.Contains(detail, "1 streamed with a session gap") {
		t.Errorf("detail = %q", detail)
	}
	if v, _ := st.GetMeta(ctx, "gapfill_last_"+strconv.FormatInt(gap.ID, 10)); v == "" {
		t.Errorf("cooldown meta not recorded")
	}

	detail, err = r.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again := drain(bf); len(again) != 0 || !strings.Contains(detail, "gap-filled 0 streamed") {
		t.Errorf("second pass must respect the cooldown: enqueued %v, detail %q", again, detail)
	}
}

func TestBackfillReconcilerGapFillRespectsBudgetAndSwitch(t *testing.T) {
	t.Setenv("SIGNALDECK_1M_RETENTION_D", "10")
	t.Setenv("SIGNALDECK_1M_GAPFILL", "")
	ctx := context.Background()
	st, _ := gapFixture(t)
	bf := NewBackfiller(st, nil, nil)
	r := &BackfillReconciler{St: st, BF: bf}

	t.Setenv("SIGNALDECK_BUDGET_DB_MB", "1")
	detail, err := r.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q := drain(bf); len(q) != 0 || !strings.Contains(detail, "gap-fill paused") {
		t.Errorf("no headroom must pause gap-fill: enqueued %v, detail %q", q, detail)
	}

	t.Setenv("SIGNALDECK_BUDGET_DB_MB", "6144")
	t.Setenv("SIGNALDECK_1M_GAPFILL", "off")
	detail, err = r.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q := drain(bf); len(q) != 0 || !strings.Contains(detail, "gap-fill off") {
		t.Errorf("the switch must disable gap-fill: enqueued %v, detail %q", q, detail)
	}
}
