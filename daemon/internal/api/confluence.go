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
	"strconv"
	"strings"

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
	var allReturns []float64
	var longReturns, shortReturns []float64
	for _, o := range rows {
		key := [2]int64{o.SymbolID, o.Ts / 86400}
		if seen[key] {
			continue
		}
		seen[key] = true
		dayset[o.Ts/86400] = true
		// Direction-adjusted, cost-netted trade return: a LONG profits when price
		// rises, a SHORT when it falls; both pay the round-trip cost. This is the
		// PROFIT of having taken the setup, not the raw price move.
		trade := float64(o.Direction)*o.FwdReturn - 2*confluenceCostPerSide
		allReturns = append(allReturns, trade)
		if o.Direction > 0 {
			longReturns = append(longReturns, trade)
		} else if o.Direction < 0 {
			shortReturns = append(shortReturns, trade)
		}
	}
	independent := len(allReturns)
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
		resp["money"] = nil
		resp["byDirection"] = nil
		resp["note"] = notYetSignificantConfluence(independent, distinctDays)
		writeJSON(w, resp)
		return
	}

	resp["money"] = moneymetrics.FromReturns(allReturns)
	resp["byDirection"] = map[string]any{
		"long":  moneymetrics.FromReturns(longReturns),
		"short": moneymetrics.FromReturns(shortReturns),
	}
	resp["note"] = "scored by EXPECTED PROFIT (expectancy / profit factor), NOT win rate — a high win rate with large losers still loses money"
	writeJSON(w, resp)
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
