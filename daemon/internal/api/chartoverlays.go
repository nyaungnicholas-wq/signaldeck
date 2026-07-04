package api

import (
	"math"
	"net/http"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// ── STAGE 7: CHART OVERLAY MARKERS (read route) ──────────────────────────────
//
// GET /api/chart-overlays?symbol=&market=&tf=1d|1h|1m&days=N returns the markers
// the candlestick chart overlays on price: score EXTREMES (the 1d pressure score
// crossing into strong-buy / strong-sell territory), REGIME CHANGES (the
// transition IS the signal), and BREAKOUTS (donchian / volume-spike / squeeze-
// release / correlation-break). All data already exists in the DB from the
// scoring/regime/breakout workers; this endpoint just gathers it for one symbol
// over the chart's visible window so the frontend can drop markers without N
// extra round-trips.
//
// Markers are kept LEGIBLE: score-extreme markers are emitted only on the bar
// where the score FIRST crosses the extreme threshold (not every bar it stays
// extreme), so a long strong-buy run produces one entry marker, not hundreds.
// Read-only, public under SIGNALDECK_PUBLIC_READS like the other market reads.

// overlayExtreme is the |score| threshold at which a score marker is emitted.
// Matches the /honesty + verdict semantics (>=0.6 == "strong").
const overlayExtreme = 0.6

// chartOverlayMarker is one annotation to place on the price chart.
type chartOverlayMarker struct {
	Ts    int64   `json:"ts"`    // bar timestamp (unix seconds) the marker anchors to
	Type  string  `json:"type"`  // "score" | "regime" | "breakout"
	Label string  `json:"label"` // short text, e.g. "strong buy", "→ uptrend", "donchian"
	Text  string  `json:"text"`  // longer tooltip
	Value float64 `json:"value"` // the score / strength (0 when N/A)
	Up    bool    `json:"up"`    // bullish (place below bar, green) vs bearish (above, red)
}

func (d Deps) chartOverlays(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	// Window: default 365 days back (matches the 1d chart's default limit); the
	// frontend can widen/narrow with ?days=.
	days := 365
	if q := r.URL.Query().Get("days"); q != "" {
		if n, e := strconv.Atoi(q); e == nil && n > 0 && n <= 3650 {
			days = n
		}
	}
	now := time.Now().Unix()
	from := now - int64(days)*86400

	markers := make([]chartOverlayMarker, 0, 64)

	// ── score extremes: 1d pressure score crossing into strong territory ──
	// We annotate the FIRST bar of each strong run (crossing edge) to stay legible.
	scores, err := d.St.ScoreHistory(ctx, s.ID, md.H1d, from, now+1)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	prevExtreme := 0 // -1 strong-sell, 0 neutral, +1 strong-buy
	for _, sc := range scores {
		cur := 0
		if sc.Score >= overlayExtreme {
			cur = 1
		} else if sc.Score <= -overlayExtreme {
			cur = -1
		}
		if cur != 0 && cur != prevExtreme {
			label := "strong buy"
			text := "pressure score crossed into strong-buy territory"
			if cur < 0 {
				label = "strong sell"
				text = "pressure score crossed into strong-sell territory"
			}
			markers = append(markers, chartOverlayMarker{
				Ts: sc.Ts, Type: "score", Label: label, Text: text,
				Value: sc.Score, Up: cur > 0,
			})
		}
		prevExtreme = cur
	}

	// ── regime changes: the transition is the signal ──
	rcs, err := d.St.RegimeChangesForSymbol(ctx, s.ID, from, now+1)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	for _, rc := range rcs {
		markers = append(markers, chartOverlayMarker{
			Ts: rc.Ts, Type: "regime", Label: "→ " + rc.To,
			Text: "regime change: " + rc.From + " → " + rc.To,
			Up:   regimeBullish(rc.To),
		})
	}

	// ── breakouts ──
	bks, err := d.St.BreakoutsForSymbol(ctx, s.ID, from, now+1)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	for _, b := range bks {
		txt := b.Kind
		if b.Detail != "" {
			txt = b.Kind + " — " + b.Detail
		}
		markers = append(markers, chartOverlayMarker{
			Ts: b.Ts, Type: "breakout", Label: b.Kind, Text: txt,
			Value: b.Strength, Up: breakoutBullish(b.Kind, b.Strength),
		})
	}

	writeJSON(w, map[string]any{
		"symbol":  s.Symbol,
		"market":  s.Market,
		"count":   len(markers),
		"markers": markers,
	})
}

// regimeBullish maps a regime label to bullish/bearish for marker coloring.
// Uptrend is bullish; downtrend is bearish; range/squeeze are neutral (rendered
// bullish=false so they sit above the bar with a neutral tint on the frontend).
func regimeBullish(label string) bool { return label == "uptrend" }

// breakoutBullish infers direction: donchian/squeeze-release with positive
// strength read bullish; a negative strength (down-breakout) reads bearish.
func breakoutBullish(kind string, strength float64) bool {
	if math.Abs(strength) > 1e-9 {
		return strength > 0
	}
	// No signed strength: volume spikes / correlation breaks are directionless;
	// default to bullish=false so they render as neutral above the bar.
	return false
}

// registerChartOverlays wires the Stage-7 candlestick overlay-markers read route.
func (d Deps) registerChartOverlays(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/chart-overlays", d.chartOverlays)
}
