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
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// confluenceHorizon is the single forward-tracking horizon of this wave.
const confluenceHorizon = "1d"

// confluenceHorizonSecs is the forward window a setup needs before it can be
// graded (matches the "1d" horizon; daily bars).
const confluenceHorizonSecs = int64(86400)

// IsConfluenceBettableDay reports whether a bet may be OPENED at this instant,
// for a symbol in this market.
//
// THE CALENDAR IS PER-MARKET, and getting that wrong is what this signature
// exists to prevent. Crypto trades every day: BTC/USD and its peers print a
// daily bar on all 25 weekend days of a 90-day window, and the record already
// holds 11 weekend crypto setups, 9 of them graded. Gating those on the NYSE
// calendar would silently stop a 24/7 book two days in seven.
//
// For stocks the session calendar is the whole point. The scorer runs every 30
// minutes including weekends, so a bucket on a day the exchange never opened has
// no bar of its own; its entry leg then comes from the previous session and the
// same move is published once per calendar day. That is the pseudo-replication
// this wave removed — RNWWW's identical +93.33% on three consecutive buckets.
//
// This is belt to the braces of the bar-existence check at the call site
// (entryTs >= dayStart), which is market-agnostic and catches most of the same
// cases. Both are kept because neither alone is enough: the bar check would
// still admit a stock whose weekend bucket happened to carry a vendor pad, and
// this one would still admit a stock on a session whose bar has not arrived yet.
//
// It is exported and takes a bare unix second so the rule can be exercised
// directly. The alternative — asserting it through ConfluenceScorer.Run — would
// need all five independent signal families stood up before the calendar branch
// is even reached, and a test that expensive to write is a test that stops being
// written.
func IsConfluenceBettableDay(unix int64, market md.Market) bool {
	if market == md.Crypto {
		return true
	}
	return marketcal.IsTradingDay(time.Unix(unix, 0).In(marketcal.Loc()))
}

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

	// A BET CANNOT BE PLACED ON A DAY THE MARKET IS SHUT.
	//
	// The bucket above is a UTC calendar day, and the scorer runs every 30
	// minutes including weekends. A setup that persisted over a weekend
	// therefore opened three outcomes — Friday, Saturday, Sunday — and the
	// resolver graded all three against the SAME pair of bars, because both its
	// entry and its forward read fall back to the nearest bar in each direction
	// and there is no bar between Friday and Monday. RNWWW booked the identical
	// +93.33% move on 2026-07-17, 07-18 and 07-19; NXGLW and AXTI did the same.
	// One price observation was entering the published mean up to three times as
	// three "independent bets".
	//
	// Refusing to open an outcome on a non-trading day removes the duplicates at
	// the source. The ASSESSMENT still runs and is still stored for every symbol
	// — a reader looking at the weekend sees the current confluence state; it
	// simply does not become a graded bet.
	// The calendar gate is evaluated PER SYMBOL below: it depends on the market.

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
		// the PK enforce independence (one bet per symbol per day). The store
		// assigns episode_ts in the same statement, so a setup that persists
		// across consecutive days stays ONE episode rather than becoming one new
		// bet per day.
		// AND THE BUCKET'S OWN SESSION MUST HAVE PRINTED.
		//
		// The bucket is a UTC day and US daily bars are stamped 04:00/05:00 UTC,
		// so between 00:00 and the session's bar there is a window in which the
		// bucket exists and its bar does not. A setup scored there would freeze
		// the PREVIOUS day's close as its entry, and the resolver — which now
		// requires the entry bar to be inside the bucket — could never grade it.
		// Measured on the first pass after this shipped: 59 of 61 rows opened on
		// 2026-08-21 were dead on arrival for exactly this reason, and because
		// the insert is INSERT OR IGNORE on (symbol, ts, horizon) they would also
		// have BLOCKED the real setup once the session opened.
		//
		// So the bet is simply not opened until the day it belongs to has a bar.
		if IsConfluenceBettableDay(nowUnix, s.Market) {
			entryPx, entryTs, okPx, err := w.latestClose(ctx, s.ID, nowUnix)
			if err != nil {
				writeErrs++
			} else if okPx && entryTs >= dayStart {
				if err := w.St.InsertConfluenceOutcome(ctx, store.ConfluenceOutcome{
					SymbolID: s.ID, Ts: dayStart, Horizon: confluenceHorizon,
					Direction: setup.Direction, Agree: setup.Agree,
					EntryPx: entryPx, EntryTs: entryTs,
				}); err != nil {
					writeErrs++
				}
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
	// scored counts SUCCESSFUL upserts, so scored==0 with write errors means
	// every write failed and this pass persisted nothing — yet the run was
	// still filed as status "ok", which is the shape that let 94 congress dq
	// events pile up behind a green fleet view. ErrDegraded is the honest
	// filing: it does NOT count toward FailingWorkers (only "error" does, and
	// only on a streak), but it suppresses lastSuccess so staleness reports a
	// worker that keeps delivering nothing. A PARTIAL failure stays "ok" —
	// degrading on one transient write error would cry wolf.
	if writeErrs > 0 && scored == 0 {
		return detail, fmt.Errorf("%s: %w", detail, workers.ErrDegraded)
	}
	return detail, nil
}

// latestClose returns the symbol's most recent daily close at/before now (the
// entry reference frozen into a forward-tracked outcome) together with the ts of
// the bar it came from. ok=false when the symbol has no daily bar yet.
func (w *ConfluenceScorer) latestClose(ctx context.Context, symbolID, now int64) (float64, int64, bool, error) {
	bar, ok, err := w.St.BarAtOrBefore(ctx, symbolID, md.TF1d, now)
	if err != nil || !ok || bar.Close <= 0 {
		return 0, 0, false, err
	}
	return bar.Close, bar.Ts, true, nil
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
		// SAME-BASIS ENTRY. o.EntryPx was frozen when the setup was flagged, and the
		// bars underneath it can be REWRITTEN afterwards: a reverse split triggers a
		// full re-backfill (pipeline/splitrepair.go) that rescales the whole series.
		// Dividing a live exit close by a frozen PRE-rescale entry does not cancel —
		// the rescale factor lands whole in the return. DFNS was flagged at 0.0493,
		// graded against a rescaled exit, and reported +8541% on an entry price no
		// bar of that symbol has ever carried (its minimum close is 3.90).
		//
		// predict.go resolves with fwd.Close/base.Close-1, BOTH legs read live, and
		// is immune for exactly that reason. Re-read the entry bar here so the two
		// legs always share one basis. EntryPx stays stored as the audit record of
		// what the price looked like when the call was made; it is no longer the
		// denominator.
		//
		// o.Ts is the UTC day bucket and US daily bars are stamped 04:00/05:00, so
		// +86399 selects that day's bar. Measured on the live table this reproduces
		// the frozen price for 5,258 of 5,304 resolved rows — the 46 it does not are
		// precisely the rescaled ones this exists to fix.
		entry, okEntry, err := w.St.BarAtOrBefore(ctx, o.SymbolID, md.TF1d, o.Ts+86399)
		if err != nil {
			return "", err
		}
		if !okEntry || entry.Close <= 0 {
			continue // entry bar gone (purged or quarantined) — leave it pending
		}
		// THE ENTRY BAR MUST BE INSIDE THE BUCKET DAY.
		//
		// BarAtOrBefore has no lower bound, so a symbol that did not trade on its
		// own bucket day was graded from whatever bar came last — days or weeks
		// earlier. Combined with the forward read, which reaches FORWARD from the
		// same bucket, several buckets could resolve to one identical (entry,
		// exit) pair and each was published as a separate independent bet.
		//
		// A bet whose entry price is not from the day it was placed is not a bet
		// this system can honestly grade, so it stays pending rather than being
		// graded on a stale leg. Non-trading-day buckets no longer arise at all
		// (see the scorer), so what remains here is the historical residue and
		// the genuine case of a symbol that simply did not print that day.
		if entry.Ts < o.Ts {
			continue
		}
		fwdReturn := fwd.Close/entry.Close - 1
		win := (o.Direction > 0 && fwdReturn > 0) || (o.Direction < 0 && fwdReturn < 0)
		// Stamp the price LEVELS the constrained basis reads. The exit bar's
		// extremes stand in for the intra-window excursion: the horizon is one
		// day and the gap guard above keeps the exit within three of them, so
		// that bar is where a stop would have been hit.
		if err := w.St.ResolveConfluenceOutcome(ctx, o.SymbolID, o.Ts, o.Horizon, fwdReturn, win, entry.Ts,
			store.ConfluenceGradePrices{EntryClose: entry.Close, ExitLow: fwd.Low, ExitHigh: fwd.High}); err != nil {
			return "", err
		}
		resolved++
	}
	return fmt.Sprintf("resolved %d confluence outcome(s)", resolved), nil
}
