// Broad-universe LIVE cadence (live-everything wave, 2026-07-10).
//
// The original once-per-UTC-day universe gates existed because broad (stream=0)
// symbols only received daily bars. The universe-live poller now keeps minute
// bars current for ALL stocks during market hours, so the derived passes
// (scores, predictions, composite) may run far more often without writing
// identical rows: every universeLiveInterval while the market is open for
// bars, and once per UTC day when it is closed (off-hours the inputs only
// change daily, so extra passes would be pure duplicate writes).
//
// The meta cursor stores the last pass's unix seconds; legacy "2006-01-02"
// day-string cursors parse as non-integers and read as 0 — one immediate
// extra pass on upgrade, then normal cadence.
package pipeline

import (
	"context"
	"strconv"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// universeLiveInterval is the market-hours cadence for broad-universe derived
// passes (scores / predictions / composite persistence).
const universeLiveInterval = 10 * time.Minute

// universeDue reports whether a broad-universe pass is due under the live
// cadence, and returns the cursor value the caller must store (via SetMeta on
// metaKey) AFTER the pass succeeds — storing only on success keeps the
// original retry-on-next-tick behavior for mid-run errors.
func universeDue(ctx context.Context, st *store.Store, metaKey string, now time.Time) (bool, string) {
	raw, _ := st.GetMeta(ctx, metaKey)
	lastTs, _ := strconv.ParseInt(raw, 10, 64) // legacy day-strings → 0 → due
	cursor := strconv.FormatInt(now.Unix(), 10)
	if marketcal.OpenForBars(now) {
		return now.Unix()-lastTs >= int64(universeLiveInterval/time.Second), cursor
	}
	// Market closed: broad inputs only change daily — once per UTC day.
	lastDay := time.Unix(lastTs, 0).UTC().Format("2006-01-02")
	return lastDay != now.UTC().Format("2006-01-02"), cursor
}
