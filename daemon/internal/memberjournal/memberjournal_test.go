package memberjournal

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func et(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, marketcal.Loc())
}

func day(t time.Time) string { return t.In(marketcal.Loc()).Format("2006-01-02 15:04") }

// No lookahead: entry is the first session CLOSE strictly after the call.
func TestScheduleEntryIsFirstCloseAfterTheCall(t *testing.T) {
	for _, c := range []struct {
		name      string
		created   time.Time
		horizon   int
		entry, ex string
	}{
		{"before the close enters on that close", et(2026, 9, 30, 15, 59), 1, "2026-09-30 16:00", "2026-10-01 16:00"},
		{"at the close is not after it", et(2026, 9, 30, 16, 0), 1, "2026-10-01 16:00", "2026-10-02 16:00"},
		{"after the close enters next session", et(2026, 9, 30, 16, 1), 1, "2026-10-01 16:00", "2026-10-02 16:00"},
		{"pre-market enters that day", et(2026, 9, 30, 4, 0), 5, "2026-09-30 16:00", "2026-10-07 16:00"},
		{"saturday enters monday", et(2026, 10, 3, 11, 0), 1, "2026-10-05 16:00", "2026-10-06 16:00"},
		{"friday evening skips the weekend", et(2026, 10, 2, 20, 0), 5, "2026-10-05 16:00", "2026-10-12 16:00"},
		// Thanksgiving (Thu 2026-11-26) is closed; Fri 11-27 closes at 13:00.
		{"holiday eve evening enters the half day", et(2026, 11, 25, 17, 0), 1, "2026-11-27 13:00", "2026-11-30 16:00"},
		{"holiday itself enters the half day", et(2026, 11, 26, 10, 0), 1, "2026-11-27 13:00", "2026-11-30 16:00"},
		{"half day before its 13:00 close", et(2026, 11, 27, 12, 59), 1, "2026-11-27 13:00", "2026-11-30 16:00"},
		{"half day after its 13:00 close", et(2026, 11, 27, 13, 1), 1, "2026-11-30 16:00", "2026-12-01 16:00"},
		{"exit skips a holiday", et(2026, 11, 25, 10, 0), 1, "2026-11-25 16:00", "2026-11-27 13:00"},
		// 21 sessions from Tue 12-01: Christmas (Fri 12-25) is closed.
		{"21 sessions across christmas", et(2026, 12, 1, 10, 0), 21, "2026-12-01 16:00", "2026-12-31 16:00"},
		// DST ends Sun 2026-11-01: ET-local dates and 16:00 closes hold across it.
		{"across the DST change", et(2026, 10, 30, 20, 0), 5, "2026-11-02 16:00", "2026-11-09 16:00"},
	} {
		e, x := Schedule(c.created, c.horizon)
		if day(e) != c.entry || day(x) != c.ex {
			t.Errorf("%s: entry %s exit %s, want %s / %s", c.name, day(e), day(x), c.entry, c.ex)
		}
		if !e.After(c.created) {
			t.Errorf("%s: entry %s is not after the call %s (lookahead)", c.name, day(e), day(c.created))
		}
	}
}

func TestGrade(t *testing.T) {
	for _, c := range []struct {
		call        string
		entry, exit float64
		want        string
	}{
		{"up", 100, 101, "hit"}, {"up", 100, 99, "miss"}, {"down", 100, 99, "hit"},
		{"down", 100, 101, "miss"}, {"up", 100, 100, ""}, {"down", 100, 100, ""},
	} {
		if got := Grade(c.call, c.entry, c.exit); got != c.want {
			t.Errorf("Grade(%s, %v, %v) = %q, want %q", c.call, c.entry, c.exit, got, c.want)
		}
	}
}

func rows(upHit, upMiss, downHit, downMiss int) []store.MemberCall {
	var out []store.MemberCall
	add := func(n int, call, outcome string) {
		for i := 0; i < n; i++ {
			// One call per entry session: each is its own independent unit.
			out = append(out, store.MemberCall{Call: call, Status: "resolved", Outcome: outcome,
				EntryTs: int64(len(out)+1) * 86400})
		}
	}
	add(upHit, "up", "hit")
	add(upMiss, "up", "miss")
	add(downHit, "down", "hit")
	add(downMiss, "down", "miss")
	return out
}

func TestSummarizeWilsonBaselineAndWithholding(t *testing.T) {
	// 28 hits of 40: Wilson 95% = [0.5457, 0.8193] (textbook value).
	// Went up: 20 up-hits + 2 down-misses = 22 of 40.
	calls := append(rows(20, 10, 8, 2),
		store.MemberCall{Status: "open"}, store.MemberCall{Status: "void"}, store.MemberCall{Status: "withdrawn"})
	s := Summarize(calls)
	if s.Resolved != 40 || s.Hits != 28 || s.Misses != 12 || s.Open != 1 || s.Void != 1 || s.Withdrawn != 1 || s.Withheld {
		t.Fatalf("counts: %+v", s)
	}
	near := func(what string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 5e-5 {
			t.Errorf("%s = %v, want %.4f", what, got, want)
		}
	}
	near("hitRate", s.HitRate, 0.7)
	near("ciLow", s.CILow, 0.5457)
	near("ciHigh", s.CIHigh, 0.8193)
	near("baselineUpRate", s.BaselineUpRate, 0.55)

	// The floor: 29 resolved shows counts only; 30 shows the figures.
	s = Summarize(rows(29, 0, 0, 0))
	if !s.Withheld || s.HitRate != nil || s.CILow != nil || s.CIHigh != nil || s.BaselineUpRate != nil || s.MinN != 30 || s.Hits != 29 {
		t.Errorf("29 resolved: %+v, want withheld with no rates", s)
	}
	s = Summarize(rows(30, 0, 0, 0))
	if s.Withheld || s.HitRate == nil || *s.HitRate != 1 {
		t.Errorf("30 resolved: %+v, want shown", s)
	}
	// Void, open and withdrawn calls never count toward the floor.
	s = Summarize(append(rows(29, 0, 0, 0), store.MemberCall{Status: "void"}, store.MemberCall{Status: "open"}))
	if !s.Withheld || s.Resolved != 29 {
		t.Errorf("29 resolved + void + open: %+v", s)
	}
}

// fixture: a store, a member, a stock, and daily bars stamped at ET midnight
// (the US bar stamp) for the given sessions.
type fixture struct {
	st  *store.Store
	uid int64
	sym md.Symbol
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	uid, err := st.CreateVerifiedUser(ctx, "ann", "ann@gmail.com", "x")
	if err != nil {
		t.Fatal(err)
	}
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{st, uid, sym}
}

func (f fixture) bar(t *testing.T, d time.Time, close float64) {
	t.Helper()
	ts := marketcal.SessionDate(d).Unix()
	if err := f.st.UpsertBars(context.Background(), []md.Bar{{SymbolID: f.sym.ID, TF: md.TF1d, Ts: ts,
		Open: close, High: close, Low: close, Close: close, Volume: 1}}); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) call(t *testing.T, created time.Time, call string, horizon int) int64 {
	t.Helper()
	e, x := Schedule(created, horizon)
	id, err := f.st.InsertMemberCall(context.Background(), store.MemberCall{UserID: f.uid, SymbolID: f.sym.ID,
		Market: "stocks", Call: call, Horizon: horizon, CreatedTs: created.Unix(), EntryTs: e.Unix(), ExitDueTs: x.Unix()},
		marketcal.SessionDate(created).Unix())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) get(t *testing.T, id int64) store.MemberCall {
	t.Helper()
	c, ok, err := f.st.MemberCall(context.Background(), f.uid, id)
	if err != nil || !ok {
		t.Fatalf("call %d: ok=%v err=%v", id, ok, err)
	}
	return c
}

func TestResolverGradesSettledCallsOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Mon 09-28 .. Mon 10-05 closes.
	closes := map[int]float64{28: 100, 29: 105, 30: 103, 1: 103, 2: 110, 5: 111}
	date := func(d int) time.Time {
		if d > 20 {
			return et(2026, 9, d, 12, 0)
		}
		return et(2026, 10, d, 12, 0)
	}
	// Call made Mon 09-28 15:59: entry is THAT close (100), not Tuesday's.
	up := f.call(t, et(2026, 9, 28, 15, 59), "up", 1)     // 100 -> 105 hit
	down := f.call(t, et(2026, 9, 28, 16, 30), "down", 1) // 105 -> 103 hit (entry Tue, not Mon)
	flat := f.call(t, et(2026, 9, 29, 17, 0), "up", 1)    // 103 -> 103 void
	week := f.call(t, et(2026, 9, 28, 10, 0), "down", 5)  // 100 -> 111 (Mon 10-05) miss
	r := &Resolver{St: f.st}
	run := func(now time.Time) string {
		t.Helper()
		r.Now = func() time.Time { return now }
		out, err := r.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, d := range []int{28, 29} {
		f.bar(t, date(d), closes[d])
	}
	// Tue 18:00: the up call's exit bar (Tue) exists but no later bar yet, so
	// its close may still be live: stays open.
	run(et(2026, 9, 29, 18, 0))
	if c := f.get(t, up); c.Status != "open" {
		t.Fatalf("graded on an unsettled exit bar: %+v", c)
	}
	// Wed 18:00: Wed's bar settles Tue.
	f.bar(t, date(30), closes[30])
	run(et(2026, 9, 30, 18, 0))
	if c := f.get(t, up); c.Status != "resolved" || c.Outcome != "hit" {
		t.Errorf("up call: %+v, want resolved hit", c)
	}
	if c := f.get(t, down); c.Status != "open" {
		t.Errorf("down call (exit Wed) graded before Wed settled: %+v", c)
	}
	for _, d := range []int{1, 2, 5} {
		f.bar(t, date(d), closes[d])
	}
	out := run(et(2026, 10, 5, 18, 0))
	if c := f.get(t, down); c.Status != "resolved" || c.Outcome != "hit" {
		t.Errorf("down call: %+v, want resolved hit (entry Tue 105, exit Wed 103)", c)
	}
	if c := f.get(t, flat); c.Status != "void" || c.Outcome != "" {
		t.Errorf("flat call: %+v, want void", c)
	}
	if c := f.get(t, week); c.Status != "open" {
		t.Errorf("week call graded before its exit (Mon 10-05) settled: %+v", c)
	}
	if !strings.Contains(out, "graded 1 member calls, voided 1") {
		t.Errorf("run detail %q", out)
	}
	f.bar(t, et(2026, 10, 6, 12, 0), 90)
	run(et(2026, 10, 6, 18, 0))
	if c := f.get(t, week); c.Status != "resolved" || c.Outcome != "miss" {
		t.Errorf("week call: %+v, want resolved miss (100 -> 111)", c)
	}
	// Idempotent: a rerun changes nothing.
	before, _ := f.st.MemberCalls(ctx, f.uid)
	if out := run(et(2026, 10, 7, 18, 0)); !strings.HasPrefix(out, "graded 0 member calls, voided 0, 0 waiting") {
		t.Errorf("rerun detail %q", out)
	}
	after, _ := f.st.MemberCalls(ctx, f.uid)
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("rerun rewrote %+v -> %+v", before[i], after[i])
		}
	}
}

func TestResolverVoidsAfterTenSessionsWithoutData(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.call(t, et(2026, 9, 28, 10, 0), "up", 1) // exit Tue 09-29; no bars ever
	r := &Resolver{St: f.st}
	at := func(now time.Time) store.MemberCall {
		r.Now = func() time.Time { return now }
		if _, err := r.Run(ctx); err != nil {
			t.Fatal(err)
		}
		return f.get(t, id)
	}
	// Sessions closed after Tue 09-29: 09-30 .. 10-12 is 9 (Columbus Day is not
	// an NYSE holiday), 10-13 makes 10.
	if c := at(et(2026, 10, 12, 18, 0)); c.Status != "open" {
		t.Fatalf("voided after 9 sessions: %+v", c)
	}
	if c := at(et(2026, 10, 13, 18, 0)); c.Status != "void" {
		t.Fatalf("still %s after 10 sessions without data", c.Status)
	}
}

func TestWithdrawOnlyBeforeTheEntryBar(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.call(t, et(2026, 9, 28, 20, 0), "up", 1) // entry Tue 09-29 close
	c := f.get(t, id)
	ok := func(now time.Time) bool {
		t.Helper()
		v, err := CanWithdraw(ctx, f.st, c, now)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !ok(et(2026, 9, 29, 8, 0)) {
		t.Error("cannot withdraw before the entry bar exists")
	}
	if ok(et(2026, 9, 29, 16, 0)) {
		t.Error("withdrawable at the entry close even with no bar (its close is public)")
	}
	f.bar(t, et(2026, 9, 29, 12, 0), 100)
	if ok(et(2026, 9, 29, 9, 45)) {
		t.Error("withdrawable after the entry bar exists")
	}
	c.Status = "resolved"
	if v, _ := CanWithdraw(ctx, f.st, c, et(2026, 9, 28, 21, 0)); v {
		t.Error("withdrawable once settled")
	}
}

func TestResolverSchedule(t *testing.T) {
	r := &Resolver{}
	if r.Name() != "member-call-resolver" {
		t.Errorf("name %q", r.Name())
	}
	// Friday 19:00 after a Friday 18:00 run: next is Monday 18:00.
	last := et(2026, 10, 2, 18, 0)
	if got := r.NextFire(last, et(2026, 10, 2, 19, 0)); day(got) != "2026-10-05 18:00" {
		t.Errorf("next fire %s, want Monday 18:00", day(got))
	}
}

// TestJournalFeedsNoForecast holds publisher guardrail 10: members' calls are
// graded, never read by anything that forecasts. Only the files below may
// name the journal's table, rows or package.
func TestJournalFeedsNoForecast(t *testing.T) {
	allowed := map[string]bool{
		"internal/store/membercalls.go":           true, // the table's only reader/writer
		"internal/store/accounts.go":              true, // purge with the account
		"internal/store/accountdata.go":           true, // the member's own export (graded calls, no prices) and erase with the account (AUD-05)
		"internal/memberjournal/memberjournal.go": true,
		"internal/api/journal.go":                 true, // the member's own routes
		"internal/api/accounts.go":                true, // memberRoutes entry
		"cmd/signaldeckd/run.go":                  true, // registers the resolver
		"internal/copilot/catalog.go":             true, // ask the data: the asking member's OWN counts, served only to them
	}
	seen := 0
	for _, dir := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			seen++
			rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(p), "../../"))
			src := string(b)
			if (strings.Contains(src, "member_calls") || strings.Contains(src, "MemberCall") ||
				strings.Contains(src, "memberjournal")) && !allowed[rel] {
				t.Errorf("%s reads the member journal: members' calls must never feed a forecast "+
					"(docs/PUBLISHER_GUARDRAILS.md rule 10)", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen < 500 {
		t.Fatalf("scanned %d Go files: the walk is not seeing the tree", seen)
	}
}
