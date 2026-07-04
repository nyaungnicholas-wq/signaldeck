// The anomaly-scanner worker: runs the pure detectors in anomaly.go over
// stored data on two cadences —
//
//   - HOT SET, every 5-minute tick: every active crypto symbol (order-book
//     imbalance from snapshots_1s + realized-vol/TR-spike + same-time-of-day
//     volume on 1m bars — crypto never closes) and every STREAMED stock
//     (stream=1: volume-side imbalance proxy + vol + volume on 1m bars),
//     gated on marketcal.OpenForBars so closed-market stock scans are skipped
//     entirely (stale bars would only ever z-score against themselves);
//   - DAILY-ONLY UNIVERSE, once per ET trading day (meta-key date cursor):
//     every active non-streamed stock on daily bars (vol + volume only — no
//     intraday data exists for these, so no imbalance proxy is fabricated).
//
// Storage dedup (idx_anomalies_dedup, one row per symbol+kind+hour) makes
// every insert idempotent, so a persisting condition pings once per hour and
// re-runs re-insert nothing.
package anomaly

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Scan windows (constants, stated in every detail string via the detectors).
const (
	imbRecentSec   = 300  // crypto imbalance: recent 5m of snapshots…
	imbBaseSec     = 3600 // …vs the trailing 60m of snapshots
	minuteWindow   = 30   // 1m-bar detectors: 30-bar recent window
	minuteBaseWins = 8    // …vs ≥8 trailing 30-bar windows
	minuteBarsMax  = 3000 // 1m bars fetched per symbol (≈7 stock sessions)
	volumeMinDays  = 5    // same-time-of-day baseline needs ≥5 prior days
	dailyVolWindow = 5    // 1d vol: realized vol of last 5 daily bars
	dailyVolBase   = 10   // …vs ≥10 trailing 5-bar windows
	dailyBarsMax   = 200  // 1d bars fetched per symbol in the daily sweep
	dailyVolumeN   = 60   // 1d volume baseline: up to 60 trailing days
	dailyVolumeMin = 20   // …needing ≥20 of them
)

// metaDailySweepKey stores the ET date (YYYY-MM-DD) of the last completed
// daily-universe sweep, so it runs exactly once per trading day.
const metaDailySweepKey = "anomaly_daily_sweep_day"

// Scanner is the anomaly-scanner worker (implements workers.Worker).
type Scanner struct {
	St *store.Store
	// Threshold overrides the |z| threshold; 0 ⇒ SIGNALDECK_ANOM_Z (default 2.5).
	Threshold float64
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

// Name implements workers.Worker.
func (w *Scanner) Name() string { return "anomaly-scanner" }

// Interval implements workers.Worker.
func (w *Scanner) Interval() time.Duration { return 5 * time.Minute }

func (w *Scanner) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Scanner) threshold() float64 {
	if w.Threshold > 0 {
		return w.Threshold
	}
	return Threshold(os.Getenv("SIGNALDECK_ANOM_Z"))
}

// Run performs one scan pass. Per-symbol read errors abort the run (they are
// store-level failures, not data insufficiency); detectors returning "no
// signal" are the normal quiet path.
func (w *Scanner) Run(ctx context.Context) (string, error) {
	now := w.now()
	thr := w.threshold()

	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}

	stocksOpen := marketcal.OpenForBars(now)
	hotScanned, hotNew := 0, 0

	for _, s := range syms {
		var events []Event
		switch {
		case s.Market == md.Crypto:
			evs, err := w.scanCrypto(ctx, s, now, thr)
			if err != nil {
				return "", err
			}
			events = evs
		case s.Market == md.Stocks && s.Stream && stocksOpen:
			evs, err := w.scanStock1m(ctx, s, thr)
			if err != nil {
				return "", err
			}
			events = evs
		default:
			continue
		}
		hotScanned++
		n, err := w.insert(ctx, s.ID, events)
		if err != nil {
			return "", err
		}
		hotNew += n
	}

	// Daily-only universe: once per ET trading day.
	dailyScanned, dailyNew, dailyRan, err := w.dailySweep(ctx, syms, now, thr)
	if err != nil {
		return "", err
	}

	detail := fmt.Sprintf("hot: %d symbol(s) scanned, %d new anomal(ies)", hotScanned, hotNew)
	if !stocksOpen {
		detail += " (stock 1m scans skipped: market closed)"
	}
	if dailyRan {
		detail += fmt.Sprintf("; daily sweep: %d symbol(s), %d new", dailyScanned, dailyNew)
	}
	return detail, nil
}

// scanCrypto runs imbalance (real order-book snapshots) + vol + volume
// (1m bars) for one crypto symbol. Crypto never closes: no calendar gate.
func (w *Scanner) scanCrypto(ctx context.Context, s md.Symbol, now time.Time, thr float64) ([]Event, error) {
	var events []Event

	from := now.Unix() - imbRecentSec - imbBaseSec - 1
	snaps, err := w.St.Snaps(ctx, s.ID, from, now.Unix()+1, 0)
	if err != nil {
		return nil, err
	}
	if ev, ok := DetectImbalanceSnaps(snaps, imbRecentSec, imbBaseSec, thr); ok {
		events = append(events, ev)
	}

	// Crypto 1m bars trade around the clock, so ~7 calendar days ≈ 10080 bars;
	// fetch that much so the same-time-of-day volume baseline can form.
	bars, err := w.St.LastBars(ctx, s.ID, md.TF1m, 7*24*60)
	if err != nil {
		return nil, err
	}
	if ev, ok := DetectVolatility(bars, minuteWindow, minuteBaseWins, thr, "1m"); ok {
		events = append(events, ev)
	}
	if ev, ok := DetectVolumeMinute(bars, minuteWindow, volumeMinDays, thr, time.UTC); ok {
		events = append(events, ev)
	}
	return events, nil
}

// scanStock1m runs the 1m-bar detectors for one STREAMED stock: the
// volume-side imbalance proxy, realized vol / TR spike, and same-time-of-day
// volume (on the exchange clock). Caller already gated on OpenForBars.
func (w *Scanner) scanStock1m(ctx context.Context, s md.Symbol, thr float64) ([]Event, error) {
	bars, err := w.St.LastBars(ctx, s.ID, md.TF1m, minuteBarsMax)
	if err != nil {
		return nil, err
	}
	var events []Event
	if ev, ok := DetectImbalanceBars(bars, minuteWindow, minuteBaseWins, thr); ok {
		events = append(events, ev)
	}
	if ev, ok := DetectVolatility(bars, minuteWindow, minuteBaseWins, thr, "1m"); ok {
		events = append(events, ev)
	}
	if ev, ok := DetectVolumeMinute(bars, minuteWindow, volumeMinDays, thr, marketcal.Loc()); ok {
		events = append(events, ev)
	}
	return events, nil
}

// dailySweep scans the DAILY-ONLY stock universe (active, stream=0) on daily
// bars, at most once per ET calendar day and only on trading days (weekend /
// full-holiday runs leave the cursor untouched so the next trading day runs).
func (w *Scanner) dailySweep(ctx context.Context, syms []md.Symbol, now time.Time, thr float64) (scanned, created int, ran bool, err error) {
	et := now.In(marketcal.Loc())
	if wd := et.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return 0, 0, false, nil
	}
	if marketcal.IsFullHoliday(now) {
		return 0, 0, false, nil
	}
	today := et.Format("2006-01-02")
	if last, _ := w.St.GetMeta(ctx, metaDailySweepKey); last == today {
		return 0, 0, false, nil
	}

	for _, s := range syms {
		if s.Market != md.Stocks || s.Stream {
			continue
		}
		bars, err := w.St.LastBars(ctx, s.ID, md.TF1d, dailyBarsMax)
		if err != nil {
			return scanned, created, true, err
		}
		var events []Event
		if ev, ok := DetectVolatility(bars, dailyVolWindow, dailyVolBase, thr, "1d"); ok {
			events = append(events, ev)
		}
		if ev, ok := DetectVolumeDaily(bars, dailyVolumeN, dailyVolumeMin, thr); ok {
			events = append(events, ev)
		}
		scanned++
		n, err := w.insert(ctx, s.ID, events)
		if err != nil {
			return scanned, created, true, err
		}
		created += n
	}
	// Advance the date cursor only after the whole sweep succeeded, so a
	// partial failure re-runs (inserts are dedup-idempotent).
	if err := w.St.SetMeta(ctx, metaDailySweepKey, today); err != nil {
		return scanned, created, true, err
	}
	return scanned, created, true, nil
}

func (w *Scanner) insert(ctx context.Context, symbolID int64, events []Event) (int, error) {
	created := 0
	for _, ev := range events {
		fresh, err := w.St.InsertAnomaly(ctx, store.AnomalyRow{
			SymbolID: symbolID, Ts: ev.Ts, Kind: ev.Kind, Z: ev.Z, Detail: ev.Detail,
		})
		if err != nil {
			return created, err
		}
		if fresh {
			created++
		}
	}
	return created, nil
}
