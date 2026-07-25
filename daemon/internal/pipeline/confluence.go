// ConfluenceScorer + ConfluenceResolver (CONFLUENCE GATE + MONEY SCOREBOARD
// wave): the workers behind the per-symbol confluence gate. The scorer gathers
// five INDEPENDENT signal families per symbol — smart-money positioning, regime
// trend, the flagship calibrated prediction, cross-sectional relative strength,
// and the freshest breakout — feeds them to the PURE internal/confluence engine,
// upserts the assessment for EVERY symbol, and (only when the gate flags a
// setup) forward-tracks it as an outcome + emits a deduped "confluence setup"
// event. The resolver later grades matured outcomes against realized bars.
//
// HONESTY: this manufactures no edge — it is a strict AND over signals that
// already exist, shown transparently. Reads are best-effort per family (an error
// just makes that family absent this pass, never a fabricated vote), a symbol
// with too few present families simply isn't a setup, and one symbol's failure
// never fails the whole run. Outcomes carry NO lookahead: the resolver fills the
// forward return only once the forward bar exists.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/confluence"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// confluenceHorizon is the single forward-tracking horizon of this wave.
const confluenceHorizon = "1d"

// confluenceHorizonSecs is the forward window a setup needs before it can be
// graded (matches the "1d" horizon; daily bars).
const confluenceHorizonSecs = int64(86400)

// ── ConfluenceScorer ─────────────────────────────────────────────────────────

// ConfluenceScorer computes and persists the per-symbol confluence assessment.
type ConfluenceScorer struct {
	St *store.Store
}

func (w *ConfluenceScorer) Name() string            { return "confluence-scorer" }
func (w *ConfluenceScorer) Interval() time.Duration { return 30 * time.Minute }

func (w *ConfluenceScorer) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	nowUnix := now.Unix()

	// Fleet-wide context loaded ONCE (best-effort — a read error just leaves that
	// family absent for every symbol this pass, never a fabricated vote).
	regimeLbls, err := w.St.RegimeLabels(ctx)
	if err != nil {
		regimeLbls = map[int64]string{}
	}
	rankPcts, err := w.St.RankingPercentiles(ctx)
	if err != nil {
		rankPcts = map[int64]float64{}
	}

	setupTs := now.Truncate(time.Minute).Unix()
	dayStart := (nowUnix / 86400) * 86400 // outcome ts bucket: one independent bet per symbol/day
	dayBucket := now.UTC().Format("2006-01-02")

	scored, setups, events, readErrs, writeErrs := 0, 0, 0, 0, 0

	for _, s := range syms {
		in := confluence.Inputs{}

		// 1) smart_money — the positioning band from the SMART MONEY wave.
		if sm, ok, err := w.St.LatestSmartMoneyScore(ctx, s.ID); err != nil {
			readErrs++
		} else if ok {
			in.SmartMoneyPresent = true
			in.SmartMoneyLabel = sm.Label
			in.SmartMoneyScore = sm.Score
		}

		// 2) trend — the regime classifier's label.
		if lbl, ok := regimeLbls[s.ID]; ok && lbl != "" {
			in.RegimePresent = true
			in.RegimeLabel = lbl
		}

		// 3) prediction — the flagship calibrated P(up) at 1d.
		if p, ok, err := w.St.LatestPrediction(ctx, s.ID, md.H1d); err != nil {
			readErrs++
		} else if ok {
			in.PredictionPresent = true
			in.CalProb = p.CalProb
		}

		// 4) rel_strength — the cross-sectional ranking percentile.
		if pct, ok := rankPcts[s.ID]; ok {
			in.RankPresent = true
			in.RankPct = pct
		}

		// 5) breakout — the freshest trend-creation event (engine applies the
		// freshness gate; a stale one drops out). Direction: donchian by kind,
		// squeeze by the release bar's own body sign.
		if b, ok, err := w.St.LatestBreakoutFor(ctx, s.ID); err != nil {
			readErrs++
		} else if ok {
			in.BreakoutPresent = true
			in.BreakoutKind = b.Kind
			in.BreakoutAgeDays = float64(nowUnix-b.Ts) / 86400
			switch b.Kind {
			case "donchian_up":
				in.BreakoutDir = 1
			case "donchian_down":
				in.BreakoutDir = -1
			case "squeeze_release":
				if bar, okb, err := w.St.BarAtOrBefore(ctx, s.ID, md.TF1d, b.Ts); err == nil && okb {
					if bar.Close > bar.Open {
						in.BreakoutDir = 1
					} else if bar.Close < bar.Open {
						in.BreakoutDir = -1
					}
				}
			}
		}

		setup := confluence.Assess(in)
		blob, err := json.Marshal(setup)
		if err != nil {
			writeErrs++
			continue
		}
		if err := w.St.UpsertConfluenceSetup(ctx, store.ConfluenceSetup{
			SymbolID: s.ID, Ts: setupTs, Direction: setup.Direction,
			Agree: setup.Agree, Dissent: setup.Dissent, Score: setup.Score,
			IsSetup: setup.IsSetup, Payload: string(blob),
		}); err != nil {
			writeErrs++
			continue
		}
		scored++

		if !setup.IsSetup {
			continue
		}
		setups++

		// FORWARD-TRACK the flagged setup: freeze entry_px at the latest close and
		// store one outcome per (symbol, day, horizon) — the day-bucket ts makes
		// the PK enforce independence (one bet per symbol per day).
		entryPx, okPx, err := w.latestClose(ctx, s.ID, nowUnix)
		if err != nil {
			writeErrs++
		} else if okPx {
			if err := w.St.InsertConfluenceOutcome(ctx, store.ConfluenceOutcome{
				SymbolID: s.ID, Ts: dayStart, Horizon: confluenceHorizon,
				Direction: setup.Direction, Agree: setup.Agree, EntryPx: entryPx,
			}); err != nil {
				writeErrs++
			}
		}

		// EVENT (day-bucket deduped): one "confluence setup" per symbol per day.
		fresh, err := w.St.InsertConfluenceEvent(ctx, store.ConfluenceEvent{
			SymbolID: s.ID, Ts: nowUnix, Kind: "confluence_setup",
			Detail:    confluenceDetail(s.Symbol, setup),
			DayBucket: dayBucket,
		})
		if err != nil {
			writeErrs++
		} else if fresh {
			events++
		}
	}

	detail := fmt.Sprintf("assessed %d symbol(s), %d setup(s), %d new event(s)", scored, setups, events)
	if readErrs > 0 {
		detail += fmt.Sprintf("; %d family read(s) unavailable this pass", readErrs)
	}
	if writeErrs > 0 {
		detail += fmt.Sprintf("; %d write error(s)", writeErrs)
	}
	return detail, nil
}

// latestClose returns the symbol's most recent daily close at/before now (the
// entry reference frozen into a forward-tracked outcome). ok=false when the
// symbol has no daily bar yet.
func (w *ConfluenceScorer) latestClose(ctx context.Context, symbolID, now int64) (float64, bool, error) {
	bar, ok, err := w.St.BarAtOrBefore(ctx, symbolID, md.TF1d, now)
	if err != nil || !ok || bar.Close <= 0 {
		return 0, false, err
	}
	return bar.Close, true, nil
}

// confluenceDetail builds the event line: "SYM: N-signal LONG/SHORT confluence
// (families…)", naming the families that AGREE with the setup direction.
func confluenceDetail(symbol string, s confluence.Setup) string {
	var fams []string
	for _, v := range s.Votes {
		if v.Dir == s.Direction {
			fams = append(fams, v.Family)
		}
	}
	return fmt.Sprintf("%s: %d-signal %s confluence (%s)",
		symbol, s.Agree, confluence.DirectionWord(s.Direction), strings.Join(fams, ", "))
}

// ── ConfluenceResolver ───────────────────────────────────────────────────────

// ConfluenceResolver grades matured confluence outcomes against realized bars —
// the money scoreboard's data source. It mirrors the PredictionResolver: no
// lookahead (an outcome is graded only once its forward bar exists).
type ConfluenceResolver struct {
	St *store.Store
}

func (w *ConfluenceResolver) Name() string            { return "confluence-resolver" }
func (w *ConfluenceResolver) Interval() time.Duration { return 15 * time.Minute }

func (w *ConfluenceResolver) Run(ctx context.Context) (string, error) {
	now := time.Now().Unix()
	// Only outcomes at least one horizon old can possibly have a forward bar.
	pending, err := w.St.UnresolvedConfluenceOutcomes(ctx, now-confluenceHorizonSecs, 1500)
	if err != nil {
		return "", err
	}
	resolved := 0
	for _, o := range pending {
		target := o.Ts + confluenceHorizonSecs
		if now < target {
			continue // not matured yet (defensive; the query already filters)
		}
		fwd, ok, err := w.St.BarAtOrAfter(ctx, o.SymbolID, md.TF1d, target)
		if err != nil {
			return "", err
		}
		// No forward bar yet, bad entry, or too large a gap (weekend/halt beyond
		// 3 horizons) → leave it pending rather than grade on a distant bar.
		if !ok || o.EntryPx <= 0 || fwd.Ts-target > 3*confluenceHorizonSecs || fwd.Close <= 0 {
			continue
		}
		fwdReturn := fwd.Close/o.EntryPx - 1
		win := (o.Direction > 0 && fwdReturn > 0) || (o.Direction < 0 && fwdReturn < 0)
		if err := w.St.ResolveConfluenceOutcome(ctx, o.SymbolID, o.Ts, o.Horizon, fwdReturn, win); err != nil {
			return "", err
		}
		resolved++
	}
	return fmt.Sprintf("resolved %d confluence outcome(s)", resolved), nil
}
