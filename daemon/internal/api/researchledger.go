package api

import (
	"net/http"
	"time"

	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/researchx"
)

// decayRow is one hypothesis's decay summary in the ledger payload: where the
// posterior peaked, where it sits now, and whether it is weakening or has
// stopped being tested.
type decayRow struct {
	ID            string  `json:"id"`
	Peak          float64 `json:"peak"`
	Current       float64 `json:"current"`
	EdgeWeakening bool    `json:"edgeWeakening"`
	Stale         bool    `json:"stale"`
}

// researchLedger serves the Bayesian research ledger: every program-level
// hypothesis with its prior → posterior, status band, replication /
// contradiction counters, open questions, and full evidence chain (each row a
// Bayes factor from an experiment, replication, backtest, or self-attack),
// plus the meta-analysis (which signal families keep surviving; which attack
// kills the most research), the historical research-weeks stats, evidence-kind
// counts, the live-vs-backtest replication split, and a per-hypothesis decay
// summary. ?id=H008 narrows the evidence — and therefore the decay rows — to
// one hypothesis.
// gateRow reports one hypothesis's tradability state: the position it implies,
// the run that graded that position, and the first supported-gate it still
// fails ("" when it fails none).
type gateRow struct {
	ID           string `json:"id"`
	TradableForm string `json:"tradableForm"`
	EconomicTest string `json:"economicTest"`
	UnmetGate    string `json:"unmetGate"`
}

func (d Deps) researchLedger(w http.ResponseWriter, r *http.Request) {
	hyps, err := d.St.LedgerHypotheses(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	idFilter := r.URL.Query().Get("id")
	evidence, err := d.St.LedgerEvidence(r.Context(), idFilter)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	weeks, err := d.St.ResearchWeeksStats(r.Context())
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	kinds := map[string]int{}
	chains := map[string][]rl.Evidence{}
	for _, e := range evidence {
		kinds[e.Kind]++
		chains[e.HypID] = append(chains[e.HypID], e)
	}
	// When ?id narrows the loaded evidence, decay is reported only for that
	// hypothesis — an unloaded chain must not read as "never graded".
	now := time.Now().Unix()
	decay := make([]decayRow, 0, len(hyps))
	for _, h := range hyps {
		if idFilter != "" && h.ID != idFilter {
			continue
		}
		rep := researchx.Decay(h.Prior, chains[h.ID], now)
		decay = append(decay, decayRow{
			ID: h.ID, Peak: rep.Peak, Current: rep.Current,
			EdgeWeakening: rep.EdgeWeakening, Stale: rep.Stale,
		})
	}

	// The tradability gate, per hypothesis: a strong posterior held at
	// "tentative" must say WHY, or it reads as arbitrary withholding.
	gates := make([]gateRow, 0, len(hyps))
	for _, h := range hyps {
		g := rl.Gates{
			Replications: h.Replications, Regimes: h.Regimes,
			TradableForm: h.TradableForm, EconomicTest: h.EconomicTest,
		}
		gates = append(gates, gateRow{
			ID: h.ID, TradableForm: h.TradableForm, EconomicTest: h.EconomicTest,
			UnmetGate: g.UnmetGate(),
		})
	}

	writeJSON(w, map[string]any{
		"hypotheses":    hyps,
		"gates":         gates,
		"evidence":      evidence,
		"weeks":         weeks,
		"evidenceKinds": kinds,
		"liveVsBacktest": map[string]int{
			"live":     kinds[rl.KindReplication],
			"backtest": kinds[rl.KindBacktest],
		},
		"decay": decay,
		"meta": map[string]any{
			"families": rl.Meta(hyps),
			"attacks":  rl.AttackLethality(evidence),
		},
		"discipline": "priors fixed at creation; posterior = prior odds × ∏ Bayes factors (each clamped to [1/20,20] — no single experiment can reach certainty); replications grade only NEW disjoint data windows; failed self-attacks enter the same evidence chain; 'supported' additionally requires ≥2 replications and ≥2 volatility regimes; historical era grades are BACKTEST evidence on a survivor universe — penalized as such, never presented as live.",
		"tradabilityGate": "A hypothesis may not reach 'supported' on a statistic alone. It must state the POSITION that would have to earn the money and have that position graded net of costs. H018 is why: it held a 73.1% point estimate with a tight out-of-sample interval for eight days, and its tradable form — a cointegration spread — turned out indistinguishable from picking pairs at random, because the quantity that persists is shared market beta and a dollar-neutral spread cancels exactly that. The 1-session news-sentiment IC repeated the lesson from the other side: an interval excluding zero even after Bonferroni, with a NEGATIVE cost-net book. In neither case was the missing ingredient sample size.",
	})
}
