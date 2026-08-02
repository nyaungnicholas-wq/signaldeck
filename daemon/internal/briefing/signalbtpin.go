package briefing

// ── STAGE 2: WEEKLY SIGNAL-BACKTEST PIN ──────────────────────────────────────
//
// Every Sunday evening (NY, one hour after the weekly self-report; meta
// week-key dedup) this worker runs the internal/signalbt evaluation for each
// prediction horizon and PINS the result: the full graded Result JSON is
// stored in meta under signalbt_weekly:<sunday> (immutable per-week record)
// and signalbt_latest (what /api/signal-backtest?pinned=1 serves), plus ONE
// insight (kind "signalbt_weekly") summarizing it honestly — IC, quintile
// spread, independent-N, and whether the honesty gate withheld the numbers.
//
// The evaluation uses the SAME parameters as the live endpoint (lags, obs cap,
// primary lag, engine defaults via signalbt.Default*), so "pinned" vs
// "recompute live" on the web page differ only by WHEN they ran, never by what
// they measured. Summary statistics are computed on the full equity curve;
// only the STORED curve may be decimated (flagged EquityDownsampled).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signalbt"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// SignalBTKind is the insight kind stored in the data blob (json data.kind).
const SignalBTKind = "signalbt_weekly"

// metaSignalBTWeekKey remembers the last NY week (its Sunday's date) a pin was
// written — the dedup gate.
const metaSignalBTWeekKey = "signalbt_weekly_last_week"

// signalBTRunHour is the NY hour (on Sunday) from which the pin may fire —
// one hour after the weekly self-report so the two land in order.
const signalBTRunHour = 18

// signalBTHorizons are the horizons the weekly evaluation grades — the same
// set the live endpoint serves.
var signalBTHorizons = []md.Horizon{md.H1d, md.H1w}

// SignalBTPinWorker runs + stores the weekly own-signal backtest (implements
// workers.Worker). It ticks every 30 minutes but fires once per NY week, at/
// after 6:00pm Sunday ET (catching up later in the week if the daemon was down).
type SignalBTPinWorker struct {
	St *store.Store
	// Loc overrides the timezone (tests); nil = America/New_York.
	Loc *time.Location
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

// Name implements workers.Worker.
func (w *SignalBTPinWorker) Name() string { return "signalbt-weekly" }

// Interval implements workers.Worker.
func (w *SignalBTPinWorker) Interval() time.Duration { return 30 * time.Minute }

// NextFire implements workers.ScheduledWorker: Sunday 18:00 ET, one hour after
// the weekly self-report, so the two land in order.
//
// WHY: this worker ticked every 30 minutes to do a once-a-week job — 336
// wakeups per useful run, 335 of which wrote a "waiting" row into worker_runs.
// The week-key gate in Run is UNCHANGED and still authoritative (it is what
// makes a catch-up idempotent); the schedule now agrees with it instead of
// hammering it.
func (w *SignalBTPinWorker) NextFire(last, now time.Time) time.Time {
	return weeklyNextFire(last, now, time.Sunday, signalBTRunHour)
}

// Run applies the once-per-NY-week gate, evaluates every horizon, stores the
// pinned snapshot, and writes the summarizing insight.
func (w *SignalBTPinWorker) Run(ctx context.Context) (string, error) {
	loc := w.Loc
	if loc == nil {
		loc = NYLoc()
	}
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	lastKey, err := w.St.GetMeta(ctx, metaSignalBTWeekKey)
	if err != nil {
		return "", err
	}
	run, weekKey := ShouldRunWeekly(now, lastKey, loc, signalBTRunHour)
	if !run {
		return fmt.Sprintf("waiting (fires once per week from Sunday %d:00 ET; last=%s)", signalBTRunHour, lastKey), nil
	}

	pinned := signalbt.Pinned{
		DayKey:          weekKey,
		ComputedTs:      now.Unix(),
		BenchmarkSymbol: "SPY",
		Results:         map[string]signalbt.Result{},
	}
	for _, h := range signalBTHorizons {
		rawObs, err := w.St.SignalBacktestObs(ctx, h, signalbt.DefaultDecayLags, signalbt.DefaultMaxObs)
		if err != nil {
			return "", fmt.Errorf("assemble %s: %w", h, err)
		}
		obs := make([]signalbt.Observation, len(rawObs))
		for i, o := range rawObs {
			obs[i] = signalbt.Observation{
				SymbolID: o.SymbolID, Ts: o.Ts, Signal: o.Signal, FwdByLag: o.FwdByLag,
			}
		}
		spyTs, spyClose, err := w.St.SPYDailyCloses(ctx, signalbt.DefaultMaxObs)
		if err != nil {
			return "", fmt.Errorf("spy closes: %w", err)
		}
		benchmark := signalbt.BenchmarkCurve(obs, spyTs, spyClose)
		if len(benchmark) > 0 {
			pinned.HasBenchmark = true
		}
		res := signalbt.Backtest(obs, benchmark, string(h), signalbt.Params{
			PrimaryLag: signalbt.PrimaryLagFor(string(h)),
			DecayLags:  signalbt.DefaultDecayLags,
		})
		// Bound the STORED curve; all summary stats were computed on the full
		// one inside Backtest, so returns are unchanged and the flag says so.
		var ds bool
		res.Equity, ds = signalbt.DownsampleEquity(res.Equity, signalbt.MaxStoredEquityPoints)
		if ds {
			pinned.EquityDownsampled = true
		}
		pinned.Results[string(h)] = res
	}

	if err := w.St.SetJSON(ctx, signalbt.MetaKeyWeekly(weekKey), pinned); err != nil {
		return "", err
	}
	if err := w.St.SetJSON(ctx, signalbt.MetaKeyLatest, pinned); err != nil {
		return "", err
	}

	headline, body := composeSignalBT(pinned, loc)
	data, err := json.Marshal(struct {
		Kind   string          `json:"kind"`
		DayKey string          `json:"dayKey"`
		Pinned signalbt.Pinned `json:"pinned"`
	}{Kind: SignalBTKind, DayKey: weekKey, Pinned: pinned})
	if err != nil {
		return "", err
	}
	if err := w.St.InsertInsight(ctx, md.Insight{
		Scope: "market", Ts: now.Unix(), Headline: headline, Body: body, Data: string(data),
	}); err != nil {
		return "", err
	}
	if err := w.St.SetMeta(ctx, metaSignalBTWeekKey, weekKey); err != nil {
		return "", err
	}
	return "pinned weekly signal-backtest for week of " + weekKey, nil
}

// composeSignalBT renders the honest deterministic summary of a pinned
// evaluation: per horizon either the gated truth ("withheld, k/threshold") or
// the measured IC / quintile spread / hit-rate with its N — always labeled
// backtested, never live.
func composeSignalBT(p signalbt.Pinned, loc *time.Location) (headline, body string) {
	sunday, _ := time.ParseInLocation("2006-01-02", p.DayKey, loc)
	headline = "Weekly signal backtest — week of " + sunday.Format("Jan 2")

	var b []string
	for _, h := range signalBTHorizons {
		res, ok := p.Results[string(h)]
		if !ok {
			continue
		}
		if res.Gated {
			b = append(b, fmt.Sprintf("%s: GATED — %d of %d independent (symbol, day) resolutions; skill numbers withheld until the gate clears.",
				h, res.IndependentN, res.MinIndependentN))
			continue
		}
		b = append(b, fmt.Sprintf("%s: IC %.3f, quintile spread %+.2f%%, hit-rate %.0f%% over %d independent observations (net of %.1f bps/side).",
			h, res.IC, res.QuintileSpread*100, res.HitRate*100, res.IndependentN, res.CostBps))
	}
	if len(b) == 0 {
		b = append(b, "No horizons evaluated — the feature store held no resolved observations.")
	}
	b = append(b, "These are BACKTESTED replay numbers of the platform's own committed signals (no lookahead), not a live track record.")
	b = append(b, disclaimer)
	return headline, strings.Join(b, " ")
}
