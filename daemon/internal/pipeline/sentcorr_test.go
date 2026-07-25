package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/news"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/newssent"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sentcorr"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newSentWorkerStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sentworker.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// The scorer must turn raw headlines into aligned daily features, and the
// alignment must be as-of: an after-close headline belongs to the NEXT session.
func TestSentimentLexScorerBuildsAsOfAlignedFeatures(t *testing.T) {
	ctx := context.Background()
	st := newSentWorkerStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Wed 2026-07-08: one headline during the session, one after the close.
	during := time.Date(2026, time.July, 8, 11, 0, 0, 0, marketcal.Loc()).Unix()
	after := time.Date(2026, time.July, 8, 18, 0, 0, 0, marketcal.Loc()).Unix()
	news := []store.NewsItem{
		{ID: "n1", SymbolID: sym.ID, Ts: during, Headline: "AAA beats estimates and raises guidance"},
		{ID: "n2", SymbolID: sym.ID, Ts: after, Headline: "AAA cuts guidance after the bell"},
		{ID: "n3", SymbolID: sym.ID, Ts: during, Headline: "AAA to hold its annual meeting"},
	}
	for _, n := range news {
		if err := st.InsertNews(ctx, n); err != nil {
			t.Fatalf("insert %s: %v", n.ID, err)
		}
	}

	w := &SentimentLexScorer{St: st, Now: func() time.Time {
		return time.Date(2026, time.July, 9, 12, 0, 0, 0, time.UTC)
	}}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "scored 3 headlines") {
		t.Errorf("summary = %q, want it to report 3 scored", msg)
	}

	feats, err := st.SymbolSentimentFeatures(ctx, sym.ID, 10)
	if err != nil {
		t.Fatalf("features: %v", err)
	}
	byDay := map[string]store.SentimentFeature{}
	for _, f := range feats {
		byDay[f.Day] = f
	}

	d8, ok := byDay["2026-07-08"]
	if !ok {
		t.Fatalf("no feature for 2026-07-08; got %v", byDay)
	}
	// Two headlines land on the 8th, but only ONE expressed polarity — the
	// meeting notice is not an opinion and must not be averaged in as a zero.
	if d8.NAll != 2 || d8.NPolar != 1 {
		t.Errorf("2026-07-08: nAll=%d nPolar=%d, want 2 and 1", d8.NAll, d8.NPolar)
	}
	if d8.MeanScore <= 0 {
		t.Errorf("2026-07-08 mean = %.3f, want positive (the beat)", d8.MeanScore)
	}

	d9, ok := byDay["2026-07-09"]
	if !ok {
		t.Fatalf("the after-close headline should have rolled to 2026-07-09; got %v", byDay)
	}
	if d9.MeanScore >= 0 {
		t.Errorf("2026-07-09 mean = %.3f, want negative (the guidance cut)", d9.MeanScore)
	}
	if d9.Ver != newssent.Version {
		t.Errorf("feature version = %d, want %d", d9.Ver, newssent.Version)
	}
}

func TestSentimentLexScorerIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newSentWorkerStore(t)
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	ts := time.Date(2026, time.July, 8, 11, 0, 0, 0, marketcal.Loc()).Unix()
	if err := st.InsertNews(ctx, store.NewsItem{
		ID: "n1", SymbolID: sym.ID, Ts: ts, Headline: "AAA beats estimates",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	now := func() time.Time { return time.Date(2026, time.July, 9, 12, 0, 0, 0, time.UTC) }
	w := &SentimentLexScorer{St: st, Now: now}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, _ := st.SymbolSentimentFeatures(ctx, sym.ID, 10)

	// Second pass: nothing new to score, and the features must be identical
	// rather than duplicated or drifted.
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(msg, "scored 0 headlines") {
		t.Errorf("second run summary = %q, want 0 newly scored", msg)
	}
	second, _ := st.SymbolSentimentFeatures(ctx, sym.ID, 10)
	if len(first) != len(second) {
		t.Fatalf("feature count changed %d -> %d on a re-run", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("feature %d changed on re-run: %+v vs %+v", i, first[i], second[i])
		}
	}
}

// On an empty database the study must store a GATED result per horizon rather
// than failing or inventing a number.
func TestSentCorrRunnerStoresGatedResultsOnThinData(t *testing.T) {
	ctx := context.Background()
	st := newSentWorkerStore(t)

	w := &SentCorrRunner{St: st, Now: func() time.Time {
		return time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	}}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "no verdict") {
		t.Errorf("summary = %q, want it to report no verdict on empty data", msg)
	}

	rows, err := st.SentCorrResults(ctx)
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	if len(rows) != len(SentCorrHorizons()) {
		t.Fatalf("stored %d results, want one per horizon (%d)", len(rows), len(SentCorrHorizons()))
	}
	for _, r := range rows {
		if !r.Gated {
			t.Errorf("horizon %d not gated on empty data", r.Horizon)
		}
		var res sentcorr.Result
		if err := json.Unmarshal([]byte(r.Payload), &res); err != nil {
			t.Fatalf("payload not decodable: %v", err)
		}
		if res.PartialIC != nil {
			t.Errorf("horizon %d reported a partial IC (%v) on empty data", r.Horizon, *res.PartialIC)
		}
		if res.GateReason == "" {
			t.Errorf("horizon %d gave no reason for withholding", r.Horizon)
		}
		// The family correction must be applied even when gated, so a later
		// ungated run cannot silently use a narrower standard.
		if res.FamilySize != len(SentCorrHorizons()) {
			t.Errorf("horizon %d familySize = %d, want %d",
				r.Horizon, res.FamilySize, len(SentCorrHorizons()))
		}
	}

	// Re-running overwrites in place: a repeated study must not accumulate rows
	// and masquerade as independent evidence.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	rows2, _ := st.SentCorrResults(ctx)
	if len(rows2) != len(rows) {
		t.Errorf("row count grew %d -> %d on a re-run; results must upsert in place",
			len(rows), len(rows2))
	}
}

func TestNewsBackfillerWithoutClientIsANoOp(t *testing.T) {
	// No Alpaca key is a normal configuration, not an error state.
	ctx := context.Background()
	st := newSentWorkerStore(t)
	w := &NewsBackfiller{St: st}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "skipped") {
		t.Errorf("summary = %q, want it to say it skipped", msg)
	}
}

// newsStub serves a fixed page count per request so the backfiller's budget
// behaviour can be exercised without a network.
type newsStub struct {
	pagesPerBatch int // pages before NextToken clears
	calls         int
	seen          map[string]int // symbol-set key -> times requested
}

func (s *newsStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.calls++
		syms := r.URL.Query().Get("symbols")
		if s.seen == nil {
			s.seen = map[string]int{}
		}
		tok := r.URL.Query().Get("page_token")
		idx := 0
		if tok != "" {
			idx, _ = strconv.Atoi(tok)
		}
		if idx == 0 {
			s.seen[syms]++
		}
		next := ""
		if idx+1 < s.pagesPerBatch {
			next = strconv.Itoa(idx + 1)
		}
		first := strings.Split(syms, ",")[0]
		body := map[string]any{
			"news": []map[string]any{{
				"id":         s.calls * 1000,
				"headline":   first + " beats estimates",
				"source":     "stub",
				"url":        "http://example.invalid",
				"created_at": "2026-05-04T14:00:00Z",
				"symbols":    []string{first},
			}},
		}
		if next != "" {
			body["next_page_token"] = next
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}

// THE PROGRESS TEST. The universe is many batches and one pass has a page
// budget, so a dense window cannot finish in a single run. Without a persisted
// batch cursor every pass restarts at batch 0, re-fetches the same headlines
// forever, and the archive never reaches further back — the backfill silently
// accomplishes nothing while reporting success.
func TestNewsBackfillerResumesAcrossPassesAndAdvancesTheWindow(t *testing.T) {
	ctx := context.Background()
	st := newSentWorkerStore(t)

	// Enough symbols to need several batches.
	total := news.MaxRangeSymbols*3 + 5
	for i := 0; i < total; i++ {
		if _, err := st.UpsertSymbol(ctx, fmt.Sprintf("SY%03d", i), md.Stocks, ""); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	stub := &newsStub{pagesPerBatch: 1}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	w := &NewsBackfiller{
		St:     st,
		Client: &news.Client{Base: srv.URL, HTTP: srv.Client()},
		Now:    func() time.Time { return now },
	}

	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "COMPLETE") {
		t.Fatalf("with a small universe the window should finish in one pass, got %q", msg)
	}

	floor, _ := st.GetMeta(ctx, metaNewsBackfillCursor)
	if floor == "" {
		t.Fatal("window cursor never advanced — the backfill would re-read the same month forever")
	}
	ts, _ := strconv.ParseInt(floor, 10, 64)
	firstFloor := time.Unix(ts, 0).UTC()
	if !firstFloor.Before(now) {
		t.Errorf("floor %s should be earlier than now", firstFloor)
	}

	// A second pass must move FURTHER back, not re-walk the same window.
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	floor2, _ := st.GetMeta(ctx, metaNewsBackfillCursor)
	ts2, _ := strconv.ParseInt(floor2, 10, 64)
	secondFloor := time.Unix(ts2, 0).UTC()
	if !secondFloor.Before(firstFloor) {
		t.Errorf("second pass floor %s did not move back past %s — no progress", secondFloor, firstFloor)
	}

	// Every batch was actually requested, so no symbols were skipped.
	if len(stub.seen) < 4 {
		t.Errorf("requested %d distinct symbol batches, want >= 4 (%d symbols)", len(stub.seen), total)
	}
}

// A pass that runs out of page budget must save its place, and the NEXT pass
// must continue from there rather than starting over.
func TestNewsBackfillerBudgetedPassResumesMidWindow(t *testing.T) {
	ctx := context.Background()
	st := newSentWorkerStore(t)
	// Many batches, and each batch costs several pages, so one pass cannot finish.
	total := news.MaxRangeSymbols * 40
	for i := 0; i < total; i++ {
		if _, err := st.UpsertSymbol(ctx, fmt.Sprintf("SY%04d", i), md.Stocks, ""); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	stub := &newsStub{pagesPerBatch: 3}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	w := &NewsBackfiller{
		St:     st,
		Client: &news.Client{Base: srv.URL, HTTP: srv.Client()},
		Now:    func() time.Time { return time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC) },
	}

	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "resumes next pass") {
		t.Fatalf("a budget-limited pass should say it will resume, got %q", msg)
	}
	if floor, _ := st.GetMeta(ctx, metaNewsBackfillCursor); floor != "" {
		t.Error("an unfinished window must NOT advance the floor — that would leave a permanent hole")
	}
	b1, _ := st.GetMeta(ctx, metaNewsBackfillBatch)
	if b1 == "" || b1 == "0" {
		t.Fatalf("batch cursor = %q, want a saved mid-window position", b1)
	}

	// Keep going until the window finishes. The invariant that matters is not how
	// many passes it takes, but that the passes COMPOSE: every batch gets fetched
	// exactly once and none is skipped by the budget cutting a pass short.
	passes := 1
	for {
		msg, err := w.Run(ctx)
		if err != nil {
			t.Fatalf("pass %d: %v", passes+1, err)
		}
		passes++
		if strings.Contains(msg, "COMPLETE") {
			break
		}
		if passes > 10 {
			t.Fatalf("window never completed after %d passes — the cursor is not making progress", passes)
		}
	}
	if passes < 2 {
		t.Errorf("expected the budget to force multiple passes, took %d", passes)
	}

	// Every batch requested, exactly once: nothing skipped, nothing re-walked.
	wantBatches := (total + news.MaxRangeSymbols - 1) / news.MaxRangeSymbols
	if len(stub.seen) != wantBatches {
		t.Errorf("fetched %d distinct batches, want %d — a batch was skipped", len(stub.seen), wantBatches)
	}
	for k, n := range stub.seen {
		if n != 1 {
			t.Errorf("batch %.20s fetched %d times, want exactly 1", k, n)
		}
	}
	if floor, _ := st.GetMeta(ctx, metaNewsBackfillCursor); floor == "" {
		t.Error("floor never advanced even after the window completed")
	}
	if b, _ := st.GetMeta(ctx, metaNewsBackfillBatch); b != "0" {
		t.Errorf("batch cursor = %q after completing a window, want reset to 0", b)
	}
}

// ── the feature backfill ─────────────────────────────────────────────────

// The bug this covers: the trailing rebuild is bounded at featureRebuildDays,
// so headlines older than that were stored, scored, and then never aligned. The
// study stayed pinned to ~14 months while the archive walked back years, and
// nothing reported the gap. The backwards sweep must reach that old headline.
func TestFeatureBackfillAlignsArchiveOlderThanTheTrailingWindow(t *testing.T) {
	ctx := context.Background()
	st := newSentWorkerStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	now := time.Date(2026, time.July, 9, 12, 0, 0, 0, time.UTC)
	// One headline inside the trailing window, one FAR outside it — three years
	// back, the shape of an archive the news backfiller has walked into.
	recent := now.AddDate(0, 0, -10)
	old := now.AddDate(-3, 0, 0)
	for _, n := range []store.NewsItem{
		{ID: "recent", SymbolID: sym.ID, Ts: recent.Unix(), Headline: "AAA beats estimates and raises guidance"},
		{ID: "old", SymbolID: sym.ID, Ts: old.Unix(), Headline: "AAA beats estimates and raises guidance"},
	} {
		if err := st.InsertNews(ctx, n); err != nil {
			t.Fatalf("insert %s: %v", n.ID, err)
		}
	}

	w := &SentimentLexScorer{St: st, Now: func() time.Time { return now }}

	// One pass aligns the recent headline and starts the backwards walk; the
	// old one is thousands of days back, so it takes several bounded passes.
	// Run until the cursor reports it has reached the archive floor.
	var lastMsg string
	for i := 0; i < 40; i++ {
		msg, err := w.Run(ctx)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		lastMsg = msg
		if strings.Contains(msg, "feature backfill complete") {
			break
		}
	}
	if !strings.Contains(lastMsg, "feature backfill complete") {
		t.Fatalf("backfill never completed; last message: %q", lastMsg)
	}

	stats, err := st.SentimentFeatureStats(ctx, newssent.Version)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	wantDay := store.ActionableSession(old.Unix())
	if stats.FirstDay != wantDay {
		t.Errorf("aligned archive starts %q, want %q — the old headline was never aligned",
			stats.FirstDay, wantDay)
	}
	// And the cursor — the thing the API reports catch-up from — must have
	// reached the oldest scored headline. Note it is the CURSOR, not a
	// comparison of the two first-days: the aligned day legitimately sits after
	// the headline's own calendar day whenever the session key rolls forward.
	oldestScored, err := st.OldestScoredNewsTs(ctx, newssent.Version)
	if err != nil {
		t.Fatalf("oldest scored: %v", err)
	}
	raw, _ := st.GetMeta(ctx, MetaFeatureBackfillCursor)
	cursor, _ := strconv.ParseInt(raw, 10, 64)
	if cursor == 0 || cursor > oldestScored {
		t.Errorf("cursor at %d, oldest scored headline at %d — the sweep stopped short",
			cursor, oldestScored)
	}
}

// A day whose headlines straddle a backfill window boundary must keep ALL of
// them. store.ActionableSession rolls an after-close headline to the next
// session, so without the read-ahead overlap the older pass would rewrite that
// day from its own rows alone and silently drop the newer half.
func TestFeatureBackfillOverlapKeepsBoundaryDaysWhole(t *testing.T) {
	ctx := context.Background()
	st := newSentWorkerStore(t)
	sym, err := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	now := time.Date(2026, time.July, 9, 12, 0, 0, 0, time.UTC)
	// Place two headlines on the same SESSION but on opposite sides of the
	// first backfill window's boundary: one just before the cursor's starting
	// edge, one just after.
	boundary := now.AddDate(0, 0, -featureRebuildDays)
	before := boundary.Add(-2 * time.Hour)
	after := boundary.Add(2 * time.Hour)
	for i, ts := range []time.Time{before, after} {
		if err := st.InsertNews(ctx, store.NewsItem{
			ID: fmt.Sprintf("b%d", i), SymbolID: sym.ID, Ts: ts.Unix(),
			Headline: "AAA beats estimates and raises guidance",
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	w := &SentimentLexScorer{St: st, Now: func() time.Time { return now }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	feats, err := st.SymbolSentimentFeatures(ctx, sym.ID, 50)
	if err != nil {
		t.Fatalf("features: %v", err)
	}
	total := 0
	for _, f := range feats {
		total += f.NAll
	}
	if total != 2 {
		t.Errorf("aligned %d headlines across %d feature rows, want both kept — a "+
			"boundary day was rewritten from one side only", total, len(feats))
	}
}
