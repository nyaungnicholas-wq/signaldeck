// GET /api/market-regimes — the same structural regime engines, read at the
// INDEX and SECTOR level instead of one name at a time.
//
// Nothing here is a new predictor. The trend/vol/liquidity calls for SPY, QQQ,
// IWM, DIA and the eleven SPDR sector funds already exist; they were simply
// buried among ~885 single names, where a market-wide read is invisible. This
// route pulls exactly those symbols out and groups them, so "risk is coming
// off across every cyclical sector" is one glance rather than a sort.
//
// Two honesty properties are deliberate:
//
//   - The accuracy tiers are the SAME measured tiers the per-symbol forecasts
//     carry, and they were measured on a stock universe. An ETF is a basket, so
//     its regimes are mechanically smoother than a single name's — the tier
//     number is inherited, not re-measured, and the payload says so rather than
//     quietly implying an ETF-specific validation that does not exist.
//   - A missing sector is reported as missing. Silence would read as "no
//     regime", which is a claim; "we have no call for XLU" is the truth.
package api

import (
	"net/http"
	"sort"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/universe"
)

// marketRegimeRow is one basket's call for one kind.
type marketRegimeRow struct {
	Symbol     string  `json:"symbol"`
	Name       string  `json:"name"`
	Group      string  `json:"group"` // index | sector
	Kind       string  `json:"kind"`
	Regime     string  `json:"regime"`
	Conviction float64 `json:"conviction"`
	Tier       string  `json:"tier"`
	Accuracy   float64 `json:"historicalAccuracy"`
	Ts         int64   `json:"ts"`
	Horizon    int     `json:"horizonDays"`
}

func (d Deps) marketRegimes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	baskets := universe.MarketETFs()
	nameOf := make(map[string]universe.TapeETF, len(baskets))
	for _, b := range baskets {
		nameOf[b.Symbol] = b
	}

	calls, err := d.St.RegimeForecastCalls(ctx)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "market-regimes: "+err.Error())
		return
	}
	// symbol_id -> symbol, for the baskets only.
	symOf := map[int64]string{}
	for _, b := range baskets {
		if s, err := d.St.GetSymbol(ctx, b.Symbol, md.Stocks); err == nil {
			symOf[s.ID] = s.Symbol
		}
	}

	rows := []marketRegimeRow{}
	covered := map[string]bool{}
	for _, c := range calls {
		sym, ok := symOf[c.SymbolID]
		if !ok {
			continue
		}
		b := nameOf[sym]
		rows = append(rows, marketRegimeRow{
			Symbol: sym, Name: b.Name, Group: b.Kind,
			Kind: string(c.Kind), Regime: c.Regime,
			Conviction: c.Conviction, Tier: convictionTier(c.Conviction),
			Accuracy: c.HistoricalAccuracy, Ts: c.Ts, Horizon: c.HorizonDays,
		})
		covered[sym] = true
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Group != rows[j].Group {
			return rows[i].Group == "index" // indices first
		}
		if rows[i].Symbol != rows[j].Symbol {
			return rows[i].Symbol < rows[j].Symbol
		}
		return rows[i].Kind < rows[j].Kind
	})

	// Baskets with no call at all. Named, because an absent sector silently
	// dropped from a market-wide view is the difference between "neutral" and
	// "unknown", and only one of those is true.
	var uncovered []string
	for _, b := range baskets {
		if !covered[b.Symbol] {
			uncovered = append(uncovered, b.Symbol)
		}
	}
	sort.Strings(uncovered)

	// Breadth: how the sector calls split, per kind. This is the number the
	// page exists to show — one sector being "elevated" is noise, eleven of
	// eleven is a market state.
	breadth := map[string]map[string]int{}
	for _, row := range rows {
		if row.Group != "sector" {
			continue
		}
		if breadth[row.Kind] == nil {
			breadth[row.Kind] = map[string]int{}
		}
		breadth[row.Kind][row.Regime]++
	}

	writeJSON(w, map[string]any{
		"rows":      rows,
		"breadth":   breadth,
		"uncovered": uncovered,
		"sectors":   len(universe.SectorETFs) + countKind(universe.TapeETFs, "sector"),
		"indices":   countKind(universe.TapeETFs, "index"),
		"howToRead": "Each row is the SAME structural call the per-symbol surfaces carry, for an " +
			"index or sector basket. breadth counts how the sector calls split within each kind: a " +
			"single elevated sector is noise, eleven of eleven is a market state.",
		"inheritedAccuracy": "The accuracy attached to each row is the tier measured on the STOCK " +
			"universe, because that is where these predictors were validated. An ETF is a basket and " +
			"its regimes are mechanically smoother than a single name's, so the true ETF-level " +
			"accuracy is probably different — most likely higher for persistence-style calls. It has " +
			"NOT been measured, so the stock number is shown unchanged rather than adjusted by a " +
			"guess or quietly relabelled as an ETF result.",
		"whyThisExists": "These calls already existed; they were just spread across ~885 symbols " +
			"where nothing market-wide was visible. Grouping them adds no new prediction and makes no " +
			"new claim.",
		"tradeability": "A regime call is situational awareness with a measured hit rate, not a " +
			"trade. The 2026-07-24 re-validation found the most ACCURATE trend band carries a negative " +
			"mean forward return, so a high hit rate here is not a weak form of an edge — it is a " +
			"different quantity.",
		"survivorship": survivorshipBlock(),
	})
}

func countKind(etfs []universe.TapeETF, kind string) int {
	n := 0
	for _, e := range etfs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// convictionTier maps a conviction to the band whose measured accuracy applies.
// Bands match the ones /api/regimes publishes, so the two surfaces cannot drift
// into quoting different tiers for the same number.
func convictionTier(c float64) string {
	switch {
	case c >= 0.9:
		return "very-high conviction"
	case c >= 0.8:
		return "high conviction"
	case c >= 0.5:
		return "moderate conviction"
	default:
		return "low conviction"
	}
}

func (d Deps) registerMarketRegimes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/market-regimes", d.marketRegimes)
}
