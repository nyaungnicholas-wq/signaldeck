package api

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"strconv"
	"net/http"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/riskgate"
	"github.com/nyaungnicholas-wq/signaldeck/internal/stresslab"
)

// ── STRESS LAB (Layer 4 middle path) ────────────────────────────────────────
//
// GET  /api/stress/scenarios — the scenario catalog (definitions as data).
// POST /api/stress/run       — replay named scenarios (jointly composed)
//                              through the REAL decision path over stored
//                              bars + COMMITTED signals; optionally over
//                              regime-conditional block-bootstrap paths.
//
// The run route is AUTH-REQUIRED and capped: it is CPU-bound compute, and the
// A11 lesson (api/ledger.go) is that an unbounded compute endpoint is a
// resource-exhaustion lever — so at most stressRunConcurrency runs at once
// (the rest 429 immediately) under a hard context deadline.

const (
	// stressMaxSymbols bounds one request's symbol fan-out.
	stressMaxSymbols = 5
	// stressMaxWindowBars bounds the replay window (~2 years of dailies).
	stressMaxWindowBars = 500
	stressDefaultWindow = 250
	// stressMaxBootPaths bounds bootstrap resamples per symbol.
	stressMaxBootPaths = 50
	// stressRunTimeout bounds one run end-to-end.
	stressRunTimeout = 30 * time.Second
	// stressRunConcurrency caps concurrent runs across the daemon.
	stressRunConcurrency = 2
	// stressMaxSignals bounds the committed-signal read per symbol.
	stressMaxSignals = 5000
)

var stressRunSem = make(chan struct{}, stressRunConcurrency)

type stressRunReq struct {
	Scenarios []string `json:"scenarios"`
	Symbols   []string `json:"symbols"`
	Market    string   `json:"market"`  // "crypto" | "stocks"
	Horizon   string   `json:"horizon"` // default "1d"
	Seed      int64    `json:"seed"`
	WindowBars int     `json:"windowBars"`
	// BootstrapPaths > 0 additionally replays that many regime-conditional
	// block-bootstrap counterfactual paths per symbol.
	BootstrapPaths int `json:"bootstrapPaths"`
	BlockLen       int `json:"blockLen"`
}

func (d Deps) registerStress(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/stress/scenarios", d.stressScenarios)
	mux.HandleFunc("POST /api/stress/run", d.stressRun)
}

func (d Deps) stressScenarios(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"scenarios": stresslab.Catalog(),
		"note": "scenarios compose: POST /api/stress/run with several names applies their JOINT effect vector. " +
			"The replay pushes COMMITTED signals (never recomputed) through the real threshold decision, " +
			"riskgate envelope and papertrade execution-cost model; it reports system behavior, not a PnL claim.",
	})
}

func (d Deps) stressRun(w http.ResponseWriter, r *http.Request) {
	if userID(r) == 0 {
		httpErr(w, http.StatusUnauthorized, "sign in required — stress replays are compute")
		return
	}

	var req stressRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return
	}
	if len(req.Scenarios) == 0 {
		httpErr(w, http.StatusBadRequest, "need at least one scenario name (see GET /api/stress/scenarios)")
		return
	}
	scenarios, err := stresslab.Lookup(req.Scenarios)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Symbols) == 0 || len(req.Symbols) > stressMaxSymbols {
		httpErr(w, http.StatusBadRequest, "need 1.."+strconv.Itoa(stressMaxSymbols)+" symbols")
		return
	}
	market := md.Market(req.Market)
	if market != md.Crypto && market != md.Stocks {
		httpErr(w, http.StatusBadRequest, "need market=crypto|stocks")
		return
	}
	h := md.Horizon(strings.TrimSpace(req.Horizon))
	if h == "" {
		h = md.Horizon("1d")
	}
	windowBars := req.WindowBars
	if windowBars <= 0 {
		windowBars = stressDefaultWindow
	}
	if windowBars > stressMaxWindowBars {
		httpErr(w, http.StatusBadRequest, "windowBars capped at "+strconv.Itoa(stressMaxWindowBars))
		return
	}
	if req.BootstrapPaths < 0 || req.BootstrapPaths > stressMaxBootPaths {
		httpErr(w, http.StatusBadRequest, "bootstrapPaths capped at "+strconv.Itoa(stressMaxBootPaths))
		return
	}

	// Admission: bounded concurrency, immediate 429 (the A11 pattern).
	select {
	case stressRunSem <- struct{}{}:
		defer func() { <-stressRunSem }()
	default:
		httpErr(w, http.StatusTooManyRequests, "the stress lab is at capacity — retry shortly")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), stressRunTimeout)
	defer cancel()

	joint := stresslab.Combine(scenarios...)
	label := strings.Join(req.Scenarios, "+")
	lim := riskgate.Defaults()
	regimes, _ := d.St.RegimeLabels(ctx) // best-effort; nil is fine

	type symbolResult struct {
		Symbol   string                    `json:"symbol"`
		Baseline stresslab.SystemBehavior  `json:"baseline"`
		Stressed stresslab.SystemBehavior  `json:"stressed"`
		Bootstrap []stresslab.SystemBehavior `json:"bootstrap,omitempty"`
		Skipped  string                    `json:"skipped,omitempty"`
	}
	var results []symbolResult

	for _, raw := range req.Symbols {
		if err := ctx.Err(); err != nil {
			break
		}
		symName := strings.ToUpper(strings.TrimSpace(raw))
		sym, err := d.St.GetSymbol(ctx, symName, market)
		if err != nil {
			results = append(results, symbolResult{Symbol: symName, Skipped: "not tracked"})
			continue
		}
		bars, err := d.St.LastBars(ctx, sym.ID, md.TF1d, windowBars)
		if err != nil || len(bars) < 20 {
			results = append(results, symbolResult{Symbol: symName, Skipped: "fewer than 20 stored daily bars"})
			continue
		}
		sigs, err := d.St.CommittedSignals(ctx, sym.ID, h, bars[0].Ts, bars[len(bars)-1].Ts, stressMaxSignals)
		if err != nil {
			httpErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if len(sigs) == 0 {
			results = append(results, symbolResult{Symbol: symName, Skipped: "no committed signals in the window — nothing to replay honestly"})
			continue
		}
		signals := make([]stresslab.Signal, len(sigs))
		for i, s := range sigs {
			signals[i] = stresslab.Signal{Ts: s.Ts, P: s.Prob}
		}
		adv := advFromBars(bars)
		var regimeCol []string
		if lbl := regimes[sym.ID]; lbl != "" {
			// One latest label per symbol is what the store keeps; it
			// conditions the bootstrap uniformly. Per-bar labels can slot in
			// when a regime HISTORY is stored.
			regimeCol = make([]string, len(bars))
			for i := range regimeCol {
				regimeCol[i] = lbl
			}
		}
		base := stresslab.Window{Market: market, Bars: bars, Signals: signals, Regimes: regimeCol, ADVUSD: adv, SpreadMult: 1}

		rng := rand.New(rand.NewSource(req.Seed)) //nolint:gosec // deterministic replay, not crypto
		res := symbolResult{
			Symbol:   symName,
			Baseline: stresslab.Replay(base, lim, nil, "baseline"),
			Stressed: stresslab.Replay(stresslab.Apply(base, joint, rng), lim, nil, label),
		}
		blockLen := req.BlockLen
		if blockLen <= 0 {
			blockLen = stresslab.DefaultBlockLen
		}
		for p := 0; p < req.BootstrapPaths && ctx.Err() == nil; p++ {
			bw := base
			bw.Bars = stresslab.BlockBootstrap(base.Bars, base.Regimes, blockLen, rng)
			res.Bootstrap = append(res.Bootstrap,
				stresslab.Replay(stresslab.Apply(bw, joint, rng), lim, nil, label+"/boot"))
		}
		results = append(results, res)
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		httpErr(w, http.StatusGatewayTimeout, "stress run exceeded its deadline — narrow the symbol set or window")
		return
	}
	writeJSON(w, map[string]any{
		"scenario": label,
		"effects":  joint,
		"seed":     req.Seed,
		"results":  results,
		"note": "system-behavior replay over stored bars + committed signals; baseline vs stressed on the same decision path. " +
			"Not a PnL forecast: costs and refusals are modelled, order-book dynamics are not.",
	})
}

// advFromBars mirrors pipeline.PaperTrader's ADV convention: mean dollar
// volume over the trailing 21 priced bars.
func advFromBars(bars []md.Bar) float64 {
	const look = 21
	var sum float64
	var n int
	for i := len(bars) - 1; i >= 0 && n < look; i-- {
		if bars[i].Close > 0 && bars[i].Volume > 0 {
			sum += bars[i].Close * bars[i].Volume
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
