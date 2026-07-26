// CONFLUENCE GATE + MONEY SCOREBOARD wave API: the per-symbol confluence setup,
// the leaderboard of current setups, and the accruing MONEY scoreboard over
// FORWARD-TRACKED, resolved setups. Read-only, gated like every other read.
//
// HONESTY, carried VERBATIM in every payload (confluenceCaveat): confluence
// manufactures no edge — it is a strict AND over INDEPENDENT signal families,
// shown transparently (every family's vote), forward-tracked with no lookahead,
// and scored by EXPECTED PROFIT (expectancy / profit factor), not win rate. The
// track stays gated (money fields null) until enough INDEPENDENT resolutions
// exist — a thin sample can show any expectancy by luck.
package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/confluence"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/moneymetrics"
)

// confluenceCaveat ships VERBATIM with every payload and every UI surface.
const confluenceCaveat = "Confluence = agreement across independent signals; this is a FORWARD-tracked record with no lookahead. Trades less often, aims for higher-quality setups — not a guarantee."

// Track gate: money numbers stay withheld below these floors. Independent obs
// (one per symbol+day) AND distinct days must BOTH clear — cross-sectional obs
// on one day share one market move, so day-count is the binding time measure.
const (
	confluenceMinIndependent = 30
	confluenceMinDays        = 10
	// confluenceCostPerSide is the per-side round-trip cost netted from each
	// setup's realized move before scoring profit (round trip = 2×). Explicit,
	// never hidden.
	confluenceCostPerSide = 0.001
	// confluenceTrackWindow bounds how many resolved outcomes the scoreboard
	// reads (generous — the record is far from this today).
	confluenceTrackWindow = 50000
)

// confluenceSymbol resolves ?symbol= (with optional &market=) to a tracked
// symbol. A bare ticker tries stocks then crypto (mirrors smartMoneySymbol).
// given=false means no symbol was supplied.
func (d Deps) confluenceSymbol(r *http.Request) (sym md.Symbol, given bool, err error) {
	s := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	if s == "" {
		return md.Symbol{}, false, nil
	}
	if m := md.Market(r.URL.Query().Get("market")); m == md.Crypto || m == md.Stocks {
		row, err := d.St.GetSymbol(r.Context(), s, m)
		return row, true, err
	}
	if row, err := d.St.GetSymbol(r.Context(), s, md.Stocks); err == nil {
		return row, true, nil
	}
	row, err := d.St.GetSymbol(r.Context(), s, md.Crypto)
	return row, true, err
}

// confluence serves one symbol's transparent confluence assessment.
// GET /api/confluence?symbol=[&market=]
func (d Deps) confluence(w http.ResponseWriter, r *http.Request) {
	s, given, err := d.confluenceSymbol(r)
	if !given {
		httpErr(w, 400, "need symbol=")
		return
	}
	if err != nil {
		httpErr(w, 404, "unknown symbol — confluence is universe-scoped to tracked symbols")
		return
	}
	row, ok, err := d.St.LatestConfluenceSetup(r.Context(), s.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeJSON(w, map[string]any{
			"available": false,
			"symbol":    s.Symbol,
			"market":    s.Market,
			"reason":    "no confluence assessment for this symbol yet — the scorer runs every 30m",
			"caveat":    confluenceCaveat,
		})
		return
	}
	var setup confluence.Setup
	if err := json.Unmarshal([]byte(row.Payload), &setup); err != nil {
		httpErr(w, 500, "stored payload does not parse: "+err.Error())
		return
	}
	votes := setup.Votes
	if votes == nil {
		votes = []confluence.Vote{}
	}
	writeJSON(w, map[string]any{
		"available": true,
		"symbol":    s.Symbol,
		"market":    s.Market,
		"ts":        row.Ts,
		"direction": setup.Direction,
		"isSetup":   setup.IsSetup,
		"agree":     setup.Agree,
		"dissent":   setup.Dissent,
		"present":   setup.Present,
		"score":     setup.Score,
		"votes":     votes,
		"caveat":    confluenceCaveat,
	})
}

// confluenceTopRow is one leaderboard row.
type confluenceTopRow struct {
	Symbol    string  `json:"symbol"`
	Market    string  `json:"market"`
	Direction int     `json:"direction"`
	Agree     int     `json:"agree"`
	Score     float64 `json:"score"`
	IsSetup   bool    `json:"isSetup"`
	Ts        int64   `json:"ts"`
}

// confluenceTop serves the ranked leaderboard (flagged setups + strongest
// agreement first). GET /api/confluence/top?market=&limit=&onlySetups=
func (d Deps) confluenceTop(w http.ResponseWriter, r *http.Request) {
	market := ""
	if m := md.Market(r.URL.Query().Get("market")); m == md.Crypto || m == md.Stocks {
		market = string(m)
	}
	onlySetups := boolParam(r, "onlySetups")
	rows, err := d.St.TopConfluenceSetups(r.Context(), market, limitParam(r, 50, 500), onlySetups)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if len(rows) == 0 {
		writeJSON(w, map[string]any{
			"available": false,
			"reason": map[bool]string{
				true:  "no confluence SETUPS right now — independent families are not agreeing on any tracked symbol",
				false: "no confluence assessments stored yet — the scorer runs every 30m",
			}[onlySetups],
			"caveat": confluenceCaveat,
			"rows":   []confluenceTopRow{},
		})
		return
	}
	out := make([]confluenceTopRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, confluenceTopRow{
			Symbol: row.Symbol, Market: row.Market, Direction: row.Direction,
			Agree: row.Agree, Score: row.Score, IsSetup: row.IsSetup, Ts: row.Ts,
		})
	}
	writeJSON(w, map[string]any{
		"available": true,
		"caveat":    confluenceCaveat,
		"rows":      out,
	})
}

// confluenceTrack serves the accruing MONEY scoreboard over RESOLVED, forward-
// tracked setups — scored by EXPECTED PROFIT, not win rate. Independent by
// (symbol, UTC-day); money withheld until both gates clear.
// GET /api/confluence/track
func (d Deps) confluenceTrack(w http.ResponseWriter, r *http.Request) {
	rows, err := d.St.ResolvedConfluenceOutcomes(r.Context(), confluenceTrackWindow)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	// Collapse to ONE independent observation per (symbol, UTC-day), keeping the
	// latest (rows are ts DESC, so the first seen per key wins). Setups are
	// already day-bucketed, so this is belt-and-suspenders — and it powers the
	// distinct-day gate.
	seen := map[[2]int64]bool{}
	dayset := map[int64]bool{}
	var all, long, short []confluenceTrade
	for _, o := range rows {
		day := o.Ts / 86400
		key := [2]int64{o.SymbolID, day}
		if seen[key] {
			continue
		}
		seen[key] = true
		dayset[day] = true
		// Direction-adjusted, cost-netted trade return: a LONG profits when price
		// rises, a SHORT when it falls; both pay the round-trip cost. This is the
		// PROFIT of having taken the setup, not the raw price move.
		t := confluenceTrade{day: day, ret: float64(o.Direction)*o.FwdReturn - 2*confluenceCostPerSide}
		all = append(all, t)
		if o.Direction > 0 {
			long = append(long, t)
		} else if o.Direction < 0 {
			short = append(short, t)
		}
	}
	independent := len(all)
	distinctDays := len(dayset)

	resp := map[string]any{
		"available": true,
		"live":      true,
		"resolved":  independent,
		"gate": map[string]any{
			"minIndependent": confluenceMinIndependent,
			"distinctDays":   confluenceMinDays,
		},
		"caveat": confluenceCaveat,
	}

	if independent < confluenceMinIndependent || distinctDays < confluenceMinDays {
		// GATED: withhold every money number, say exactly how far off we are.
		// The cluster block still ships, stripped to sample-size facts only —
		// how much evidence exists is not a profitability claim, and publishing
		// it is what stops "2,530 resolutions" reading as 2,530 trials.
		resp["money"] = nil
		resp["byDirection"] = nil
		resp["cluster"] = trackClusterDescriptive(clusterstat.Grade(confluenceObs(all)))
		resp["expectancy"] = nil
		resp["note"] = notYetSignificantConfluence(independent, distinctDays)
		writeJSON(w, resp)
		return
	}

	allBook := confluenceGrade(all)
	refusedReason := ""
	if allBook.ExpectancyCI == nil {
		refusedReason = allBook.ExpectancyCINote
	}
	resp["money"] = allBook.Money
	resp["cluster"] = allBook.Cluster
	resp["expectancy"] = map[string]any{
		"point":   allBook.Money.Expectancy,
		"ci":      allBook.ExpectancyCI,
		"refused": allBook.ExpectancyCI == nil,
		"reason":  refusedReason,
		"method":  confluenceExpectancyMethod,
	}
	resp["byDirection"] = map[string]any{
		"long":  confluenceGrade(long),
		"short": confluenceGrade(short),
	}
	resp["note"] = "scored by EXPECTED PROFIT (expectancy / profit factor), NOT win rate — a high win rate with large losers still loses money"
	resp["clusterNote"] = "raw N is not a sample size: every setup flagged on one day rides the same market move, so cluster reports the " +
		"distinct-day count, the MEASURED design effect and the effective N those imply, and every interval resamples DAYS. " +
		"The long and short books are graded separately and each must clear the day floor on its own."
	writeJSON(w, resp)
}

// ── cluster-robust plumbing for the money scoreboard (C4) ────────────────────

// confluenceTrade is one independent (symbol, UTC-day) setup: the day that
// clusters it and its cost-netted realized profit.
type confluenceTrade struct {
	day int64
	ret float64
}

const confluenceExpectancyMethod = "percentile interval of the mean cost-netted trade return over WHOLE DAYS resampled with " +
	"replacement. A row-level standard error would assert one independent trial per setup; the variance on this record lives " +
	"between days, not between symbols within a day."

// confluenceBook is one book's money scoreboard together with the cluster-robust
// evidence behind it. Money is embedded so its fields stay at the top level of
// the JSON object — the confluence page reads byDirection.long.expectancy and
// .trades directly, and moving them would break it.
type confluenceBook struct {
	moneymetrics.Money
	// Cluster grades the WIN RATE as a day-clustered proportion. Its intervals
	// are nil below the day floor, with Reason saying so.
	Cluster clusterstat.Result `json:"cluster"`
	// ExpectancyCI is the day-resampled interval around the headline expectancy,
	// nil when the book cannot support one. Win rate is not profitability, so an
	// interval on the win rate alone would leave the number that decides whether
	// this makes money standing without one.
	ExpectancyCI     *clusterstat.Interval `json:"expectancyCI"`
	ExpectancyCINote string                `json:"expectancyCINote"`
}

// confluenceObs projects trades into the shape clusterstat grades. Dir stays
// DirNone deliberately: the outcome here is "did this setup PROFIT after cost",
// not "did the model call the market's direction", and feeding the setup's
// long/short side in as a direction would make clusterstat's breadth diagnostic
// recover a realized direction from a profitability flag — a setup that moved
// the right way but not far enough to clear costs would be recorded as the
// market having moved the other way.
func confluenceObs(trades []confluenceTrade) []clusterstat.Obs {
	out := make([]clusterstat.Obs, len(trades))
	for i, t := range trades {
		out[i] = clusterstat.Obs{Day: t.day, Hit: t.ret > 0}
	}
	return out
}

// confluenceGrade scores one book and attaches its cluster-robust evidence.
func confluenceGrade(trades []confluenceTrade) confluenceBook {
	rets := make([]float64, len(trades))
	for i, t := range trades {
		rets[i] = t.ret
	}
	b := confluenceBook{
		Money:   moneymetrics.FromReturns(rets),
		Cluster: clusterstat.Grade(confluenceObs(trades)),
	}
	b.ExpectancyCI, b.ExpectancyCINote = dayResampledMeanCI(trades)
	return b
}

// dayResampledMeanCI bootstraps the mean trade return by resampling WHOLE DAYS.
// It returns nil and a stated reason below clusterstat.MinDistinctDays — the
// same refusal the proportion side takes, because an expectancy estimated from a
// handful of days is exactly as overconfident as a proportion from the same
// days, and this record's mean is routinely one day's move (live: a −22.6% day
// carries the entire −4.4% headline).
func dayResampledMeanCI(trades []confluenceTrade) (*clusterstat.Interval, string) {
	byDay := map[int64][]float64{}
	for _, t := range trades {
		byDay[t.day] = append(byDay[t.day], t.ret)
	}
	days := make([]int64, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	if len(days) < clusterstat.MinDistinctDays {
		return nil, "no interval: " + strconv.Itoa(len(days)) + "/" + strconv.Itoa(clusterstat.MinDistinctDays) +
			" distinct days. Every setup on one day shares one market move, so this book's expectancy cannot be given " +
			"an honest interval yet — a narrow one would be worse than none."
	}
	// Deterministic order so the resample draws map to the same days every run
	// and the published interval is reproducible.
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	iv, ok := clusterstat.BootstrapStat(len(days), 2000, 0.05, func(idx []int) (float64, bool) {
		var sum float64
		var n int
		for _, i := range idx {
			for _, r := range byDay[days[i]] {
				sum += r
				n++
			}
		}
		if n == 0 {
			return 0, false
		}
		return sum / float64(n), true
	})
	if !ok {
		return nil, "no interval: the day resample did not produce enough usable draws"
	}
	return &iv, confluenceExpectancyMethod
}

// notYetSignificantConfluence is the honest gate note: how far the accruing
// record is from BOTH thresholds.
func notYetSignificantConfluence(independent, distinctDays int) string {
	return "confluence track accruing — " +
		strconv.Itoa(independent) + "/" + strconv.Itoa(confluenceMinIndependent) + " independent, " +
		strconv.Itoa(distinctDays) + "/" + strconv.Itoa(confluenceMinDays) + " days; not yet significant"
}

// boolParam reads a truthy query flag ("1"/"true"/"yes").
func boolParam(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// registerConfluence wires the CONFLUENCE GATE read routes.
func (d Deps) registerConfluence(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/confluence", d.confluence)
	mux.HandleFunc("GET /api/confluence/top", d.confluenceTop)
	mux.HandleFunc("GET /api/confluence/track", d.confluenceTrack)
}
