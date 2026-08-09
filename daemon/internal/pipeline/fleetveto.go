// The fleet veto, measured the way the leg is actually used.
//
// A leg ranks symbols AGAINST EACH OTHER on a given day. The veto used to read
// store.FleetLegAUC — an n-weighted mean of PER-SYMBOL AUCs, each estimated over
// that symbol's own ~30 resolved days — which answers a different question
// ("does this symbol's score predict its own moves over time") and answers it
// with a statistic whose standard error is near 0.1, averaged as though it were
// precise.
//
// Measured on the live 1d pressure record 2026-08-08 the two disagree enough to
// change the verdict:
//
//	n-weighted mean of per-symbol AUCs   0.3614   -> vetoed outright
//	day-clustered within-day AUC         0.4636   95% CI [0.4113, 0.5158]
//
// The interval contains chance. The leg is not backwards, it is uninformative,
// and benching it for being "strongly backwards" was an artifact of the
// estimator rather than a property of the leg.
package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// fleetVetoWindow is how far back the within-day AUCs are measured. Wide enough
// to clear clusterstat.MinDaysForInterval with room for non-trading days.
const fleetVetoWindow = 45 * 24 * time.Hour

// legComponents is the subset of the frozen components JSON the veto reads. Only
// the legs that carry a directional score are listed; a leg absent from a row is
// simply not part of that day's cross-section.
type legComponents struct {
	PressureScore     *float64 `json:"PressureScore"`
	ExpectancyHitRate *float64 `json:"ExpectancyHitRate"`
	ForecastProb      *float64 `json:"ForecastProb"`
	SentimentScore    *float64 `json:"SentimentScore"`
	GBMProb           *float64 `json:"GBMProb"`
	MeanRevProb       *float64 `json:"MeanRevProb"`
	AlphaXProb        *float64 `json:"AlphaXProb"`
}

func (c legComponents) value(leg string) (float64, bool) {
	var p *float64
	switch leg {
	case ensemble.LegPressure:
		p = c.PressureScore
	case ensemble.LegExpectancy:
		p = c.ExpectancyHitRate
	case ensemble.LegForecast:
		p = c.ForecastProb
	case ensemble.LegSentiment:
		p = c.SentimentScore
	case ensemble.LegGBM:
		p = c.GBMProb
	case ensemble.LegMeanRev:
		p = c.MeanRevProb
	case ensemble.LegAlphaX:
		p = c.AlphaXProb
	}
	if p == nil {
		return 0, false
	}
	return *p, true
}

// fleetVetoes returns the set of "leg|horizon" keys the fleet demotes, measured
// as the day-clustered within-day cross-sectional AUC.
//
// A key is present ONLY when the leg is measurably not better than chance — the
// whole interval at or below 0.5. A leg with too few days, or one whose interval
// straddles chance, is absent and falls through to the per-symbol rank edge,
// which carries its own Wilson lower bound. That keeps the veto one-directional:
// a good fleet never promotes a bad symbol, only a demonstrably bad fleet
// demotes a good one.
func fleetVetoes(ctx context.Context, st *store.Store, horizons []md.Horizon) map[string]bool {
	out := map[string]bool{}
	since := time.Now().Add(-fleetVetoWindow)
	for _, h := range horizons {
		obs, err := st.LegDailyObservations(ctx, string(h), since)
		if err != nil {
			slog.Warn("fleet veto: cross-section unreadable, no leg vetoed this pass",
				"horizon", h, "err", err)
			continue
		}
		// day -> leg -> (scores, outcomes)
		type xs struct{ preds, ups []float64 }
		byDay := map[string]map[string]*xs{}
		for _, o := range obs {
			var c legComponents
			if json.Unmarshal([]byte(o.Components), &c) != nil {
				continue
			}
			for _, leg := range ensemble.LegNames {
				v, ok := c.value(leg)
				if !ok {
					continue
				}
				if byDay[o.Day] == nil {
					byDay[o.Day] = map[string]*xs{}
				}
				if byDay[o.Day][leg] == nil {
					byDay[o.Day][leg] = &xs{}
				}
				e := byDay[o.Day][leg]
				e.preds = append(e.preds, v)
				e.ups = append(e.ups, float64(o.Up))
			}
		}
		// One AUC per leg per day, then the clustered verdict.
		daily := map[string][]float64{}
		for _, legs := range byDay {
			for leg, e := range legs {
				// A day whose cross-section is too thin to rank says nothing
				// about ranking skill; including it would just add noise.
				if len(e.preds) < minCrossSectionForAUC {
					continue
				}
				daily[leg] = append(daily[leg], clusterstat.RankAUC(e.preds, e.ups))
			}
		}
		for leg, vals := range daily {
			veto, mean, measured := clusterstat.VetoOnDayClusteredAUC(vals)
			if !measured {
				continue
			}
			if veto {
				out[leg+"|"+string(h)] = true
				slog.Warn("fleet veto: leg benched on day-clustered cross-sectional AUC",
					"leg", leg, "horizon", h, "meanDailyAUC", mean, "days", len(vals))
			}
		}
	}
	return out
}

// minCrossSectionForAUC is the smallest same-day cross-section worth ranking.
// Below it an AUC is dominated by which handful of symbols happened to report.
const minCrossSectionForAUC = 30

// settleBackfillBatch bounds the per-pass settle_ts backfill. Large enough that
// the live tables converge in a couple of hours of ordinary passes, small enough
// that no single pass is delayed by it.
//
// Raised 20k -> 200k on 2026-08-08 when the key was extended to score_outcomes.
// The old value was sized for the ~500k-row prediction_outcomes table;
// score_outcomes is 2.2M rows, where 20k/pass is a ~18-hour drain and the fold
// falls back to the calendar day for every row still waiting.
//
// MEASURED on a copy of the live DB rather than guessed, because this holds the
// single SQLite write lock and a busy fleet is behind it: the correlated bar
// lookup is free (200k in 0.14s, indexed) and the UPDATE is linear at ~2.9us/row
// — 200k in 0.617s, 500k in 1.438s. 200k buys the 10x drain (18h -> ~2h) while
// keeping the lock hold under a second. 500k was rejected: it saves ~70min on a
// ONE-TIME drain for 2.3x the lock hold, and after convergence this costs
// nothing either way.
const settleBackfillBatch = 200000
