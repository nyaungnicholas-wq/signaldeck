package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// engineTestSymbols registers n stock symbols and returns their ids.
func engineTestSymbols(t *testing.T, st *store.Store, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := range ids {
		s, err := st.UpsertSymbol(context.Background(), fmt.Sprintf("E%02d", i), md.Stocks, "")
		if err != nil {
			t.Fatalf("sym: %v", err)
		}
		ids[i] = s.ID
	}
	return ids
}

// seedEngineHyp registers one machine-gradable hypothesis at its prior.
func seedEngineHyp(t *testing.T, st *store.Store, id string, prior float64) {
	t.Helper()
	h := rl.Hypothesis{
		ID: id, Family: "meanrev", Horizon: "1w",
		Statement: "test seed: " + id, Prior: prior, MaxEdge: rl.DefaultMaxEdge,
	}
	h.Posterior = prior
	h.Status = rl.Status(prior, 0, 1)
	h.Regimes = 1
	if err := st.UpsertLedgerHypothesis(context.Background(), h, 1); err != nil {
		t.Fatalf("seed hyp: %v", err)
	}
}

// plantWeeks builds one era of planted weekly observations: len(symIDs) obs
// per calendar week, alternating up/down (folded baseline exactly 0.5). The
// inverse-pressure call is correct for 8/10 symbols on winning weeks and
// 4/10 on the weeks in losing. extra merges additional Vec keys into every
// obs (for discovery atoms).
func plantWeeks(symIDs []int64, startTs int64, weeks int, era string, highVol bool, losing map[int]bool, extra map[string]float64) []store.ResearchWeek {
	var out []store.ResearchWeek
	for wk := 0; wk < weeks; wk++ {
		base := startTs + int64(wk)*histfeat.WeekSecs
		for i, sid := range symIDs {
			up := i%2 == 0
			correct := i < 8
			if losing[wk] {
				correct = i < 4
			}
			pressure := 0.5 // inverse call: short
			if up == correct {
				pressure = -0.5 // inverse call: long
			}
			fwd := -0.01
			if up {
				fwd = 0.01
			}
			vec := map[string]float64{"pressure_score": pressure}
			for k, v := range extra {
				vec[k] = v
			}
			ts := base + int64(i)
			out = append(out, store.ResearchWeek{
				SymbolID: sid, Week: ts / histfeat.WeekSecs, Ts: ts, Vec: vec,
				FwdReturn: fwd, Up: up, Era: era, HighVol: highVol,
			})
		}
	}
	return out
}

// autoHyps returns the auto-family hypotheses sorted by ID.
func autoHyps(t *testing.T, st *store.Store) []rl.Hypothesis {
	t.Helper()
	hyps, err := st.LedgerHypotheses(context.Background())
	if err != nil {
		t.Fatalf("hyps: %v", err)
	}
	var out []rl.Hypothesis
	for _, h := range hyps {
		if h.Family == "auto" {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func evidenceKinds(ev []rl.Evidence) []string {
	out := make([]string, len(ev))
	for i, e := range ev {
		out[i] = e.Kind
	}
	return out
}

// A planted 2-era inverse-pressure edge becomes kind=backtest evidence on
// H002: the posterior rises, attacks precede each grade row, the seeded
// high-vol era flips regimes to 2, decay fields land, the sentinel drops the
// impossible row, H002-R1 is seeded — and the backtest cursor prevents any
// re-grade on the next run.
func TestResearchEnginePlantedEdgeGradesH002(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)
	ids := engineTestSymbols(t, st, 10)
	seedEngineHyp(t, st, "H002", 0.25)

	nowTs := int64(1_800_000_000)
	losing := map[int]bool{8: true, 9: true} // 8/10 winning weeks per era
	rows := plantWeeks(ids, 1_583_020_800, 10, histfeat.EraCovidCrash, true, losing, nil)
	rows = append(rows, plantWeeks(ids, 1_601_510_400, 10, histfeat.EraBull2021, false, losing, nil)...)
	// One structurally impossible row: the sentinel must drop it, not grade it.
	badTs := int64(1_601_510_400) + 10*histfeat.WeekSecs
	rows = append(rows, store.ResearchWeek{
		SymbolID: ids[0], Week: badTs / histfeat.WeekSecs, Ts: badTs,
		Vec:       map[string]float64{"pressure_score": -0.5},
		FwdReturn: 2.0, Up: true, Era: histfeat.EraBull2021, HighVol: false,
	})
	if err := st.UpsertResearchWeeks(ctx, rows, nowTs); err != nil {
		t.Fatalf("seed weeks: %v", err)
	}

	w := &ResearchEngineWorker{St: st, Now: func() time.Time { return time.Unix(nowTs, 0) }}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "graded 2 era-grades over 1 hyps") {
		t.Errorf("detail %q, want 2 era grades on the one registered hypothesis", msg)
	}
	if !strings.Contains(msg, "sentinel dropped 1") {
		t.Errorf("detail %q lacks the sentinel drop", msg)
	}
	dqs, err := st.RecentDQ(ctx, 10)
	if err != nil {
		t.Fatalf("dq: %v", err)
	}
	foundSentinel := false
	for _, d := range dqs {
		if d.Kind == "research_sentinel" {
			foundSentinel = true
		}
	}
	if !foundSentinel {
		t.Errorf("no research_sentinel dq event recorded for the dropped row")
	}

	h2 := ledgerHypByID(t, st, "H002")
	if h2.Posterior <= h2.Prior {
		t.Errorf("posterior %.4f did not rise above prior %.2f on a planted 2-era edge", h2.Posterior, h2.Prior)
	}
	if h2.Regimes != 2 {
		t.Errorf("regimes = %d, want 2 (100 high-vol obs graded)", h2.Regimes)
	}
	if h2.Replications != 2 || h2.Contradictions != 0 {
		t.Errorf("counters = (%d,%d), want (2,0) — backtest era grades count as replications",
			h2.Replications, h2.Contradictions)
	}
	if h2.PeakPosterior <= h2.Prior || h2.PeakTs != nowTs || h2.LastGradeTs != nowTs {
		t.Errorf("decay fields = (%.4f, %d, %d), want peak above prior at ts %d",
			h2.PeakPosterior, h2.PeakTs, h2.LastGradeTs, nowTs)
	}

	ev, err := st.LedgerEvidence(ctx, "H002")
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	var backtests []rl.Evidence
	attacks := 0
	for _, e := range ev {
		switch e.Kind {
		case rl.KindBacktest:
			backtests = append(backtests, e)
		case rl.KindAttack:
			attacks++
		}
	}
	if len(backtests) != 2 || attacks != 6 {
		t.Fatalf("evidence = %d backtests + %d attacks, want 2 + 6 (kinds %v)",
			len(backtests), attacks, evidenceKinds(ev))
	}
	for i, era := range []string{histfeat.EraCovidCrash, histfeat.EraBull2021} {
		e := backtests[i]
		if e.K != 8 || e.N != 10 {
			t.Errorf("era grade %d = %d/%d week trials, want 8/10", i, e.K, e.N)
		}
		if !strings.Contains(e.Note, "era="+era) || !strings.Contains(e.Note, "historical backfill, survivor universe") {
			t.Errorf("era grade note %q lacks era marker / survivor-universe label", e.Note)
		}
	}
	// Attack-before-grade crash discipline (LedgerEvidence orders by ts, id).
	if ev[0].Kind != rl.KindAttack || ev[3].Kind != rl.KindBacktest ||
		ev[4].Kind != rl.KindAttack || ev[7].Kind != rl.KindBacktest {
		t.Errorf("evidence order broke attacks-before-grade: kinds %v", evidenceKinds(ev))
	}
	// Exactly one survivorship penalty stays EFFECTIVE (static-state dedupe).
	surv := 0
	for _, e := range rl.EffectiveChain(ev) {
		if e.Kind == rl.KindAttack && strings.HasPrefix(e.Note, "survivorship:") {
			surv++
		}
	}
	if surv != 1 {
		t.Errorf("effective chain carries %d survivorship rows, want 1 (deduped)", surv)
	}

	// H002-R1 seeded (step D) with its machine-readable spec.
	r1 := ledgerHypByID(t, st, "H002-R1")
	if r1.Prior != 0.20 || r1.MaxEdge != 0.20 || r1.Family != "meanrev" {
		t.Errorf("H002-R1 = (prior %.2f, maxEdge %.2f, family %s), want (0.20, 0.20, meanrev)",
			r1.Prior, r1.MaxEdge, r1.Family)
	}
	var rule researchx.Rule
	if err := json.Unmarshal([]byte(r1.Spec), &rule); err != nil || len(rule.Conds) != 2 || rule.Call != "inverse_pressure" {
		t.Errorf("H002-R1 spec %q did not parse into the 2-cond inverse rule (%v)", r1.Spec, err)
	}

	// Next day: the backtest cursor forbids re-grading the consumed eras.
	before := h2.Posterior
	w.Now = func() time.Time { return time.Unix(nowTs+86_400, 0) }
	msg, err = w.Run(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(msg, "graded 0 era-grades") {
		t.Errorf("second run %q re-graded consumed eras", msg)
	}
	ev2, err := st.LedgerEvidence(ctx, "H002")
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if len(ev2) != len(ev) {
		t.Errorf("evidence grew on re-run: %d -> %d rows", len(ev), len(ev2))
	}
	if got := ledgerHypByID(t, st, "H002").Posterior; got != before {
		t.Errorf("posterior moved without new data: %.6f -> %.6f", before, got)
	}
}

// Zero historical rows: the engine reports the honest empty state, creates
// nothing, and does NOT consume the day — a fresh deploy's boot pass must not
// stall the first real pass behind the daily gate while hist-backfill is
// still producing rows.
func TestResearchEngineHonestEmptyState(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)
	w := &ResearchEngineWorker{St: st, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	msg, err := w.Run(ctx)
	if err != nil || msg != "no historical weeks yet — retrying on the next heartbeat" {
		t.Fatalf("run = (%q, %v), want the honest empty state", msg, err)
	}
	hyps, err := st.LedgerHypotheses(ctx)
	if err != nil {
		t.Fatalf("hyps: %v", err)
	}
	if len(hyps) != 0 {
		t.Errorf("empty pass created %d hypotheses, want 0", len(hyps))
	}
	msg, err = w.Run(ctx)
	if err != nil || msg != "no historical weeks yet — retrying on the next heartbeat" {
		t.Fatalf("second empty run = (%q, %v), want a retry — the empty state must not burn the day gate", msg, err)
	}
}

// Auto-discovery registers surviving candidates as new ledger hypotheses:
// capped at MaxNewHyps per run, idempotent across runs, each carrying its
// attack battery plus exactly one capped in-sample experiment row — and an
// auto hypothesis's own in-sample window blocks every later era grade.
func TestResearchEngineDiscoveryCappedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)
	ids := engineTestSymbols(t, st, 10)

	nowTs := int64(1_800_000_000)
	// Four single-cond survivors by construction: ext_score, rsi_pct,
	// vol_pct, consec_dir all sit safely past their grid thresholds on every
	// obs, and the inverse-pressure edge wins 34/40 weeks across two eras.
	extra := map[string]float64{"ext_score": 0.85, "rsi_pct": 0.9, "vol_pct": 0.9, "consec_dir": 0.5}
	losing := map[int]bool{5: true, 11: true, 17: true} // 17/20 winning weeks per era
	rows := plantWeeks(ids, 1_601_510_400, 20, histfeat.EraBull2021, false, losing, extra)
	rows = append(rows, plantWeeks(ids, 1_644_000_000, 20, histfeat.EraBear2022, false, losing, extra)...)
	if err := st.UpsertResearchWeeks(ctx, rows, nowTs); err != nil {
		t.Fatalf("seed weeks: %v", err)
	}

	w := &ResearchEngineWorker{St: st, MaxNewHyps: 2, Now: func() time.Time { return time.Unix(nowTs, 0) }}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "discovered 2") {
		t.Errorf("detail %q, want exactly 2 discoveries (the cap)", msg)
	}

	autos := autoHyps(t, st)
	if len(autos) != 2 {
		t.Fatalf("auto hypotheses after run 1 = %d, want 2 (capped)", len(autos))
	}
	for _, h := range autos {
		if h.Prior != 0.15 || h.MaxEdge != 0.20 {
			t.Errorf("%s prior/maxEdge = %.2f/%.2f, want 0.15/0.20", h.ID, h.Prior, h.MaxEdge)
		}
		if !strings.HasPrefix(h.ID, "AD-") {
			t.Errorf("auto id %q lacks the AD- prefix", h.ID)
		}
		var rule researchx.Rule
		if err := json.Unmarshal([]byte(h.Spec), &rule); err != nil || rule.Call == "" {
			t.Errorf("%s spec %q does not parse (%v)", h.ID, h.Spec, err)
		}
		ev, err := st.LedgerEvidence(ctx, h.ID)
		if err != nil {
			t.Fatalf("evidence: %v", err)
		}
		exp := 0
		for i, e := range ev {
			if e.Kind != rl.KindExperiment {
				continue
			}
			exp++
			if i == 0 {
				t.Errorf("%s: experiment row not preceded by attacks", h.ID)
			}
			if e.BF > rl.DiscoveryMaxBF {
				t.Errorf("%s experiment BF %.2f exceeds DiscoveryMaxBF %.1f", h.ID, e.BF, rl.DiscoveryMaxBF)
			}
			if !strings.Contains(e.Note, "auto-discovered on this very window") || !strings.Contains(e.Note, "era=all") {
				t.Errorf("%s experiment note %q lacks the in-sample caveat / era=all marker", h.ID, e.Note)
			}
		}
		if exp != 1 {
			t.Errorf("%s carries %d experiment rows, want exactly 1", h.ID, exp)
		}
	}
	firstID := autos[0].ID
	firstEv, err := st.LedgerEvidence(ctx, firstID)
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}

	// Run 2 (next day): the remaining survivors land; no duplicates; the
	// run-1 hypotheses gain NO new evidence — their in-sample experiment
	// window covers all data, so the cursor discipline blocks every era grade.
	w.Now = func() time.Time { return time.Unix(nowTs+86_400, 0) }
	if msg, err = w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !strings.Contains(msg, "discovered 2") {
		t.Errorf("run 2 %q, want the remaining 2 survivors", msg)
	}
	if n := len(autoHyps(t, st)); n != 4 {
		t.Fatalf("auto hypotheses after run 2 = %d, want 4", n)
	}
	if ev, err := st.LedgerEvidence(ctx, firstID); err != nil {
		t.Fatalf("evidence: %v", err)
	} else if len(ev) != len(firstEv) {
		t.Errorf("auto hyp %s evidence grew on run 2: %d -> %d (in-sample window re-graded)",
			firstID, len(firstEv), len(ev))
	}

	// Run 3: everything already registered — nothing new, nothing duplicated.
	w.Now = func() time.Time { return time.Unix(nowTs+2*86_400, 0) }
	if msg, err = w.Run(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if !strings.Contains(msg, "discovered 0") {
		t.Errorf("run 3 %q, want zero new discoveries", msg)
	}
	if n := len(autoHyps(t, st)); n != 4 {
		t.Errorf("auto hypotheses after run 3 = %d, want 4", n)
	}
}
