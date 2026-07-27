package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// An automated rule search is a p-hacking machine unless its guardrails hold.
// These pin the three that matter most: it refuses thin data, it records the
// rules it KILLS as well as the ones it keeps, and it cannot promote its own
// findings into production.

func newLoopStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedLoopCorpus writes enough weekly observations that the min-observation
// refusal is not what stops a pass. The values are deliberately structureless —
// these tests are about whether the loop RECORDS what it judges, never about
// what it judges.
func seedLoopCorpus(t *testing.T, st *store.Store, n int) {
	t.Helper()
	rows := make([]store.ResearchWeek, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, store.ResearchWeek{
			SymbolID: int64(i%40 + 1), Week: int64(i/40 + 1),
			Ts:  int64(i/40+1) * 604800,
			Vec: map[string]float64{"rsi14": float64(i%100) / 100},
			// Alternating sign: a coin flip, so nothing can survive on merit.
			FwdReturn: float64(i%2)*0.02 - 0.01, Up: i%2 == 0,
			Era: []string{"pre-covid", "covid", "post-covid"}[i%3],
		})
	}
	if err := st.UpsertResearchWeeks(context.Background(), rows, 1); err != nil {
		t.Fatal(err)
	}
}

func TestLoopRefusesThinData(t *testing.T) {
	// Searching 48 rules over a handful of observations finds structure in noise
	// every single time. Refusing is the only correct behaviour.
	w := &ResearchLoop{St: newLoopStore(t)}
	msg, err := w.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "skip") || !strings.Contains(msg, "noise-fitting") {
		t.Fatalf("thin data must be refused with a reason, got %q", msg)
	}
	hyps, _ := w.St.LoopHypotheses(context.Background(), 0)
	if len(hyps) != 0 {
		t.Fatalf("a refused run must record no hypotheses, got %d", len(hyps))
	}
	// A refusal is a result and must leave a durable trace: without one, "the
	// loop declined to look today" survives only in a worker_runs line that is
	// eventually pruned, and the record cannot distinguish a refusal from a
	// loop that never ran at all.
	runs, err := w.St.LoopRuns(context.Background(), 0)
	if err != nil || len(runs) != 1 {
		t.Fatalf("a refusal must record exactly one run row, got %d (%v)", len(runs), err)
	}
	if runs[0].RefusalReason == "" {
		t.Error("the run row must say WHY the pass refused to search")
	}
	if runs[0].Survivors != 0 || runs[0].Judged != 0 {
		t.Errorf("a refusal judged nothing: %+v", runs[0])
	}
}

// TestLoopFailsLoudWhenTheLedgerCannotAcceptRejections is the regression this
// wave exists for. The loop ran nightly for weeks against a database whose
// ledger tables were never migrated, reporting "searched a 48-rule grid —
// NOTHING survived" as a SUCCESS while research_loop_hypotheses stayed at zero
// rows and research_loop_runs did not exist at all. Discarded write errors made
// a pass that recorded nothing indistinguishable from a pass that recorded a
// null, which is precisely the audit guarantee the package doc claims.
//
// Here the run row cannot be written; the pass must FAIL rather than return a
// summary describing evidence that was never persisted.
func TestLoopFailsLoudWhenTheLedgerCannotAcceptRejections(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	// Simulate the un-migrated database: the ledger table the pass must write
	// to is gone, so every write against it errors.
	if _, err := st.DB().ExecContext(ctx, `DROP TABLE research_loop_runs`); err != nil {
		t.Fatal(err)
	}
	w := &ResearchLoop{St: st}
	if _, err := w.Run(ctx); err == nil {
		t.Fatal("a pass whose ledger write failed must return an error, not a " +
			"success string describing writes that did not happen")
	}
}

// A search whose judgments cannot be stored must not be TAKEN. Failing after
// researchx.Discover — which is what the loop did for weeks against an
// un-migrated database — spends the multiplicity and then throws the verdicts
// away, which is exactly the keep-the-winners-forget-the-kills asymmetry the
// ledger exists to prevent. Here the append-only judgment table is missing: the
// pass must refuse, and must charge no look for the search it did not take.
func TestLoopRefusesToSearchWhenJudgmentsCannotBeStored(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `DROP TABLE research_loop_judgments`); err != nil {
		t.Fatal(err)
	}
	if err := st.LoopLedgerReady(ctx); err == nil {
		t.Fatal("a ledger missing the judgment table must not report itself ready")
	}
	// Enough observations that the thin-data refusal is not what stops the pass.
	seedLoopCorpus(t, st, minObsForLoop+50)

	w := &ResearchLoop{St: st}
	before := w.priorSearches(ctx)
	if _, err := w.Run(ctx); err == nil {
		t.Fatal("a pass whose judgments cannot be stored must fail rather than search")
	}
	if got := w.priorSearches(ctx); got != before {
		t.Fatalf("a search that was refused must cost no look: %d -> %d", before, got)
	}
	// And it must not have marked the day done: nothing was judged today.
	if last, _ := st.GetMeta(ctx, loopMetaDay); last != "" {
		t.Fatalf("a refused-to-search pass must stay retryable today, gate=%q", last)
	}
}

// The hypotheses table exists but predates the columns that name the null a
// verdict was judged against. A row that records "rejected" without its bar is
// not an auditable record, so this must refuse for the same reason.
func TestLoopRefusesWhenTheVerdictCannotNameItsNull(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx,
		`ALTER TABLE research_loop_hypotheses DROP COLUMN null_p0`); err != nil {
		t.Fatal(err)
	}
	if err := st.LoopLedgerReady(ctx); err == nil {
		t.Fatal("a hypotheses table that cannot carry null_p0 must not report ready")
	}
}

// The Bonferroni divisor is the price of looks already taken, so it must be
// monotone across a log prune: PruneWorkerRuns cannot refund multiplicity.
func TestDivisorIsMonotoneAcrossAPrune(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	w := &ResearchLoop{St: st}

	// Three searched nights, recorded ONLY as worker_runs log lines — the
	// pre-durable state this backfill and exemption exist for.
	for i, day := range []int64{1, 2, 3} {
		id, err := st.StartWorkerRun(ctx, "research-loop")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.FinishWorkerRun(ctx, id, "ok",
			"searched a 48-rule grid over 166285 observations (search #1)"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx,
			`UPDATE worker_runs SET started_at=? WHERE id=?`, day*86400, id); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	// Flood the log so the newest-N window would otherwise evict them.
	for i := 0; i < 50; i++ {
		id, err := st.StartWorkerRun(ctx, "noisy-worker")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.FinishWorkerRun(ctx, id, "ok", "tick"); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := st.BackfillLoopRuns(ctx); err != nil || n != 3 {
		t.Fatalf("backfill must recover one row per historical search, got %d (%v)", n, err)
	}
	before := w.priorSearches(ctx)
	if before != 3 {
		t.Fatalf("three recoverable searches must seed three looks, got %d", before)
	}
	if err := st.PruneWorkerRuns(ctx, 5); err != nil {
		t.Fatal(err)
	}
	after := w.priorSearches(ctx)
	if after < before {
		t.Fatalf("pruning logs must never refund a look: %d -> %d", before, after)
	}
	// The exemption itself: the searched-grid lines survive the prune, so even
	// the worker_runs lower bound cannot fall.
	hist, err := st.HistoricalLoopSearches(ctx)
	if err != nil || hist != 3 {
		t.Fatalf("research-loop search lines must be exempt from pruning, got %d (%v)", hist, err)
	}
}

func TestLoopIsIdempotentPerDay(t *testing.T) {
	w := &ResearchLoop{St: newLoopStore(t)}
	ctx := context.Background()
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "already ran today") {
		t.Fatalf("second same-day run should short-circuit, got %q", msg)
	}
}

func TestLedgerRecordsRejectionsNotJustWinners(t *testing.T) {
	// The rejected hypotheses are the more valuable half: they are what stops
	// the same dead idea being retried, and a search that logs only its winners
	// cannot be audited.
	st := newLoopStore(t)
	ctx := context.Background()
	for _, h := range []store.LoopHypothesis{
		{ID: "AD-win", Desc: "survived", Status: "shadow", WilsonLower: 0.55, Survives: true, FoundAt: 1},
		{ID: "AD-dead1", Desc: "killed", Status: "rejected", WilsonLower: 0.48, Survives: false, FoundAt: 1},
		{ID: "AD-dead2", Desc: "killed", Status: "rejected", WilsonLower: 0.41, Survives: false, FoundAt: 1},
	} {
		if err := st.UpsertLoopHypothesis(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.LoopHypotheses(ctx, 0)
	if err != nil || len(got) != 3 {
		t.Fatalf("all outcomes must be ledgered, got %d (%v)", len(got), err)
	}
	var rejected int
	for _, h := range got {
		if h.Status == "rejected" {
			rejected++
		}
	}
	if rejected != 2 {
		t.Fatalf("rejections must persist, got %d", rejected)
	}
}

// A rejection is only auditable if the row says which gate killed the rule and
// under what correction — "it failed" is not a record.
func TestRejectionRowCarriesItsGateAndCorrection(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	in := store.LoopHypothesis{
		ID: "AD-dead", Desc: "killed", Status: "rejected", WilsonLower: 0.42,
		Survives: false, FoundAt: 1, Divisor: 96, GridSize: 48, Weeks: 120,
		RejectedBy: "wilson_lower", ObsWindow: "research_weeks:100-200",
	}
	if err := st.UpsertLoopHypothesis(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoopHypotheses(ctx, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("want one row, got %d (%v)", len(got), err)
	}
	if got[0].RejectedBy != in.RejectedBy || got[0].Divisor != in.Divisor ||
		got[0].GridSize != in.GridSize || got[0].Weeks != in.Weeks ||
		got[0].ObsWindow != in.ObsWindow {
		t.Errorf("audit fields lost on round-trip: %+v", got[0])
	}
}

func TestLoopCannotPromoteToProduction(t *testing.T) {
	// The loop may reach `shadow`; a human moves anything further. Automation
	// that can put its own output live is how a p-hacked rule becomes a
	// position.
	st := newLoopStore(t)
	ctx := context.Background()
	_ = st.UpsertLoopHypothesis(ctx, store.LoopHypothesis{
		ID: "AD-x", Desc: "d", Status: "shadow", Survives: true, FoundAt: 1})
	got, _ := st.LoopHypotheses(ctx, 0)
	for _, h := range got {
		if h.Status == "promoted" {
			t.Fatal("the loop must never write a promoted status")
		}
	}
}

func TestRediscoveryUpdatesRatherThanDuplicates(t *testing.T) {
	// The same rule found again tomorrow is the same hypothesis. Duplicating it
	// would let one idea masquerade as repeated independent evidence.
	st := newLoopStore(t)
	ctx := context.Background()
	h := store.LoopHypothesis{ID: "AD-same", Desc: "d", Status: "rejected",
		WilsonLower: 0.40, Survives: false, FoundAt: 100}
	_ = st.UpsertLoopHypothesis(ctx, h)
	h.Status, h.WilsonLower, h.Survives, h.FoundAt = "shadow", 0.58, true, 200
	_ = st.UpsertLoopHypothesis(ctx, h)

	got, _ := st.LoopHypotheses(ctx, 0)
	if len(got) != 1 {
		t.Fatalf("rediscovery must update in place, got %d rows", len(got))
	}
	if got[0].Status != "shadow" || !got[0].Survives {
		t.Fatalf("the newer verdict should win: %+v", got[0])
	}
}

func TestFilingsDriftIsGatedOnPreregistration(t *testing.T) {
	// The filings-drift candidate is only worth anything because its hypothesis
	// is chained BEFORE any forecast exists. On an empty chain the loop must
	// freeze nothing and say why, not run the study "provisionally".
	w := &ResearchLoop{St: newLoopStore(t)}
	msg, err := w.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "not yet pre-registered") {
		t.Fatalf("unregistered hypothesis must be refused visibly, got %q", msg)
	}
}

func TestFilingsDriftRunsOncePreregistered(t *testing.T) {
	// After the registrar chains the hypothesis, the loop runs the (empty)
	// study instead of the refusal — the gate is the chain, not a flag.
	st := newLoopStore(t)
	ctx := context.Background()
	if _, err := (&PreregRegistrar{St: st, GraderVersion: cleanGrader}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	w := &ResearchLoop{St: st}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "filings-drift21: froze 0 post-filing calls") {
		t.Fatalf("registered hypothesis should run the study, got %q", msg)
	}
}

// TestLoopChargesOnlyRealSearches pins the multiplicity counter that feeds
// researchx.DiscoverConfig.PriorSearches. Every night the grid actually runs is
// another look at the same corpus and must raise the Bonferroni divisor; a
// same-day skip and a thin-data refusal take no look and must cost nothing. A
// counter that drifts either way corrupts the correction — too low and the loop
// corrects for one night's grid while taking many nights' chances, too high and
// it inflates the bar with searches that never happened.
func TestLoopChargesOnlyRealSearches(t *testing.T) {
	w := &ResearchLoop{St: newLoopStore(t)}
	ctx := context.Background()
	if got := w.priorSearches(ctx); got != 0 {
		t.Fatalf("a fresh store must record no prior look, got %d", got)
	}
	// Thin data: refused before Discover, so no look is charged.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.priorSearches(ctx); got != 0 {
		t.Fatalf("a refused run must not consume a look, got %d", got)
	}
	// Same-day skip: also no look.
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.priorSearches(ctx); got != 0 {
		t.Fatalf("a same-day skip must not consume a look, got %d", got)
	}
	// A recorded look is read back, and being non-zero it strictly raises the
	// divisor above the single-night correction.
	_ = w.St.SetMeta(ctx, loopMetaSearches, "7")
	if got := w.priorSearches(ctx); got != 7 {
		t.Fatalf("priorSearches = %d, want 7", got)
	}
	withPrior := researchx.DiscoverConfig{MaxCandidates: 48, PriorSearches: 7}.Divisor()
	firstNight := researchx.DiscoverConfig{MaxCandidates: 48}.Divisor()
	if withPrior <= firstNight {
		t.Fatal("prior searches must strictly raise the Bonferroni divisor")
	}
	_ = w.St.SetMeta(ctx, loopMetaSearches, "not-a-number")
	if got := w.priorSearches(ctx); got != 0 {
		t.Fatalf("an unreadable counter must read as 0, got %d", got)
	}
}

// The upsert-keyed hypothesis table cannot answer "has this dead rule been
// re-tested?" — a rule judged on 200 nights leaves ONE row there. The
// append-only judgment table can, and this pins that: the same rule judged on
// two different days must leave two rows.
func TestJudgmentsAreAppendOnlyAcrossDays(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	for _, day := range []string{"2026-07-01", "2026-07-02"} {
		if err := st.InsertLoopJudgment(ctx, store.LoopJudgment{
			Day: day, RuleID: "AD-dead", Status: "rejected", WilsonLower: 0.42,
			P0: 0.5, Divisor: 96, GridSize: 48, Weeks: 120,
			RejectedBy: researchx.RejectWilson, ObsWindow: "research_weeks:1-2",
		}); err != nil {
			t.Fatal(err)
		}
		// The current-state view collapses both nights into one row — which is
		// exactly why it cannot be the ledger.
		if err := st.UpsertLoopHypothesis(ctx, store.LoopHypothesis{
			ID: "AD-dead", Desc: "killed", Status: "rejected", Survives: false, FoundAt: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	js, err := st.LoopJudgments(ctx, 0)
	if err != nil || len(js) != 2 {
		t.Fatalf("two nights of judging one rule must leave two judgment rows, got %d (%v)", len(js), err)
	}
	hyps, err := st.LoopHypotheses(ctx, 0)
	if err != nil || len(hyps) != 1 {
		t.Fatalf("the current-state view must still hold one row per rule, got %d (%v)", len(hyps), err)
	}
	gates, err := st.LoopRejectionsByGate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gates[researchx.RejectWilson] != 2 {
		t.Fatalf("the gate tally counts JUDGMENTS, not rules: got %d, want 2",
			gates[researchx.RejectWilson])
	}
	// A re-run of the SAME day is the same look, not a second one.
	if err := st.InsertLoopJudgment(ctx, store.LoopJudgment{
		Day: "2026-07-02", RuleID: "AD-dead", Status: "rejected", P0: 0.5,
		RejectedBy: researchx.RejectWilson,
	}); err != nil {
		t.Fatal(err)
	}
	if js, _ = st.LoopJudgments(ctx, 0); len(js) != 2 {
		t.Fatalf("a same-day re-judgment must not add a look, got %d rows", len(js))
	}
}

// The Bonferroni divisor is the price of every look already taken. Sourcing the
// look count from worker_runs alone lets PruneWorkerRuns REFUND multiplicity —
// the divisor would fall as logs rotate. The durable sources make it monotone.
func TestMultiplicityDivisorSurvivesLogPruning(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	w := &ResearchLoop{St: st}

	// Three nights of searching, recorded in the append-only tables only.
	for _, day := range []string{"2026-07-01", "2026-07-02", "2026-07-03"} {
		if err := st.UpsertLoopRun(ctx, store.LoopRun{Day: day, GridSize: 48, Judged: 1}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertLoopJudgment(ctx, store.LoopJudgment{
			Day: day, RuleID: "AD-x", Status: "rejected", P0: 0.5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A refusal took no look and must not be charged as one.
	if err := st.UpsertLoopRun(ctx, store.LoopRun{
		Day: "2026-07-04", GridSize: 0, RefusalReason: "thin corpus",
	}); err != nil {
		t.Fatal(err)
	}
	// worker_runs holds nothing (pruned) and the meta counter was never set:
	// the pre-fix reader would report zero prior looks here.
	if got := w.priorSearches(ctx); got != 3 {
		t.Fatalf("prior looks must come from the unprunable tables: got %d, want 3", got)
	}
	if n, err := st.DurableLoopSearches(ctx); err != nil || n != 3 {
		t.Fatalf("DurableLoopSearches counts searched passes only: got %d (%v)", n, err)
	}
	// And a stale-but-higher meta counter still wins: the count may only rise.
	if err := st.SetMeta(ctx, loopMetaSearches, "9"); err != nil {
		t.Fatal(err)
	}
	if got := w.priorSearches(ctx); got != 9 {
		t.Fatalf("the look count must be a max over sources, got %d, want 9", got)
	}
}

// A DEAD ENGINE MUST NOT READ LIKE AN HONEST NULL. The loop reported "NOTHING
// survived Bonferroni correction" on two consecutive days while
// research_loop_runs held no row at all: a fat corpus, no search on record, and
// a summary indistinguishable from a corrected grid working as designed. The
// liveness assertion separates the two — silence on a corpus far above the
// search floor is a data-quality event, not a research finding.
func TestFatCorpusWithNoRunsReadsAsADeadEngine(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	seedLoopCorpus(t, st, minObsForLoop+50)

	now := time.Now()
	h, err := st.LoopEngineHealth(ctx, minObsForLoop, now)
	if err != nil {
		t.Fatal(err)
	}
	if h.Healthy || h.State != store.LoopEngineSilent {
		t.Fatalf("a fat corpus with an empty runs table is a dead engine: %+v", h)
	}
	if h.LastRunDay != "" || h.LastRunAge != -1 {
		t.Fatalf("no pass was ever recorded: %+v", h)
	}
	if !strings.Contains(h.Detail, "no judgment since") {
		t.Errorf("the unhealthy state must name the silence, got %q", h.Detail)
	}

	w := &ResearchLoop{St: st}
	if suffix := w.engineLiveness(ctx, now); !strings.Contains(suffix, store.LoopEngineSilent) {
		t.Fatalf("an unhealthy engine must annotate the summary, got %q", suffix)
	}
	dq, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range dq {
		if e.Kind == "research_loop_silent" {
			found = true
		}
	}
	if !found {
		t.Error("engine silence on a searchable corpus must raise a dq_event")
	}

	// A thin corpus is the opposite finding: the refusal working, not silence.
	thin, err := newLoopStore(t).LoopEngineHealth(ctx, minObsForLoop, now)
	if err != nil {
		t.Fatal(err)
	}
	if !thin.Healthy || thin.State != store.LoopEngineWarmingUp {
		t.Fatalf("below the floor, silence is the refusal working: %+v", thin)
	}
}

// A pass that judged rules must be able to RE-READ the row that holds them
// before it describes the null it found. Writing and then trusting the write is
// how the loop narrated ~96 judgments no table held.
func TestSearchReadsBackItsOwnRunRow(t *testing.T) {
	st := newLoopStore(t)
	ctx := context.Background()
	seedLoopCorpus(t, st, minObsForLoop+50)
	w := &ResearchLoop{St: st}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	row, ok, err := st.LoopRunForDay(ctx, today)
	if err != nil || !ok {
		t.Fatalf("the pass row must exist for the day it described: ok=%v err=%v", ok, err)
	}
	if row.Judged == 0 || row.Judged != row.GridSize {
		t.Fatalf("the row must hold every judgment the summary claims: %+v", row)
	}
	js, err := st.LoopJudgments(ctx, 0)
	if err != nil || len(js) != row.Judged {
		t.Fatalf("append-only judgments must match the run row: %d vs %d (%v)",
			len(js), row.Judged, err)
	}
	if !strings.Contains(msg, "searched a") {
		t.Fatalf("a recorded search must say so, got %q", msg)
	}
}

// "froze 0" collapsed three different worlds into one sentence. The tally must
// distinguish a quiet filing calendar from filings that were seen and skipped.
func TestFilingsFreezeTallyNamesWhyNothingFroze(t *testing.T) {
	quiet := filingsFreezeTally{}
	if !strings.Contains(quiet.summary(), "no fresh 10-Q/10-K") {
		t.Errorf("an empty calendar must say so, got %q", quiet.summary())
	}
	seen := filingsFreezeTally{fresh: 4, reactionUnprinted: 1, missingBars: 3}
	s := seen.summary()
	for _, want := range []string{"4 fresh", "awaiting the reaction bar", "missing bars"} {
		if !strings.Contains(s, want) {
			t.Errorf("the skip reasons must be named individually, got %q", s)
		}
	}
}
