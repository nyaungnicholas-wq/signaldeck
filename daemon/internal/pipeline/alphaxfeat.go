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
	// Since 22 Oct 2026 it and shortVolMinPrior gate only the smart-money
	// scorer, whose inputs feed a registered forward test (smartmoney.go).
	shortIntMaxAgeDays = 20
	// fundingMaxAgeSecs bounds the crypto perp snapshot (worker polls 15m).
	fundingMaxAgeSecs = 24 * 3600
	// wikiZMaxAgeDays / wikiZMinPrior mirror /api/wiki-attention's z gates:
	// latest day ≤3 days old, ≥10 prior days, stddev floor.
	wikiZMaxAgeDays = 3
	wikiZMinPrior   = 10
	// cotMaxAgeDays bounds the COT report date (Tuesday positions published
	// Friday — weekly cadence plus lag; 21d absorbs holiday gaps).
	cotMaxAgeDays = 21
	// cotSPXPattern selects the E-mini S&P 500 legacy report row (curated
	// constant, matching the cot-poller's WantCOT filter — never user input).
	cotSPXPattern = "%E-MINI S&P 500%"
	// insiderWindowSecs bounds the Form 4 open-market lookback for the insider
	// net-buy ratio — mirrors the composite factor's 90-day window.
	insiderWindowSecs = 90 * 86400
	// shortVolMinPrior mirrors /api/shorts' z gate: ≥10 prior days with a
	// non-flat baseline before a short-volume z is computed at all.
	shortVolMinPrior = 10
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
//	cot_spx_net — latest E-mini S&P 500 noncommercial net-long ratio
//	              (long−short)/openInterest from the weekly COT report, ≤21d.
//
// pc_total (Cboe put/call) was removed on 22 Oct 2026: Cboe's terms bar
// storing and deriving from its statistics (gbm.RetiredSourceKey).
//
// Best-effort: any error or stale/absent data → the field is absent.
func alphaMarketFeatures(ctx context.Context, st *store.Store, now time.Time) map[string]float64 {
	out := map[string]float64{}
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
//	funding_rate        — latest Hyperliquid perp funding, ≤24h (crypto
//	                       only). oi_z is deliberately SKIPPED: there is no
//	                       stored OI baseline helper yet, and fabricating a
//	                       z from an ad-hoc window here would not match any
//	                       served number — funding alone carries the signal.
//	wiki_z               — latest day's Wikipedia views standardized vs the
//	                       symbol's own trailing baseline (same gates as
//	                       /api/wiki-attention: ≥10 prior days, stddev
//	                       floor), latest day ≤3 days old.
//
// stocktwits_bull_ratio and tv_reco were removed on 2026-10-21: their sources'
// terms do not clearly permit this use (gbm.RetiredSourceKey). short_int_dtc and
// short_vol_z (FINRA) were removed on 22 Oct 2026: finra.org's terms bar
// building a database and derivative works from its files.
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
		// insider_net_ratio — Form 4 OPEN-MARKET net-buy ratio over 90d:
		// (buys−sells)/(buys+sells) in [-1,1], scale-free so a mega-cap and a
		// small-cap are comparable. Absent when no open-market P/S trades in the
		// window (absence is information). Form 4 filings lag ~2 business days.
		if buys, sells, nBuys, nSells, err := st.InsiderNetActivity(ctx, s.ID, now.Unix()-insiderWindowSecs); err == nil &&
			nBuys+nSells > 0 && buys+sells > 0 {
			out["insider_net_ratio"] = (buys - sells) / (buys + sells)
		}
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

	// tv_webhook_signal retired 2026-10-22: TradingView ToS s.3 bars non-display
	// use of alerts; webhook still stored for history (gbm.RetiredSourceKey).

	if len(out) == 0 {
		return nil
	}
	return out
}
