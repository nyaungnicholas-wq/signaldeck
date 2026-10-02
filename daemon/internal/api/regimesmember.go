package api

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	// memberRegimesTop is how many rows of each kind a member's read carries
	// when it names no symbols and no limit: the top of each kind, which is
	// what /market/regimes shows before "show more" and all /today needs to
	// pick a read when the watchlist is empty.
	memberRegimesTop = 25
	// memberRegimesMaxLimit is the largest page a member may ask for. 100
	// trend21 rows, the widest kind, are ~120 KB.
	memberRegimesMaxLimit = 100
)

// regimeKindStats is what a member page can no longer count from the rows it
// receives: every row of the kind, and the count and mean of those that carry
// a historicalAccuracy. It covers the WHOLE kind whatever symbols= names, so it
// is the same for every member: a total over a member's own watchlist would be
// a number that depends on what else they watch (docs/PUBLISHER_GUARDRAILS.md
// rule 1).
type regimeKindStats struct {
	Count                  int     `json:"count"`
	Measured               int     `json:"measured"`
	MeanHistoricalAccuracy float64 `json:"meanHistoricalAccuracy,omitempty"`
}

// memberRegimesView is the member view of /api/regimes (REGIMES-SIZE,
// 2026-10-02). The full payload measured 3.6 MB for 4,151 rows on the
// 2026-10-01 snapshot, 2.1 MB of it the per-row evidenceCaveat, and every
// member page loaded all of it; /market/regimes re-polled it every 2 minutes.
// Each member page needs a slice (one symbol, the watchlist, the top of each
// kind, a page of one kind), so a member asks for that slice:
//
//	symbols=A,B  every row of those symbols (no default limit)
//	kind=K       only kind K
//	offset=N     skip the first N matching rows of each kind
//	limit=N      at most N rows per kind (max memberRegimesMaxLimit);
//	             default memberRegimesTop when no symbols are named
//
// Rows are selected, never changed, so every honesty field a row carries still
// ships with it, and a symbol's row is the same whatever else is asked for.
// kindStats carries each kind's totals. resp is the output of
// withoutCryptoForecasts and is a shared cached value: nothing in it is written.
// The operator keeps the full payload.
func memberRegimesView(resp map[string]any, q url.Values) map[string]any {
	byKind, _ := resp["forecasts"].(map[string][]any)
	var want map[string]bool
	for _, s := range strings.Split(q.Get("symbols"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			if want == nil {
				want = map[string]bool{}
			}
			want[s] = true
		}
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	offset = max(offset, 0)
	limit := memberRegimesTop
	if want != nil {
		limit = -1 // every row of the named symbols
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
		limit = min(l, memberRegimesMaxLimit)
	}

	forecasts := map[string][]any{}
	stats := map[string]regimeKindStats{}
	for kind, rows := range byKind {
		if k := q.Get("kind"); k != "" && kind != k {
			continue
		}
		var match []any
		var st regimeKindStats
		var sum float64
		for _, row := range rows {
			var sym string
			var acc float64
			switch f := row.(type) {
			case store.RegimeForecast:
				sym, acc = f.Symbol, f.HistoricalAccuracy
			case map[string]any:
				sym, _ = f["symbol"].(string)
				acc, _ = f["historicalAccuracy"].(float64)
			default:
				continue
			}
			st.Count++
			if acc > 0 {
				st.Measured++
				sum += acc
			}
			if want == nil || want[sym] {
				match = append(match, row)
			}
		}
		if st.Count == 0 {
			continue
		}
		if st.Measured > 0 {
			st.MeanHistoricalAccuracy = sum / float64(st.Measured)
		}
		stats[kind] = st
		if len(match) == 0 {
			continue
		}
		start := min(offset, len(match))
		end := len(match)
		if limit >= 0 {
			end = min(start+limit, end)
		}
		forecasts[kind] = match[start:end:end] // non-nil: marshals as [], never null
	}

	out := make(map[string]any, len(resp)+1)
	for k, v := range resp {
		out[k] = v
	}
	out["forecasts"] = forecasts
	out["kindStats"] = stats
	return out
}
