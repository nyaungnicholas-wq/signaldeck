// Stage 5 — FINRA Reg SHO worker: finra-shorts (6h tick).
//
// Ingests FINRA's FREE, registration-less Consolidated NMS daily short sale
// volume file (cdn.finra.org/equity/regsho/daily/CNMSshvolYYYYMMDD.txt —
// verified live 2026-07-06; FINRA posts it by ~6:00pm ET on the trade date)
// into the UNIVERSE-SCOPED short_volume table: only symbols we track are
// stored, out of ~12k in each file.
//
// Cadence: the 6h tick is just a heartbeat — TargetShortVolDay embeds the
// real gate (a day's file is only expected once NY time passes ~18:30 on
// that trading day; weekends + full NYSE holidays step back via marketcal),
// and the meta day-key (finra_shorts_last_day) dedups so each trade date is
// ingested exactly once. First run backfills the last ~30 TRADING days (30
// paced requests, ~15s at the client's 500ms spacing; interrupted backfills
// resume next tick — REPLACE upserts make overlap idempotent).
//
// GRACEFUL DEGRADATION: a missing file on a day the gate says should exist
// records a dq event (finra_shorts_unavailable) and retries next tick; fetch
// errors record finra_shorts_error. The fleet NEVER fails over FINRA being
// down, and nothing is ever fabricated.
//
// HONESTY (carried verbatim by the API/UI): the stored short_pct is the
// daily short sale VOLUME ratio — NOT short interest; it includes
// market-maker liquidity provision, and a high ratio is NOT directly
// bearish.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/finra"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// finraShortsLastDayKey holds the last ingested trade date (YYYY-MM-DD).
	finraShortsLastDayKey = "finra_shorts_last_day"
	// finraShortsBackfillKey marks the one-time ~30-trading-day backfill done.
	finraShortsBackfillKey = "finra_shorts_backfill_v1"
	// finraShortsPublishMins: FINRA posts daily files "no later than 6:00pm ET
	// of the same trade date" — expect the file only after 18:30 ET (slack).
	finraShortsPublishMins = 18*60 + 30
	// finraShortsBackfillDays is the default first-run backfill window in
	// TRADING days.
	finraShortsBackfillDays = 30
)

// ShortVolPoller is the finra-shorts worker.
type ShortVolPoller struct {
	St     *store.Store
	Client *finra.Client
	// BackfillDays overrides the first-run trading-day backfill window
	// (tests); 0 = finraShortsBackfillDays.
	BackfillDays int
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *ShortVolPoller) Name() string            { return "finra-shorts" }
func (w *ShortVolPoller) Interval() time.Duration { return 6 * time.Hour }

func (w *ShortVolPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// isTradingDay: weekday and not a full NYSE holiday (half-days still trade
// and still get a daily file).
func isTradingDay(d time.Time) bool {
	if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	return !marketcal.IsFullHoliday(d)
}

// prevTradingDay steps back to the previous NYSE trading day.
func prevTradingDay(d time.Time) time.Time {
	for {
		d = d.AddDate(0, 0, -1)
		if isTradingDay(d) {
			return d
		}
	}
}

// TargetShortVolDay is the PURE gate: the most recent NY trading day whose
// Reg SHO daily file should exist at `now` — today when today trades and NY
// time is past ~18:30 (FINRA's same-day publish deadline + slack), else the
// previous trading day (weekends and full holidays skipped via marketcal).
func TargetShortVolDay(now time.Time) time.Time {
	et := now.In(marketcal.Loc())
	day := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, marketcal.Loc())
	if isTradingDay(day) && et.Hour()*60+et.Minute() >= finraShortsPublishMins {
		return day
	}
	return prevTradingDay(day)
}

func (w *ShortVolPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		// Shouldn't happen — FINRA needs no key — but degrade honestly.
		return "skipped: no finra client", nil
	}
	now := w.now()
	target := TargetShortVolDay(now)
	key := target.Format("2006-01-02")

	// Universe scope: ALL active tracked stocks (streamed hot set AND the
	// broad daily-only universe), ticker → symbol_id.
	all, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	tickerToID := make(map[string]int64)
	for _, s := range all {
		if s.Market == md.Stocks {
			tickerToID[strings.ToUpper(s.Symbol)] = s.ID
		}
	}
	if len(tickerToID) == 0 {
		return "skipped: no tracked stocks yet", nil
	}

	// One-time backfill of the last ~30 trading days (resumes if interrupted).
	if done, _ := w.St.GetMeta(ctx, finraShortsBackfillKey); done == "" {
		return w.backfill(ctx, target, key, tickerToID, now)
	}

	// Day-key dedup: each trade date ingested exactly once.
	if last, _ := w.St.GetMeta(ctx, finraShortsLastDayKey); last == key {
		return fmt.Sprintf("up to date (%s)", key), nil
	}
	// Self-healing: if the meta key was lost but the day is already stored,
	// don't refetch — just restore the cursor.
	if has, herr := w.St.HasShortVolumeDay(ctx, key); herr == nil && has {
		_ = w.St.SetMeta(ctx, finraShortsLastDayKey, key)
		return fmt.Sprintf("up to date (%s, day already stored)", key), nil
	}

	stored, fileRows, skipped, err := w.ingestDay(ctx, target, tickerToID)
	if errors.Is(err, finra.ErrNotAvailable) {
		// The gate says this trading day's file should be up by now — record
		// the gap honestly and retry next tick (day key NOT set).
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now.Unix(), Kind: "finra_shorts_unavailable",
			Detail: fmt.Sprintf("Reg SHO daily file for %s not available after publish deadline", key),
		})
		return fmt.Sprintf("file for %s not available yet (dq recorded; will retry)", key), nil
	}
	if err != nil {
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now.Unix(), Kind: "finra_shorts_error",
			Detail: fmt.Sprintf("%s: %v", key, err),
		})
		return fmt.Sprintf("fetch %s failed (dq recorded; will retry): %v", key, err), nil
	}
	_ = w.St.SetMeta(ctx, finraShortsLastDayKey, key)
	detail := fmt.Sprintf("day %s: %d tracked rows upserted (file had %d)", key, stored, fileRows)
	if skipped > 0 {
		detail += fmt.Sprintf("; %d malformed line(s) skipped", skipped)
	}
	return detail, nil
}

// backfill ingests the last BackfillDays TRADING days ending at target, paced
// by the client's own limiter. Unavailable days are counted (files can be
// re-posted/updated by FINRA, but gaps are never fabricated); a hard error
// aborts WITHOUT setting the done-key so the next tick resumes (idempotent
// REPLACE upserts make the overlap free).
func (w *ShortVolPoller) backfill(ctx context.Context, target time.Time, key string,
	tickerToID map[string]int64, now time.Time,
) (string, error) {
	days := w.BackfillDays
	if days <= 0 {
		days = finraShortsBackfillDays
	}
	d := target
	okDays, rowsTotal, missing := 0, 0, 0
	targetStored := false
	for i := 0; i < days; i++ {
		stored, _, _, err := w.ingestDay(ctx, d, tickerToID)
		switch {
		case errors.Is(err, finra.ErrNotAvailable):
			missing++
		case err != nil:
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: now.Unix(), Kind: "finra_shorts_error",
				Detail: fmt.Sprintf("backfill %s: %v", d.Format("2006-01-02"), err),
			})
			return fmt.Sprintf("backfill interrupted at %s after %d day(s), %d rows (dq recorded; will resume next tick)",
				d.Format("2006-01-02"), okDays, rowsTotal), nil
		default:
			okDays++
			rowsTotal += stored
			if i == 0 {
				targetStored = true
			}
		}
		d = prevTradingDay(d)
	}
	_ = w.St.SetMeta(ctx, finraShortsBackfillKey, now.Format(time.RFC3339))
	if targetStored {
		// Only mark the newest day done when it actually landed — if FINRA was
		// late with it, the next tick's single-day path retries it.
		_ = w.St.SetMeta(ctx, finraShortsLastDayKey, key)
	}
	return fmt.Sprintf("backfilled %d trading day(s), %d tracked rows (%d file(s) unavailable)",
		okDays, rowsTotal, missing), nil
}

// ingestDay fetches one trade date's file and upserts the tracked-symbol
// subset (ratio computed here; the rest of the ~12k rows are discarded).
func (w *ShortVolPoller) ingestDay(ctx context.Context, day time.Time,
	tickerToID map[string]int64,
) (stored, fileRows, skipped int, err error) {
	rows, skipped, err := w.Client.FetchDaily(ctx, day)
	if err != nil {
		return 0, 0, skipped, err
	}
	batch := make([]store.ShortVolumeRow, 0, 512)
	for _, r := range rows {
		id, ok := tickerToID[r.Symbol]
		if !ok {
			continue
		}
		batch = append(batch, store.ShortVolumeRow{
			SymbolID: id, Day: r.Day,
			ShortVol: r.ShortVol, ShortExempt: r.ShortExempt, TotalVol: r.TotalVol,
			ShortPct: finra.Ratio(r.ShortVol, r.TotalVol),
		})
	}
	if err := w.St.UpsertShortVolume(ctx, batch); err != nil {
		return 0, len(rows), skipped, err
	}
	return len(batch), len(rows), skipped, nil
}
