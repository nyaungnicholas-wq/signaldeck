// DATA-EXPANSION wave — CFTC Commitments of Traders worker: cot-poller (24h).
//
// Ingests the WEEKLY legacy futures-only COT report from the CFTC's free
// public Socrata API (verified live 2026-07-10) for a small curated set of
// contracts relevant to what SignalDeck tracks: E-mini S&P 500, Nasdaq mini,
// and the CME bitcoin/ether complex. The report-date window is fetched
// server-side; contract selection is a CLIENT-SIDE name filter (WantCOT).
//
// First run backfills ~1 year in ≤13-week windows (each window stays under
// Socrata's row cap for the ~250-market weekly dataset); steady-state runs
// re-fetch a trailing 35-day window (idempotent REPLACE upserts absorb CFTC
// revisions).
//
// HONESTY (carried verbatim by the API/UI): COT is Tuesday positions
// published Friday — a built-in lag — and it is POSITIONING, NOT PREDICTION;
// commercials hedge and speculators are often wrong at extremes. Descriptive
// context only, never a scored factor.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cftc"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

const (
	// cotBackfillKey marks the one-time ~1y backfill done.
	cotBackfillKey = "cot_backfill_v1"
	// cotBackfillDays is the first-run history window.
	cotBackfillDays = 370
	// cotWindowDays chunks the backfill (~13 weeks ≈ 3300 rows < 5000 cap).
	cotWindowDays = 90
	// cotSteadyDays is the steady-state trailing re-fetch window.
	cotSteadyDays = 35
)

// cotWanted are the case-insensitive substrings a contract must match (in
// either market_and_exchange_names or contract_market_name) to be stored.
var cotWanted = []string{"E-MINI S&P 500", "NASDAQ MINI", "BITCOIN", "ETHER"}

// WantCOT is the PURE contract filter: does either name identify one of the
// curated contracts?
func WantCOT(market, contract string) bool {
	m := strings.ToUpper(market)
	c := strings.ToUpper(contract)
	for _, w := range cotWanted {
		if strings.Contains(m, w) || strings.Contains(c, w) {
			return true
		}
	}
	return false
}

// COTPoller is the cot-poller worker.
type COTPoller struct {
	St     *store.Store
	Client *cftc.Client
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *COTPoller) Name() string            { return "cot-poller" }
func (w *COTPoller) Interval() time.Duration { return 24 * time.Hour }

// cotStaleAfter is how long without a run forces a catch-up poll regardless of
// weekday. A US federal holiday pushes the COT release from Friday to Monday,
// and a daemon that was down over a Friday would otherwise wait a full week.
const cotStaleAfter = 9 * 24 * time.Hour

// NextFire implements workers.ScheduledWorker: the CFTC publishes the
// Commitments of Traders report ONCE A WEEK, Friday at 15:30 ET, for Tuesday's
// positions. A daily poll was seven runs for one release.
//
// Saturday 09:00 ET rather than Friday 15:31: the release is routinely a few
// minutes late and is never revised on the same day, so polling the morning
// after removes an entire class of "fetched the previous week's file" races for
// the price of ~17 hours of staleness on data that is already three days old
// when published.
func (w *COTPoller) NextFire(last, now time.Time) time.Time {
	if !last.IsZero() && now.Sub(last) > cotStaleAfter {
		return now // missed a release (holiday shift or downtime) — catch up
	}
	return workers.WeeklyAtET(now, time.Saturday, 9, 0)
}

func (w *COTPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *COTPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		// Shouldn't happen — the Socrata API needs no key — but degrade honestly.
		return "skipped: no cftc client", nil
	}
	now := w.now()

	// Window plan: one trailing window in steady state; chunked ~1y backfill
	// on the first run (the done-key is only set after ALL windows land, so an
	// interrupted backfill resumes next tick — upserts make overlap free).
	type window struct{ since, until time.Time }
	var windows []window
	backfilled, _ := w.St.GetMeta(ctx, cotBackfillKey)
	if backfilled == "" {
		start := now.AddDate(0, 0, -cotBackfillDays)
		for s := start; s.Before(now); s = s.AddDate(0, 0, cotWindowDays) {
			u := s.AddDate(0, 0, cotWindowDays)
			if u.After(now) {
				u = time.Time{} // open upper bound on the last window
			}
			windows = append(windows, window{since: s, until: u})
		}
	} else {
		windows = []window{{since: now.AddDate(0, 0, -cotSteadyDays)}}
	}

	stored, skippedRows, fetched := 0, 0, 0
	contracts := map[string]bool{}
	latest := ""
	for _, win := range windows {
		rows, skipped, err := w.Client.FetchWindow(ctx, win.since, win.until)
		if err != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: now.Unix(), Kind: "cot_error",
				Detail: fmt.Sprintf("window from %s: %v", win.since.Format("2006-01-02"), err),
			})
			return fmt.Sprintf("fetch failed after %d stored row(s) (dq recorded; will retry next tick): %v", stored, err), nil
		}
		skippedRows += skipped
		fetched += len(rows)
		batch := make([]store.COTRow, 0, 16)
		for _, r := range rows {
			if !WantCOT(r.Market, r.Contract) {
				continue
			}
			batch = append(batch, store.COTRow{
				Contract: r.Contract, ReportDate: r.ReportDate,
				NoncommLong: r.NoncommLong, NoncommShort: r.NoncommShort,
				CommLong: r.CommLong, CommShort: r.CommShort,
				OpenInterest: r.OpenInterest,
			})
			contracts[r.Contract] = true
			if r.ReportDate > latest {
				latest = r.ReportDate
			}
		}
		if err := w.St.UpsertCOT(ctx, batch); err != nil {
			return "", err
		}
		stored += len(batch)
	}
	if backfilled == "" {
		_ = w.St.SetMeta(ctx, cotBackfillKey, now.Format(time.RFC3339))
	}
	detail := fmt.Sprintf("upserted %d row(s) across %d contract(s) from %d fetched", stored, len(contracts), fetched)
	if latest != "" {
		detail += fmt.Sprintf("; latest report %s", latest)
	}
	if skippedRows > 0 {
		detail += fmt.Sprintf("; %d malformed row(s) skipped", skippedRows)
	}
	return detail, nil
}
