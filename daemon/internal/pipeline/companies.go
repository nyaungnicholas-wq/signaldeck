// Signal8 wave — Stage 5 worker: companies-sync (24h). Refreshes the FULL
// SEC company directory (cik/name/ticker/exchange, ~10.4k rows) from the free
// EDGAR file company_tickers_exchange.json in ONE request per run, upserted
// in one transaction. SIC enrichment is NOT done here — it rides along on the
// submissions fetches the filings-poller already makes (zero added volume).
// Always enabled: the directory depends on nothing but EDGAR (no Alpaca gate);
// a nil client (tests) is a clean no-op. Failures degrade gracefully: the run
// errors visibly in worker_runs, the stored directory keeps serving.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// companiesSyncTsKey records the last successful sync (unix seconds) so the
// API can tell the user how fresh the directory is.
const companiesSyncTsKey = "companies_sync_ts"

// CompaniesSync mirrors the SEC company map into the companies table.
type CompaniesSync struct {
	St *store.Store
	// Client is the daemon-wide SHARED EDGAR client (one limiter for the whole
	// process — see run()). nil ⇒ no-op.
	Client *edgar.Client
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *CompaniesSync) Name() string            { return "companies-sync" }
func (w *CompaniesSync) Interval() time.Duration { return 24 * time.Hour }

func (w *CompaniesSync) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no EDGAR client", nil
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	rows, err := w.Client.CompanyTickersExchange(ctx)
	if err != nil {
		return "", err
	}
	ts := now().Unix()
	for i := range rows {
		rows[i].UpdatedTs = ts
	}
	if err := w.St.UpsertCompanies(ctx, rows); err != nil {
		return "", err
	}
	_ = w.St.SetMeta(ctx, companiesSyncTsKey, fmt.Sprint(ts))
	total, _ := w.St.CompanyCount(ctx)
	return fmt.Sprintf("companies directory: %d rows synced (%d stored) from one EDGAR request", len(rows), total), nil
}
