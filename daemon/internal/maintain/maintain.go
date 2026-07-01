// Package maintain holds the housekeeping agents: bar rollups + retention,
// score-outcome resolution (the honesty backtest's feeder), and the
// data-quality auditor. All are periodic workers.
package maintain

import (
	"context"
	"fmt"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── Downsampler ─────────────────────────────────────────────────────────

// Downsampler rolls 1m bars into 1h and enforces retention. It NEVER builds
// daily bars from intraday rollups — official daily bars come from the
// sources (Alpaca 1Day, Kraken interval=1440), so a partial trading day can
// never masquerade as a real daily bar.
type Downsampler struct {
	St *store.Store
	// retention
	Keep1m    time.Duration // default 90 days
	KeepSnaps time.Duration // default 7 days
}

// Name implements workers.Worker.
func (d *Downsampler) Name() string { return "downsampler" }

// Interval implements workers.Worker.
func (d *Downsampler) Interval() time.Duration { return 5 * time.Minute }

// Run rolls up recent minutes and prunes old rows.
func (d *Downsampler) Run(ctx context.Context) (string, error) {
	syms, err := d.St.ListSymbols(ctx, false)
	if err != nil {
		return "", err
	}
	now := time.Now()
	from := now.Add(-72 * time.Hour).Unix()
	for _, s := range syms {
		if err := d.St.Rollup(ctx, s.ID, md.TF1m, md.TF1h, 3600, from, now.Unix()); err != nil {
			return "", fmt.Errorf("rollup %s: %w", s.Symbol, err)
		}
	}
	keep1m := d.Keep1m
	if keep1m == 0 {
		keep1m = 90 * 24 * time.Hour
	}
	keepSnaps := d.KeepSnaps
	if keepSnaps == 0 {
		keepSnaps = 7 * 24 * time.Hour
	}
	prunedBars, err := d.St.PruneBars(ctx, md.TF1m, now.Add(-keep1m).Unix())
	if err != nil {
		return "", err
	}
	prunedSnaps, err := d.St.PruneSnaps(ctx, now.Add(-keepSnaps).Unix())
	if err != nil {
		return "", err
	}
	if err := d.St.PruneWorkerRuns(ctx, 2000); err != nil {
		return "", err
	}
	return fmt.Sprintf("rolled up %d symbols; pruned %d 1m bars, %d snaps", len(syms), prunedBars, prunedSnaps), nil
}

// ── OutcomeResolver ─────────────────────────────────────────────────────

// OutcomeResolver fills score_outcomes with realized forward returns once a
// score's horizon window has closed. Weekends/holidays are handled naturally:
// the forward price is the first bar AT OR AFTER the target time, so a Friday
// 1d score resolves against Monday's bar.
type OutcomeResolver struct {
	St *store.Store
}

// Name implements workers.Worker.
func (o *OutcomeResolver) Name() string { return "outcome-resolver" }

// Interval implements workers.Worker.
func (o *OutcomeResolver) Interval() time.Duration { return 10 * time.Minute }

func horizonSeconds(h md.Horizon) int64 {
	switch h {
	case md.H1h:
		return 3600
	case md.H1d:
		return 86400
	default:
		return 7 * 86400 // calendar week ≈ 5 trading days via at-or-after
	}
}

func horizonTF(h md.Horizon) md.Timeframe {
	if h == md.H1h {
		return md.TF1m
	}
	return md.TF1d
}

// Run resolves up to 500 pending outcomes per pass.
func (o *OutcomeResolver) Run(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	pending, err := o.St.UnresolvedOutcomes(ctx, now-3600, 500) // 1h is the shortest window
	if err != nil {
		return "", err
	}
	resolved, voided, waiting := 0, 0, 0
	for _, p := range pending {
		target := p.Ts + horizonSeconds(p.Horizon)
		if now < target {
			waiting++
			continue
		}
		tf := horizonTF(p.Horizon)
		base, okBase, err := o.St.BarAtOrBefore(ctx, p.SymbolID, tf, p.Ts)
		if err != nil {
			return "", err
		}
		fwd, okFwd, err := o.St.BarAtOrAfter(ctx, p.SymbolID, tf, target)
		if err != nil {
			return "", err
		}
		switch {
		case okBase && okFwd && base.Close > 0:
			// Guard: if the "forward" bar is absurdly late (>3x the window),
			// the data has a hole — resolving would attribute a multi-week
			// move to a 1d score. Void it instead.
			if fwd.Ts-target > 3*horizonSeconds(p.Horizon) {
				if err := o.St.ResolveOutcomeVoid(ctx, p.SymbolID, p.Horizon, p.Ts); err != nil {
					return "", err
				}
				voided++
				continue
			}
			ret := fwd.Close/base.Close - 1
			if err := o.St.ResolveOutcome(ctx, p.SymbolID, p.Horizon, p.Ts, ret); err != nil {
				return "", err
			}
			resolved++
		case now-p.Ts > 30*24*3600:
			// A month with no forward data: permanently unresolvable.
			if err := o.St.ResolveOutcomeVoid(ctx, p.SymbolID, p.Horizon, p.Ts); err != nil {
				return "", err
			}
			voided++
		default:
			waiting++ // data not in yet; try next pass
		}
	}
	return fmt.Sprintf("resolved %d, voided %d, waiting %d", resolved, voided, waiting), nil
}

// ── DQAuditor ───────────────────────────────────────────────────────────

// DQAuditor detects stale feeds and bar gaps per active symbol and records
// them as dq_events (rate-limited via meta keys, one per symbol per hour).
type DQAuditor struct {
	St *store.Store
}

// Name implements workers.Worker.
func (a *DQAuditor) Name() string { return "dq-auditor" }

// Interval implements workers.Worker.
func (a *DQAuditor) Interval() time.Duration { return 5 * time.Minute }

// usMarketLikelyOpen is a deliberately loose Mon–Fri 13:00–21:30 UTC window
// (covers EST/EDT regular hours). Staleness of stock bars outside it is
// normal, not an incident. Documented imprecision beats a tz dependency.
func usMarketLikelyOpen(t time.Time) bool {
	u := t.UTC()
	if wd := u.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	mins := u.Hour()*60 + u.Minute()
	return mins >= 13*60 && mins <= 21*60+30
}

// Run checks freshness for every active symbol.
func (a *DQAuditor) Run(ctx context.Context) (string, error) {
	syms, err := a.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	flagged := 0
	for _, s := range syms {
		latest, err := a.St.LatestBarTs(ctx, s.ID, md.TF1m)
		if err != nil {
			return "", err
		}
		var stale bool
		var detail string
		age := now.Unix() - latest
		switch s.Market {
		case md.Crypto:
			stale = latest > 0 && age > 45*60 // Kraken minute refresh cadence + slack
			detail = fmt.Sprintf("last 1m bar %dm old (crypto trades 24/7)", age/60)
		case md.Stocks:
			stale = latest > 0 && age > 20*60 && usMarketLikelyOpen(now)
			detail = fmt.Sprintf("last 1m bar %dm old during likely market hours", age/60)
		}
		if !stale {
			continue
		}
		// Rate-limit: one event per symbol per hour.
		key := "dq_last_stale_" + s.Symbol + string(s.Market)
		if lastStr, _ := a.St.GetMeta(ctx, key); lastStr != "" {
			if last, _ := strconv.ParseInt(lastStr, 10, 64); now.Unix()-last < 3600 {
				continue
			}
		}
		if err := a.St.SetMeta(ctx, key, strconv.FormatInt(now.Unix(), 10)); err != nil {
			return "", err
		}
		sid := s.ID
		if err := a.St.InsertDQ(ctx, md.DQEvent{SymbolID: &sid, Ts: now.Unix(), Kind: "stale", Detail: detail}); err != nil {
			return "", err
		}
		flagged++
	}
	return fmt.Sprintf("checked %d active symbols, flagged %d", len(syms), flagged), nil
}
