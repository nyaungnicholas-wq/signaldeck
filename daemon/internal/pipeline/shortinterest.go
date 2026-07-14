// DATA-EXPANSION wave — FINRA bi-monthly short interest worker:
// finra-shortint (12h tick).
//
// Ingests FINRA's FREE bi-monthly short interest files
// (cdn.finra.org/equity/otcmarket/biweekly/shrtYYYYMMDD.csv — settlement
// dates are the 15th and EOM; verified live 2026-07-10) into the
// UNIVERSE-SCOPED short_interest table: only symbols we track are stored,
// same scoping as the finra-shorts daily worker.
//
// Cadence: the 12h tick is a heartbeat. Publication LAGS settlement by ~9
// business days, so each run probes the most recent settlement dates (up to
// 3 cycles = 6 dates, newest first) until one 200s; FINRA's CDN answers a
// not-yet-published date with 403 (= finra.ErrNotAvailable = honest skip).
// A meta settlement-date key dedups so each file is ingested exactly once,
// and probing stops at dates already ingested.
//
// GRACEFUL DEGRADATION: a file still missing past the expected publication
// window records a dq event (finra_shortint_late) and retries next tick;
// fetch errors record finra_shortint_error. The fleet NEVER fails over FINRA
// being down, and nothing is fabricated.
//
// HONESTY (carried verbatim by the API/UI): this is bi-monthly FINRA short
// interest — settlement-dated and published ~2 weeks lagged. DESCRIPTIVE
// positioning context, never a scored factor and not advice.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/finra"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// shortIntLastSettleKey holds the newest ingested settlement date.
	shortIntLastSettleKey = "finra_shortint_last_settle"
	// shortIntCycles is how many bi-monthly cycles back a run probes.
	shortIntCycles = 3
	// shortIntPublishLagDays: publication lags settlement ~9 BUSINESS days;
	// past this many CALENDAR days a still-missing file is flagged (dq), not
	// silently ignored.
	shortIntPublishLagDays = 16
)

// ShortInterestPoller is the finra-shortint worker.
type ShortInterestPoller struct {
	St     *store.Store
	Client *finra.SIClient
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *ShortInterestPoller) Name() string            { return "finra-shortint" }
func (w *ShortInterestPoller) Interval() time.Duration { return 12 * time.Hour }

func (w *ShortInterestPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// ShortIntOverdue is the PURE lateness gate: a settlement date's file is
// overdue when `now` is past settlement + the expected publication window.
func ShortIntOverdue(settlement, now time.Time) bool {
	return now.After(settlement.AddDate(0, 0, shortIntPublishLagDays))
}

func (w *ShortInterestPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		// Shouldn't happen — FINRA needs no key — but degrade honestly.
		return "skipped: no finra short-interest client", nil
	}
	now := w.now()

	// Universe scope: ALL active tracked stocks, ticker → symbol_id (the same
	// scoping the finra-shorts daily worker uses).
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

	candidates := finra.SettlementDates(now, shortIntCycles*2)
	last, _ := w.St.GetMeta(ctx, shortIntLastSettleKey)
	newestKey := candidates[0].Format("2006-01-02")
	if last != "" && last >= newestKey {
		return fmt.Sprintf("up to date (settlement %s)", last), nil
	}

	notYet := 0
	for _, d := range candidates {
		key := d.Format("2006-01-02")
		if last != "" && key <= last {
			// Everything from here back is already ingested.
			break
		}
		rows, skipped, ferr := w.Client.FetchShortInterest(ctx, d)
		if errors.Is(ferr, finra.ErrNotAvailable) {
			notYet++
			if ShortIntOverdue(d, now) {
				// Past the expected publication window — record the gap
				// honestly and keep probing older cycles.
				_ = w.St.InsertDQ(ctx, md.DQEvent{
					Ts: now.Unix(), Kind: "finra_shortint_late",
					Detail: fmt.Sprintf("short interest file for settlement %s still unpublished %d+ days after settlement", key, shortIntPublishLagDays),
				})
			}
			continue
		}
		if ferr != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: now.Unix(), Kind: "finra_shortint_error",
				Detail: fmt.Sprintf("%s: %v", key, ferr),
			})
			return fmt.Sprintf("fetch settlement %s failed (dq recorded; will retry): %v", key, ferr), nil
		}
		batch := make([]store.ShortInterestRow, 0, 512)
		for _, r := range rows {
			id, ok := tickerToID[r.Symbol]
			if !ok {
				continue
			}
			batch = append(batch, store.ShortInterestRow{
				SymbolID: id, Settlement: r.Settlement,
				ShortQty: r.ShortQty, PrevQty: r.PrevQty, ADV: r.ADV,
				DaysToCover: r.DaysToCover, ChangePct: r.ChangePct,
			})
		}
		if uerr := w.St.UpsertShortInterest(ctx, batch); uerr != nil {
			return "", uerr
		}
		_ = w.St.SetMeta(ctx, shortIntLastSettleKey, key)
		detail := fmt.Sprintf("settlement %s: %d tracked rows upserted (file had %d)", key, len(batch), len(rows))
		if skipped > 0 {
			detail += fmt.Sprintf("; %d malformed line(s) skipped", skipped)
		}
		if notYet > 0 {
			detail += fmt.Sprintf("; %d newer cycle(s) not yet published", notYet)
		}
		return detail, nil
	}
	if last == "" {
		return fmt.Sprintf("no short interest file published yet across %d probed settlement date(s)", notYet), nil
	}
	return fmt.Sprintf("newest cycle(s) not yet published (%d probed); latest stored settlement %s", notYet, last), nil
}
