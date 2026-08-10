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
// EvidenceMix / MachineGrades say what the posterior is MADE of: a 0.95 from
// one hand-typed `manual` row must not render like a 0.95 from thirty machine
// grades, so the mix travels next to the band. EvidenceMix is null (not empty)
// for hypotheses whose chain was not loaded because ?id narrowed the query —
// null (and MachineGrades -1) means "unknown here", never "nothing".
type gateRow struct {
	ID            string         `json:"id"`
	TradableForm  string         `json:"tradableForm"`
	EconomicTest  string         `json:"economicTest"`
	UnmetGate     string         `json:"unmetGate"`
	EvidenceMix   map[string]int `json:"evidenceMix"`
	MachineGrades int            `json:"machineGrades"`
}

func (d Deps) researchLedger(w http.ResponseWriter, r *http.Request) {
	hyps, err := d.St.LedgerHypotheses(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	idFilter := r.URL.Query().Get("id")
	evidence, err := d.St.LedgerEvidence(r.Context(), idFilter)
	if err != nil {
		httpInternal(w, err)
		return
	}
	weeks, err := d.St.ResearchWeeksStats(r.Context())
	if err != nil {
		httpInternal(w, err)
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
		chain, loaded := chains[h.ID], idFilter == "" || h.ID == idFilter
		var mix map[string]int
		if loaded {
			mix = rl.EvidenceMix(chain)
		}
		g := rl.Gates{
			MachineGrades: rl.MachineGrades(chain),
			Replications:  h.Replications, Regimes: h.Regimes,
			TradableForm: h.TradableForm, EconomicTest: h.EconomicTest,
		}
		row := gateRow{
			ID: h.ID, TradableForm: h.TradableForm, EconomicTest: h.EconomicTest,
			EvidenceMix: mix, MachineGrades: g.MachineGrades,
		}
		if loaded {
			row.UnmetGate = g.UnmetGate()
		} else {
			row.MachineGrades = -1
			// Chain not loaded: report only the gates derivable from the
			// stored counters, never "no machine evidence" from an absence
			// this request created.
			g.MachineGrades = 1
			row.UnmetGate = g.UnmetGate()
		}
		gates = append(gates, row)
	}

	// The testing verdict travels beside the truth verdict. A band says how
	// strong a posterior is and cannot say when it was last put at risk, so a
	// 0.95 no replication ever touched renders UNREPLICATED here rather than
	// "tentative" — see store.LedgerEngineHealth. A read error is reported as
	// an absent verdict, never as a healthy one.
	liveness, lerr := d.St.LedgerEngineHealth(r.Context(), time.Now())
	var livenessPayload any
	if lerr == nil {
		livenessPayload = liveness
	}

	writeJSON(w, map[string]any{
		"hypotheses":    hyps,
		"liveness":      livenessPayload,
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
		"discipline":      "priors fixed at creation; posterior = prior odds × ∏ Bayes factors (each clamped to [1/20,20] — no single experiment can reach certainty); replications grade only NEW disjoint data windows; failed self-attacks enter the same evidence chain; a band above 'uncertain' additionally requires at least one MACHINE-graded row — a posterior resting only on hand-entered `manual` evidence is capped at 'uncertain' however high it is; 'supported' additionally requires ≥2 replications and ≥2 volatility regimes; historical era grades are BACKTEST evidence on a survivor universe — penalized as such, never presented as live.",
		"livenessRule": "The band and the liveness verdict answer different questions. A hypothesis at posterior >= 0.9 with ZERO replication rows renders UNREPLICATED, not 'tentative': its number was seeded and never re-graded on a fresh disjoint window. A hypothesis with no evidence row of any kind for 14+ days while the research corpus holds newer data renders STALE. Neither state moves a posterior, a threshold or a null — they can only make a hypothesis read weaker than its band does.",
		"tradabilityGate": "A hypothesis may not reach 'supported' on a statistic alone. It must state the POSITION that would have to earn the money and have that position graded net of costs. H018 is why: it held a 73.1% point estimate with a tight out-of-sample interval for eight days, and its tradable form — a cointegration spread — turned out indistinguishable from picking pairs at random, because the quantity that persists is shared market beta and a dollar-neutral spread cancels exactly that. The 1-session news-sentiment IC repeated the lesson from the other side: an interval excluding zero even after Bonferroni, with a NEGATIVE cost-net book. In neither case was the missing ingredient sample size.",
	})
}
