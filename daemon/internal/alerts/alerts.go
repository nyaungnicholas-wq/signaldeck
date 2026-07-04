// Package alerts is the per-user alert engine: a 5-minute sweep that turns
// stored events (breakouts, regime changes) and calibrated predictions
// crossing conviction thresholds into actionable, watchlist-scoped alert
// rows — plus a batched macOS notification so events become pings instead of
// a tab you must remember to open.
package alerts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Alert kinds (mirror the CHECK-style comment in schema.sql).
const (
	KindBreakout       = "breakout"
	KindRegimeChange   = "regime_change"
	KindPredictionHigh = "prediction_high"
	KindPredictionLow  = "prediction_low"
	// Signal8 wave Stage 3: anomaly kinds. These mirror the anomalies-table
	// kinds 1:1 (internal/anomaly) — the sweep fans stored anomaly rows out
	// to watchers, so the alert kind IS the anomaly kind. All three are
	// DESCRIPTIVE (z vs the symbol's own baseline), never predictions, and
	// the fanned-out detail carries the detector's window/baseline/proxy
	// wording verbatim.
	KindAnomalyImbalance = "anomaly_imbalance"
	KindAnomalyVol       = "anomaly_vol"
	KindAnomalyVolume    = "anomaly_volume"
)

// Default prediction thresholds (calibrated P(up)).
const (
	DefaultHi = 0.65
	DefaultLo = 0.35
)

// Meta keys: sweep cursors (rowids, not timestamps — breakout/regime rows
// carry bar timestamps that can be older than their insertion time, so an
// id cursor is the only gap-free "since last sweep" marker).
const (
	metaBreakoutCursor = "alerts_last_breakout_id"
	metaRegimeCursor   = "alerts_last_regime_id"
	// Signal8 wave Stage 3: anomaly-sweep rowid cursor (same gap-free
	// id-cursor pattern as breakouts/regime changes).
	metaAnomalyCursor = "alerts_last_anomaly_id"
)

// predictionDedupWindow: at most one prediction alert per
// (user, symbol, horizon, side) per this window.
const predictionDedupWindow = 24 * time.Hour

// notifyCooldown limits macOS notifications to one per 30 minutes.
const notifyCooldown = 30 * time.Minute

// sweepBatch bounds how many events one sweep will consume per table.
const sweepBatch = 500

// Thresholds parses the hi/lo strings (from SIGNALDECK_ALERT_HI/LO).
// Invalid, out-of-range, or inverted values fall back to the defaults —
// the engine must never run with a nonsensical band.
func Thresholds(hiStr, loStr string) (hi, lo float64) {
	hi, lo = DefaultHi, DefaultLo
	h, errH := strconv.ParseFloat(hiStr, 64)
	l, errL := strconv.ParseFloat(loStr, 64)
	if hiStr != "" && errH == nil {
		hi = h
	}
	if loStr != "" && errL == nil {
		lo = l
	}
	if !(lo > 0 && hi < 1 && lo < hi) {
		return DefaultHi, DefaultLo
	}
	return hi, lo
}

// PredictionKind is the pure alert rule for one calibrated probability:
// >= hi → prediction_high, <= lo → prediction_low, otherwise no alert.
func PredictionKind(calProb, hi, lo float64) (string, bool) {
	switch {
	case calProb >= hi:
		return KindPredictionHigh, true
	case calProb <= lo:
		return KindPredictionLow, true
	default:
		return "", false
	}
}

// predHorizons mirrors pipeline.predHorizons (the ensemble's horizons).
var predHorizons = []md.Horizon{md.H1d, md.H1w}

// Runner is the alert-runner worker (implements workers.Worker).
type Runner struct {
	St *store.Store
	// Hi/Lo override the prediction thresholds; both zero = read
	// SIGNALDECK_ALERT_HI / SIGNALDECK_ALERT_LO (defaults 0.65 / 0.35).
	Hi, Lo float64
	// Notify shows a user-facing alert; nil = osascript display notification.
	Notify func(msg string) error
	// Now is a test hook; nil = time.Now.
	Now func() time.Time

	lastNotify time.Time
}

// Name implements workers.Worker.
func (r *Runner) Name() string { return "alert-runner" }

// Interval implements workers.Worker.
func (r *Runner) Interval() time.Duration { return 5 * time.Minute }

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) thresholds() (float64, float64) {
	if r.Hi != 0 || r.Lo != 0 {
		return r.Hi, r.Lo
	}
	return Thresholds(os.Getenv("SIGNALDECK_ALERT_HI"), os.Getenv("SIGNALDECK_ALERT_LO"))
}

// Run performs one sweep: fan events + threshold-crossing predictions out to
// every user whose watchlist contains the symbol.
func (r *Runner) Run(ctx context.Context) (string, error) {
	now := r.now()
	hi, lo := r.thresholds()

	// Per-user watchlists (symbol id → symbol), built once per sweep.
	userIDs, err := r.St.ListUserIDs(ctx)
	if err != nil {
		return "", err
	}
	watch := make(map[int64]map[int64]md.Symbol, len(userIDs))
	for _, uid := range userIDs {
		syms, err := r.St.ListUserSymbols(ctx, uid)
		if err != nil {
			return "", err
		}
		m := make(map[int64]md.Symbol, len(syms))
		for _, s := range syms {
			m[s.ID] = s
		}
		watch[uid] = m
	}

	created := 0

	// ── breakouts since last sweep (id cursor; first sweep looks back 24h) ──
	bCursor, firstB := r.cursor(ctx, metaBreakoutCursor)
	sinceTs := int64(0)
	maxB := bCursor
	if firstB {
		// Skip anything older than 24h AND start the cursor at the table's
		// current max, so skipped ancient rows can never fire later.
		sinceTs = now.Add(-24 * time.Hour).Unix()
		if maxB, err = r.St.MaxBreakoutID(ctx); err != nil {
			return "", err
		}
	}
	bEvents, err := r.St.BreakoutsAfterID(ctx, bCursor, sinceTs, sweepBatch)
	if err != nil {
		return "", err
	}
	for _, ev := range bEvents {
		if ev.ID > maxB {
			maxB = ev.ID
		}
		if ev.SymbolID == nil {
			continue // watchlist-wide events (correlation breaks) have no owner symbol
		}
		for _, uid := range userIDs {
			s, watched := watch[uid][*ev.SymbolID]
			if !watched {
				continue
			}
			sid := *ev.SymbolID
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: KindBreakout,
				Detail: fmt.Sprintf("%s: %s %s", s.Symbol, ev.Kind, ev.Detail),
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
		}
	}

	// ── regime changes since last sweep ─────────────────────────────────
	rCursor, firstR := r.cursor(ctx, metaRegimeCursor)
	sinceTs = 0
	maxR := rCursor
	if firstR {
		sinceTs = now.Add(-24 * time.Hour).Unix()
		if maxR, err = r.St.MaxRegimeChangeID(ctx); err != nil {
			return "", err
		}
	}
	rEvents, err := r.St.RegimeChangesAfterID(ctx, rCursor, sinceTs, sweepBatch)
	if err != nil {
		return "", err
	}
	for _, ev := range rEvents {
		if ev.ID > maxR {
			maxR = ev.ID
		}
		for _, uid := range userIDs {
			s, watched := watch[uid][ev.SymbolID]
			if !watched {
				continue
			}
			sid := ev.SymbolID
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: KindRegimeChange,
				Detail: fmt.Sprintf("%s: regime %s → %s", s.Symbol, ev.From, ev.To),
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
		}
	}

	// ── anomalies since last sweep (Signal8 wave Stage 3) ───────────────
	// Fan stored anomaly rows (internal/anomaly's scanner writes them,
	// already hour-deduped per symbol+kind) out to watchers. The alert kind
	// is the anomaly kind and the detail carries the detector's honest
	// window/baseline/proxy wording verbatim, prefixed with the symbol —
	// e.g. "BTC/USD: unusual buy pressure … (z=+3.1 vs trailing 60m
	// baseline …)". idx_alerts_dedup makes partial-sweep retries no-ops.
	aCursor, firstA := r.cursor(ctx, metaAnomalyCursor)
	sinceTs = 0
	maxA := aCursor
	if firstA {
		sinceTs = now.Add(-24 * time.Hour).Unix()
		if maxA, err = r.St.MaxAnomalyID(ctx); err != nil {
			return "", err
		}
	}
	aEvents, err := r.St.AnomaliesAfterID(ctx, aCursor, sinceTs, sweepBatch)
	if err != nil {
		return "", err
	}
	for _, ev := range aEvents {
		if ev.ID > maxA {
			maxA = ev.ID
		}
		for _, uid := range userIDs {
			s, watched := watch[uid][ev.SymbolID]
			if !watched {
				continue
			}
			sid := ev.SymbolID
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: ev.Kind,
				Detail: fmt.Sprintf("%s: %s", s.Symbol, ev.Detail),
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
		}
	}

	// ── predictions crossing thresholds (per-user dedup: 24h per side) ──
	dedupSince := now.Add(-predictionDedupWindow).Unix()
	freshCutoff := now.Add(-24 * time.Hour).Unix() // ignore stale predictions
	for _, uid := range userIDs {
		for sid, s := range watch[uid] {
			for _, h := range predHorizons {
				p, ok, err := r.St.LatestPrediction(ctx, sid, h)
				if err != nil {
					return "", err
				}
				if !ok || p.Ts < freshCutoff {
					continue
				}
				kind, fire := PredictionKind(p.CalProb, hi, lo)
				if !fire {
					continue
				}
				dup, err := r.St.HasAlertSince(ctx, uid, sid, string(h), kind, dedupSince)
				if err != nil {
					return "", err
				}
				if dup {
					continue
				}
				symID := sid
				if err := r.St.InsertAlert(ctx, store.Alert{
					UserID: uid, SymbolID: &symID, Horizon: string(h), Kind: kind,
					Detail: fmt.Sprintf("%s %s: calibrated P(up) %.0f%% (ensemble n=%d)",
						s.Symbol, h, p.CalProb*100, p.NUsed),
					Ts: now.Unix(),
				}); err != nil {
					return "", err
				}
				created++
			}
		}
	}

	// Advance the sweep cursors only after all inserts succeeded.
	if err := r.St.SetMeta(ctx, metaBreakoutCursor, strconv.FormatInt(maxB, 10)); err != nil {
		return "", err
	}
	if err := r.St.SetMeta(ctx, metaRegimeCursor, strconv.FormatInt(maxR, 10)); err != nil {
		return "", err
	}
	if err := r.St.SetMeta(ctx, metaAnomalyCursor, strconv.FormatInt(maxA, 10)); err != nil {
		return "", err
	}

	// Batched macOS notification: best-effort, cooldown-limited, never fatal.
	if created > 0 && now.Sub(r.lastNotify) > notifyCooldown {
		r.lastNotify = now
		notify := r.Notify
		if notify == nil {
			notify = osascriptNotify
		}
		_ = notify(fmt.Sprintf("%d new SignalDeck alert(s)", created))
	}

	return fmt.Sprintf("created %d alert(s) across %d user(s)", created, len(userIDs)), nil
}

// cursor reads an id cursor from meta; first=true when it was never set.
func (r *Runner) cursor(ctx context.Context, key string) (int64, bool) {
	v, err := r.St.GetMeta(ctx, key)
	if err != nil || v == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, true
	}
	return id, false
}

// osascriptNotify pops a macOS notification (same pattern as internal/health).
// Best-effort: any failure (no osascript, headless session) is never fatal.
func osascriptNotify(msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := fmt.Sprintf("display notification %q with title %q", msg, "SignalDeck alerts")
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}
