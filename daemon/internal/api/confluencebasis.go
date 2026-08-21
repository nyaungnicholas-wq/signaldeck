package api

// WHAT A CONFLUENCE "TRADE RETURN" IS.
//
// The scoreboard used to answer one question — direction*rawReturn, cost-netted,
// one row per (symbol, UTC day) — and present the answer as account performance.
// Two things were wrong with that, and they compound.
//
//  1. THE POPULATION. A setup that persisted opened a new row every calendar day
//     and each row was averaged in as an independent bet. RNWWW's single
//     Friday-to-Monday +93.33% move appeared three times. AXTI's -85.66% twice.
//     Fixed at the source (see pipeline/confluence.go), and collapsed here into
//     EPISODES: one bet per continuous same-direction run, its return compounded
//     across the days it was held and its costs charged once.
//
//  2. THE BASIS. An unconstrained normalised short is unbounded below, so CELUW
//     — a warrant that genuinely rose 533% in a day — contributed -533% to a mean
//     labelled "expectancy". That number is real and stays published, under the
//     name RAW. Beside it now sits the CONSTRAINED basis: the same trades under
//     position sizing, short collateral, a risk budget with gap-through, borrow
//     and transaction costs, and a tradable-price floor. Only the constrained
//     number describes an account.
//
// Nothing here clips, winsorises or drops a price. Every refusal is counted and
// published; every basis is named where it is used.

import (
	"math"
	"sort"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/confluence"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// confluenceEpisode is ONE bet: a continuous run of same-direction setups on one
// symbol, held from the first day's entry to the last day's exit.
type confluenceEpisode struct {
	SymbolID  int64
	Symbol    string  `json:"symbol"`
	Market    string  `json:"market"`
	Day       int64   `json:"day"`       // clustering day = the episode's entry day
	Direction int     `json:"direction"` // +1 long, -1 short
	Days      int     `json:"days"`      // sessions the setup persisted
	EntryPx   float64 `json:"entryPx"`
	ExitPx    float64 `json:"exitPx"`
	LowPx     float64 `json:"lowPx"`
	HighPx    float64 `json:"highPx"`
	// RawUnderlying is the compounded move of the instrument across the episode.
	RawUnderlying float64 `json:"rawUnderlying"`
	// RawTrade is direction*RawUnderlying: unconstrained, uncosted, unbounded.
	RawTrade float64 `json:"rawTrade"`
	Warrant  bool    `json:"warrant"`
	// PriceLevels is false when no row of this episode recorded a price level
	// (rows graded before the columns existed). Such an episode has a RAW return
	// but cannot be evaluated on the constrained basis, and is counted as such
	// rather than silently assumed tradable.
	PriceLevels bool `json:"priceLevels"`
}

// buildConfluenceEpisodes collapses graded rows into episodes.
//
// Rows arrive newest-first. An episode is keyed by (symbol, episode_ts); a row
// whose episode_ts was never assigned falls back to its own ts, which makes it a
// singleton — the conservative reading, since the alternative would merge rows
// the classifier never grouped.
func buildConfluenceEpisodes(rows []store.ConfluenceOutcome) []confluenceEpisode {
	type acc struct {
		ep         confluenceEpisode
		firstTs    int64 // earliest row: supplies the entry
		lastTs     int64 // latest row: supplies the exit
		growth     float64
		haveGrowth bool
	}
	byKey := map[[2]int64]*acc{}
	var order [][2]int64

	for _, r := range rows {
		epTs := r.EpisodeTs
		if epTs == 0 {
			epTs = r.Ts
		}
		key := [2]int64{r.SymbolID, epTs}
		a, ok := byKey[key]
		if !ok {
			a = &acc{growth: 1, ep: confluenceEpisode{
				SymbolID: r.SymbolID, Symbol: r.Symbol, Market: r.Market,
				Day: md.SettleDay(r.SettleTs, epTs), Direction: r.Direction,
				Warrant: isWarrantOrUnit(r.Symbol),
			}}
			a.firstTs, a.lastTs = r.Ts, r.Ts
			byKey[key] = a
			order = append(order, key)
		}
		a.ep.Days++
		// Compound the daily moves: a setup held four sessions produced ONE
		// four-session move, not four independent one-session bets.
		a.growth *= 1 + r.FwdReturn
		a.haveGrowth = true

		if r.EntryClose > 0 || r.ExitLow > 0 || r.ExitHigh > 0 {
			a.ep.PriceLevels = true
		}
		if r.Ts <= a.firstTs {
			a.firstTs = r.Ts
			if r.EntryClose > 0 {
				a.ep.EntryPx = r.EntryClose
			} else if r.EntryPx > 0 {
				// Fall back to the call-time audit price. It may sit on a basis a
				// later re-backfill rescaled, which is exactly why entry_close
				// exists; PriceLevels stays false so the constrained basis skips it.
				a.ep.EntryPx = r.EntryPx
			}
		}
		if r.Ts >= a.lastTs {
			a.lastTs = r.Ts
			if r.EntryClose > 0 {
				a.ep.ExitPx = r.EntryClose * (1 + r.FwdReturn)
			}
		}
		if r.ExitLow > 0 && (a.ep.LowPx == 0 || r.ExitLow < a.ep.LowPx) {
			a.ep.LowPx = r.ExitLow
		}
		if r.ExitHigh > a.ep.HighPx {
			a.ep.HighPx = r.ExitHigh
		}
	}

	out := make([]confluenceEpisode, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		if !a.haveGrowth {
			continue
		}
		a.ep.RawUnderlying = a.growth - 1
		a.ep.RawTrade = float64(a.ep.Direction) * a.ep.RawUnderlying
		out = append(out, a.ep)
	}
	return out
}

// isWarrantOrUnit flags instruments whose book is too thin and whose spread too
// wide for the ordinary cost model, and whose shortability is least likely to be
// real.
//
// It is a TICKER HEURISTIC and is labelled as one wherever it is published: US
// warrants carry a trailing W (a 5-letter root plus W, or WW on a 4-letter root)
// and SPAC units a trailing U or a ".U" suffix. It cannot distinguish those from
// an ordinary 5-letter ticker that happens to end in W, so it will over-flag. The
// direction of that error is conservative — it widens assumed costs rather than
// narrowing them.
func isWarrantOrUnit(symbol string) bool {
	s := strings.ToUpper(strings.TrimSpace(symbol))
	if s == "" {
		return false
	}
	if strings.HasSuffix(s, ".U") || strings.HasSuffix(s, ".WS") {
		return true
	}
	if len(s) >= 5 && (strings.HasSuffix(s, "W") || strings.HasSuffix(s, "U")) {
		return true
	}
	return false
}

// confluenceConstrained is the account-level view of one book.
type confluenceConstrained struct {
	// Trades is how many episodes could be evaluated on this basis.
	Trades int `json:"trades"`
	// MeanAccountContribution is the average per-episode contribution to ACCOUNT
	// return. This is the number that may be called performance.
	MeanAccountContribution float64 `json:"meanAccountContribution"`
	// MeanCapitalAtRisk is the average return on the capital each position tied
	// up — the right number for sizing questions, not for account performance.
	MeanCapitalAtRisk float64 `json:"meanCapitalAtRisk"`
	// TotalAccountReturn sums the contributions after per-day exposure caps.
	TotalAccountReturn float64 `json:"totalAccountReturn"`
	// DaysScaled counts days whose gross or net exposure hit a cap and had to be
	// scaled down. A book that never says this is claiming leverage it never had.
	DaysScaled int `json:"daysScaled"`
	// Refusals: episodes the account could not have taken, and why.
	Untradable      int            `json:"untradable"`
	UntradablePct   float64        `json:"untradablePct"`
	UntradableWhy   map[string]int `json:"untradableWhy"`
	Liquidated      int            `json:"liquidated"`
	GappedThrough   int            `json:"gappedThrough"`
	NoPriceLevels   int            `json:"noPriceLevels"`
	ShortUnverified int            `json:"shortUnverified"`
	ShortTotal      int            `json:"shortTotal"`
	// ShortUnverifiedPct is the share of the SHORT book whose borrow could not be
	// verified. This system holds no historical borrow file, so this is expected
	// to be 100% and is published rather than assumed away.
	ShortUnverifiedPct float64 `json:"shortUnverifiedPct"`
}

// constrainedNote states, in the payload, what the constrained basis assumes.
const constrainedNote = "CONSTRAINED basis: every episode is sized at the model's position weight, shorts post collateral " +
	"and pay borrow, each position carries a risk budget that liquidates it (at the gap price when the move gapped through " +
	"the stop), both sides pay cost and slippage, and instruments below the tradable price floor are refused outright. " +
	"Per-day gross and net exposure caps are applied to the book. NO return is capped, clipped or winsorised: a refused " +
	"trade keeps its raw number and is counted under untradableWhy."

// shortabilityNote is published beside every short number.
const shortabilityNote = "Shortability is UNVERIFIED for this whole record: no historical borrow file exists in this system. " +
	"The short book is therefore computed as if borrow was available, and shortUnverifiedPct says how much of it rests on " +
	"that assumption. It is not evidence that these shorts were executable."

// gradeConstrained evaluates a set of episodes under the constraint model and
// returns the account-level book plus the per-episode results (for concentration
// and worst-trade reporting).
func gradeConstrained(eps []confluenceEpisode, c confluence.Constraints) (confluenceConstrained, []confluence.Result) {
	out := confluenceConstrained{UntradableWhy: map[string]int{}}
	results := make([]confluence.Result, len(eps))
	byDay := map[int64][]confluence.Result{}

	var sumAcct, sumCaR float64
	for i, e := range eps {
		if !e.PriceLevels || e.EntryPx <= 0 || e.ExitPx <= 0 {
			out.NoPriceLevels++
			results[i] = confluence.Result{
				Direction: e.Direction, RawUnderlying: e.RawUnderlying, RawTrade: e.RawTrade,
				Reason: "no recorded price level — graded before the basis columns existed",
			}
			if e.Direction < 0 {
				out.ShortTotal++
				out.ShortUnverified++
			}
			continue
		}
		days := e.Days
		if days < 1 {
			days = 1
		}
		cc := c
		cc.HoldingDays = float64(days)
		r := confluence.Evaluate(confluence.Trade{
			Direction: e.Direction, EntryPx: e.EntryPx, ExitPx: e.ExitPx,
			LowPx: e.LowPx, HighPx: e.HighPx,
			Short: confluence.ShortUnknown, Warrant: e.Warrant,
		}, cc)
		results[i] = r

		if e.Direction < 0 {
			out.ShortTotal++
			out.ShortUnverified++ // no borrow file: every short is unverified
		}
		if !r.Tradable {
			out.Untradable++
			out.UntradableWhy[r.Reason]++
			continue
		}
		out.Trades++
		sumAcct += r.Constrained
		sumCaR += r.CapitalAtRisk
		if r.Liquidated {
			out.Liquidated++
		}
		if r.GappedThrough {
			out.GappedThrough++
		}
		byDay[e.Day] = append(byDay[e.Day], r)
	}

	if n := len(eps); n > 0 {
		out.UntradablePct = float64(out.Untradable) / float64(n)
	}
	if out.ShortTotal > 0 {
		out.ShortUnverifiedPct = float64(out.ShortUnverified) / float64(out.ShortTotal)
	}
	if out.Trades > 0 {
		out.MeanAccountContribution = sumAcct / float64(out.Trades)
		out.MeanCapitalAtRisk = sumCaR / float64(out.Trades)
	}
	// Exposure caps bind per DAY: an account cannot hold every setup a busy day
	// flags at full weight.
	for _, rs := range byDay {
		acct, _, _, scaled := confluence.Aggregate(rs, c)
		out.TotalAccountReturn += acct
		if scaled {
			out.DaysScaled++
		}
	}
	return out, results
}

// confluenceQuantiles reports the shape of a return distribution, which a mean
// alone hides — this record's mean is routinely one day's move.
type confluenceQuantiles struct {
	Min float64 `json:"min"`
	P05 float64 `json:"p05"`
	P25 float64 `json:"p25"`
	P50 float64 `json:"p50"`
	P75 float64 `json:"p75"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

// quantilesOf computes order statistics by nearest-rank on a sorted copy.
func quantilesOf(xs []float64) *confluenceQuantiles {
	if len(xs) == 0 {
		return nil
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	at := func(p float64) float64 {
		i := int(math.Round(p * float64(len(s)-1)))
		if i < 0 {
			i = 0
		}
		if i >= len(s) {
			i = len(s) - 1
		}
		return s[i]
	}
	return &confluenceQuantiles{
		Min: s[0], P05: at(0.05), P25: at(0.25), P50: at(0.50),
		P75: at(0.75), P95: at(0.95), Max: s[len(s)-1],
	}
}

// confluenceShare is one contributor's share of the book.
type confluenceShare struct {
	Key    string  `json:"key"`
	Trades int     `json:"trades"`
	Sum    float64 `json:"sum"`
	Share  float64 `json:"share"` // share of TOTAL ABSOLUTE contribution
}

// concentrationBy ranks contributors by their share of the book's total absolute
// movement. A mean carried by one name is not an expectancy, and this is where a
// reader sees that.
func concentrationBy(keys []string, vals []float64, top int) []confluenceShare {
	type agg struct {
		n   int
		sum float64
		abs float64
	}
	m := map[string]*agg{}
	var totalAbs float64
	for i := range keys {
		a := m[keys[i]]
		if a == nil {
			a = &agg{}
			m[keys[i]] = a
		}
		a.n++
		a.sum += vals[i]
		a.abs += math.Abs(vals[i])
		totalAbs += math.Abs(vals[i])
	}
	out := make([]confluenceShare, 0, len(m))
	for k, a := range m {
		s := confluenceShare{Key: k, Trades: a.n, Sum: a.sum}
		if totalAbs > 0 {
			s.Share = a.abs / totalAbs
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Share != out[j].Share {
			return out[i].Share > out[j].Share
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > top {
		out = out[:top]
	}
	return out
}

// worstConfluenceTrade is the single worst episode with its full price path, so
// the reader can check the extreme rather than take the summary's word for it.
type worstConfluenceTrade struct {
	Episode    confluenceEpisode `json:"episode"`
	Instrument string            `json:"instrument"`
	Constraint confluence.Result `json:"constrained"`
}

// worstOf finds the episode with the lowest RAW trade return.
func worstOf(eps []confluenceEpisode, rs []confluence.Result) *worstConfluenceTrade {
	if len(eps) == 0 {
		return nil
	}
	worst := 0
	for i := range eps {
		if eps[i].RawTrade < eps[worst].RawTrade {
			worst = i
		}
	}
	inst := "common stock (inferred from ticker)"
	if eps[worst].Warrant {
		inst = "warrant or unit (inferred from ticker suffix)"
	}
	w := &worstConfluenceTrade{Episode: eps[worst], Instrument: inst}
	if worst < len(rs) {
		w.Constraint = rs[worst]
	}
	return w
}
