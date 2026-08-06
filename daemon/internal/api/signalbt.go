package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
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
//
// The equity block carries its own gate. The engine compounds ONE cross-
// sectional book per day; if the accounting ever yields a path a capped,
// unlevered book cannot reach, equity/strategyReturn/benchmarkReturn/
// excessReturn come back null with gated=true and the reason in note. This
// endpoint published -99.95% at 0.03% turnover while it compounded once per
// (symbol,day) row, so the equity numbers here are null-able by design and the
// UI must read gated before reading them.
//
// STAGE 2 addition: ?pinned=1 serves the WEEKLY STORED evaluation (written by
// the signalbt-weekly worker every Sunday evening NY, meta signalbt_latest)
// instead of recomputing — so the page can show the exact "as of Sunday" grade
// the weekly insight described. When no pin exists yet the handler falls back
// to a live compute and SAYS SO (pinned:false + pinnedNote), never 404s.

// signalBTDecayLags are the extra forward lags (trading-day bars) graded for the
// IC-decay curve, on top of the horizon's own primary lag. Shared with the
// weekly pin worker via signalbt.DefaultDecayLags so the pinned result and a
// live recompute grade the same thing.
var signalBTDecayLags = signalbt.DefaultDecayLags

// signalBTMaxObs caps how many resolved outcomes we replay (newest-first). Large
// enough to cover the full accrued history at current cadence; bounded so the
// endpoint stays cheap. Shared with the weekly pin worker.
const signalBTMaxObs = signalbt.DefaultMaxObs

func (d Deps) signalBacktest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d // the daily feature store is what this backtester replays
	}

	// STAGE 2: pinned weekly result, when asked for and available.
	pinnedRequested := r.URL.Query().Get("pinned") == "1"
	if pinnedRequested {
		if raw, err := d.St.GetMeta(ctx, signalbt.MetaKeyLatest); err == nil && raw != "" {
			var p signalbt.Pinned
			if json.Unmarshal([]byte(raw), &p) == nil {
				if res, ok := p.Results[string(h)]; ok {
					writeJSON(w, map[string]any{
						"result":            res,
						"horizons":          []md.Horizon{md.H1d, md.H1w},
						"benchmarkSymbol":   p.BenchmarkSymbol,
						"hasBenchmark":      p.HasBenchmark,
						"pinned":            true,
						"pinnedDay":         p.DayKey,
						"pinnedTs":          p.ComputedTs,
						"equityDownsampled": p.EquityDownsampled,
						"survivorship":      survivorshipBlock(), // #20: stats rest on the tracked-universe bars table
						// C4: the pin stores a Result, not the observations behind
						// it, so no design effect can be measured from it. Attaching
						// a cluster grade from a fresh live read would put Sunday's
						// point estimate beside Wednesday's interval, so this says
						// nothing rather than something from the wrong sample.
						"cluster":    nil,
						"icInterval": nil,
						"clusterNote": "no cluster-robust interval accompanies a pinned result: the weekly snapshot stores the graded " +
							"numbers, not the per-observation record, so the design effect cannot be measured from it. Request without " +
							"?pinned=1 for a live compute that carries one.",
					})
					return
				}
			}
		}
		// No stored pin (or unreadable) — fall through to a live compute,
		// labeled honestly below.
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

	res := signalbt.Backtest(obs, benchmark, string(h), signalbt.Params{
		PrimaryLag: signalbt.PrimaryLagFor(string(h)),
		DecayLags:  signalBTDecayLags,
		// Cost + thresholds left zero → engine fills the honest defaults
		// (per-side stock-like cost, 0.60/0.40 deadband) matching the paper book.
	})
	// C4 — the engine stays pure (points in, points out) and the INTERVAL is
	// attached here by the one module that owns intervals. Graded over the same
	// primary lag and the same (symbol, UTC-day) set the engine reduced to, so
	// the interval and the point estimate can never describe different samples.
	cluster, icBlock := signalBTCluster(obs, signalbt.PrimaryLagFor(string(h)))

	// Advertise the horizons the UI can request so it can render a selector
	// without hardcoding the daemon's supported set.
	resp := map[string]any{
		"result":          res,
		"horizons":        []md.Horizon{md.H1d, md.H1w},
		"benchmarkSymbol": "SPY",
		"hasBenchmark":    len(benchmark) > 0,
		"pinned":          false,
		"survivorship":    survivorshipBlock(), // #20: stats rest on the tracked-universe bars table
		"cluster":         cluster,
		"icInterval":      icBlock,
		"clusterNote": "independentN is a count of (symbol, UTC-day) rows, not a sample size: the ~1,000 symbols graded on one day " +
			"share one market move. cluster reports the distinct-day count, the MEASURED design effect and the effective N it " +
			"implies, and both intervals resample DAYS. Below the day floor they are withheld with a reason rather than narrowed.",
	}
	if pinnedRequested {
		resp["pinnedNote"] = "no pinned weekly result stored yet — showing a live compute (the signalbt-weekly worker pins one every Sunday evening ET)"
	}
	writeJSON(w, resp)
}

// ── cluster-robust plumbing for the own-signal grade (C4) ────────────────────

// signalBTICMethod names what produced the IC interval, in the payload.
const signalBTICMethod = "percentile interval of the Spearman IC over WHOLE DAYS resampled with replacement. Ranks are recomputed " +
	"inside each resample, so the statistic bootstrapped is the same one the engine reports. A Fisher-z interval at the row " +
	"count would assert one independent (signal, return) pair per symbol-day."

// signalBTCluster grades the own-signal record with the day as the resampling
// unit and returns (hit-rate cluster grade, IC interval block).
//
// It reduces obs to ONE per (symbol, UTC-day) keeping the LATEST signal that
// day, and scores a hit as signal>0.5 predicting fwd>0 — both rules copied from
// internal/signalbt (dedupeIndependent and hitRate). They are duplicated rather
// than exported because the engine is deliberately I/O-free and interval-free;
// the coupling is named here so a change to either rule is known to need both.
// The check that they have not drifted is that cluster.p equals result.hitRate,
// which the tests assert.
func signalBTCluster(obs []signalbt.Observation, primaryLag int) (clusterstat.Result, map[string]any) {
	type key struct{ sym, day int64 }
	best := make(map[key]signalbt.Observation, len(obs))
	for _, o := range obs {
		k := key{sym: o.SymbolID, day: md.TradingDay(o.Ts)}
		if cur, ok := best[k]; !ok || o.Ts > cur.Ts {
			best[k] = o
		}
	}

	type pair struct{ sig, fwd float64 }
	byDay := map[int64][]pair{}
	cobs := make([]clusterstat.Obs, 0, len(best))
	for k, o := range best {
		f, ok := o.FwdByLag[primaryLag]
		if !ok {
			continue // no forward return at the graded lag: not in the engine's set either
		}
		// Dir stays DirNone: the engine's hit rule is "the signal's side matched
		// the realized side", which the Hit flag already carries. Supplying a
		// direction as well would have clusterstat recover the market's realized
		// direction a second way and report a breadth diagnostic derived from a
		// deadbanded [0,1] probability rather than from a position.
		cobs = append(cobs, clusterstat.Obs{Day: k.day, Hit: (o.Signal > 0.5) == (f > 0)})
		byDay[k.day] = append(byDay[k.day], pair{sig: o.Signal, fwd: f})
	}

	cluster := clusterstat.Grade(cobs)

	days := make([]int64, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	// Deterministic order so the resample draws map to the same days on every
	// run and the published interval is reproducible.
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	sigs := make([]float64, 0, len(cobs))
	fwds := make([]float64, 0, len(cobs))
	for _, d := range days {
		for _, p := range byDay[d] {
			sigs = append(sigs, p.sig)
			fwds = append(fwds, p.fwd)
		}
	}
	ic, _ := pearsonOK(rankOf(sigs), rankOf(fwds))

	icBlock := map[string]any{"point": ic, "method": signalBTICMethod}
	if len(days) < clusterstat.MinDistinctDays {
		icBlock["ci"] = nil
		icBlock["refused"] = true
		icBlock["reason"] = "no interval: " + strconv.Itoa(len(days)) + "/" + strconv.Itoa(clusterstat.MinDistinctDays) +
			" distinct days. The pairs graded on one day share one market move, so this correlation cannot be given an " +
			"honest interval yet — a narrow one would be worse than none."
		return cluster, icBlock
	}

	// Buffers hoisted out of the closure: each draw rebuilds a sample the size of
	// the whole record, and reallocating it thousands of times is the difference
	// between milliseconds and seconds on this endpoint.
	xs := make([]float64, 0, len(sigs))
	ys := make([]float64, 0, len(fwds))
	iv, ok := clusterstat.BootstrapStat(len(days), 1000, 0.05, func(idx []int) (float64, bool) {
		xs, ys = xs[:0], ys[:0]
		for _, i := range idx {
			for _, p := range byDay[days[i]] {
				xs = append(xs, p.sig)
				ys = append(ys, p.fwd)
			}
		}
		if len(xs) < 4 {
			return 0, false
		}
		// A draw with no variance left in the signal or the outcome has an
		// UNDEFINED correlation, not a zero one. Counting it as 0 would pull the
		// percentile interval toward a spuriously certain [0,0].
		return pearsonOK(rankOf(xs), rankOf(ys))
	})
	if !ok {
		icBlock["ci"] = nil
		icBlock["refused"] = true
		icBlock["reason"] = "no interval: the day resample did not produce enough usable draws"
		return cluster, icBlock
	}
	icBlock["ci"] = iv
	icBlock["refused"] = false
	icBlock["reason"] = ""
	return cluster, icBlock
}

// rankOf returns average ranks (ties share the mean of the ranks they span), so
// Pearson over rankOf is Spearman — the same statistic internal/signalbt reports.
func rankOf(xs []float64) []float64 {
	n := len(xs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return xs[idx[a]] < xs[idx[b]] })
	out := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		avg := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			out[idx[k]] = avg
		}
		i = j + 1
	}
	return out
}

// registerSignalBT wires the Stage-5 own-signal backtester read route.
func (d Deps) registerSignalBT(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/signal-backtest", d.signalBacktest)
}
