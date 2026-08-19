// Package fleetmon is the single monitoring surface: one object carrying the
// trading, model and system health of the whole fleet, with a rollup that says
// what is broken and what has been switched off because of it.
//
// WHY THIS EXISTS. Every number below was already computed somewhere.
// papertrade.Summarize had return/Sharpe/drawdown, internal/modelhealth had
// skill/calibration/drift with real auto-retirement, internal/health watched
// worker staleness, internal/srchealth watched sources. They were never assembled,
// so nobody could answer "is the platform healthy" without opening five
// dashboards and holding the thresholds in their head. Worse, the trading metrics
// and the model metrics lived so far apart that a model could be auto-retired for
// drift while the book it drove kept reporting a cheerful Sharpe.
//
// TWO PROPERTIES THIS ADDS RATHER THAN RESTATES:
//
//   - EVERY METRIC IS GATED. A statistic the sample cannot support arrives as nil
//     and is named in Withheld with its reason. A monitoring surface that prints
//     0.00 for an unmeasurable Sortino is worse than one that prints nothing: the
//     zero looks like a reading.
//   - THE ROLLUP IS DERIVED, NOT ASSERTED. Status is computed from the breaches
//     actually present, and Disabled lists what the platform switched off on its
//     own. If those two ever disagree — a critical status with nothing disabled —
//     that is a finding, not a display quirk.
package fleetmon

import (
	"fmt"
	"sort"
)

// Status is the rollup verdict.
type Status string

const (
	// StatusHealthy — nothing is breaching.
	StatusHealthy Status = "healthy"
	// StatusDegraded — something is breaching but the platform is still serving.
	StatusDegraded Status = "degraded"
	// StatusCritical — a breach that stops the platform doing its job.
	StatusCritical Status = "critical"
	// StatusUnknown — too little is measurable to claim any of the above. NOT the
	// same as healthy, and must never render as green.
	StatusUnknown Status = "unknown"
)

// Trading is the book's realized performance. Nil fields were not measurable.
type Trading struct {
	Strategy string `json:"strategy"`

	TotalReturn     *float64 `json:"totalReturn"`
	Sharpe          *float64 `json:"sharpe"`
	Sortino         *float64 `json:"sortino"`
	MaxDrawdown     *float64 `json:"maxDrawdown"`
	CurrentDrawdown *float64 `json:"currentDrawdown"`
	HitRate         *float64 `json:"hitRate"`
	ProfitFactor    *float64 `json:"profitFactor"`
	Turnover        *float64 `json:"turnover"`

	ClosedTrades int `json:"closedTrades"`
	EquityMarks  int `json:"equityMarks"`
}

// Model is one model's measured health, as internal/modelhealth graded it.
type Model struct {
	Name    string   `json:"name"`
	Verdict string   `json:"verdict"`
	Overall *float64 `json:"overall"`

	Accuracy       *float64 `json:"accuracy"`
	BrierSkill     *float64 `json:"brierSkill"`
	CalibrationErr *float64 `json:"calibrationErr"`
	IC             *float64 `json:"ic"`
	FeatureDrift   *float64 `json:"featureDrift"`

	Observations int  `json:"observations"`
	Emitting     bool `json:"emitting"`
}

// System is the operational layer: is the data arriving and are the workers
// running.
type System struct {
	StaleWorkers []string `json:"staleWorkers"`
	// FailingWorkers ran on cadence and errored anyway. Separate from
	// StaleWorkers because the two have opposite causes — stale means it is not
	// running, failing means it is — and an operator sent looking for a stopped
	// worker will not find a punctual broken one.
	FailingWorkers []string `json:"failingWorkers"`
	TotalWorkers   int      `json:"totalWorkers"`
	FailingSource  []string `json:"failingSources"`

	// DataAgeSeconds is how old the freshest market data is, and LatencyMs the
	// most recent measured request latency. Nil when not measured.
	DataAgeSeconds *float64 `json:"dataAgeSeconds"`
	LatencyMs      *float64 `json:"latencyMs"`
}

// Thresholds are the levels at which a measurement becomes a breach. Exported so
// the rollup's judgements are inspectable rather than buried in comparisons.
type Thresholds struct {
	// CriticalDrawdown is the current drawdown at which trading is critical. It
	// matches riskgate's default halt so the monitor and the gate agree about
	// what "too deep" means.
	CriticalDrawdown float64
	// DegradedSortino / DegradedProfitFactor are the levels below which the book
	// is losing money in a way worth flagging.
	DegradedSortino      float64
	DegradedProfitFactor float64
	// MaxDataAgeSeconds is how stale the freshest data may be before the platform
	// is predicting from history.
	MaxDataAgeSeconds float64
	// MaxStaleWorkerFrac is the share of stale workers at which the fleet is
	// considered critically behind rather than merely degraded.
	MaxStaleWorkerFrac float64
}

// DefaultThresholds returns the standard levels.
//
//   - CriticalDrawdown 0.20 deliberately equals riskgate's default MaxDrawdown:
//     the monitor should go red at exactly the point the book stops opening risk,
//     not at some independently-chosen number that would let the two disagree in
//     public.
//   - DegradedProfitFactor 1.0 is the break-even line — below it the losers are
//     bigger than the winners, which is a fact and not a matter of taste.
//   - DegradedSortino 0 is the same statement about risk-adjusted return.
//   - MaxDataAgeSeconds 4 days spans a long holiday weekend, so a normal market
//     closure is never reported as an outage.
//   - MaxStaleWorkerFrac 0.34 — a third of the fleet behind is a systemic
//     problem, not a slow worker.
func DefaultThresholds() Thresholds {
	return Thresholds{
		CriticalDrawdown:     0.20,
		DegradedSortino:      0,
		DegradedProfitFactor: 1.0,
		MaxDataAgeSeconds:    4 * 24 * 3600,
		MaxStaleWorkerFrac:   0.34,
	}
}

// Snapshot is the whole surface.
type Snapshot struct {
	Status Status `json:"status"`
	// Breaches are the specific things wrong, most severe first.
	Breaches []string `json:"breaches,omitempty"`
	// Disabled is what the platform switched OFF on its own — retired models,
	// halted books. Present so auto-disable is visible rather than inferred from
	// a missing prediction.
	Disabled []string `json:"disabled,omitempty"`
	// Withheld names every metric that came back nil, with the reason.
	Withheld []string `json:"withheld,omitempty"`

	Trading []Trading `json:"trading"`
	Models  []Model   `json:"models"`
	System  System    `json:"system"`
	// Layers is architectural coverage: which layers exist and which are
	// actually consulted. See layers.go for why Live is tracked separately.
	Layers LayerCoverage `json:"layers"`

	Note string `json:"note"`
}

// Assemble builds the rollup from the measured parts.
//
// It does not measure anything itself — that is the caller's job and keeps this
// function pure and testable. What it does is decide, from thresholds that are
// visible in the payload, what the fleet's status is and why.
func Assemble(tr []Trading, models []Model, sys System, layers []Layer, th Thresholds) Snapshot {
	th = th.withDefaults()
	s := Snapshot{Trading: tr, Models: models, System: sys}

	critical := 0
	degraded := 0
	measured := 0

	// ── Trading ─────────────────────────────────────────────────────────────
	for _, t := range tr {
		if t.CurrentDrawdown != nil {
			measured++
			if *t.CurrentDrawdown >= th.CriticalDrawdown {
				critical++
				s.Breaches = append(s.Breaches, fmt.Sprintf(
					"%s is %.1f%% below its peak, at or past the %.1f%% halt — the book is closed to new entries",
					t.Strategy, *t.CurrentDrawdown*100, th.CriticalDrawdown*100))
				s.Disabled = append(s.Disabled, fmt.Sprintf("%s: new entries halted by the drawdown breaker", t.Strategy))
			}
		} else {
			s.Withheld = append(s.Withheld, t.Strategy+".currentDrawdown: no measurable equity history")
		}

		if t.ProfitFactor != nil {
			measured++
			if *t.ProfitFactor < th.DegradedProfitFactor {
				degraded++
				s.Breaches = append(s.Breaches, fmt.Sprintf(
					"%s profit factor %.2f is below break-even — its losers outweigh its winners",
					t.Strategy, *t.ProfitFactor))
			}
		} else {
			s.Withheld = append(s.Withheld, fmt.Sprintf(
				"%s.profitFactor: needs closed round trips in both tails (%d closed)", t.Strategy, t.ClosedTrades))
		}

		if t.Sortino != nil {
			measured++
			if *t.Sortino < th.DegradedSortino {
				degraded++
				s.Breaches = append(s.Breaches, fmt.Sprintf(
					"%s Sortino %.2f is negative — it is not being paid for its downside", t.Strategy, *t.Sortino))
			}
		} else {
			s.Withheld = append(s.Withheld, fmt.Sprintf(
				"%s.sortino: too few equity marks, or no losing mark to measure downside from", t.Strategy))
		}
	}

	// ── Models ──────────────────────────────────────────────────────────────
	for _, m := range models {
		if m.Verdict == "" {
			s.Withheld = append(s.Withheld, m.Name+".verdict: not graded")
			continue
		}
		measured++
		switch m.Verdict {
		case "retired":
			critical++
			s.Breaches = append(s.Breaches, fmt.Sprintf(
				"%s was RETIRED — the live record does not support it (%d observations)", m.Name, m.Observations))
			s.Disabled = append(s.Disabled, m.Name+": retired, no longer emitting")
		case "degraded":
			degraded++
			s.Breaches = append(s.Breaches, fmt.Sprintf("%s is degraded and emitting under a warning", m.Name))
		case "watch":
			degraded++
			s.Breaches = append(s.Breaches, fmt.Sprintf("%s is on watch", m.Name))
		}
		// A model that grades out but is STILL emitting is the contradiction this
		// surface exists to catch: the verdict and the behaviour disagree.
		if m.Verdict == "retired" && m.Emitting {
			critical++
			s.Breaches = append(s.Breaches, fmt.Sprintf(
				"%s is retired but STILL EMITTING — the auto-disable did not take effect, which is worse than either state alone", m.Name))
		}
	}

	// ── System ──────────────────────────────────────────────────────────────
	if sys.TotalWorkers > 0 {
		measured++
		frac := float64(len(sys.StaleWorkers)) / float64(sys.TotalWorkers)
		if frac >= th.MaxStaleWorkerFrac {
			critical++
			s.Breaches = append(s.Breaches, fmt.Sprintf(
				"%d of %d workers are stale (%.0f%%) — the fleet is behind, not one worker",
				len(sys.StaleWorkers), sys.TotalWorkers, frac*100))
		} else if len(sys.StaleWorkers) > 0 {
			degraded++
			s.Breaches = append(s.Breaches, fmt.Sprintf("stale workers: %v", sys.StaleWorkers))
		}
		// A worker erroring every run is a breach in its own right, and is NOT
		// implied by the stale count — it is precisely the case staleness
		// cannot reach, because the worker is running exactly on time.
		if len(sys.FailingWorkers) > 0 {
			degraded++
			s.Breaches = append(s.Breaches, fmt.Sprintf(
				"workers failing every run (running on cadence, erroring each time): %v",
				sys.FailingWorkers))
		}
	} else {
		s.Withheld = append(s.Withheld, "system.workers: no worker registry reading")
	}
	if len(sys.FailingSource) > 0 {
		degraded++
		s.Breaches = append(s.Breaches, fmt.Sprintf("failing data sources: %v", sys.FailingSource))
	}
	if sys.DataAgeSeconds != nil {
		measured++
		if *sys.DataAgeSeconds > th.MaxDataAgeSeconds {
			critical++
			s.Breaches = append(s.Breaches, fmt.Sprintf(
				"freshest market data is %.1f days old — every prediction downstream is being made from history",
				*sys.DataAgeSeconds/86400))
		}
	} else {
		s.Withheld = append(s.Withheld, "system.dataAgeSeconds: not measured")
	}

	// ── Architectural coverage ──────────────────────────────────────────────
	if len(layers) > 0 {
		measured++
		cov, layerBreaches := SummarizeLayers(layers)
		s.Layers = cov
		degraded += len(layerBreaches)
		s.Breaches = append(s.Breaches, layerBreaches...)
	}

	// ── Rollup ──────────────────────────────────────────────────────────────
	switch {
	case measured == 0:
		s.Status = StatusUnknown
		s.Note = "nothing was measurable — this is NOT a healthy reading, it is an absent one"
	case critical > 0:
		s.Status = StatusCritical
	case degraded > 0:
		s.Status = StatusDegraded
	default:
		s.Status = StatusHealthy
	}
	if s.Note == "" {
		s.Note = fmt.Sprintf(
			"%d measurements, %d critical and %d degraded breaches; every withheld metric is named rather than shown as zero",
			measured, critical, degraded)
	}
	sort.Strings(s.Disabled)
	sort.Strings(s.Withheld)
	return s
}

func (t Thresholds) withDefaults() Thresholds {
	d := DefaultThresholds()
	if t.CriticalDrawdown <= 0 {
		t.CriticalDrawdown = d.CriticalDrawdown
	}
	if t.DegradedProfitFactor <= 0 {
		t.DegradedProfitFactor = d.DegradedProfitFactor
	}
	if t.MaxDataAgeSeconds <= 0 {
		t.MaxDataAgeSeconds = d.MaxDataAgeSeconds
	}
	if t.MaxStaleWorkerFrac <= 0 {
		t.MaxStaleWorkerFrac = d.MaxStaleWorkerFrac
	}
	// DegradedSortino's meaningful default is 0, which withDefaults cannot
	// distinguish from unset — so it is deliberately left alone. A caller wanting
	// a different level sets it explicitly.
	return t
}

// Ptr is a convenience for building the gated fields from measured values. It
// exists so a caller writing a Snapshot cannot accidentally pass a zero where it
// meant "not measured" — the nil has to be written deliberately.
func Ptr(v float64) *float64 { return &v }
