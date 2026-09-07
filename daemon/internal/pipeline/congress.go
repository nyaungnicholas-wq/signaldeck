// Signal8 wave — Stage 2 worker: congress-poller (~12h).
//
// Pulls US congressional stock-transaction disclosures from the FREE public
// Stock Watcher mirrors (Senate + House JSON dumps; see internal/ingest/
// congress for the verified-2026-07-04 mirror status), hashes each row to a
// deterministic id, maps disclosed tickers onto our tracked stock universe
// (unknown tickers keep symbol_id NULL, honestly), and INSERT OR IGNOREs into
// congress_trades — every re-download of the cumulative dumps is idempotent.
//
// GRACEFUL DEGRADATION IS THE DESIGNED-FOR PATH: both mirrors are currently
// dead (DNS gone / S3 403). A failed chamber records a dq event
// ("congress_mirror_error") and an honest per-chamber status in meta
// (congress_mirror_status, surfaced by /api/congress) — the worker NEVER
// fails the fleet over a dead mirror and never fabricates rows.
//
// HONESTY: public-domain STOCK Act data; disclosures lag 30-45 days BY LAW,
// amounts are ranges. The API's lag note states both.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/congress"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// congressStatusKey is the meta key holding the per-chamber mirror status
// (JSON) written every run — the API's honest "is this source alive?" answer.
const congressStatusKey = "congress_mirror_status"

// CongressChamberStatus is one chamber's last-run outcome.
type CongressChamberStatus struct {
	OK      bool   `json:"ok"`
	Fetched int    `json:"fetched"`          // rows parsed from the dump (0 when dead)
	New     int    `json:"new"`              // rows newly inserted this run
	Detail  string `json:"detail,omitempty"` // error text when !ok (honest, not hidden)
}

// CongressMirrorStatus is the whole meta payload.
type CongressMirrorStatus struct {
	CheckedTs int64                 `json:"checkedTs"`
	Senate    CongressChamberStatus `json:"senate"`
	House     CongressChamberStatus `json:"house"`
}

// CongressPoller ingests both chambers' disclosure dumps.
type CongressPoller struct {
	St     *store.Store
	Client *congress.Client // nil ⇒ no-op (shouldn't happen; congress needs no key)
	// Now is a test hook; nil = time.Now.
	Now func() time.Time

	// deadRuns counts CONSECUTIVE runs where both mirrors were unreachable. It
	// drives the circuit breaker in NextFire; a single success resets it. It is
	// in-process only (a restart re-probes once at the base cadence), which is
	// the right bias: a restart is exactly when a human may have fixed the
	// source.
	deadRuns int
}

func (w *CongressPoller) Name() string            { return "congress-poller" }
func (w *CongressPoller) Interval() time.Duration { return 12 * time.Hour }

// congressBackoffMax is the widest retry for a dead mirror. A week is long
// enough to stop paying for a known-403 and short enough that a restored source
// is picked up without a human noticing it came back.
const congressBackoffMax = 7 * 24 * time.Hour

// NextFire implements workers.ScheduledWorker as a CIRCUIT BREAKER.
//
// WHY: both free mirrors (senatestockwatcher.com, housestockwatcher.com) went
// dead in 2026-07 — DNS gone, S3 403 — and this poller kept hitting them twice a
// day purely to write a dq event. Disclosures are also not a live feed: the STOCK
// Act gives members 45 days to file, so nothing is lost by checking daily instead
// of twice daily, and nothing at all is lost by backing off a source that is
// returning 403 to every request.
//
// Healthy: once a day at 09:00 ET, after the overnight mirror rebuilds.
// Failing: exponential from the base cadence out to a week.
func (w *CongressPoller) NextFire(last, now time.Time) time.Time {
	if w.deadRuns > 0 {
		return workers.BackoffAfter(now, w.deadRuns, 12*time.Hour, congressBackoffMax)
	}
	return workers.DailyAtET(now, 9, 0)
}

func (w *CongressPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *CongressPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no congress client", nil
	}
	now := w.now()

	// Ticker → symbol_id map over ALL tracked stocks (active or not): a
	// disclosure about a symbol we've ever tracked should link to its page.
	all, err := w.St.ListSymbols(ctx, false)
	if err != nil {
		return "", err
	}
	tickerToID := make(map[string]int64)
	for _, s := range all {
		if s.Market == md.Stocks {
			tickerToID[strings.ToUpper(s.Symbol)] = s.ID
		}
	}

	status := CongressMirrorStatus{CheckedTs: now.Unix()}
	status.Senate = w.ingestChamber(ctx, congress.ChamberSenate, tickerToID, now)
	status.House = w.ingestChamber(ctx, congress.ChamberHouse, tickerToID, now)
	_ = w.St.SetJSON(ctx, congressStatusKey, status)

	if status.Senate.OK || status.House.OK {
		w.deadRuns = 0 // a live chamber closes the breaker
	} else {
		w.deadRuns++
	}

	if !status.Senate.OK && !status.House.OK {
		// Both mirrors dead — the CURRENT real-world state. Honest skip, dq
		// recorded per chamber, fleet marches on. Reported as DEGRADED rather
		// than ok: this branch ran on every poll for seven days while
		// congress_trades held zero rows, and filing it as success is what let
		// 94 dq events pile up behind a green fleet view.
		return "congress mirrors unavailable (dq recorded; stored history still served)",
			fmt.Errorf("congress mirrors unavailable (dq recorded; stored history still served): %w",
				workers.ErrDegraded)
	}
	return fmt.Sprintf("congress: senate %d new/%d fetched, house %d new/%d fetched",
		status.Senate.New, status.Senate.Fetched, status.House.New, status.House.Fetched), nil
}

// ingestChamber fetches + stores one chamber; failures are contained here.
func (w *CongressPoller) ingestChamber(ctx context.Context, chamber string,
	tickerToID map[string]int64, now time.Time,
) CongressChamberStatus {
	var trades []congress.Trade
	var err error
	if chamber == congress.ChamberSenate {
		trades, err = w.Client.FetchSenate(ctx)
	} else {
		trades, err = w.Client.FetchHouse(ctx)
	}
	if err != nil { // Stock Watcher mirrors are dead (403 since 2026): fall back to Kadoa's daily JSON (kadoa.go)
		if kad, kerr := w.Client.FetchKadoa(ctx, chamber); kerr == nil {
			trades, err = kad, nil
		} else {
			err = fmt.Errorf("%v; kadoa fallback: %v", err, kerr)
		}
	}
	if err != nil {
		// The free mirrors have been dead since 2026-07 (DNS gone, S3 403) and
		// the poller runs on a schedule that produced ~56 identical dq events a
		// day. A permanently-failing known source is not news; it is noise that
		// buries the events a human should actually act on, and this platform
		// already learned that lesson with the watchdog's alert cooldown.
		//
		// One event per chamber per UTC day preserves the signal (a human can
		// still see the source is down, and the per-chamber status in meta is
		// unaffected and updated every pass) while removing the flood. A source
		// that starts working again clears the marker on its next success, so a
		// recovery is never suppressed.
		key := "congress_dq_day:" + chamber
		day := now.UTC().Format("2006-01-02")
		if last, _ := w.St.GetMeta(ctx, key); last != day {
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: now.Unix(), Kind: "congress_mirror_error",
				Detail: fmt.Sprintf("%s: %v", chamber, err),
			})
			_ = w.St.SetMeta(ctx, key, day)
		}
		return CongressChamberStatus{OK: false, Detail: err.Error()}
	}
	// A successful fetch clears the day marker so a recovery-then-failure is
	// reported rather than swallowed by a stale marker.
	_ = w.St.SetMeta(ctx, "congress_dq_day:"+chamber, "")
	st := CongressChamberStatus{OK: true, Fetched: len(trades)}
	for _, t := range trades {
		row := store.CongressTradeRow{
			ID: t.ID, Chamber: t.Chamber, Member: t.Member, Symbol: t.Ticker,
			TxType: t.TxType, AmountRange: t.Amount,
			TxTs: t.TxTs, DisclosedTs: t.DisclosedTs,
		}
		if id, ok := tickerToID[t.Ticker]; ok {
			row.SymbolID = &id
		}
		isNew, ierr := w.St.InsertCongressTrade(ctx, row)
		if ierr != nil {
			// A store error is a real problem worth surfacing per chamber —
			// but still never a fleet failure.
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: now.Unix(), Kind: "congress_store_error",
				Detail: fmt.Sprintf("%s %s: %v", chamber, t.ID, ierr),
			})
			st.Detail = ierr.Error()
			continue
		}
		if isNew {
			st.New++
		}
	}
	return st
}
