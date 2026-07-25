package briefing

// ── WEEKLY DIGEST (#16) ──────────────────────────────────────────────────────
//
// Once a week (Sunday >= 17:00 America/New_York, meta week-key dedup — the
// weekly-report gate reused verbatim) the platform composes ONE plain-text
// digest from STORED data only and delivers it through the existing
// internal/notify transports:
//
//   - regime CHANGES this week per watchlist-active symbol: each (symbol,
//     kind)'s EARLIEST frozen call of the week (regime_outcomes) vs the
//     CURRENT forecast (regime_forecasts);
//   - resolved regime outcomes this week + live accuracy so far, per kind,
//     with the track-record's 30-resolution honesty gate;
//   - the top 3 current highest-conviction validated calls;
//   - paper-book week P&L when available (flagship books).
//
// Delivery contract: an unconfigured notifier is an HONEST NO-OP — the digest
// text is still composed and stored (meta digest_last_text) for inspection at
// GET /api/digest, and the worker detail says "digest skipped: no transport".
// The week key uses the house Sunday-date convention (ShouldRunWeekly), which
// is one-to-one with the ISO week for a Sunday-17:00 gate.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Digest meta keys: the dedup week key, the last composed text + when, and
// when it was last handed to a configured transport (0 = never sent).
const (
	MetaDigestLastWeek = "digest_last_week"
	MetaDigestLastText = "digest_last_text"
	MetaDigestLastTs   = "digest_last_ts"
	MetaDigestSentAt   = "digest_sent_at"
)

// digestRunHour is the NY hour (on Sunday) from which the digest may fire.
const digestRunHour = 17

// digestMinResolutions mirrors the track-record's per-kind gate: below it a
// kind's live accuracy is withheld, never quoted thin.
const digestMinResolutions = 30

// DigestChange is one regime flip observed this week.
type DigestChange struct {
	Symbol string
	Kind   string
	From   string
	To     string
}

// DigestKindRecord is one kind's live-vs-claimed scoreboard line.
type DigestKindRecord struct {
	Kind      string
	Resolved  int
	Correct   int
	NewThisWk int
}

// DigestTopCall is one current high-conviction validated call.
type DigestTopCall struct {
	Symbol     string
	Kind       string
	Regime     string
	Conviction float64
	Claimed    float64
}

// DigestFacts is everything the digest is allowed to say.
type DigestFacts struct {
	WeekKey  string
	Changes  []DigestChange
	Records  []DigestKindRecord
	TopCalls []DigestTopCall
	Paper    []WeeklyPaper
}

// CollectDigestFacts measures the digest inputs from the store — nothing
// invented, every number a stored row.
func CollectDigestFacts(ctx context.Context, st *store.Store, now time.Time) (DigestFacts, error) {
	var f DigestFacts
	since := now.Add(-7 * 24 * time.Hour).Unix()

	// Current forecasts: the "to" side of changes + the top-call pool.
	cur, err := st.RegimeForecasts(ctx)
	if err != nil {
		return f, err
	}
	type sk struct{ sym, kind string }
	current := map[sk]store.RegimeForecast{}
	for _, c := range cur {
		current[sk{c.Symbol, string(c.Kind)}] = c
	}

	// Earliest frozen call this week per (symbol, kind) — rows arrive ts ASC,
	// so keep-first is the earliest.
	calls, err := st.RegimeOutcomeCallsSince(ctx, since)
	if err != nil {
		return f, err
	}
	earliest := map[sk]string{}
	for _, c := range calls {
		k := sk{c.Symbol, string(c.Kind)}
		if _, seen := earliest[k]; !seen {
			earliest[k] = c.Regime
		}
	}
	for k, from := range earliest {
		if c, ok := current[k]; ok && c.Regime != from {
			f.Changes = append(f.Changes, DigestChange{
				Symbol: k.sym, Kind: k.kind, From: from, To: c.Regime,
			})
		}
	}
	sort.Slice(f.Changes, func(i, j int) bool {
		if f.Changes[i].Symbol != f.Changes[j].Symbol {
			return f.Changes[i].Symbol < f.Changes[j].Symbol
		}
		return f.Changes[i].Kind < f.Changes[j].Kind
	})

	// Resolved record per kind (all-time correct/total + this week's count) —
	// the same rows the track-record regimes section grades.
	resolved, err := st.ResolvedRegimeOutcomes(ctx, 50000)
	if err != nil {
		return f, err
	}
	recs := map[string]*DigestKindRecord{}
	for _, r := range resolved {
		rec := recs[string(r.Kind)]
		if rec == nil {
			rec = &DigestKindRecord{Kind: string(r.Kind)}
			recs[string(r.Kind)] = rec
		}
		rec.Resolved++
		if r.Correct == 1 {
			rec.Correct++
		}
		if r.ResolvedAt >= since {
			rec.NewThisWk++
		}
	}
	for _, rec := range recs {
		f.Records = append(f.Records, *rec)
	}
	sort.Slice(f.Records, func(i, j int) bool { return f.Records[i].Kind < f.Records[j].Kind })

	// Top 3 current calls by conviction (RegimeForecasts orders by conviction
	// within kind — re-rank globally).
	byConv := append([]store.RegimeForecast(nil), cur...)
	sort.Slice(byConv, func(i, j int) bool { return byConv[i].Conviction > byConv[j].Conviction })
	for _, c := range byConv {
		if len(f.TopCalls) == 3 {
			break
		}
		f.TopCalls = append(f.TopCalls, DigestTopCall{
			Symbol: c.Symbol, Kind: string(c.Kind), Regime: c.Regime,
			Conviction: c.Conviction, Claimed: c.HistoricalAccuracy,
		})
	}

	// Paper-book week P&L — the weekly report's measurement, reused.
	for _, strat := range weeklyPaperStrategies {
		p := WeeklyPaper{Strategy: strat}
		curve, err := st.PaperEquityCurve(ctx, strat, 5000)
		if err != nil {
			return f, err
		}
		if len(curve) > 0 {
			p.Available = true
			p.EquityNow = curve[len(curve)-1].Equity
			p.EquityWeekAgo = curve[0].Equity
			p.SinceInception = true
			for i := len(curve) - 1; i >= 0; i-- {
				if curve[i].Ts <= since {
					p.EquityWeekAgo = curve[i].Equity
					p.SinceInception = false
					break
				}
			}
			if p.EquityWeekAgo != 0 {
				p.ChangePct = (p.EquityNow/p.EquityWeekAgo - 1) * 100
			}
		}
		f.Paper = append(f.Paper, p)
	}
	return f, nil
}

// ComposeDigest renders the deterministic weekly digest text.
func ComposeDigest(f DigestFacts, loc *time.Location) string {
	sunday, _ := time.ParseInLocation("2006-01-02", f.WeekKey, loc)
	var b []string
	b = append(b, "SignalDeck weekly digest — week of "+sunday.Format("Jan 2")+".")

	if len(f.Changes) == 0 {
		b = append(b, "Regime changes this week: none among watchlist-active symbols.")
	} else {
		parts := make([]string, 0, len(f.Changes))
		for _, c := range f.Changes {
			parts = append(parts, fmt.Sprintf("%s %s %s→%s", c.Symbol, c.Kind, c.From, c.To))
		}
		b = append(b, "Regime changes this week: "+strings.Join(parts, "; ")+".")
	}

	if len(f.Records) == 0 {
		b = append(b, "Live regime record: no resolved regime outcomes yet.")
	} else {
		parts := make([]string, 0, len(f.Records))
		for _, r := range f.Records {
			line := fmt.Sprintf("%s %d resolved (+%d this week)", r.Kind, r.Resolved, r.NewThisWk)
			if r.Resolved >= digestMinResolutions {
				line += fmt.Sprintf(", live accuracy %.1f%%", float64(r.Correct)/float64(r.Resolved)*100)
			} else {
				line += ", not yet significant — " + strconv.Itoa(r.Resolved) + "/" +
					strconv.Itoa(digestMinResolutions)
			}
			parts = append(parts, line)
		}
		b = append(b, "Live regime record: "+strings.Join(parts, "; ")+".")
	}

	if len(f.TopCalls) == 0 {
		b = append(b, "Highest-conviction current calls: none stored.")
	} else {
		parts := make([]string, 0, len(f.TopCalls))
		for _, c := range f.TopCalls {
			parts = append(parts, fmt.Sprintf("%s %s %q (conviction %.2f, claimed %.1f%%)",
				c.Symbol, c.Kind, c.Regime, c.Conviction, c.Claimed*100))
		}
		b = append(b, "Highest-conviction current calls: "+strings.Join(parts, "; ")+".")
	}

	for _, p := range f.Paper {
		if !p.Available {
			continue
		}
		window := "this week"
		if p.SinceInception {
			window = "since inception (book younger than a week)"
		}
		b = append(b, fmt.Sprintf("Paper book %s: equity %.2f (%+.2f%% %s).",
			p.Strategy, p.EquityNow, p.ChangePct, window))
	}

	b = append(b, disclaimer)
	return strings.Join(b, " ")
}

// DigestWorker composes + delivers the weekly digest (implements
// workers.Worker). Ticks hourly; the NY Sunday-17:00 week gate does the
// pacing, catching up later in the week if the daemon was down.
type DigestWorker struct {
	St       *store.Store
	Notifier *notify.Notifier // nil / unconfigured = honest no-op delivery
	Loc      *time.Location   // tests; nil = America/New_York
	Now      func() time.Time // tests; nil = time.Now
}

func (w *DigestWorker) Name() string            { return "weekly-digest" }
func (w *DigestWorker) Interval() time.Duration { return time.Hour }

// Run applies the week gate, composes + stores the digest, and delivers it
// through every configured transport (none configured = logged no-op).
func (w *DigestWorker) Run(ctx context.Context) (string, error) {
	loc := w.Loc
	if loc == nil {
		loc = NYLoc()
	}
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	lastKey, err := w.St.GetMeta(ctx, MetaDigestLastWeek)
	if err != nil {
		return "", err
	}
	run, weekKey := ShouldRunWeekly(now, lastKey, loc, digestRunHour)
	if !run {
		return fmt.Sprintf("waiting (fires once per week from Sunday %d:00 ET; last=%s)", digestRunHour, lastKey), nil
	}

	facts, err := CollectDigestFacts(ctx, w.St, now)
	if err != nil {
		return "", err
	}
	facts.WeekKey = weekKey
	text := ComposeDigest(facts, loc)

	// Store for inspection FIRST — the digest is on the record whether or not
	// any transport exists.
	if err := w.St.SetMeta(ctx, MetaDigestLastText, text); err != nil {
		return "", err
	}
	if err := w.St.SetMeta(ctx, MetaDigestLastTs, strconv.FormatInt(now.Unix(), 10)); err != nil {
		return "", err
	}

	detail := "composed weekly digest for week of " + weekKey
	if w.Notifier.Enabled() {
		w.Notifier.Send(ctx, notify.Message{
			Title: "SignalDeck weekly digest", Body: text, Kind: "digest", Ts: now.Unix(),
		})
		if err := w.St.SetMeta(ctx, MetaDigestSentAt, strconv.FormatInt(now.Unix(), 10)); err != nil {
			return "", err
		}
		detail += " — delivered to " + strings.Join(w.Notifier.ConfiguredNames(), ",")
	} else {
		detail += " — digest skipped: no transport"
	}
	if err := w.St.SetMeta(ctx, MetaDigestLastWeek, weekKey); err != nil {
		return "", err
	}
	return detail, nil
}
