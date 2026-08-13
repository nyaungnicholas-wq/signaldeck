// Package alerts is the per-user alert engine: a 5-minute sweep that turns
// stored events (breakouts, regime changes) and calibrated predictions
// crossing conviction thresholds into actionable, watchlist-scoped alert
// rows — plus a batched macOS notification so events become pings instead of
// a tab you must remember to open.
package alerts

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/envcfg"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
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
	// SMART MONEY FACTS wave: positioning-event kinds. These mirror the
	// smart_money_events kinds 1:1 (internal/smartmoney + pipeline). Both are
	// reads of POSITIONING — what informed participants are DOING — never a
	// forecast; the fanned-out detail carries the scorer's wording verbatim.
	KindInsiderCluster = "insider_cluster"
	KindSqueeze        = "squeeze_setup"
	// CONFLUENCE GATE wave: the confluence-setup kind. Mirrors the
	// confluence_events kind 1:1 (internal/confluence + pipeline). It fires only
	// when several INDEPENDENT signal families AGREE on a direction — no
	// manufactured edge; the fanned-out detail carries the scorer's wording
	// verbatim ("SYM: N-signal LONG/SHORT confluence (families…)").
	KindConfluence = "confluence_setup"
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
	// SMART MONEY FACTS wave: smart-money-event sweep rowid cursor (same
	// gap-free id-cursor pattern as breakouts/regime/anomalies).
	metaSmartMoneyCursor = "alerts_last_smart_money_id"
	// CONFLUENCE GATE wave: confluence-event sweep rowid cursor (same gap-free
	// id-cursor pattern as every other event table).
	metaConfluenceCursor = "alerts_last_confluence_id"
)

// predictionDedupWindow: at most one prediction alert per
// (user, symbol, horizon, side) per this window.
const predictionDedupWindow = 24 * time.Hour

// notifyCooldown limits notifications to one per 30 minutes. Stage 3: the
// SAME single cooldown gates the macOS popup AND every remote transport
// (Discord/Telegram/webhook) — one shared clock, deliberately not per-transport.
const notifyCooldown = 30 * time.Minute

// notifyMaxLines caps the per-sweep batched remote message at this many alert
// detail lines; overflow is summarized as "+N more".
const notifyMaxLines = 5

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
	} else if hiStr != "" {
		envcfg.Reject("SIGNALDECK_ALERT_HI", hiStr, "not a number",
			strconv.FormatFloat(DefaultHi, 'g', -1, 64))
	}
	if loStr != "" && errL == nil {
		lo = l
	} else if loStr != "" {
		envcfg.Reject("SIGNALDECK_ALERT_LO", loStr, "not a number",
			strconv.FormatFloat(DefaultLo, 'g', -1, 64))
	}
	if !(lo > 0 && hi < 1 && lo < hi) {
		// Both are reported: the pair is rejected as a pair, and naming only one
		// would send an operator to fix a value that was individually fine.
		if hiStr != "" || loStr != "" {
			envcfg.Reject("SIGNALDECK_ALERT_HI", hiStr, "alert band invalid (need 0 < lo < hi < 1)",
				strconv.FormatFloat(DefaultHi, 'g', -1, 64))
			envcfg.Reject("SIGNALDECK_ALERT_LO", loStr, "alert band invalid (need 0 < lo < hi < 1)",
				strconv.FormatFloat(DefaultLo, 'g', -1, 64))
		}
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
	// Notify shows a user-facing alert; nil = notify.Local (the platform's
	// desktop popup, or an explicit unsupported error).
	Notify func(msg string) error
	// Remote fans the same batched sweep message out to the env-configured
	// remote transports (Discord/Telegram/generic webhook — internal/notify);
	// nil or unconfigured = macOS-only. Shares the SAME 30m cooldown as the
	// macOS popup, and its failures degrade to dq events (never the sweep).
	Remote *notify.Notifier
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

	// Stage 3 (remote delivery): collect UNIQUE event lines for the ONE
	// batched remote message per sweep. Fan-out to multiple watchers repeats
	// the same detail — the notification is device-level, so each distinct
	// event appears once. Capped at notifyMaxLines; the unique total drives
	// the "+N more" overflow.
	lineSeen := map[string]struct{}{}
	var lines []string
	uniqueEvents := 0
	addLine := func(detail string) {
		if _, dup := lineSeen[detail]; dup {
			return
		}
		lineSeen[detail] = struct{}{}
		uniqueEvents++
		if len(lines) < notifyMaxLines {
			lines = append(lines, detail)
		}
	}

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
			detail := fmt.Sprintf("%s: %s %s", s.Symbol, ev.Kind, ev.Detail)
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: KindBreakout,
				Detail: detail,
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
			addLine(detail)
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
			detail := fmt.Sprintf("%s: regime %s → %s", s.Symbol, ev.From, ev.To)
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: KindRegimeChange,
				Detail: detail,
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
			addLine(detail)
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
			detail := fmt.Sprintf("%s: %s", s.Symbol, ev.Detail)
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: ev.Kind,
				Detail: detail,
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
			addLine(detail)
		}
	}

	// ── smart-money events since last sweep (SMART MONEY FACTS wave) ────
	// Fan stored insider-cluster / squeeze-setup events (the smart-money-scorer
	// writes them, already day-deduped per symbol+kind) out to watchers. The
	// alert kind IS the event kind and the detail carries the scorer's
	// positioning wording verbatim (already symbol-prefixed — e.g. "NVDA: 3
	// insiders net-bought $2.1M (Form 4)"). These are reads of POSITIONING,
	// what informed participants are DOING, NEVER a forecast.
	// idx_alerts_dedup makes partial-sweep retries no-ops.
	smCursor, firstSM := r.cursor(ctx, metaSmartMoneyCursor)
	sinceTs = 0
	maxSM := smCursor
	if firstSM {
		sinceTs = now.Add(-24 * time.Hour).Unix()
		if maxSM, err = r.St.MaxSmartMoneyEventID(ctx); err != nil {
			return "", err
		}
	}
	smEvents, err := r.St.SmartMoneyEventsAfterID(ctx, smCursor, sinceTs, sweepBatch)
	if err != nil {
		return "", err
	}
	for _, ev := range smEvents {
		if ev.ID > maxSM {
			maxSM = ev.ID
		}
		for _, uid := range userIDs {
			if _, watched := watch[uid][ev.SymbolID]; !watched {
				continue
			}
			sid := ev.SymbolID
			detail := ev.Detail // already "SYM: …" (scorer wording, verbatim)
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: ev.Kind,
				Detail: detail,
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
			addLine(detail)
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
				detail := fmt.Sprintf("%s %s: calibrated P(up) %.0f%% (ensemble n=%d)",
					s.Symbol, h, p.CalProb*100, p.NUsed)
				if err := r.St.InsertAlert(ctx, store.Alert{
					UserID: uid, SymbolID: &symID, Horizon: string(h), Kind: kind,
					Detail: detail,
					Ts:     now.Unix(),
				}); err != nil {
					return "", err
				}
				created++
				addLine(detail)
			}
		}
	}

	// ── confluence setups since last sweep (CONFLUENCE GATE wave) ──────
	// Fan stored "confluence setup" events (the confluence-scorer writes them,
	// already day-deduped per symbol) out to watchers. A setup fires only when
	// several INDEPENDENT signal families AGREE on a direction — no manufactured
	// edge; the detail carries the scorer's wording verbatim (already symbol-
	// prefixed, e.g. "NVDA: 4-signal LONG confluence (smart_money, trend, ...)").
	// idx_alerts_dedup makes partial-sweep retries no-ops.
	cCursor, firstC := r.cursor(ctx, metaConfluenceCursor)
	sinceTs = 0
	maxC := cCursor
	if firstC {
		sinceTs = now.Add(-24 * time.Hour).Unix()
		if maxC, err = r.St.MaxConfluenceEventID(ctx); err != nil {
			return "", err
		}
	}
	cEvents, err := r.St.ConfluenceEventsAfterID(ctx, cCursor, sinceTs, sweepBatch)
	if err != nil {
		return "", err
	}
	for _, ev := range cEvents {
		if ev.ID > maxC {
			maxC = ev.ID
		}
		for _, uid := range userIDs {
			if _, watched := watch[uid][ev.SymbolID]; !watched {
				continue
			}
			sid := ev.SymbolID
			detail := ev.Detail // already "SYM: ..." (scorer wording, verbatim)
			if err := r.St.InsertAlert(ctx, store.Alert{
				UserID: uid, SymbolID: &sid, Kind: ev.Kind,
				Detail: detail,
				Ts:     ev.Ts,
			}); err != nil {
				return "", err
			}
			created++
			addLine(detail)
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
	if err := r.St.SetMeta(ctx, metaSmartMoneyCursor, strconv.FormatInt(maxSM, 10)); err != nil {
		return "", err
	}
	if err := r.St.SetMeta(ctx, metaConfluenceCursor, strconv.FormatInt(maxC, 10)); err != nil {
		return "", err
	}

	// Batched notification: best-effort, cooldown-limited, never fatal.
	// ONE shared 30m cooldown gates the macOS popup AND all remote transports.
	if created > 0 && now.Sub(r.lastNotify) > notifyCooldown {
		r.lastNotify = now
		local := r.Notify
		if local == nil {
			local = notify.Local
		}
		// The error was discarded here (`_ = local(...)`). That is how the
		// macOS-only osascript path stayed invisible for the whole Windows
		// migration: the watchdog at least logged its failure, this did not.
		// Best-effort still means never fatal — it does not mean unobserved.
		if err := local(fmt.Sprintf("%d new SignalDeck alert(s)", created)); err != nil {
			slog.Warn("alerts: local notification failed", "err", err)
		}
		// Stage 3: ONE batched remote message per sweep (Discord/Telegram/
		// webhook). Send degrades to dq events internally and never errors,
		// so a dead webhook can never fail the sweep.
		if r.Remote != nil {
			title, body := BatchMessage(created, uniqueEvents, lines)
			r.Remote.Send(ctx, notify.Message{Title: title, Body: body, Kind: "alerts", Ts: now.Unix()})
		}
	}

	return fmt.Sprintf("created %d alert(s) across %d user(s)", created, len(userIDs)), nil
}

// BatchMessage builds the ONE batched per-sweep remote notification.
// created = alert rows inserted (fan-out across users counts), events =
// distinct event lines observed, lines = the first <=notifyMaxLines of them.
// Overflow beyond the shown lines is summarized honestly as "+N more".
func BatchMessage(created, events int, lines []string) (title, body string) {
	title = fmt.Sprintf("SignalDeck: %d new alert(s)", created)
	if len(lines) > notifyMaxLines {
		lines = lines[:notifyMaxLines]
	}
	body = strings.Join(lines, "\n")
	if extra := events - len(lines); extra > 0 {
		if body != "" {
			body += "\n"
		}
		body += fmt.Sprintf("+%d more", extra)
	}
	return title, body
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

// The local desktop popup now lives in notify.Local (platform-dispatched).
// The osascriptNotify that stood here was macOS-only, so on Windows every
// alert notification failed — silently, because its error was discarded.
// See internal/notify/local.go.
