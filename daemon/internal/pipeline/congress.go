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
}

func (w *CongressPoller) Name() string            { return "congress-poller" }
func (w *CongressPoller) Interval() time.Duration { return 12 * time.Hour }

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

	if !status.Senate.OK && !status.House.OK {
		// Both mirrors dead — the CURRENT real-world state. Honest skip, dq
		// recorded per chamber, fleet marches on.
		return "congress mirrors unavailable (dq recorded; stored history still served)", nil
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
	if err != nil {
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			Ts: now.Unix(), Kind: "congress_mirror_error",
			Detail: fmt.Sprintf("%s: %v", chamber, err),
		})
		return CongressChamberStatus{OK: false, Detail: err.Error()}
	}
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
