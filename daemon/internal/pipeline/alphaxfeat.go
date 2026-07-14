// CROSS-SECTIONAL ALPHA wave — featureVersion 5 NEW-SOURCE FEATURES.
//
// This file turns the DATA-EXPANSION wave's stored external context (short
// interest, crypto perp funding, StockTwits sentiment, Wikipedia attention,
// TradingView ratings, CBOE put/call, CFTC COT) into per-prediction feature
// fields, following the exact v3→v4 pattern (news_vol_z): every field is
// GATE-HONORING and FRESHNESS-BOUNDED, and an unavailable/stale/thin source
// means the field is simply ABSENT from the vector — absence is information,
// not zero. All reads are best-effort: a store error only means the field is
// absent from this pass's vector, never a failed prediction.
//
// HONESTY: these fields feed the LABELED TRAINING SET only. Whether any of
// them ever influences a live output is decided downstream by measured OOS
// lift gates (per-symbol GBM leg; pooled alphax model) — a new data source
// earns its way into the blend, it is never granted entry.
package pipeline

import (
	"context"
	"math"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Freshness/thinness gates for the v5 fields. Stale context is absent, never
// silently carried forward.
const (
	// shortIntMaxAgeDays bounds the FINRA settlement date (bi-monthly data
	// published ~2wks lagged — beyond 20d the positioning is a stale echo).
	shortIntMaxAgeDays = 20
	// fundingMaxAgeSecs bounds the crypto perp snapshot (worker polls 15m).
	fundingMaxAgeSecs = 24 * 3600
	// stBullMaxAgeSecs / stBullMinTotal gate the StockTwits snapshot: ≤1 day
	// old and resting on ≥10 tallied messages (a 3-message "crowd" is noise).
	stBullMaxAgeSecs = 24 * 3600
	stBullMinTotal   = 10
	// wikiZMaxAgeDays / wikiZMinPrior mirror /api/wiki-attention's z gates:
	// latest day ≤3 days old, ≥10 prior days, stddev floor.
	wikiZMaxAgeDays = 3
	wikiZMinPrior   = 10
	// tvRecoMaxAgeSecs bounds the TradingView scanner rating (worker: 15m).
	tvRecoMaxAgeSecs = 24 * 3600
	// pcTotalMaxAgeDays bounds the CBOE put/call trade date (daily publish).
	pcTotalMaxAgeDays = 3
	// cotMaxAgeDays bounds the COT report date (Tuesday positions published
	// Friday — weekly cadence plus lag; 21d absorbs holiday gaps).
	cotMaxAgeDays = 21
	// cotSPXPattern selects the E-mini S&P 500 legacy report row (curated
	// constant, matching the cot-poller's WantCOT filter — never user input).
	cotSPXPattern = "%E-MINI S&P 500%"
)

// trailingZ standardizes the LAST value of a series against the mean/stddev
// of all PRIOR values — the identical math (min prior count, stddev floor) as
// the API's shortsZ / the wiki-attention endpoint, kept local because that
// helper lives in the api package. ok=false below the gate.
func trailingZ(vals []float64, minPrior int) (z float64, ok bool) {
	if len(vals) < minPrior+1 {
		return 0, false
	}
	prior := vals[:len(vals)-1]
	mean := 0.0
	for _, v := range prior {
		mean += v
	}
	mean /= float64(len(prior))
	varSum := 0.0
	for _, v := range prior {
		varSum += (v - mean) * (v - mean)
	}
	sd := math.Sqrt(varSum / float64(len(prior)))
	if sd < 1e-9 {
		return 0, false
	}
	return (vals[len(vals)-1] - mean) / sd, true
}

// dayWithinUTC reports whether a YYYY-MM-DD day string parses and falls
// within maxAgeDays of `now` (UTC calendar arithmetic).
func dayWithinUTC(day string, now time.Time, maxAgeDays int) bool {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return false
	}
	cutoff := now.UTC().AddDate(0, 0, -maxAgeDays)
	return !d.Before(time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, time.UTC))
}

// alphaMarketFeatures assembles the MARKET-WIDE v5 fields, loaded ONCE per
// predictor pass (identical for every symbol at this instant — the vix_*
// precedent):
//
//	pc_total    — latest CBOE market-wide total put/call ratio, ≤3 days old.
//	cot_spx_net — latest E-mini S&P 500 noncommercial net-long ratio
//	              (long−short)/openInterest from the weekly COT report, ≤21d.
//
// Best-effort: any error or stale/absent data → the field is absent.
func alphaMarketFeatures(ctx context.Context, st *store.Store, now time.Time) map[string]float64 {
	out := map[string]float64{}
	if series, err := st.CboePCSeries(ctx, 7); err == nil && len(series) > 0 {
		latest := series[len(series)-1]
		if dayWithinUTC(latest.Day, now, pcTotalMaxAgeDays) {
			out["pc_total"] = latest.TotalPC
		}
	}
	if cot, ok, err := st.LatestCOTByContract(ctx, cotSPXPattern); err == nil && ok &&
		cot.OpenInterest > 0 && dayWithinUTC(cot.ReportDate, now, cotMaxAgeDays) {
		out["cot_spx_net"] = (cot.NoncommLong - cot.NoncommShort) / cot.OpenInterest
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// alphaSymbolFeatures assembles the PER-SYMBOL v5 fields (shared across
// horizons, loaded once per symbol per pass):
//
//	short_int_dtc        — latest FINRA days-to-cover, settlement ≤20d old
//	                       (stocks; the table only holds stocks anyway).
//	funding_rate         — latest Hyperliquid perp funding, ≤24h (crypto
//	                       only). oi_z is deliberately SKIPPED: there is no
//	                       stored OI baseline helper yet, and fabricating a
//	                       z from an ad-hoc window here would not match any
//	                       served number — funding alone carries the signal.
//	stocktwits_bull_ratio — bullish/(bullish+bearish) from the latest page
//	                       snapshot, total ≥10 and ≤1 day old.
//	wiki_z               — latest day's Wikipedia views standardized vs the
//	                       symbol's own trailing baseline (same gates as
//	                       /api/wiki-attention: ≥10 prior days, stddev
//	                       floor), latest day ≤3 days old.
//	tv_reco              — TradingView's latest reco_all rating, ≤1 day old
//	                       (an EXTERNAL descriptive rating — the model may
//	                       learn from it; it is never OUR output).
//
// Best-effort: any error or gate failure → that field is absent.
func alphaSymbolFeatures(ctx context.Context, st *store.Store, s md.Symbol, now time.Time) map[string]float64 {
	out := map[string]float64{}

	if s.Market == md.Crypto {
		if p, ok, err := st.LatestCryptoPerp(ctx, s.ID); err == nil && ok &&
			now.Unix()-p.Ts <= fundingMaxAgeSecs {
			out["funding_rate"] = p.Funding
		}
	} else {
		if rows, err := st.ShortInterestRecent(ctx, s.ID, 1); err == nil && len(rows) > 0 &&
			rows[0].DaysToCover > 0 && dayWithinUTC(rows[0].Settlement, now, shortIntMaxAgeDays) {
			out["short_int_dtc"] = rows[0].DaysToCover
		}
	}

	if st2, ok, err := st.LatestStocktwits(ctx, s.ID); err == nil && ok &&
		now.Unix()-st2.Ts <= stBullMaxAgeSecs && st2.Total >= stBullMinTotal &&
		st2.Bullish+st2.Bearish > 0 {
		out["stocktwits_bull_ratio"] = float64(st2.Bullish) / float64(st2.Bullish+st2.Bearish)
	}

	if series, err := st.WikiViewsSeries(ctx, s.ID, 40); err == nil && len(series) > 0 &&
		dayWithinUTC(series[len(series)-1].Day, now, wikiZMaxAgeDays) {
		views := make([]float64, len(series))
		for i, p := range series {
			views[i] = float64(p.Views)
		}
		if z, ok := trailingZ(views, wikiZMinPrior); ok {
			out["wiki_z"] = z
		}
	}

	if r, ok := st.LatestTVRating(ctx, s.ID); ok && now.Unix()-r.Ts <= tvRecoMaxAgeSecs {
		out["tv_reco"] = r.RecoAll
	}

	if len(out) == 0 {
		return nil
	}
	return out
}
