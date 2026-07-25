// GET /api/signal-report — the one-call payload behind the per-signal detail
// report page (/signals/report/[market]/[symbol]?kind=...). For any clicked
// signal it returns: the signal itself, WHY it fired (raw model inputs, never
// vibes), the signal's own walk-forward history on THIS symbol, the symbol's
// full current signal stack, and plain trade context — all with the same
// honesty labels the rest of the platform carries.
package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

// reportBars caps the daily-bar load: ~10y is plenty for every history walk.
const reportBars = 2600

func (d Deps) signalReport(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	kind := r.URL.Query().Get("kind")
	switch kind {
	case "trend21", "liquidity21", "vol21", "vol63", "prediction", "composite",
		"breakout", "anomaly", "overview":
	default:
		kind = "overview"
	}
	ctx := r.Context()
	bars, err := d.St.LastBars(ctx, s.ID, md.TF1d, reportBars)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if len(bars) == 0 {
		httpErr(w, 404, "no daily bars for symbol")
		return
	}
	ts := make([]int64, 0, len(bars))
	closes := make([]float64, 0, len(bars))
	vols := make([]float64, 0, len(bars))
	for _, b := range bars {
		if b.Close > 0 {
			ts = append(ts, b.Ts)
			closes = append(closes, b.Close)
			vols = append(vols, b.Volume)
		}
	}
	rets := make([]float64, 0, len(closes))
	retTs := make([]int64, 0, len(closes))
	for i := 1; i < len(closes); i++ {
		rets = append(rets, closes[i]/closes[i-1]-1)
		retTs = append(retTs, ts[i])
	}

	out := map[string]any{
		"symbol": s.Symbol,
		"market": string(s.Market),
		"kind":   kind,
		"asOf":   bars[len(bars)-1].Ts,
		"barAge": time.Now().Unix() - bars[len(bars)-1].Ts,
	}

	// ── the signal + why it fired + its history on this symbol ──
	switch kind {
	case "trend21":
		if f, inputs, ok := structregime.ExplainTrend(closes); ok {
			out["signal"] = f
			out["whyFired"] = inputs
			// The measured forward return of this call's conviction band — the
			// number that says whether the hit rate is worth anything. Hoisted out
			// of the signal so the page cannot show accuracy without it.
			if f.Tradeability != "" {
				out["tradeability"] = f.Tradeability
			}
		}
		out["history"] = summarizeInstances(structregime.TrendHistory(ts, closes))
	case "liquidity21":
		if f, inputs, ok := structregime.ExplainLiquidity(closes, vols); ok {
			out["signal"] = f
			out["whyFired"] = inputs
		}
		out["history"] = summarizeInstances(structregime.LiquidityHistory(ts, closes, vols))
	case "vol21":
		if f, inputs, ok := structregime.ExplainVol21(rets); ok {
			out["signal"] = f
			out["whyFired"] = inputs
		}
		out["history"] = summarizeInstances(structregime.Vol21History(retTs, rets))
	case "vol63":
		if f, ok := volregime.Predict(rets); ok {
			out["signal"] = f
			out["whyFired"] = []structregime.Input{
				{Name: "rank", Value: f.Rank, Note: "current EWMA vol percentile in its trailing 200d; >0.5 ⇒ elevated"},
				{Name: "conviction", Value: f.Conviction, Note: "2×|rank−0.5| — extremes are the most predictable"},
			}
		}
		out["history"] = summarizeVolInstances(volregime.History(retTs, rets, 63))
	case "overview":
		// The default kind CompanyPeek links to had no case at all, so the page's
		// "why it fired" panel rendered empty. Overview is not a fired signal, so
		// the honest content is the measured state the rest of the report reasons
		// from — same Input convention, every value computed from stored bars.
		out["whyFired"] = overviewInputs(bars, closes, vols)
	case "prediction", "composite":
		// history = this symbol's slice of the LIVE forward record
		rows, correct, total, err := d.St.PredictionOutcomesForSymbol(ctx, s.ID, "1d", 50)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		h := map[string]any{"rows": rows, "correct": correct, "total": total,
			"note": "this symbol's slice of the LIVE forward record — probabilities frozen at prediction time"}
		if total > 0 {
			h["hitRate"] = float64(correct) / float64(total)
		}
		out["history"] = h
	case "breakout":
		out["history"] = d.breakoutHistory(ctx, s.ID, ts, closes)
	case "anomaly":
		rows, err := d.St.Anomalies(ctx, s.ID, "", 50)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		out["history"] = map[string]any{"anomalies": withFwd(rowsToEvents(rows), ts, closes),
			"note": "each event with the realized forward 5-session move — descriptive, not a graded prediction"}
	}

	// ── the symbol's full current signal stack (always) ──
	if stack, err := d.St.RegimeForecastsForSymbol(ctx, s.ID); err == nil {
		// tradeability is not persisted (pure function of kind + conviction), so
		// derive it per row — kinds whose forward return was never measured get ""
		// and the field is omitted rather than borrowing another kind's number.
		for i := range stack {
			stack[i].Tradeability = structregime.TradeabilityFor(stack[i].Kind, stack[i].Conviction)
		}
		out["regimeStack"] = stack
	}
	// Earnings-window label (credibility wave): added only when the
	// filing-cadence ESTIMATE falls within the next 7 days; best-effort and a
	// label only — forecasts are never suppressed.
	if p, ok, err := d.St.LatestPeriodicFiling(ctx, s.ID); err == nil && ok && p.FiledTs > 0 {
		if _, days, within := earningsWindowFrom(p.FiledTs, time.Now().Unix()); within {
			out["earningsWindow"] = map[string]any{
				"daysUntil": days, "withinWindow": true, "note": earningsRiskNote,
			}
		}
	}
	if vf, ok, err := d.St.VolForecastForSymbol(ctx, s.ID); err == nil && ok {
		out["vol63"] = vf
	}
	if p, ok, err := d.St.LatestPrediction(ctx, s.ID, md.H1d); err == nil && ok {
		pv := map[string]any{"ts": p.Ts, "rawProb": p.RawProb, "calProb": p.CalProb, "nUsed": p.NUsed}
		if json.Valid([]byte(p.Components)) {
			pv["components"] = json.RawMessage(p.Components)
		}
		out["predictionNow"] = pv
	}
	if cs, ok, err := d.St.LatestCompositeScore(ctx, s.ID, "1d"); err == nil && ok {
		cv := map[string]any{"ts": cs.Ts, "score": cs.Score, "curvePct": cs.CurvePct, "edge": cs.Edge}
		if json.Valid([]byte(cs.Payload)) {
			cv["payload"] = json.RawMessage(cs.Payload)
		}
		out["composite"] = cv
	}
	if sc, ok, err := d.St.LatestScore(ctx, s.ID, md.H1d); err == nil && ok {
		out["pressure1d"] = sc
	}
	liveN, liveWin, err := d.St.LiveDirectionalRecord(ctx, md.H1d)
	if err == nil && liveN >= minIndependentN {
		out["liveDirectionalNote"] = fmtLiveNote(liveN, liveWin)
	}

	// ── trade context + recent events (always) ──
	out["tradeContext"] = tradeContext(bars, closes, vols)
	now := time.Now().Unix()
	if bk, err := d.St.BreakoutsForSymbol(ctx, s.ID, now-90*86400, now); err == nil {
		out["recentBreakouts"] = withFwd(markersToEvents(bk), ts, closes)
	}
	if an, err := d.St.Anomalies(ctx, s.ID, "", 10); err == nil {
		out["recentAnomalies"] = an
	}
	out["honesty"] = "Every accuracy on this page is the measured walk-forward number for the signal's conviction band on the whole universe; the history table is THIS symbol's own record and is usually a small sample — judge it as one. An accuracy is not a return: the 2026-07-24 re-validation measured trend21's most accurate conviction band (>=0.9) at a NEGATIVE mean forward 21d return (-0.39%), because high conviction means price is already extended from its 200-day average and extended names mean-revert. Where that applies to a signal shown here it is stated in its tradeability field."
	writeJSON(w, out)
}

func fmtLiveNote(n int, win float64) string {
	return "LIVE directional record across the platform: win rate " +
		trimPct(win*100) + "% over " + itoa(int64(n)) +
		" independent symbol-days — no demonstrated directional edge; the validated signals are the regime forecasts."
}

// summarizeInstances packages a history walk with its summary counts.
func summarizeInstances(inst []structregime.Instance) map[string]any {
	correct := 0
	for _, x := range inst {
		if x.Correct {
			correct++
		}
	}
	// newest first for display, cap 40
	rev := make([]structregime.Instance, 0, len(inst))
	for i := len(inst) - 1; i >= 0 && len(rev) < 40; i-- {
		rev = append(rev, inst[i])
	}
	out := map[string]any{"instances": rev, "correct": correct, "total": len(inst),
		"note": "non-overlapping walk-forward replay on this symbol only — small-sample by design"}
	if len(inst) > 0 {
		out["hitRate"] = float64(correct) / float64(len(inst))
	}
	return out
}

func summarizeVolInstances(inst []volregime.Instance) map[string]any {
	correct := 0
	for _, x := range inst {
		if x.Correct {
			correct++
		}
	}
	rev := make([]volregime.Instance, 0, len(inst))
	for i := len(inst) - 1; i >= 0 && len(rev) < 40; i-- {
		rev = append(rev, inst[i])
	}
	out := map[string]any{"instances": rev, "correct": correct, "total": len(inst),
		"note": "non-overlapping walk-forward replay on this symbol only — small-sample by design"}
	if len(inst) > 0 {
		out["hitRate"] = float64(correct) / float64(len(inst))
	}
	return out
}

// event is a past breakout/anomaly with its realized forward move.
type event struct {
	Ts      int64   `json:"ts"`
	Kind    string  `json:"kind"`
	Detail  string  `json:"detail"`
	Z       float64 `json:"z,omitempty"`
	Fwd5Pct float64 `json:"fwd5Pct"`
	HasFwd  bool    `json:"hasFwd"`
}

func markersToEvents(bk []store.BreakoutMarker) []event {
	out := make([]event, 0, len(bk))
	for _, b := range bk {
		out = append(out, event{Ts: b.Ts, Kind: b.Kind, Detail: b.Detail, Z: b.Strength})
	}
	return out
}

func rowsToEvents(an []store.AnomalyRow) []event {
	out := make([]event, 0, len(an))
	for _, a := range an {
		out = append(out, event{Ts: a.Ts, Kind: a.Kind, Detail: a.Detail, Z: a.Z})
	}
	return out
}

// withFwd fills each event's realized forward 5-session close-to-close move.
func withFwd(ev []event, ts []int64, closes []float64) []event {
	for i := range ev {
		// last bar at or before the event
		j := -1
		for k := len(ts) - 1; k >= 0; k-- {
			if ts[k] <= ev[i].Ts {
				j = k
				break
			}
		}
		if j >= 0 && j+5 < len(closes) && closes[j] > 0 {
			ev[i].Fwd5Pct = (closes[j+5]/closes[j] - 1) * 100
			ev[i].HasFwd = true
		}
	}
	return ev
}

func (d Deps) breakoutHistory(ctx context.Context, symbolID int64, ts []int64, closes []float64) map[string]any {
	now := time.Now().Unix()
	bk, err := d.St.BreakoutsForSymbol(ctx, symbolID, now-365*86400, now)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	evs := withFwd(markersToEvents(bk), ts, closes)
	return map[string]any{"breakouts": evs,
		"note": "every breakout detection on this symbol in the last year, each with its realized forward 5-session move — descriptive history, not a graded accuracy claim"}
}

// tradeContext computes the plain price context every report carries.
func tradeContext(bars []md.Bar, closes, vols []float64) map[string]any {
	n := len(closes)
	last := closes[n-1]
	lo252 := n - 252
	if lo252 < 0 {
		lo252 = 0
	}
	hi52, lo52 := closes[lo252], closes[lo252]
	for _, c := range closes[lo252:] {
		if c > hi52 {
			hi52 = c
		}
		if c < lo52 {
			lo52 = c
		}
	}
	sma := func(w int) float64 {
		if n < w {
			return 0
		}
		var s float64
		for _, c := range closes[n-w:] {
			s += c
		}
		return s / float64(w)
	}
	// ATR(14) as % of price, from the raw bars (high/low aware)
	atr := 0.0
	if len(bars) >= 15 {
		var s float64
		cnt := 0
		for i := len(bars) - 14; i < len(bars); i++ {
			b, prev := bars[i], bars[i-1]
			if prev.Close <= 0 {
				continue
			}
			tr := b.High - b.Low
			if x := math.Abs(b.High - prev.Close); x > tr {
				tr = x
			}
			if x := math.Abs(b.Low - prev.Close); x > tr {
				tr = x
			}
			s += tr / prev.Close
			cnt++
		}
		if cnt > 0 {
			atr = s / float64(cnt) * 100
		}
	}
	lo20 := n - 20
	if lo20 < 0 {
		lo20 = 0
	}
	swingHi, swingLo := closes[lo20], closes[lo20]
	for _, c := range closes[lo20:] {
		if c > swingHi {
			swingHi = c
		}
		if c < swingLo {
			swingLo = c
		}
	}
	var dollar float64
	cnt := 0
	for i := max0(n - 21); i < n; i++ {
		if vols[i] > 0 {
			dollar += closes[i] * vols[i]
			cnt++
		}
	}
	if cnt > 0 {
		dollar /= float64(cnt)
	}
	out := map[string]any{
		"lastClose":      last,
		"high52w":        hi52,
		"low52w":         lo52,
		"sma20":          sma(20),
		"sma50":          sma(50),
		"sma200":         sma(200),
		"atr14Pct":       atr,
		"swingHigh20d":   swingHi,
		"swingLow20d":    swingLo,
		"avgDollarVol21": dollar,
	}
	if hi52 > 0 {
		out["offHigh52Pct"] = (last/hi52 - 1) * 100
	}
	if lo52 > 0 {
		out["offLow52Pct"] = (last/lo52 - 1) * 100
	}
	return out
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

func trimPct(x float64) string {
	return fmtFloat(x, 1)
}

func fmtFloat(x float64, dec int) string {
	pow := math.Pow(10, float64(dec))
	v := math.Round(x*pow) / pow
	b, _ := json.Marshal(v)
	return string(b)
}

// ── overview inputs (2026-07-24: the default report kind had no whyFired) ──

// overviewInputs is the "why it fired" surface for kind=overview, which is not
// a fired signal at all — so the honest content is the measured price state the
// rest of the report reasons from, in the same Input shape the structural kinds
// use. Every value comes from the stored daily bars via tradeContext, so the
// page and this panel can never disagree; a quantity the history is too short
// to support (no 200-day average yet) is omitted rather than shown as zero.
func overviewInputs(bars []md.Bar, closes, vols []float64) []structregime.Input {
	tc := tradeContext(bars, closes, vols)
	// Two different absences: tradeContext omits the 52-week keys outright when
	// it cannot compute them, but reports 0 for an average it lacks history for.
	// present() keeps a genuine 0 (a stock AT its 52-week high) visible; nonzero()
	// drops the placeholder zeros.
	present := func(k string) (float64, bool) {
		v, ok := tc[k].(float64)
		return v, ok
	}
	nonzero := func(k string) (float64, bool) {
		v, ok := tc[k].(float64)
		return v, ok && v != 0
	}
	last := closes[len(closes)-1]
	in := []structregime.Input{
		{Name: "close", Value: last, Note: "latest stored daily close — everything below is derived from it"},
	}
	if n := len(closes); n >= 2 && closes[n-2] > 0 {
		in = append(in, structregime.Input{Name: "day_change_pct",
			Value: (last/closes[n-2] - 1) * 100, Note: "close-to-close move on the latest session"})
	}
	if v, ok := present("offHigh52Pct"); ok {
		in = append(in, structregime.Input{Name: "off_52w_high_pct", Value: v,
			Note: "distance from the highest close of the last 252 sessions; 0 = at the high"})
	}
	if v, ok := present("offLow52Pct"); ok {
		in = append(in, structregime.Input{Name: "off_52w_low_pct", Value: v,
			Note: "distance above the lowest close of the last 252 sessions"})
	}
	if sma, ok := nonzero("sma50"); ok {
		in = append(in, structregime.Input{Name: "distance_from_sma50_pct",
			Value: (last/sma - 1) * 100, Note: "close vs the 50-day average — the medium-term structure"})
	}
	if sma, ok := nonzero("sma200"); ok {
		in = append(in, structregime.Input{Name: "distance_from_sma200_pct",
			Value: (last/sma - 1) * 100,
			Note: "close vs the 200-day average; the SIGN is the trend21/trend63 call and the " +
				"|magnitude| percentile is their conviction — large values mean already extended"})
	}
	if v, ok := nonzero("atr14Pct"); ok {
		in = append(in, structregime.Input{Name: "atr14_pct", Value: v,
			Note: "average true range over 14 sessions as % of price — the typical daily range"})
	}
	if v, ok := nonzero("avgDollarVol21"); ok {
		in = append(in, structregime.Input{Name: "avg_dollar_volume_21d", Value: v,
			Note: "mean daily close×volume over the last 21 sessions — tradability, not a signal"})
	}
	return in
}
