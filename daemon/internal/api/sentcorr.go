// GET /api/sentiment-correlation — does news text predict returns, and does any
// of it survive controlling for price?
//
// This is the platform's first NON-PRICE signal test. Every predictor tested
// before it (trend, vol, liquidity, gap-fill, 52-week-high, squeeze, drawdown,
// beta) was derived from price or volume, which is why the documented directional
// ceiling applies to all of them at once. Text is genuinely orthogonal input, so
// it is worth testing on its own merits — and worth testing carefully, because
// naive sentiment IC is the easiest fake in the domain: headlines are written
// about moves that already happened.
//
// The payload therefore leads with the PARTIAL information coefficient (sentiment
// vs forward return, after both are residualised on same-session and trailing
// price moves), not the raw correlation. The raw number is included only so the
// gap between the two is visible, because that gap is the measurement of how much
// of the apparent signal was price wearing a costume.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/newssent"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/sentcorr"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func (d Deps) sentimentCorrelation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rows, err := d.St.SentCorrResults(ctx)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "sentiment-correlation: "+err.Error())
		return
	}
	// Studies as raw result objects, so the page renders exactly the numbers the
	// worker measured rather than a re-derivation that could drift from them.
	studies := []map[string]any{}
	for _, row := range rows {
		var res sentcorr.Result
		if json.Unmarshal([]byte(row.Payload), &res) != nil {
			continue
		}
		studies = append(studies, map[string]any{
			"feature": row.Feature,
			"ranAt":   row.Ts,
			"result":  res,
		})
	}

	stats, err := d.St.SentimentFeatureStats(ctx, newssent.Version)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "sentiment-correlation: "+err.Error())
		return
	}

	// Optional per-symbol view: the aligned daily sentiment rows behind the
	// fleet-wide study, for one symbol.
	var symbolRows any = []any{}
	symbolName := ""
	if sym := r.URL.Query().Get("symbol"); sym != "" {
		market := md.Market(r.URL.Query().Get("market"))
		if market == "" {
			market = md.Stocks
		}
		if s, err := d.St.GetSymbol(ctx, strings.ToUpper(strings.TrimSpace(sym)), market); err == nil {
			limit := 60
			if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 400 {
				limit = n
			}
			if fs, err := d.St.SymbolSentimentFeatures(ctx, s.ID, limit); err == nil {
				// [] not null: the page maps over this and "no aligned sentiment
				// rows yet" is the normal state for most symbols.
				if fs == nil {
					fs = []store.SentimentFeature{}
				}
				symbolRows = fs
				symbolName = s.Symbol
			}
		}
	}

	// Catch-up progress, read from the BACKFILL CURSOR rather than from the
	// gap between the archive's first day and the first aligned day.
	//
	// Those two dates are not comparable: a sentiment feature is keyed to the
	// first session on which a headline was already public, so the oldest
	// aligned day legitimately sits after the oldest headline's calendar day. A
	// date subtraction therefore reports a one-day gap that never closes, which
	// is exactly the kind of permanently-almost-done number that teaches people
	// to ignore a progress indicator. The cursor answers the real question:
	// has the backwards sweep reached the oldest scored headline?
	oldestScored, err := d.St.OldestScoredNewsTs(ctx, newssent.Version)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "sentiment-correlation: "+err.Error())
		return
	}
	alignment := map[string]any{
		"archiveFirstDay": stats.NewsFirst,
		"alignedFirstDay": stats.FirstDay,
		"note": "The study can only use sessions that have an aligned sentiment feature. " +
			"Until the backwards feature backfill reaches the oldest scored headline, the " +
			"study is measured on a shorter history than the archive actually holds — which " +
			"is the state that once capped it at ~14 months while the archive reached back " +
			"years. The sentiment-lex-scorer aligns one window per pass.",
	}
	if oldestScored == 0 {
		alignment["caughtUp"] = true
		alignment["note"] = "No headline has been scored yet, so there is nothing to align."
	} else {
		cursor := int64(0)
		if raw, _ := d.St.GetMeta(ctx, pipeline.MetaFeatureBackfillCursor); raw != "" {
			cursor, _ = strconv.ParseInt(raw, 10, 64)
		}
		if cursor == 0 {
			// No cursor written yet: the sweep has not run a pass, so it has
			// reached no further back than the trailing rebuild window.
			alignment["caughtUp"] = false
			alignment["backfillReachedDay"] = ""
		} else {
			alignment["caughtUp"] = cursor <= oldestScored
			alignment["backfillReachedDay"] = time.Unix(cursor, 0).UTC().Format("2006-01-02")
		}
		alignment["archiveOldestScoredDay"] = time.Unix(oldestScored, 0).UTC().Format("2006-01-02")
	}

	writeJSON(w, map[string]any{
		"studies":        studies,
		"alignment":      alignment,
		"horizons":       pipeline.SentCorrHorizons(),
		"coverage":       stats,
		"symbol":         symbolName,
		"symbolFeatures": symbolRows,
		"lexiconVersion": newssent.Version,

		"headlineMetric": "partialIC",
		"methodology": "Each observation is one (symbol, session) pair: the mean lexicon " +
			"polarity of that symbol's headlines, keyed to the FIRST SESSION ON WHICH THEY " +
			"WERE ALREADY PUBLIC, against the forward return measured from that session's " +
			"close. Observations are thinned per symbol so no two forward windows overlap. " +
			"Both sentiment and forward return are residualised on the same-session return " +
			"and the trailing 5-session return, and the reported partial IC is the " +
			"correlation of those residuals. The 95% interval is a bootstrap that resamples " +
			"whole CALENDAR MONTHS, not rows.",
		"whyPartialNotRaw": "Headlines are written about moves that already happened — " +
			"\"shares surge on strong guidance\" is published BECAUSE the price rose. Any " +
			"study that correlates sentiment with forward returns without removing " +
			"contemporaneous and trailing price movement is measuring momentum with extra " +
			"steps, and it will report a confident number. priceExplainedShare is how much " +
			"of the raw correlation turned out to be exactly that.",
		"whyClusteredCI": "Headlines are cross-sectionally correlated — one macro day " +
			"colours every name's news at once — and serially correlated within a symbol. A " +
			"row-level bootstrap assumes away that dependence and returns an interval " +
			"several times too narrow.",
		"scoring": "Deterministic financial lexicon (internal/newssent), version " +
			strconv.Itoa(newssent.Version) + ", NOT the LLM tagger. The LLM reads better per " +
			"headline but is capped at ~2,000 calls/day and is not reproducible, so it cannot " +
			"score years of archive and a prompt change would silently rewrite history. A " +
			"headline containing no polarity words is recorded as HAVING NO SENTIMENT, not as " +
			"sentiment zero, and is excluded from the mean.",
		"gates": "A horizon reports no verdict below " + strconv.Itoa(sentcorr.DefaultConfig(1).MinObs) +
			" independent observations, " + strconv.Itoa(sentcorr.DefaultConfig(1).MinSymbols) +
			" symbols, or " + strconv.Itoa(sentcorr.DefaultConfig(1).MinMonths) + " calendar months of " +
			"coverage. Withheld metrics are null, never zero — \"we cannot tell yet\" and " +
			"\"there is no effect\" are different claims and the payload keeps them apart.",
		"caveat": "EXPERIMENTAL and not a signal. Nothing here is wired into any " +
			"prediction, score or alert, and it will not be unless a partial IC survives with " +
			"an interval excluding zero AND a cost-net quintile spread that is positive. A " +
			"statistically detectable IC of the size text sentiment plausibly carries is a " +
			"ranking input, never a directional call. Headline coverage is also survivorship-" +
			"biased: the universe is currently-tracked names, so companies that were covered " +
			"and then delisted are absent.",
		"expectation": "The prior here is a negative result. Published post-2010 work finds " +
			"headline-sentiment effects that are small, concentrated in the first hours, and " +
			"largely arbitraged in liquid US large caps — which is most of this universe. A " +
			"clean null would still be worth the build: it retires the last live reason to run " +
			"the sentiment pipeline at all.",
		"survivorship": survivorshipBlock(),
	})
}

func (d Deps) registerSentCorr(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sentiment-correlation", d.sentimentCorrelation)
}
