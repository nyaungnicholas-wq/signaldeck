// TradingView SCANNER RATINGS store methods (see internal/pipeline/tvrating.go
// and internal/ingest/tvscanner). Two tables: tv_exchange (the resolution cache
// mapping symbol_id → EXCHANGE, since symbols has no exchange) and tv_ratings
// (the append-only external-rating time series). Writes go through the single
// write connection s.w; reads use s.db.
//
// HONESTY: the stored reco_* are TradingView's OWN descriptive TA rating on
// delayed data — an EXTERNAL signal, NOT our model and not advice. The API
// carries that caveat verbatim.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TVRatingRow is one symbol's TradingView rating at a pass timestamp.
type TVRatingRow struct {
	SymbolID  int64   `json:"symbolId"`
	Ts        int64   `json:"ts"`
	RecoAll   float64 `json:"recoAll"`
	RecoMA    float64 `json:"recoMA"`
	RecoOther float64 `json:"recoOther"`
	RSI       float64 `json:"rsi"`
	Close     float64 `json:"close"`
	Label     string  `json:"label"`
}

// UpsertExchange caches a symbol's resolved EXCHANGE (INSERT OR REPLACE on the
// symbol_id PK, stamping resolved_at with the current time).
func (s *Store) UpsertExchange(ctx context.Context, symbolID int64, exchange string) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO tv_exchange (symbol_id, exchange, resolved_at)
		VALUES (?,?,?)`, symbolID, exchange, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("upsert tv_exchange %d: %w", symbolID, err)
	}
	return nil
}

// ExchangesFor returns the cached exchange for each of symbolIDs that has one
// (absent ids are simply missing from the map — honest absence, not an error).
func (s *Store) ExchangesFor(ctx context.Context, symbolIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(symbolIDs))
	if len(symbolIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(symbolIDs))
	args := make([]any, len(symbolIDs))
	for i, id := range symbolIDs {
		ph[i] = "?"
		args[i] = id
	}
	q := `SELECT symbol_id, exchange FROM tv_exchange WHERE symbol_id IN (` +
		strings.Join(ph, ",") + `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var id int64
		var exch string
		if err := rows.Scan(&id, &exch); err != nil {
			return nil, err
		}
		out[id] = exch
	}
	return out, rows.Err()
}

// SymbolsMissingExchange returns up to `limit` ACTIVE symbols in `market` that
// have no tv_exchange row yet — the incremental resolution sweep's work queue.
// Ordered by id for a deterministic cursor-free sweep.
func (s *Store) SymbolsMissingExchange(ctx context.Context, market string, limit int) ([]md.Symbol, error) {
	if limit <= 0 || limit > 500 {
		limit = 60
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.symbol, s.market, s.name, s.active, s.added_at, s.stream
		FROM symbols s
		LEFT JOIN tv_exchange x ON x.symbol_id = s.id
		WHERE s.active=1 AND s.market=? AND x.symbol_id IS NULL
		ORDER BY s.id LIMIT ?`, market, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []md.Symbol
	for rows.Next() {
		var sym md.Symbol
		var active, stream int
		var mkt string
		if err := rows.Scan(&sym.ID, &sym.Symbol, &mkt, &sym.Name, &active, &sym.AddedAt, &stream); err != nil {
			return nil, err
		}
		sym.Market, sym.Active, sym.Stream = md.Market(mkt), active == 1, stream == 1
		out = append(out, sym)
	}
	return out, rows.Err()
}

// UpsertTVRating appends (or REPLACEs on a same-second re-run) one rating row.
func (s *Store) UpsertTVRating(ctx context.Context, row TVRatingRow) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT OR REPLACE INTO tv_ratings
		  (symbol_id, ts, reco_all, reco_ma, reco_other, rsi, close_px, label)
		VALUES (?,?,?,?,?,?,?,?)`,
		row.SymbolID, row.Ts, row.RecoAll, row.RecoMA, row.RecoOther,
		row.RSI, row.Close, row.Label)
	if err != nil {
		return fmt.Errorf("upsert tv_ratings %d/%d: %w", row.SymbolID, row.Ts, err)
	}
	return nil
}

// LatestTVRating returns a symbol's most recent stored rating. ok=false when
// none exists (honest absence) OR on a read error — matching the best-effort
// (row, bool) idiom of symbolIDByTicker; the worker always writes non-null
// values, so a present row scans cleanly.
func (s *Store) LatestTVRating(ctx context.Context, symbolID int64) (TVRatingRow, bool) {
	var r TVRatingRow
	var recoAll, recoMA, recoOther, rsi, closePx sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT symbol_id, ts, reco_all, reco_ma, reco_other, rsi, close_px, label
		FROM tv_ratings WHERE symbol_id=? ORDER BY ts DESC LIMIT 1`, symbolID).
		Scan(&r.SymbolID, &r.Ts, &recoAll, &recoMA, &recoOther, &rsi, &closePx, &r.Label)
	if err != nil {
		return TVRatingRow{}, false
	}
	r.RecoAll, r.RecoMA, r.RecoOther = recoAll.Float64, recoMA.Float64, recoOther.Float64
	r.RSI, r.Close = rsi.Float64, closePx.Float64
	return r, true
}

// ── measured skill of the external TradingView rating (composite factor gate) ──
//
// HONESTY: the tvrating composite factor must EARN its verdict. TVRatingSkill
// measures whether past reco_all actually forecast realized forward returns —
// exactly the alertstats.go forward-return join, pooled across symbols and
// deduped to one independent observation per (symbol, UTC-day) like
// trackrecord.go — so the composite can gate the factor to CONTEXT-only until
// the evidence clears (n>=30, |IC| above a floor). Below the gate the external
// rating is shown but never scored.

// TVSkill is the pooled measured skill of the external TradingView rating: the
// directional hit rate + information coefficient of reco_all vs realized 1d
// forward returns, over N independent (symbol, UTC-day) observations.
type TVSkill struct {
	HitRate float64 `json:"hitRate"`
	IC      float64 `json:"ic"`
	N       int     `json:"n"`
}

// TVRatingSkill measures how well TradingView's own reco_all rating forecast
// the next trading day's return, pooled across every tracked symbol. Base = the
// close of the last daily bar at/before the rating ts; the forward return is the
// NEXT stored daily bar. Observations are collapsed to one per (symbol, UTC-day)
// — keeping the last rating that day — so the 15-minute scan cadence cannot
// pseudo-replicate the same daily move. Returns the pooled IC + directional hit
// rate + independent N (all zero when nothing is resolvable yet — honest
// absence, the composite renders the gate reason).
func (s *Store) TVRatingSkill(ctx context.Context) (TVSkill, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbol_id, ts, reco_all FROM tv_ratings
		WHERE reco_all IS NOT NULL ORDER BY symbol_id, ts`)
	if err != nil {
		return TVSkill{}, err
	}
	type rating struct {
		symbolID int64
		ts       int64
		reco     float64
	}
	var ratings []rating
	symbolSet := map[int64]bool{}
	for rows.Next() {
		var r rating
		if err := rows.Scan(&r.symbolID, &r.ts, &r.reco); err != nil {
			rows.Close() //nolint:errcheck
			return TVSkill{}, err
		}
		ratings = append(ratings, r)
		symbolSet[r.symbolID] = true
	}
	rows.Close() //nolint:errcheck
	if err := rows.Err(); err != nil {
		return TVSkill{}, err
	}

	// Daily close series per involved symbol, ascending — loaded once each.
	type series struct {
		ts     []int64
		closes []float64
	}
	bars := map[int64]*series{}
	for id := range symbolSet {
		r, err := s.db.QueryContext(ctx, `
			SELECT ts, close FROM bars WHERE symbol_id=? AND tf='1d' ORDER BY ts`, id)
		if err != nil {
			return TVSkill{}, err
		}
		sr := &series{}
		for r.Next() {
			var ts int64
			var c float64
			if err := r.Scan(&ts, &c); err != nil {
				r.Close() //nolint:errcheck
				return TVSkill{}, err
			}
			sr.ts = append(sr.ts, ts)
			sr.closes = append(sr.closes, c)
		}
		r.Close() //nolint:errcheck
		if err := r.Err(); err != nil {
			return TVSkill{}, err
		}
		bars[id] = sr
	}

	// One independent obs per (symbol, UTC-day): last rating that day (ratings
	// are ascending, so a later same-day row overwrites the earlier one).
	// Keyed on the BASE BAR, not the calendar day. A rating stamped on a Saturday,
	// a Sunday and a Monday holiday all resolve to the SAME Friday base bar and the
	// same next-bar forward return, so folding on md.TradingDay(r.ts) republished
	// one observation up to four times and inflated N by ~35% on the live corpus
	// (22,099 as coded vs 16,391 distinct). N is published verbatim as the factor's
	// SkillN chip and inside the composite gateReason, and it is the denominator of
	// the IC the gate reads — so the duplicates were being counted as independent
	// evidence they are not.
	type key struct {
		sym    int64
		baseTs int64
	}
	type pair struct{ reco, fwd float64 }
	obs := map[key]pair{}
	for _, r := range ratings {
		sr := bars[r.symbolID]
		if sr == nil || len(sr.ts) == 0 {
			continue
		}
		idx := sort.Search(len(sr.ts), func(i int) bool { return sr.ts[i] > r.ts }) - 1
		if idx < 0 || sr.closes[idx] <= 0 || idx+1 >= len(sr.ts) {
			continue // no base bar, or no next-day bar to resolve against yet
		}
		fwd := sr.closes[idx+1]/sr.closes[idx] - 1
		obs[key{sym: r.symbolID, baseTs: sr.ts[idx]}] = pair{reco: r.reco, fwd: fwd}
	}

	recos := make([]float64, 0, len(obs))
	fwds := make([]float64, 0, len(obs))
	var dirN, hits int
	for _, p := range obs {
		recos = append(recos, p.reco)
		fwds = append(fwds, p.fwd)
		if p.reco != 0 {
			dirN++
			if (p.reco > 0 && p.fwd > 0) || (p.reco < 0 && p.fwd < 0) {
				hits++
			}
		}
	}
	out := TVSkill{N: len(obs)}
	if dirN > 0 {
		out.HitRate = float64(hits) / float64(dirN)
	}
	if ic, ok := pearsonCorr(recos, fwds); ok {
		out.IC = ic
	}
	return out, nil
}

// pearsonCorr is the Pearson correlation of x and y (ok=false when n<3 or either
// side has zero variance — correlation is undefined, not zero). Same shape as
// adaptive.pearson; kept here so the store stays dependency-free.
func pearsonCorr(x, y []float64) (float64, bool) {
	n := len(x)
	if n < 3 || len(y) != n {
		return 0, false
	}
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += x[i]
		sy += y[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var cov, vx, vy float64
	for i := 0; i < n; i++ {
		dx, dy := x[i]-mx, y[i]-my
		cov += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx <= 0 || vy <= 0 {
		return 0, false
	}
	r := cov / math.Sqrt(vx*vy)
	if r > 1 {
		r = 1
	} else if r < -1 {
		r = -1
	}
	return r, true
}
