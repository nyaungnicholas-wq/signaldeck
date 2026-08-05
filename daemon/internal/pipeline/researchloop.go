// Autonomous research loop (2026-07-25).
//
// WHY THIS IS THE ONE THAT COMPOUNDS
// ----------------------------------
// Every other improvement to this platform is a fixed gain: a faster endpoint
// stays faster, a repaired data feed stays repaired. Hypotheses tested per week
// is different — it is a RATE, and until now that rate was bounded by how often
// a human sat down and asked a question. The discovery grid, the walk-forward
// grader and the Bayes ledger were all built and all waited to be invoked by
// hand.
//
// This connects them: generate -> test -> judge -> ledger -> kill or promote,
// on a schedule, without anyone deciding to start it.
//
// WHAT KEEPS IT FROM MANUFACTURING GARBAGE
// ----------------------------------------
// An automated search over a rule grid is a p-hacking machine unless every one
// of these holds, so they are enforced here rather than trusted:
//
//  1. BONFERRONI over the whole grid. Testing 48 rules at alpha=0.05 finds
//     ~2 "significant" results from pure noise. researchx.Discover corrects for
//     the grid size, and the loop never widens the grid to chase a result.
//  2. NON-OVERLAPPING weekly observations. Daily samples of a forward window
//     share days and inflate n by the horizon length.
//  3. ERA COVERAGE. A rule must survive in multiple market eras, not just win
//     the one regime that dominates the sample.
//  4. REJECTIONS ARE KEPT. researchx.Discover returns every rule it judged,
//     each rejection naming the gate that killed it, and this loop ledgers
//     them as durably as the survivors (status='rejected'). Every pass —
//     including the ones that refuse to search on a thin corpus — also writes
//     a research_loop_runs row with the corpus span, grid size, divisor and
//     corrected alpha. Zero survivors is the normal, honest outcome, and it is
//     now retained as evidence rather than as a log line: a search that keeps
//     only its winners cannot be audited, and the recorded kills are what make
//     silent re-testing of a dead rule detectable.
//  5. NOTHING auto-promotes to live. The loop can move a hypothesis to
//     `shadow`; a human moves it further. Automation that can put its own
//     output into production is how a p-hacked rule becomes a position.
package pipeline

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// ResearchLoop runs the discovery cycle end to end.
type ResearchLoop struct {
	St *store.Store

	// MaxCandidates caps the grid. Widening it does not find more edge, it
	// finds more false positives — the Bonferroni correction scales with it, so
	// a bigger grid makes each individual rule HARDER to clear, not easier.
	MaxCandidates int
}

func (w *ResearchLoop) Name() string            { return "research-loop" }
func (w *ResearchLoop) Interval() time.Duration { return 24 * time.Hour }

const (
	defaultLoopCandidates = 48
	// loopMetaDay gates real work to once per UTC day even though the worker
	// heartbeats more often.
	loopMetaDay = "research_loop_last_day"
	// loopMetaSearches counts how many times the grid has ACTUALLY been searched
	// over this corpus. It is the multiplicity term the Bonferroni divisor was
	// missing: a nightly loop that corrects for 48 rules is correcting for one
	// night's grid while taking every night's chances. It advances ONLY on
	// passes that reach researchx.Discover — the same-day skip and the
	// min-observation refusal take no look and must not cost one.
	loopMetaSearches = "research_loop_searches"
	// minObsForLoop is the floor below which the loop reports "not enough data"
	// instead of searching. A grid search over a thin sample finds structure in
	// noise every single time.
	minObsForLoop = 2000
	// loopCorpusGrowthMin is the fraction of NEW observations, measured against
	// the last corpus actually searched, below which tonight's corpus is treated
	// as the same evidence and no look is charged. The window advancing past the
	// last searched obs_ts_to always counts as new evidence regardless of size,
	// so a genuinely fresh week is never dismissed as noise-sized growth.
	//
	// 1% is deliberately low: it lets a real week of data through (the corpus
	// gains ~1300 rows a week against ~326k, plus a new obs_ts_to) while
	// rejecting the 15-row and 82-row no-ops the live ledger charged full looks
	// for. Raise it and real evidence starts arriving free; lower it toward 0 and
	// the runaway divisor comes back.
	loopCorpusGrowthMin = 0.01
)

func (w *ResearchLoop) Run(ctx context.Context) (string, error) {
	today := time.Now().UTC().Format("2006-01-02")
	// LIVENESS, checked before the same-day gate: a dead engine looks exactly
	// like a quiet one from the outside, and the same-day skip would hide it.
	liveness := w.engineLiveness(ctx, time.Now())
	if last, _ := w.St.GetMeta(ctx, loopMetaDay); last == today {
		return "skip — already ran today" + liveness, nil
	}

	// The filings-drift candidate runs on the filings table, not on
	// research_weeks, so it is deliberately OUTSIDE the min-observation refusal
	// below: a thin weekly sample says nothing about whether fresh 10-Q/10-Ks
	// exist to freeze.
	drift := w.filingsDrift(ctx, today)

	// Point-in-time coverage of the corpus about to be searched. Recorded on
	// the pass row (including a refusal's) because a search that names its
	// corpus, its grid and its correction but not how much of the market that
	// corpus contained is still under-describing its own evidence. Best-effort:
	// a failed measurement leaves 0, which the column defines as "unmeasured".
	corpusCoverage := 0.0
	if cs, cerr := w.St.ResearchWeeksStats(ctx); cerr == nil {
		corpusCoverage = cs.CoverageMin
	}

	obs, err := w.St.ResearchObservations(ctx, 0)
	if err != nil {
		return "", err
	}
	if len(obs) < minObsForLoop {
		// Honest refusal. The alternative — searching anyway and reporting the
		// best of 48 rules on a thin sample — is how a platform acquires a
		// "discovery" that evaporates on contact with new data.
		reason := fmt.Sprintf("only %d observations, need %d", len(obs), minObsForLoop)
		// A refusal is a result, and it is recorded as durably as a search:
		// otherwise the only trace of "the loop declined to look today" is a
		// worker_runs line that gets pruned. If that write fails the pass must
		// FAIL — reporting a refusal that was never recorded is a claim about
		// evidence that does not exist.
		if err := w.St.UpsertLoopRun(ctx, store.LoopRun{
			Day: today, RanAt: time.Now().Unix(), ObsCount: len(obs),
			ObsTsFrom: obsFrom(obs), ObsTsTo: obsTo(obs),
			RefusalReason: reason, GitRev: lineage.BuildRevision(),
			CorpusCoverage: corpusCoverage,
		}); err != nil {
			return "", fmt.Errorf("record refusal run row: %w", err)
		}
		// READ-BACK. A write that returned nil is not the same as a row that
		// exists — the loop has already described outcomes twice while all three
		// ledger tables held nothing. Re-read before describing the refusal.
		if row, ok, rerr := w.St.LoopRunForDay(ctx, today); rerr != nil || !ok ||
			row.RefusalReason == "" {
			return "", fmt.Errorf("refusal run row for %s did not read back "+
				"(found=%v reason=%q err=%v): a refusal no table holds is not a "+
				"recorded result", today, ok, row.RefusalReason, rerr)
		}
		// Day gate DERIVED from the refusal row, not written beside it: the
		// store statement sets the key only where the row exists, so the two
		// cannot disagree the way the live database currently does.
		if err := w.St.MarkLoopDayFromRun(ctx, loopMetaDay, today); err != nil {
			return "", fmt.Errorf("record refusal day gate: %w", err)
		}
		return fmt.Sprintf("skip — %d observations, need %d before a grid search "+
			"is anything but noise-fitting; %s", len(obs), minObsForLoop, drift), nil
	}

	// UNCHANGED CORPUS IS NOT A NEW LOOK. researchx.Discover is deterministic:
	// "the same obs always yield the same survivors". Re-grading an unchanged
	// corpus therefore reproduces last night's verdicts exactly — it is the same
	// test re-read, not a second chance to be fooled — yet the old unconditional
	// charge still added a full grid to the divisor for it. Measured on the live
	// ledger 2026-08-04: the 08-04 pass added ZERO observations and still took
	// the divisor 384 -> 432; 08-02 added 15 rows out of 296,712 and cost the
	// same 48. Compounding +48/night regardless of data, the bar stops being a
	// function of the evidence and the loop becomes arithmetically incapable of
	// ever promoting anything.
	//
	// So the look is skipped, not merely uncharged: the refusal row carries
	// grid_size 0 and writes no judgments, which is precisely what
	// DurableLoopSearches (grid_size > 0) and the judgments day-count already
	// filter on. The counter stays monotone — it simply stops rising on nights
	// that learned nothing. A materially grown corpus still charges in full.
	if last, ok, lerr := w.St.LastSearchedRun(ctx); lerr == nil && ok {
		grown := len(obs) - last.ObsCount
		frac := 0.0
		if last.ObsCount > 0 {
			frac = float64(grown) / float64(last.ObsCount)
		}
		// Either genuinely new history (the window advanced) or a materially
		// larger corpus counts as new evidence. A backfill that widens the panel
		// is as much a new chance to be fooled as a fresh week is.
		if obsTo(obs) <= last.ObsTsTo && frac < loopCorpusGrowthMin {
			reason := fmt.Sprintf("corpus unchanged since %s: %d obs (%+d, %.3f%%), "+
				"window end %d; a deterministic grid over the same obs is the same "+
				"test, and costs no look", last.Day, len(obs), grown, frac*100, obsTo(obs))
			if err := w.St.UpsertLoopRun(ctx, store.LoopRun{
				Day: today, RanAt: time.Now().Unix(), ObsCount: len(obs),
				ObsTsFrom: obsFrom(obs), ObsTsTo: obsTo(obs),
				RefusalReason: reason, GitRev: lineage.BuildRevision(),
				CorpusCoverage: corpusCoverage,
			}); err != nil {
				return "", fmt.Errorf("record unchanged-corpus run row: %w", err)
			}
			// Same read-back discipline as the thin-corpus refusal: a skip no
			// table holds is not a recorded result.
			if row, ok, rerr := w.St.LoopRunForDay(ctx, today); rerr != nil || !ok ||
				row.RefusalReason == "" {
				return "", fmt.Errorf("unchanged-corpus run row for %s did not read "+
					"back (found=%v reason=%q err=%v)", today, ok, row.RefusalReason, rerr)
			}
			if err := w.St.MarkLoopDayFromRun(ctx, loopMetaDay, today); err != nil {
				return "", fmt.Errorf("record unchanged-corpus day gate: %w", err)
			}
			return fmt.Sprintf("skip — corpus unchanged since %s (%d obs, %+d), so "+
				"tonight's grid would reproduce the same verdicts; no look charged, "+
				"divisor stays %d; %s", last.Day, len(obs), grown, last.Divisor, drift), nil
		}
	}

	// PREFLIGHT. A search whose verdicts cannot be written must not be taken at
	// all. Failing after researchx.Discover — which is what this loop did for
	// weeks against an un-migrated database — is too late: the look has been
	// taken, the multiplicity spent, and the judgments discarded, which is the
	// keep-the-winners-forget-the-kills asymmetry the ledger exists to prevent.
	// Probing first makes an unwritable search cost nothing instead of a look.
	if err := w.St.LoopLedgerReady(ctx); err != nil {
		return "", fmt.Errorf("research-loop refused to search: the judgment "+
			"ledger cannot accept what a search would produce, and an unrecordable "+
			"search must cost no look: %w", err)
	}

	maxC := w.MaxCandidates
	if maxC <= 0 {
		maxC = defaultLoopCandidates
	}
	// PRIOR SEARCHES. Every night this loop has run is another look at largely
	// the same corpus, and looks are exactly what multiplicity corrects for.
	// Reading the counter BEFORE the increment and passing it as PriorSearches
	// makes tonight's divisor grid*(1+prior) — i.e. the grid once per search
	// ever conducted, tonight's included. The bar therefore rises every night,
	// which is the point: it strictly cannot make a rule easier to clear.
	prior := w.priorSearches(ctx)
	// BLIND FINAL ERA. The grid never grades on researchx.PreregHoldoutEra;
	// a candidate that clears every in-sample gate must clear the corrected
	// Wilson bound against its own measured null on that era a second time
	// before it can reach "shadow". It can only remove survivors, never add
	// one, and the era is a frozen constant chained in PREREGISTRATION.md so
	// it cannot be retargeted after a result is seen.
	cfg := researchx.DiscoverConfig{MaxCandidates: maxC, PriorSearches: prior, HoldoutEra: researchx.PreregHoldoutEra}
	divisor := cfg.Divisor()
	cands := researchx.Discover(obs, cfg)
	// The look has been taken; charge for it whether or not anything survived.
	// Counting only the nights that found something would let a null night be
	// a free look, which is the p-hacking this counter exists to price.
	_ = w.St.SetMeta(ctx, loopMetaSearches, strconv.Itoa(prior+1))

	// researchx.Discover returns EVERY JUDGED RULE — survivors, and rejections
	// carrying the gate that killed them. Both are ledgered here with the same
	// durability. A search that keeps only its winners is unauditable by
	// construction: a grid that found nothing is then indistinguishable from a
	// grid that never ran, and nothing stops the same dead rule being re-tested
	// until a night's noise lets it through.
	obsWindow := fmt.Sprintf("research_weeks:%d-%d", obs[0].Ts, obs[len(obs)-1].Ts)
	var survived int
	hyps := make([]store.LoopHypothesis, 0, len(cands))
	judgments := make([]store.LoopJudgment, 0, len(cands))
	for _, c := range cands {
		status := "rejected"
		if c.Survives {
			status = "shadow" // NEVER promoted automatically — see the package doc
			survived++
		}
		hyps = append(hyps, store.LoopHypothesis{
			ID:          c.ID,
			Desc:        c.Desc,
			Status:      status,
			WilsonLower: c.WilsonLower,
			// The null this rule was actually judged against, measured on its
			// own matched observations, so the ledgered verdict names its bar.
			NullP0:     c.NullP0,
			NullWeeks:  c.NullWeeks,
			Survives:   c.Survives,
			FoundAt:    time.Now().Unix(),
			Divisor:    c.Divisor,
			GridSize:   len(cands),
			Weeks:      c.Grade.Weeks,
			RejectedBy: c.RejectedBy,
			ObsWindow:  obsWindow,
		})
		// The APPEND-ONLY half. The upsert above is a current-state view keyed
		// on rule id, so on its own it cannot distinguish a rule judged once
		// from the same rule judged and killed on 200 consecutive nights —
		// which is precisely the silent re-testing the ledger exists to expose.
		// This row is per (day, rule) and is never overwritten by a later day.
		judgments = append(judgments, store.LoopJudgment{
			Day: today, RuleID: c.ID, Status: status,
			WilsonLower: c.WilsonLower, P0: c.NullP0,
			Divisor: c.Divisor, GridSize: len(cands), Weeks: c.Grade.Weeks,
			RejectedBy: c.RejectedBy, ObsWindow: obsWindow,
		})
	}

	// ONE TRANSACTION for the whole search: every judgment, every current-state
	// row, and exactly one run row saying the look was taken. Written separately
	// they could disagree — a run row charging multiplicity for judgments that
	// never landed, or judgments no run row admits looking for — and either
	// disagreement is the keep-the-winners-forget-the-kills asymmetry in a
	// different costume. A search either ledgers completely or does not count.
	if err := w.St.RecordLoopSearch(ctx, store.LoopRun{
		Day: today, RanAt: time.Now().Unix(), GridSize: len(cands),
		Divisor: divisor, CorrectedAlpha: cfg.CorrectedAlpha(),
		ObsCount: len(obs), ObsTsFrom: obsFrom(obs), ObsTsTo: obsTo(obs),
		Survivors: survived, Judged: len(cands), GitRev: lineage.BuildRevision(),
		CorpusCoverage: corpusCoverage,
	}, hyps, judgments); err != nil {
		// A pass that describes writes it did not make is worse than a pass that
		// failed: the summary string reads as evidence. Land it as a FAILED
		// worker_runs row instead, naming the write that did not happen. The
		// look is still charged above — it was taken — but nothing here claims
		// it produced a record.
		return "", fmt.Errorf("research-loop judged %d rules but could not ledger "+
			"the search; the rejection ledger is incomplete and this pass is not "+
			"evidence: %w", len(cands), err)
	}

	// READ-BACK: prove the search recorded what it judged before it is allowed
	// to describe itself as having run. RecordLoopSearch returning nil is a
	// claim about a transaction; this is the transaction's result. The loop has
	// twice reported "NOTHING survived Bonferroni correction" over ~96
	// judgments that no table held, which is the keep-the-winners-forget-the-
	// kills asymmetry with the winners half empty too. A disagreement here
	// fails the pass into worker_runs and retries rather than narrating.
	row, ok, rerr := w.St.LoopRunForDay(ctx, today)
	if rerr != nil || !ok || row.Judged != len(cands) {
		return "", fmt.Errorf("research-loop judged %d rules but the pass row for "+
			"%s did not read back with them (found=%v judged=%d err=%v); a search "+
			"may not describe a null it cannot prove it recorded",
			len(cands), today, ok, row.Judged, rerr)
	}

	for _, c := range cands {
		if !c.Survives {
			// Rejections are ledgered, not lineage-linked: the spine tracks
			// hypotheses that carry forward, and a killed rule carries nothing
			// forward. Its full record lives on the row written above.
			continue
		}
		// Lineage spine (Layers 2+8): tie the hypothesis to the exact
		// research_weeks window it was searched over and to this day's grid
		// run, with the build's git rev stamped in meta. Best-effort — a
		// lineage failure must not fail the research pass.
		hyp := "loop:" + c.ID
		_ = lineage.Link(ctx, w.St, lineage.Edge{
			SrcKind: lineage.KindHypothesis, SrcID: hyp,
			DstKind:  lineage.KindDatasetVersion,
			DstID:    fmt.Sprintf("research_weeks:%d-%d", obs[0].Ts, obs[len(obs)-1].Ts),
			EdgeKind: lineage.EdgeTestedIn, MetaJSON: lineage.RevMeta(),
		})
		_ = lineage.Link(ctx, w.St, lineage.Edge{
			SrcKind: lineage.KindHypothesis, SrcID: hyp,
			DstKind: lineage.KindExperiment, DstID: "loop-run:" + today,
			EdgeKind: lineage.EdgeGradedBy, MetaJSON: lineage.RevMeta(),
		})
	}

	// The day gate is DERIVED from the committed pass row rather than written
	// after it: meta cannot claim a search that research_loop_runs does not
	// hold. A pass that could not record its judgments stays retryable today.
	if err := w.St.MarkLoopDayFromRun(ctx, loopMetaDay, today); err != nil {
		return "", fmt.Errorf("record search day gate: %w", err)
	}
	if survived == 0 {
		// The honest and most common outcome. A grid that returns nothing after
		// Bonferroni correction is the search WORKING: it means no rule beat a
		// coin flip once the multiple-comparisons penalty was paid.
		return fmt.Sprintf("searched a %d-rule grid over %d observations (search #%d, "+
			"Bonferroni divisor %d = grid × searches; worst-week point-in-time "+
			"corpus coverage %.0f%%) — NOTHING "+
			"survived Bonferroni correction, regime-survival and the counterfactual. "+
			"That is a result, not a failure: it is what an honest search returns "+
			"when there is no edge in the grid. %s",
			maxC, len(obs), prior+1, divisor, corpusCoverage*100, drift), nil
	}
	return fmt.Sprintf("searched a %d-rule grid over %d observations (search #%d, "+
		"Bonferroni divisor %d = grid × searches; worst-week point-in-time "+
		"corpus coverage %.0f%%) — %d survived to "+
		"shadow (none auto-promoted by design); %s",
		maxC, len(obs), prior+1, divisor, corpusCoverage*100, survived, drift), nil
}

// engineLiveness asserts that a corpus fat enough to search has actually been
// searched recently, and raises a dq_event when it has not.
//
// The state it names is NOT "searched and found nothing". A corrected grid
// returning no survivor is the engine working; research_weeks holding 166,285
// observations against a 2,000 floor while research_loop_runs holds no row for
// days means no search is on record at all, and nothing — neither survivor nor
// kill — was priced. Rendering those two as the same sentence is how a dead
// engine passes for a disciplined one. Returns a suffix for the summary line;
// empty when healthy so a live pass reads unchanged.
func (w *ResearchLoop) engineLiveness(ctx context.Context, now time.Time) string {
	h, err := w.St.LoopEngineHealth(ctx, minObsForLoop, now)
	if err != nil || h.Healthy {
		return ""
	}
	_ = w.St.InsertDQ(ctx, md.DQEvent{
		Ts: now.UTC().Unix(), Kind: "research_loop_silent", Detail: h.Detail,
	})
	return " [" + h.State + ": " + h.Detail + "]"
}

// obsFrom / obsTo bound the corpus a pass looked at, 0 on an empty corpus —
// the refusal path records a run row too, and it may have no observations at
// all. Rows load ordered by (week, symbol_id), so the ends are the extremes.
func obsFrom(obs []researchx.Obs) int64 {
	if len(obs) == 0 {
		return 0
	}
	from, _ := obsSpan(obs)
	return from
}

func obsTo(obs []researchx.Obs) int64 {
	if len(obs) == 0 {
		return 0
	}
	_, to := obsSpan(obs)
	return to
}

// priorSearches reads the monotonic count of grid searches already conducted
// over this corpus.
//
// The counter itself was added long after the loop began searching nightly, so
// a missing or unparseable value must NOT read as "no prior look": that would
// retroactively hand back every look already taken, restarting the Bonferroni
// divisor at one night's grid after months of nights. It is instead seeded from
// the searches still visible in worker_runs — a lower bound, because that table
// is pruned, but a lower bound is the honest direction to err in: it charges
// fewer looks than were taken, never more.
//
// The maximum of the two is used so the seed can only ever RAISE the count. A
// history read that came back short must not be able to lower a bar already
// paid for.
func (w *ResearchLoop) priorSearches(ctx context.Context) int {
	n := 0
	v, _ := w.St.GetMeta(ctx, loopMetaSearches)
	if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && parsed > 0 {
		n = parsed
	}
	if hist, err := w.St.HistoricalLoopSearches(ctx); err == nil && hist > n {
		n = hist
	}
	// The DURABLE sources, and the reason the two above are not enough: the
	// meta counter is a single mutable cell and worker_runs is actively pruned,
	// so between them log retention could quietly REFUND looks already taken and
	// the divisor could FALL. research_loop_runs and research_loop_judgments are
	// append-only and pruned by nothing, so folding them in with a max makes the
	// count monotone by construction — the bar can only ever rise.
	if dur, err := w.St.DurableLoopSearches(ctx); err == nil && dur > n {
		n = dur
	}
	return n
}

// ── filings-drift — the second orthogonal non-price signal ──────────────
//
// Every survivor the grid above can produce is a recombination of the same
// price series ("research quality 44 — all edges are replicated price
// factors"). sentcorr was the first test built on a non-price source; this is
// the second: the 125 MB filings table, ingested since the Signal8 wave and
// never used as a signal, supplies the EVENT. The hypothesis is classic
// post-filing drift — the sign of the first post-filing session's return
// persists over the next 21 sessions.
//
// The discipline mirrors the structural predictors exactly, because the goal
// is a gradeable live record rather than another backtest:
//
//   - PREREGISTERED FIRST. Not one forecast is frozen until the hypothesis
//     and its grading spec are on the prereg chain (see filingsDriftSpec in
//     prereg.go). Registration-before-first-grade holds by construction, not
//     by schedule.
//   - ROUTED THROUGH regime_outcomes. tools/accuracy_registry.py groups that
//     table by kind, so filingsdrift21 gets the same day-clustered Wilson
//     interval, min-n/min-days refusals and frozen-claim verdict map as every
//     other predictor — no bespoke grading path to quietly loosen.
//   - UNCONDITIONAL SELECTION. Every fresh 10-Q/10-K is frozen, so the
//     bounded freeze lag (below) cannot cherry-pick events whose drift
//     already looks good.
const FilingsDriftKind = "filingsdrift21"

const (
	// filingsDriftHorizon is the forward window in trading sessions, matching
	// the 21d horizon the structural kinds already grade on.
	filingsDriftHorizon = 21
	// filingsDriftFreshDays bounds how long after filed_ts a filing may still
	// be frozen. Beyond it the forward window is materially elapsed and a
	// "forecast" would be a backtest row wearing a live timestamp.
	filingsDriftFreshDays = 7
	// filingsDriftClaim is the registered coin-flip null on the drift sign —
	// this is a hypothesis under test, not an advertised edge, and the prereg
	// spec says exactly that.
	filingsDriftClaim = 0.5
	// filingsDriftConvScale maps |reaction return| to conviction: a 10% move
	// on the reaction bar saturates at 1.0. Deterministic and frozen with the
	// call, like every other conviction.
	filingsDriftConvScale = 0.10
	// filingsDriftResolveBatch is deliberately larger than the regime worker's
	// default 5000: DueRegimeOutcomes is oldest-first across ALL kinds, and a
	// permanent structural backlog must not starve this kind's grading.
	filingsDriftResolveBatch = 20000
)

// filingsDrift freezes fresh post-filing calls and grades the due ones. It
// never fails the loop: errors are reported in the summary and retried on the
// next daily pass, which the INSERT OR IGNORE dedup makes free.
func (w *ResearchLoop) filingsDrift(ctx context.Context, today string) string {
	have, err := w.St.PreregKinds(ctx)
	if err != nil {
		return fmt.Sprintf("filings-drift21: prereg check failed (%v)", err)
	}
	if !have[FilingsDriftKind] {
		// The gate that makes this a pre-registered test rather than a story:
		// until the hypothesis is on the chain there is nothing to grade
		// against, so nothing is frozen and nothing can ever be graded early.
		return "filings-drift21: hypothesis not yet pre-registered — no forecasts frozen"
	}
	now := time.Now().UTC()
	fz, ferr := w.freezeFilingsDrift(ctx, now)
	frozen := fz.frozen
	resolved, correct, rerr := w.resolveFilingsDrift(ctx, now)
	if frozen > 0 {
		// Lineage spine, same as the grid: tie the hypothesis to this day's run.
		_ = lineage.Link(ctx, w.St, lineage.Edge{
			SrcKind: lineage.KindHypothesis, SrcID: "loop:" + FilingsDriftKind,
			DstKind: lineage.KindExperiment, DstID: "loop-run:" + today,
			EdgeKind: lineage.EdgeTestedIn, MetaJSON: lineage.RevMeta(),
		})
	}
	// A permanently empty hypothesis and a quiet filing calendar are different
	// findings, and "froze 0" says neither. The freeze path names why every
	// fresh filing it saw did not become a call, so the pre-registration cannot
	// keep accruing credit for a test that never started.
	msg := fmt.Sprintf("filings-drift21: froze %d post-filing calls, resolved %d (%d correct); %s",
		frozen, resolved, correct, fz.summary())
	if fz.fresh > 0 && frozen == 0 && fz.fresh > fz.reactionUnprinted {
		// Every fresh filing skipped for a reason OTHER than an unprinted
		// reaction bar is a silently lost forecast: the unprinted bar is the
		// designed wait, everything else is missing data.
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now.Unix(), Kind: "filingsdrift_no_freeze",
			Detail: fmt.Sprintf("%d fresh 10-Q/10-K in the %dd window and 0 calls "+
				"frozen for reasons other than an unprinted reaction bar: %s",
				fz.fresh, filingsDriftFreshDays, fz.summary()),
		})
		msg += " (dq recorded: fresh filings present, nothing frozen)"
	}
	if ferr != nil {
		msg += fmt.Sprintf("; freeze error: %v", ferr)
	}
	if rerr != nil {
		msg += fmt.Sprintf("; resolve error: %v", rerr)
	}
	return msg
}

// freezeFilingsDrift writes one ungraded regime_outcomes row per fresh
// 10-Q/10-K whose reaction bar has printed. The reaction bar is the first
// daily bar whose session OPENED at or after filed_ts — an intra-session
// filing rolls to the NEXT session, sacrificing the same-day reaction rather
// than risking lookahead.
// filingsFreezeTally counts what the freeze pass actually saw. Collapsing these
// into a single "froze 0" makes an engine with no bars indistinguishable from a
// week with no filings, and both indistinguishable from a healthy wait for the
// reaction session to print.
type filingsFreezeTally struct {
	fresh             int // fresh 10-Q/10-K inside the freshness window
	frozen            int // new ungraded calls written
	alreadyFrozen     int // dedup hit — already called on a prior pass
	missingBars       int // no daily bars (or too few) around filed_ts
	reactionUnprinted int // reaction session not printed yet, or no prior close
	badPrices         int // non-positive close on the reaction or prior bar
	zeroReaction      int // r0 == 0 — no direction to persist
}

func (t filingsFreezeTally) summary() string {
	if t.fresh == 0 {
		return "no fresh 10-Q/10-K in the freshness window (filing calendar quiet, " +
			"not a broken freeze path)"
	}
	return fmt.Sprintf("%d fresh filing(s) seen: %d newly frozen, %d already frozen, "+
		"%d awaiting the reaction bar, %d missing bars, %d bad prices, %d zero reaction",
		t.fresh, t.frozen, t.alreadyFrozen, t.reactionUnprinted, t.missingBars,
		t.badPrices, t.zeroReaction)
}

func (w *ResearchLoop) freezeFilingsDrift(ctx context.Context, now time.Time) (filingsFreezeTally, error) {
	var tally filingsFreezeTally
	latest, err := w.St.LatestPeriodicFilingAll(ctx)
	if err != nil {
		return tally, err
	}
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return tally, err
	}
	cutoff := now.AddDate(0, 0, -filingsDriftFreshDays).Unix()
	for _, s := range syms {
		if s.Market != md.Stocks {
			continue
		}
		f, ok := latest[s.ID]
		if !ok || f.FiledTs < cutoff {
			continue
		}
		tally.fresh++
		// Enough lookback for a prior close; forward bars are irrelevant here.
		bars, err := w.St.Bars(ctx, s.ID, md.TF1d, f.FiledTs-45*86400, now.Unix()+1, 0)
		if err != nil || len(bars) < 2 {
			tally.missingBars++
			continue
		}
		t := -1
		for i, b := range bars {
			if b.Ts >= f.FiledTs {
				t = i
				break
			}
		}
		if t <= 0 {
			tally.reactionUnprinted++
			continue // reaction session not printed yet, or no prior close
		}
		prev, cur := bars[t-1].Close, bars[t].Close
		if prev <= 0 || cur <= 0 {
			tally.badPrices++
			continue
		}
		r0 := cur/prev - 1
		if r0 == 0 {
			tally.zeroReaction++
			continue // no direction to persist — not called, not graded
		}
		regime := "drift-up"
		if r0 < 0 {
			regime = "drift-down"
		}
		conv := math.Abs(r0) / filingsDriftConvScale
		if conv > 1 {
			conv = 1
		}
		isNew, err := w.St.InsertRegimeOutcome(ctx, store.RegimeCall{
			SymbolID: s.ID, Kind: structregime.Kind(FilingsDriftKind),
			Ts: bars[t].Ts, HorizonDays: filingsDriftHorizon,
			Regime: regime, Conviction: conv,
			// The frozen "claim" is the registered coin-flip null, so the
			// registry's claimed column reads 0.5 — see filingsDriftSpec.
			HistoricalAccuracy: filingsDriftClaim,
		})
		if err != nil {
			return tally, err
		}
		if isNew {
			tally.frozen++
		} else {
			tally.alreadyFrozen++
		}
	}
	return tally, nil
}

// resolveFilingsDrift grades due filingsdrift21 rows with the pre-registered
// rule: correct when sign(close[t+21]/close[t]-1) matches the frozen call. The
// regime-outcome worker's resolver switch skips unknown kinds by design, so
// this kind is graded here and nowhere else.
func (w *ResearchLoop) resolveFilingsDrift(ctx context.Context, now time.Time) (resolved, correct int, err error) {
	due, err := w.St.DueRegimeOutcomes(ctx, now.Unix(), filingsDriftResolveBatch)
	if err != nil {
		return 0, 0, err
	}
	for _, o := range due {
		if string(o.Kind) != FilingsDriftKind {
			continue
		}
		bars, berr := w.St.Bars(ctx, o.SymbolID, md.TF1d, o.Ts-5*86400, now.Unix()+1, 0)
		if berr != nil {
			continue
		}
		t := -1
		for i := len(bars) - 1; i >= 0; i-- {
			if bars[i].Ts <= o.Ts {
				t = i
				break
			}
		}
		// Require the FULL forward window in real bars — the calendar-day
		// approximation in DueRegimeOutcomes alone must never grade a window
		// that hasn't printed.
		if t < 0 || len(bars)-1-t < o.HorizonDays {
			continue
		}
		c0, c1 := bars[t].Close, bars[t+o.HorizonDays].Close
		if c0 <= 0 || c1 <= 0 {
			continue
		}
		fwd := c1/c0 - 1
		if fwd == 0 {
			continue // tie — not graded rather than guessed
		}
		actual := "drift-up"
		if fwd < 0 {
			actual = "drift-down"
		}
		ok := actual == o.Regime
		if rerr := w.St.ResolveRegimeOutcome(ctx, o.ID, actual, ok, now.Unix()); rerr != nil {
			continue
		}
		resolved++
		if ok {
			correct++
		}
	}
	return resolved, correct, nil
}
