// Seed claims — real numbers from the system's own published record, encoded
// as executable examples of the engine's rules. Every value below is copied
// from data/accuracy_registry.json (generated 2026-07-26 by
// tools/accuracy_registry.py, day-clustered per audit finding A2) or from
// audits/2026-07-26-reaudit.md. Nothing here is invented, and every claim is
// marked Seeded so no surface can mistake a seed for a live measurement.
//
// The four seeds deliberately cover the engine's whole state space:
// two REFUTED claims (the retired 1d/1w direction models — the intervals sit
// entirely below the majority-class baseline) and two WEAK, expiry-bearing
// claims (the structural predictors, backtested but not yet graded live;
// their revalidate_by is 2026-08-07, the registry's own first grading date,
// so if grading slips, the sweep degrades them instead of letting a
// backtest-only number keep its standing).
package evidence

import (
	"context"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func f(v float64) *float64 { return &v }

// seedRevalidate is 2026-08-07 00:00 UTC — the accuracy registry's published
// first-grade date for the structural predictors.
var seedRevalidate = time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC).Unix()

// seedValidated is the registry generation date backing these numbers.
var seedValidated = time.Date(2026, 7, 26, 14, 5, 2, 0, time.UTC).Unix()

// SeedClaims returns the built-in claims. Exported so tests can validate the
// examples the engine ships.
func SeedClaims() []Claim {
	reg := "data/accuracy_registry.json (2026-07-26T14:05:02)"
	audit := "audits/2026-07-26-reaudit.md"
	return []Claim{
		{
			ID:   "directional-ensemble-1d",
			Text: "SEEDED: the directional ensemble predicts 1d direction better than the majority-class baseline. REFUTED — live accuracy 48.12% vs 54.58% baseline; the day-clustered interval sits entirely below the null. Model retired.",
			Scope: Scope{
				Assets: []string{"stocks", "crypto"}, Regimes: []string{"all"},
				Horizons: []string{"1d"}, DateFrom: "2026-07-01", DateTo: "2026-07-26",
			},
			Items: []Item{{
				Kind:       "live-record",
				Value:      0.48123755552151937,
				NEffective: 886.83, // raw n 13,058 / design effect 14.72 (23 distinct days)
				Method:     "live-forward, day-clustered Wilson",
				Correction: "day-clustered design effect",
				CILow:      f(0.44850426537195), CIHigh: f(0.5141326956984466),
				Baseline:  f(0.5458722622147343),
				SourceRef: reg + "; " + audit + " (48.1% headline independently reproduced)",
			}},
			Tier: TierRefuted, Status: StatusRetired,
			LastValidated: seedValidated, RevalidateBy: seedRevalidate,
			Lineage: Lineage{Models: []string{"directional-ensemble"}},
			Seeded:  true,
		},
		{
			ID:   "directional-ensemble-1w",
			Text: "SEEDED: the directional ensemble predicts 1w direction better than the majority-class baseline. REFUTED — live accuracy 46.24% vs 54.44% baseline; day-clustered interval entirely below the null.",
			Scope: Scope{
				Assets: []string{"stocks", "crypto"}, Regimes: []string{"all"},
				Horizons: []string{"1w"}, DateFrom: "2026-07-01", DateTo: "2026-07-26",
			},
			Items: []Item{{
				Kind:       "live-record",
				Value:      0.46235268441728505,
				NEffective: 605.86, // raw n 9,164 / design effect 15.13 (15 distinct days)
				Method:     "live-forward, day-clustered Wilson",
				Correction: "day-clustered design effect",
				CILow:      f(0.42301325294035697), CIHigh: f(0.502166527521974),
				Baseline:  f(0.5444129201222174),
				SourceRef: reg,
			}},
			Tier: TierRefuted, Status: StatusRetired,
			LastValidated: seedValidated, RevalidateBy: seedRevalidate,
			Lineage: Lineage{Models: []string{"directional-ensemble"}},
			Seeded:  true,
		},
		{
			ID:   "trend21-structural",
			Text: "SEEDED: the trend21 structural regime persists at ~82% (backtest walk-forward, stocks). BACKTEST-ONLY — no live grade yet; the registry's first grade is 2026-08-07, and this claim expires then. n_effective is held to the 9 distinct call-days of recorded live forecasts (audit A2), not the 3,672 forecast rows.",
			Scope: Scope{
				Assets: []string{"stocks"}, Regimes: []string{"all"},
				Horizons: []string{"21d"}, DateFrom: "2026-07-17", DateTo: "2026-07-26",
			},
			Items: []Item{{
				Kind:       "backtest",
				Value:      0.8204754901960785,
				NEffective: 9, // 3,672 recorded forecasts, 869 symbols, but only 9 distinct call-days
				Method:     "walk-forward backtest; live grading pending",
				SourceRef:  reg + " (trend21, PENDING); " + audit + " (A2: 3,672/869/9)",
			}},
			Tier: TierWeak, Status: StatusActive,
			LastValidated: seedValidated, RevalidateBy: seedRevalidate,
			Lineage: Lineage{FeatureKeys: []string{"trend21"}, Models: []string{"structural-regime"}},
			Seeded:  true,
		},
		{
			ID:   "liquidity21-structural",
			Text: "SEEDED: the liquidity21 structural regime persists at ~71% (backtest walk-forward, stocks). BACKTEST-ONLY — no live grade yet; expires at the registry's first grade date 2026-08-07.",
			Scope: Scope{
				Assets: []string{"stocks"}, Regimes: []string{"all"},
				Horizons: []string{"21d"}, DateFrom: "2026-07-17", DateTo: "2026-07-26",
			},
			Items: []Item{{
				Kind:       "backtest",
				Value:      0.7127556287753981,
				NEffective: 9, // 3,642 recorded forecasts over the same ~9 call-days as trend21
				Method:     "walk-forward backtest; live grading pending",
				SourceRef:  reg + " (liquidity21, PENDING)",
			}},
			Tier: TierWeak, Status: StatusActive,
			LastValidated: seedValidated, RevalidateBy: seedRevalidate,
			Lineage: Lineage{FeatureKeys: []string{"liquidity21"}, Models: []string{"structural-regime"}},
			Seeded:  true,
		},
	}
}

// EnsureSeeds inserts any seed claim not already present. It NEVER overwrites
// an existing row: once a seed is in the database it belongs to the sweep and
// to future re-validations, and re-seeding must not resurrect a downgrade.
// Returns how many were inserted.
func EnsureSeeds(ctx context.Context, st *store.Store) (int, error) {
	n := 0
	for _, c := range SeedClaims() {
		exists, err := st.EvidenceClaimExists(ctx, c.ID)
		if err != nil {
			return n, err
		}
		if exists {
			continue
		}
		if err := Put(ctx, st, c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
