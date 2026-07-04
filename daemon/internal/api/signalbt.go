package api

import (
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signalbt"
)

// ── STAGE 5: own-signal backtester read route ────────────────────────────────
//
// GET /api/signal-backtest?horizon=1d|1w replays the FEATURE STORE (each
// resolved prediction's calibrated signal joined to its realized forward
// return) through the internal/signalbt engine and returns the OUT-OF-SAMPLE
// grade of SignalDeck's OWN flagship signal: IC + IC-decay by lag, signal-
// quintile forward-return spread, hit-rate on independent (symbol,day) obs,
// turnover, and a costed equity curve vs SPY buy-and-hold.
//
// Unlike /api/backtest (which grades USER-TYPED SMA/RSI rules), this grades the
// platform's own predictions. It is a READ, public under SIGNALDECK_PUBLIC_READS
// like the other market-data reads. Every headline number is GATED behind
// signalbt.MinIndependentN — with ~0 resolved live outcomes today the honest
// output is "insufficient data", and the payload says so (gated=true + note).

// signalBTDecayLags are the extra forward lags (trading-day bars) graded for the
// IC-decay curve, on top of the horizon's own primary lag. Chosen to span a
// couple of weeks so the decay of the daily signal is visible.
var signalBTDecayLags = []int{2, 3, 5, 10}

// signalBTMaxObs caps how many resolved outcomes we replay (newest-first). Large
// enough to cover the full accrued history at current cadence; bounded so the
// endpoint stays cheap.
const signalBTMaxObs = 20000

func (d Deps) signalBacktest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d // the daily feature store is what this backtester replays
	}

	rawObs, err := d.St.SignalBacktestObs(ctx, h, signalBTDecayLags, signalBTMaxObs)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}

	// Map the store rows to engine observations (store stays free of the engine
	// type; the engine stays free of the store).
	obs := make([]signalbt.Observation, len(rawObs))
	for i, o := range rawObs {
		obs[i] = signalbt.Observation{
			SymbolID: o.SymbolID,
			Ts:       o.Ts,
			Signal:   o.Signal,
			FwdByLag: o.FwdByLag,
		}
	}

	// SPY buy-and-hold benchmark aligned to the observation timeline. Best-effort:
	// when SPY is not tracked the benchmark is empty and the strategy curve is
	// still returned (BenchmarkReturn=0).
	spyTs, spyClose, err := d.St.SPYDailyCloses(ctx, signalBTMaxObs)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	benchmark := signalbt.BenchmarkCurve(obs, spyTs, spyClose)

	primary := 1
	if h == md.H1w {
		primary = 5
	}
	res := signalbt.Backtest(obs, benchmark, string(h), signalbt.Params{
		PrimaryLag: primary,
		DecayLags:  signalBTDecayLags,
		// Cost + thresholds left zero → engine fills the honest defaults
		// (per-side stock-like cost, 0.60/0.40 deadband) matching the paper book.
	})
	// Advertise the horizons the UI can request so it can render a selector
	// without hardcoding the daemon's supported set.
	writeJSON(w, map[string]any{
		"result":   res,
		"horizons": []md.Horizon{md.H1d, md.H1w},
		"benchmarkSymbol": "SPY",
		"hasBenchmark":    len(benchmark) > 0,
	})
}

// registerSignalBT wires the Stage-5 own-signal backtester read route.
func (d Deps) registerSignalBT(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/signal-backtest", d.signalBacktest)
}
