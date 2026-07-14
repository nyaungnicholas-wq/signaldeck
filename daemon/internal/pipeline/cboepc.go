// DATA-EXPANSION wave — CBOE daily put/call worker: cboe-pc (6h tick).
//
// Ingests CBOE's free per-day options market statistics document
// (cdn.cboe.com/data/us/options/market_statistics/daily/{YYYY-MM-DD}
// _daily_options — probed + verified live 2026-07-10) into the cboe_pc
// table: market-wide TOTAL/INDEX/EQUITY/VIX put/call ratios + all-products
// volumes, one row per trade date.
//
// Cadence mirrors finra-shorts: the 6h tick is a heartbeat; the SAME pure
// TargetShortVolDay gate (a day's stats are expected only after ~18:30 ET on
// that trading day; weekends + full NYSE holidays step back via marketcal)
// decides which day to expect, and a meta day-key dedups each trade date to
// exactly one ingest. First run backfills ~30 trading days (paced requests;
// interrupted backfills resume next tick — REPLACE upserts make overlap
// idempotent). CBOE's CDN 403s absent days ⇒ honest skip; a file missing
// past the deadline records a dq event (cboe_pc_unavailable) and retries.
// The fleet NEVER fails over CBOE being down.
//
// HONESTY (carried verbatim by the API/UI): the put/call ratio is a
// market-wide positioning/hedging gauge — index puts are largely hedges, so
// a high index P/C is NOT directly bearish, and the equity-only ratio has a
// different (retail-tilted) meaning. Descriptive context only, never a
// scored factor and not advice.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cboe"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// cboePCLastDayKey holds the last ingested trade date (YYYY-MM-DD).
	cboePCLastDayKey = "cboe_pc_last_day"
	// cboePCBackfillKey marks the one-time ~30-trading-day backfill done.
	cboePCBackfillKey = "cboe_pc_backfill_v1"
	// cboePCBackfillDays is the default first-run backfill window (trading days).
	cboePCBackfillDays = 30
)

// CboePCPoller is the cboe-pc worker.
type CboePCPoller struct {
	St     *store.Store
	Client *cboe.Client
	// BackfillDays overrides the first-run trading-day backfill window
	// (tests); 0 = cboePCBackfillDays.
	BackfillDays int
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *CboePCPoller) Name() string            { return "cboe-pc" }
func (w *CboePCPoller) Interval() time.Duration { return 6 * time.Hour }

func (w *CboePCPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *CboePCPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		// Shouldn't happen — CBOE needs no key — but degrade honestly.
		return "skipped: no cboe client", nil
	}
	now := w.now()
	// Same publish gate as the Reg SHO daily files: both post after the close,
	// so the shared ~18:30 ET deadline + trading-day stepping applies as-is.
	target := TargetShortVolDay(now)
	key := target.Format("2006-01-02")

	// One-time backfill of the last ~30 trading days (resumes if interrupted).
	if done, _ := w.St.GetMeta(ctx, cboePCBackfillKey); done == "" {
		return w.backfill(ctx, target, key, now)
	}

	// Day-key dedup: each trade date ingested exactly once.
	if last, _ := w.St.GetMeta(ctx, cboePCLastDayKey); last == key {
		return fmt.Sprintf("up to date (%s)", key), nil
	}
	// Self-healing: meta key lost but the day already stored ⇒ restore cursor.
	if has, herr := w.St.HasCboePCDay(ctx, key); herr == nil && has {
		_ = w.St.SetMeta(ctx, cboePCLastDayKey, key)
		return fmt.Sprintf("up to date (%s, day already stored)", key), nil
	}

	stats, err := w.Client.FetchDaily(ctx, target)
	if errors.Is(err, cboe.ErrNotAvailable) {
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now.Unix(), Kind: "cboe_pc_unavailable",
			Detail: fmt.Sprintf("daily options statistics for %s not available after publish deadline", key),
		})
		return fmt.Sprintf("stats for %s not available yet (dq recorded; will retry)", key), nil
	}
	if err != nil {
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now.Unix(), Kind: "cboe_pc_error",
			Detail: fmt.Sprintf("%s: %v", key, err),
		})
		return fmt.Sprintf("fetch %s failed (dq recorded; will retry): %v", key, err), nil
	}
	if err := w.St.UpsertCboePC(ctx, statsToRow(stats)); err != nil {
		return "", err
	}
	_ = w.St.SetMeta(ctx, cboePCLastDayKey, key)
	return fmt.Sprintf("day %s: total P/C %.2f (equity %.2f, index %.2f)",
		key, stats.TotalPC, stats.EquityPC, stats.IndexPC), nil
}

// backfill ingests the last BackfillDays TRADING days ending at target, paced
// by the client's own limiter. Unavailable days are counted (never
// fabricated); a hard error aborts WITHOUT setting the done-key so the next
// tick resumes (idempotent REPLACE upserts make the overlap free).
func (w *CboePCPoller) backfill(ctx context.Context, target time.Time, key string, now time.Time) (string, error) {
	days := w.BackfillDays
	if days <= 0 {
		days = cboePCBackfillDays
	}
	d := target
	okDays, missing := 0, 0
	targetStored := false
	for i := 0; i < days; i++ {
		stats, err := w.Client.FetchDaily(ctx, d)
		switch {
		case errors.Is(err, cboe.ErrNotAvailable):
			missing++
		case err != nil:
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: now.Unix(), Kind: "cboe_pc_error",
				Detail: fmt.Sprintf("backfill %s: %v", d.Format("2006-01-02"), err),
			})
			return fmt.Sprintf("backfill interrupted at %s after %d day(s) (dq recorded; will resume next tick)",
				d.Format("2006-01-02"), okDays), nil
		default:
			if uerr := w.St.UpsertCboePC(ctx, statsToRow(stats)); uerr != nil {
				return "", uerr
			}
			okDays++
			if i == 0 {
				targetStored = true
			}
		}
		d = prevTradingDay(d)
	}
	_ = w.St.SetMeta(ctx, cboePCBackfillKey, now.Format(time.RFC3339))
	if targetStored {
		// Only mark the newest day done when it actually landed — if CBOE was
		// late with it, the next tick's single-day path retries it.
		_ = w.St.SetMeta(ctx, cboePCLastDayKey, key)
	}
	return fmt.Sprintf("backfilled %d trading day(s) (%d unavailable)", okDays, missing), nil
}

func statsToRow(s cboe.Stats) store.CboePCRow {
	return store.CboePCRow{
		Day: s.Day, TotalPC: s.TotalPC, IndexPC: s.IndexPC, EquityPC: s.EquityPC,
		VIXPC: s.VIXPC, CallVol: s.CallVol, PutVol: s.PutVol, TotalVol: s.TotalVol,
	}
}
