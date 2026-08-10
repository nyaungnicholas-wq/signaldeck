package pipeline

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/hmmregime"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// HMMCellPrefix namespaces HMM-keyed adaptive cells. Without it, flipping
// SIGNALDECK_HMM_CELLS would silently pool cells learned under two different
// labellers into one name, and no later audit could tell them apart.
const HMMCellPrefix = "hmm:"

// hmmCellsEnabled reports whether the HMM label should KEY the adaptive weight
// cells instead of the rule-based regime label.
//
// Default OFF, deliberately. What is measured is that the HMM is a better
// VOLATILITY labeller (1.59x vs 1.14x next-day absolute-return separation,
// out-of-sample over 600 symbols). What is NOT measured is that keying the
// weight cells by it improves DIRECTIONAL accuracy — a different claim needing
// its own graded evidence. Flipping the key also restarts every cell's
// MinCellSamples/MinCellDays accumulation from zero, so every symbol falls back
// to the "all" cell until ~20 distinct labeled days rebuild under the new key.
//
// The hmm_<label> one-hot goes into the feature vector either way, so the
// evidence needed to justify the switch accrues while this stays off.
func hmmCellsEnabled() bool {
	switch os.Getenv("SIGNALDECK_HMM_CELLS") {
	case "1", "true", "TRUE", "yes":
		return true
	}
	return false
}

// cellKey picks which label names the adaptive weight cell for a symbol.
// Falls back to the rule-based label whenever the HMM has not labelled the
// symbol yet, so enabling the switch can never blank out a cell key.
func cellKey(regimeLbl, hmmLbl string) string {
	if hmmCellsEnabled() && hmmLbl != "" {
		return HMMCellPrefix + hmmLbl
	}
	return regimeLbl
}

// HMMRegimeRunner fits internal/hmmregime per symbol and stores the current
// volatility label.
//
// # Why this is not lookahead
//
// hmmregime separates Fit (full forward-backward, uses the whole series it is
// given) from Filter (causal forward recursion only). Grading a HISTORICAL
// timeline therefore requires fitting on a training prefix — that is what
// cmd/hmmbakeoff does. Here only the LAST bar's label is kept, and every bar
// the fit saw is in that bar's past, so nothing from the future informs it.
// The in-sample fit affects parameter quality, not causality.
//
// The label is stored beside regime_state, never over it. It answers "how big
// is the next move likely to be", which is a different question from the
// directional one the ensemble legs answer, and it is measured: over 600
// symbols out-of-sample it separated next-day absolute return 1.59x
// highest-to-lowest label, against 1.14x for the rule-based labeller and 1.22x
// for a trailing-vol tercile.
type HMMRegimeRunner struct {
	St *store.Store
	// NStates is the fitted state count; 0 means hmmregime.Defaults().
	NStates int
}

func (w *HMMRegimeRunner) Name() string { return "hmm-regime-runner" }

// Interval is deliberately much slower than regime-runner's 30 minutes: this
// reads DAILY bars, and a volatility state that flipped every half hour would
// be noise by construction.
func (w *HMMRegimeRunner) Interval() time.Duration { return 6 * time.Hour }

func (w *HMMRegimeRunner) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	cfg := hmmregime.Defaults()
	if w.NStates > 0 {
		cfg.NStates = w.NStates
	}

	var fitted, thin int
	for _, s := range syms {
		daily, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
		if err != nil {
			return "", err
		}
		m, ok := hmmregime.Fit(daily, cfg)
		if !ok {
			// Too little history to fit. Staying silent is the honest answer;
			// a default label would be a fabricated one.
			thin++
			continue
		}
		pts := m.Filter(daily)
		if len(pts) == 0 {
			thin++
			continue
		}
		last := pts[len(pts)-1]
		sds := m.StateSDs()
		if err := w.St.UpsertHMMRegime(ctx, s.ID, last.Ts, string(last.Label),
			last.Prob, m.NStates(), sds[0], sds[len(sds)-1]); err != nil {
			return "", err
		}
		fitted++
	}
	return fmt.Sprintf("fitted %d symbols (%d too thin to fit)", fitted, thin), nil
}
