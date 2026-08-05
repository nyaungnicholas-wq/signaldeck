// SelfAuditor (self-audit / drift-watchdog wave): the honesty watchdog. Once
// per UTC day it MEASURES the platform's own reliability from resolved history
// and records findings — deterministically, no LLM — to the queryable
// self_audit table and one summary insight(kind self_audit). It answers "is the
// system still as honest as it claims?" with three checks, every one gated at
// n>=selfAuditMinN independent resolutions so a thin sample never raises a false
// alarm:
//
//   - CALIBRATION DRIFT (per horizon): reliability = mean |cal_prob − realized|
//     over recent resolved predictions, compared to the last audit — flagged
//     "degrading" when it worsens past a threshold.
//   - FACTOR-IC SIGN FLIP (per ensemble leg): the adaptive attribution's
//     information coefficient flipping sign vs the last audit — a leg that
//     predicted up now predicting down is model instability.
//   - PREDICTION BIAS (per horizon): mean(cal_prob) vs the realized base rate —
//     flagged systematic over/under-confidence.
//
// Independence: resolved predictions are collapsed to one observation per
// (symbol, UTC-day) before any statistic, mirroring the /honesty + track-record
// dedup — the minute-cadence pipeline otherwise pseudo-replicates the same daily
// move.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/adaptive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// selfAuditMinN is the independent-resolution floor below which a check is
// "insufficient" (never alarmed). Mirrors the n>=30 honesty gates elsewhere.
const selfAuditMinN = 30

// Drift/flip thresholds (measured, stated in every detail line):
//   - calibrationDriftThreshold: reliability (mean abs calibration error, in
//     [0,1]) worsening by more than this vs the last audit is "degrading";
//   - biasThreshold: |mean(cal_prob) − base rate| past this is over/under-confident;
//   - icSignEpsilon: an IC within ±this of zero is treated as no-signal, so a
//     jitter across zero is NOT called a sign flip.
//   - calibrationAtChanceThreshold: reliability AT OR ABOVE this is
//     indistinguishable from a coin flip, whichever direction it drifted from.
//     0.50 is exactly what a constant p=0.5 scores.
const (
	calibrationDriftThreshold    = 0.02
	biasThreshold                = 0.05
	icSignEpsilon                = 0.02
	calibrationAtChanceThreshold = 0.49
)

// dayFoldInflationThreshold is the share of phantom day-buckets — days that
// exist only because a trading session was cut at UTC midnight — above which
// the independence unit is called inflated. Any phantom day is a real
// overstatement of effective N, so this is deliberately near zero; 1% leaves
// room for a stray after-hours print without excusing a systematic split.
// Measured 3.94% on the live corpus 2026-08-05, so this flags today by design.
const dayFoldInflationThreshold = 0.01

// dayFoldAuditWindowDays bounds the fold measurement to recent history: the
// question is whether the CURRENT writer is splitting sessions, and a full-table
// scan of every feature row ever written is neither cheap nor relevant to that.
const dayFoldAuditWindowDays = 90

// SelfAuditor is the drift-watchdog worker.
type SelfAuditor struct {
	St *store.Store
}

func (w *SelfAuditor) Name() string            { return "self-audit" }
func (w *SelfAuditor) Interval() time.Duration { return 6 * time.Hour }

func (w *SelfAuditor) Run(ctx context.Context) (string, error) {
	now := time.Now()
	today := now.UTC().Format("2006-01-02")
	// Once per UTC day: the 6h tick is a heartbeat; the meta cursor does the
	// pacing (with catch-up later in the day if the daemon was down at midnight).
	if last, _ := w.St.GetMeta(ctx, "self_audit_day"); last == today {
		return "already audited today (" + today + ")", nil
	}

	var findings []string
	write := func(metric string, value float64, status, detail string) error {
		findings = append(findings, metric+"="+status)
		return w.St.InsertSelfAudit(ctx, store.SelfAuditRow{
			Ts: now.Unix(), Metric: metric, Value: value, Status: status, Detail: detail,
		})
	}

	// ── calibration drift + prediction bias, per predicted horizon ──────────
	for _, h := range predHorizons {
		outs, err := w.St.ResolvedPredictionOutcomes(ctx, h, 20000)
		if err != nil {
			return "", err
		}
		pts := independentPreds(outs)
		n := len(pts)
		if n < selfAuditMinN {
			reason := fmt.Sprintf("only %d independent resolved predictions (<%d) — not enough to judge", n, selfAuditMinN)
			if err := write("calibration:"+string(h), 0, "insufficient", reason); err != nil {
				return "", err
			}
			if err := write("prediction_bias:"+string(h), 0, "insufficient", reason); err != nil {
				return "", err
			}
			continue
		}
		var sumAbs, sumProb, sumUp float64
		for _, p := range pts {
			sumAbs += math.Abs(p.prob - p.up)
			sumProb += p.prob
			sumUp += p.up
		}
		reliability := sumAbs / float64(n)
		meanProb := sumProb / float64(n)
		baseRate := sumUp / float64(n)

		// Calibration drift vs the last measured audit.
		status, detail := "ok", fmt.Sprintf("reliability %.4f = mean|cal_prob−realized| over %d independent obs", reliability, n)
		if prior, ok := w.St.LastSelfAuditValue(ctx, "calibration:"+string(h)); ok {
			delta := reliability - prior
			detail += fmt.Sprintf("; prior %.4f (Δ%+.4f, worse>%.3f flags)", prior, delta, calibrationDriftThreshold)
			if delta > calibrationDriftThreshold {
				status = "degrading"
			}
		} else {
			detail += "; baseline recorded (no prior audit)"
		}
		if err := write("calibration:"+string(h), reliability, status, detail); err != nil {
			return "", err
		}

		// Calibration LEVEL, measured independently of drift. The check above
		// compares reliability only to its own prior value, so a model that has
		// been at chance since the day it was born never changes and therefore
		// never flags: on 2026-08-02 this reported "ok" at reliability 0.4982 —
		// a coin flip — because it had moved +0.0007 against a 0.020 threshold.
		// Emitted as its own metric rather than overloading the drift status, so
		// both answers stay readable.
		levelStatus := "ok"
		if reliability >= calibrationAtChanceThreshold {
			levelStatus = "at_chance"
		}
		if err := write("calibration_level:"+string(h), reliability, levelStatus,
			fmt.Sprintf("reliability %.4f vs at-chance threshold %.4f — LEVEL check, "+
				"independent of drift: a model that has always been at chance never "+
				"changes, so the drift check alone can never flag it (%d independent obs)",
				reliability, calibrationAtChanceThreshold, n)); err != nil {
			return "", err
		}

		// Prediction bias: mean confidence vs realized base rate.
		bias := meanProb - baseRate
		bstatus := "ok"
		if bias > biasThreshold {
			bstatus = "over_confident"
		} else if bias < -biasThreshold {
			bstatus = "under_confident"
		}
		if err := write("prediction_bias:"+string(h), bias,
			bstatus, fmt.Sprintf("mean(cal_prob) %.3f vs realized base rate %.3f (Δ%+.3f) over %d obs", meanProb, baseRate, bias, n)); err != nil {
			return "", err
		}
	}

	// ── factor-IC sign flip, per ensemble leg (adaptive AllCell attribution) ──
	var learned adaptive.Weights
	if raw, err := w.St.GetMeta(ctx, adaptive.MetaKey); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &learned); err != nil {
			learned = adaptive.Weights{}
		}
	}
	cell := learned.Cells[adaptive.AllCell]
	for _, leg := range ensemble.LegNames {
		metric := "factor_ic:" + leg
		n := cell.LegN[leg]
		if n < selfAuditMinN {
			if err := write(metric, 0, "insufficient",
				fmt.Sprintf("leg has %d graded samples (<%d) in the pooled attribution", n, selfAuditMinN)); err != nil {
				return "", err
			}
			continue
		}
		ic := cell.IC[leg]
		status, detail := "ok", fmt.Sprintf("IC %+.4f over %d graded samples", ic, n)
		if prior, ok := w.St.LastSelfAuditValue(ctx, metric); ok {
			detail += fmt.Sprintf("; prior IC %+.4f", prior)
			if signFlipped(prior, ic) {
				status = "sign_flip"
			}
		} else {
			detail += "; baseline recorded (no prior audit)"
		}
		if err := write(metric, ic, status, detail); err != nil {
			return "", err
		}
	}

	// ── day-fold inflation: is the independence unit itself still honest? ───
	//
	// Every statistic above divides by a count of independent (symbol, day)
	// observations, so all of them inherit whatever the day fold gets wrong.
	// The fold is a bare ts/86400 — a UTC-midnight cut — and the US extended
	// session closes at 20:00 ET, which is 00:00Z under EDT and 01:00Z under
	// EST. The tail of a session therefore lands in the NEXT UTC day and is
	// counted as a second independent observation of the same day's move.
	//
	// This check measures the gap on real stored rows rather than assuming it
	// is zero — it was assumed to be zero once, on the reasoning that the
	// predictor only writes during regular hours, and the corpus disagreed.
	{
		since := now.Unix() - dayFoldAuditWindowDays*md.SecondsPerDay
		utcDays, tradingDays, err := w.St.StockFeatureDayFold(ctx, since)
		if err != nil {
			return "", err
		}
		if tradingDays == 0 {
			if err := write("day_fold_inflation", 0, "insufficient",
				fmt.Sprintf("no stock feature rows in the last %d days — nothing to fold",
					dayFoldAuditWindowDays)); err != nil {
				return "", err
			}
		} else {
			phantom := utcDays - tradingDays
			rate := float64(phantom) / float64(tradingDays)
			status := "ok"
			if rate > dayFoldInflationThreshold {
				status = "inflated"
			}
			if err := write("day_fold_inflation", rate, status, fmt.Sprintf(
				"%d (symbol, UTC-day) buckets vs %d (symbol, trading-day) buckets over the last "+
					"%d days — %d phantom days, %.2f%% overstatement of effective N (flags above "+
					"%.2f%%). A US extended session closes 20:00 ET = 00:00Z (EDT) / 01:00Z (EST), "+
					"so its tail folds into the next UTC day and is counted twice. Every published "+
					"interval divides by this count, so the excess narrows intervals in the "+
					"direction that flatters the platform",
				utcDays, tradingDays, dayFoldAuditWindowDays, phantom,
				rate*100, dayFoldInflationThreshold*100)); err != nil {
				return "", err
			}
		}
	}

	if err := w.St.InsertInsight(ctx, selfAuditInsight(now, findings)); err != nil {
		return "", err
	}
	if err := w.St.SetMeta(ctx, "self_audit_day", today); err != nil {
		return "", err
	}
	return fmt.Sprintf("self-audit %s: %s", today, strings.Join(findings, ", ")), nil
}

// predObs is one independent (symbol, UTC-day) resolved prediction.
type predObs struct {
	prob float64
	up   float64
}

// independentPreds collapses resolved predictions to ONE observation per
// (symbol, UTC-day), keeping the newest (outs is newest-first). This is the
// effective independent sample the self-audit statistics are computed over.
func independentPreds(outs []store.ResolvedPredictionOutcome) []predObs {
	type key struct {
		sym int64
		day int64
	}
	seen := make(map[key]struct{}, len(outs))
	out := make([]predObs, 0, len(outs))
	for _, o := range outs {
		k := key{sym: o.SymbolID, day: o.Ts / 86400}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, predObs{prob: o.Prob, up: float64(o.Up)})
	}
	return out
}

// signFlipped reports whether two ICs are both meaningfully non-zero and of
// opposite sign — a factor that predicted one direction now predicting the
// other. A value within ±icSignEpsilon of zero is no-signal, not a flip.
func signFlipped(prior, cur float64) bool {
	if math.Abs(prior) < icSignEpsilon || math.Abs(cur) < icSignEpsilon {
		return false
	}
	return (prior > 0) != (cur > 0)
}

// selfAuditInsight composes the one market-scope insight (kind self_audit) that
// carries the run's findings, flagging any that are not "ok".
func selfAuditInsight(now time.Time, findings []string) md.Insight {
	var flagged []string
	for _, f := range findings {
		if i := strings.LastIndex(f, "="); i >= 0 {
			switch f[i+1:] {
			case "ok", "insufficient":
			default:
				flagged = append(flagged, f)
			}
		}
	}
	headline := "Self-audit: no drift detected"
	body := "The daily self-audit measured the platform's own calibration, factor-IC stability, and prediction bias from resolved history. All measured checks are within tolerance (checks below the n>=30 gate are marked insufficient, not judged)."
	if len(flagged) > 0 {
		headline = fmt.Sprintf("Self-audit flagged %d drift signal(s)", len(flagged))
		body = "The daily self-audit measured the platform's own honesty from resolved history and flagged: " +
			strings.Join(flagged, ", ") + ". See /api/self-audit for each metric's measured value and detail."
	}
	data, _ := json.Marshal(map[string]any{"kind": "self_audit", "findings": findings, "flagged": flagged})
	return md.Insight{
		Scope:    "market",
		Ts:       now.Unix(),
		Headline: headline,
		Body:     body,
		Data:     string(data),
	}
}
