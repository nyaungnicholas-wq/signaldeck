// Research Lab — postmortem-runner worker.
//
// PRINCIPLE (Chief-Quant): "Every failure is a research opportunity." Each
// resolved, WRONG, meaningfully-convicted prediction is attributed to a ranked
// failure taxonomy (internal/postmortem) and stored, so misses can be CLUSTERED
// and fed back into research instead of being forgotten. This is the raw-material
// step the rest of the Research Lab consumes.
//
// The worker is idempotent and self-healing: it processes only misses with no
// postmortem row yet (store.UnPostmortemedMisses), and INSERT OR IGNORE means a
// crash mid-batch simply resumes next tick. It NEVER fails the fleet — any error
// records a dq event and returns.
//
// NO fabrication: a Case is built only from evidence already on disk at
// resolution time (the prediction's own components + n_used, regime changes
// inside its forward window, in-window news tone). Where a signal is a
// current-state proxy rather than a point-in-time snapshot (VIX regime), it is
// documented as such and used only as weak corroboration, never as the sole
// cause — the taxonomy's honest default for an unexplained miss is Unexplained.
package pipeline

import (
	"context"
	"fmt"
	"math"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/postmortem"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// PostmortemWorker attributes and stores failure reports for resolved misses.
type PostmortemWorker struct {
	St    *store.Store
	Now   func() time.Time // injectable clock for tests; nil ⇒ time.Now
	Thr   postmortem.Thresholds
	Limit int // max misses processed per horizon per tick
}

// NewPostmortemWorker builds the worker with default thresholds.
func NewPostmortemWorker(st *store.Store) *PostmortemWorker {
	return &PostmortemWorker{St: st, Thr: postmortem.DefaultThresholds(), Limit: 2000}
}

func (w *PostmortemWorker) Name() string            { return "postmortem-runner" }
func (w *PostmortemWorker) Interval() time.Duration { return 1 * time.Hour }

func (w *PostmortemWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// horizonSeconds is the forward window length for regime/news in-window lookups.
func horizonSeconds(h md.Horizon) int64 {
	switch h {
	case md.H1h:
		return 3600
	case md.H1d:
		return 86400
	default: // H1w
		return 604800
	}
}

func (w *PostmortemWorker) Run(ctx context.Context) (string, error) {
	nowUnix := w.now().Unix()
	total := 0
	byReason := map[postmortem.ReasonCode]int{}

	for _, h := range predHorizons {
		// One calibration snapshot per horizon: bucketed realized hit-rate so a
		// miss whose prob bucket has been historically overconfident can cite it.
		calib := buildCalibBuckets(ctx, w.St, h)

		misses, err := w.St.UnPostmortemedMisses(ctx, h, w.Limit)
		if err != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: nowUnix, Kind: "postmortem_error",
				Detail: fmt.Sprintf("load misses %s: %v", h, err),
			})
			continue
		}
		for _, m := range misses {
			c := w.buildCase(ctx, m, h, calib)
			rep := postmortem.Classify(c, w.Thr)
			if err := w.St.InsertPostmortem(ctx, m, rep, nowUnix); err != nil {
				_ = w.St.InsertDQ(ctx, md.DQEvent{
					Ts: nowUnix, Kind: "postmortem_error",
					Detail: fmt.Sprintf("store %s %s: %v", m.Symbol, h, err),
				})
				continue
			}
			total++
			byReason[rep.Primary]++
		}
	}

	if total == 0 {
		return "no new misses to postmortem", nil
	}
	return fmt.Sprintf("postmortem'd %d misses; primary reasons: %v", total, byReason), nil
}

// buildCase assembles the evidence for one miss from the store.
func (w *PostmortemWorker) buildCase(ctx context.Context, m store.MissRow, h md.Horizon, calib calibBuckets) postmortem.Case {
	c := postmortem.Case{
		Prob:         m.Prob,
		Up:           m.Up,
		FwdReturn:    m.FwdReturn,
		NUsed:        m.NUsed,
		Disagreement: legDisagreement(m.Components),
	}

	// Regime change inside the forward window ⇒ the tape switched under the call.
	winEnd := m.Ts + horizonSeconds(h)
	if changes, err := w.St.RegimeChangesForSymbol(ctx, m.SymbolID, m.Ts, winEnd); err == nil && len(changes) > 0 {
		c.RegimeChanged = true
		c.RegimeNote = changes[0].From + "→" + changes[0].To
	}

	// In-window news tone: did fresh sentiment point AGAINST the call?
	if mean, n, err := w.St.NewsSentimentAgg(ctx, m.SymbolID, m.Ts); err == nil && n > 0 {
		against := (m.Prob > 0.5 && mean < 0) || (m.Prob < 0.5 && mean > 0)
		if against {
			c.NewsAgainst = true
			c.NewsMag = math.Abs(mean)
		}
	}

	// Macro vol regime (current-state proxy — slow-moving, weak corroboration).
	if vix, ok, err := w.St.LatestVIX(ctx); err == nil && ok {
		c.VIXRegime = vixRegime(vix)
	}

	// Calibration: was this prob bucket historically overconfident?
	if def, known := calib.deficit(m.Prob); known {
		c.CalibDeficit = def
		c.CalibKnown = true
	}
	return c
}

// legDisagreement returns the mean absolute distance of the available component
// legs from 0.5 (in [0,0.5]). A LOW value means the legs collectively sat near a
// coin-flip, so the ensemble's directional call rested on thin internal
// conviction. Legs are read from the serialized ensemble.Components (no json
// tags ⇒ Go field names) and mapped into P(up) space. Gated legs (lift<=0) are
// excluded, matching how the ensemble itself drops them.
func legDisagreement(comp map[string]float64) float64 {
	if len(comp) == 0 {
		return 0.5 // unknown ⇒ do NOT trigger the thin-conviction reason
	}
	var legs []float64
	if v, ok := comp["PressureScore"]; ok {
		legs = append(legs, (v+1)/2) // [-1,1] → [0,1]
	}
	if v, ok := comp["ExpectancyHitRate"]; ok {
		legs = append(legs, v)
	}
	if p, ok := comp["ForecastProb"]; ok {
		if lift, lok := comp["ForecastLift"]; !lok || lift > 0 {
			legs = append(legs, p)
		}
	}
	if p, ok := comp["GBMProb"]; ok {
		if lift, lok := comp["GBMLift"]; !lok || lift > 0 {
			legs = append(legs, p)
		}
	}
	if p, ok := comp["MeanRevProb"]; ok {
		if lift, lok := comp["MeanRevLift"]; !lok || lift > 0 {
			legs = append(legs, p)
		}
	}
	if v, ok := comp["AlphaXProb"]; ok {
		legs = append(legs, v)
	}
	if len(legs) == 0 {
		return 0.5
	}
	var sum float64
	for _, p := range legs {
		sum += math.Abs(p - 0.5)
	}
	return sum / float64(len(legs))
}

// vixRegime maps a VIX level to the 0..3 calm/normal/elevated/stressed band.
func vixRegime(vix float64) int {
	switch {
	case vix >= 25:
		return 3
	case vix >= 20:
		return 2
	case vix >= 15:
		return 1
	default:
		return 0
	}
}

// calibBuckets holds realized hit-rate by 0.1-wide probability bucket.
type calibBuckets struct {
	realized map[int]float64 // bucket index 0..9 → mean realized up
	known    map[int]bool
}

// deficit returns (realized hit-rate − implied prob) for the bucket containing
// prob, and whether the bucket had data. Negative ⇒ overconfident.
func (b calibBuckets) deficit(prob float64) (float64, bool) {
	idx := int(prob * 10)
	if idx > 9 {
		idx = 9
	}
	if idx < 0 {
		idx = 0
	}
	if b.known == nil || !b.known[idx] {
		return 0, false
	}
	return b.realized[idx] - prob, true
}

// buildCalibBuckets bins resolved (pred, up) pairs into 0.1-wide buckets and
// computes each bucket's realized up-rate. Uses the same resolved history the
// track-record reads (no lookahead — every pair is a resolved outcome).
func buildCalibBuckets(ctx context.Context, st *store.Store, h md.Horizon) calibBuckets {
	b := calibBuckets{realized: map[int]float64{}, known: map[int]bool{}}
	probs, ups, err := st.ResolvedPredictionPairs(ctx, h, 20000)
	if err != nil || len(probs) == 0 {
		return b
	}
	sum := map[int]float64{}
	cnt := map[int]int{}
	for i := range probs {
		idx := int(probs[i] * 10)
		if idx > 9 {
			idx = 9
		}
		if idx < 0 {
			idx = 0
		}
		sum[idx] += ups[i]
		cnt[idx]++
	}
	const minBucket = 20 // need enough resolutions before trusting a bucket rate
	for idx, n := range cnt {
		if n >= minBucket {
			b.realized[idx] = sum[idx] / float64(n)
			b.known[idx] = true
		}
	}
	return b
}
