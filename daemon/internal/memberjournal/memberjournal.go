// Package memberjournal grades members' own direction calls (plan step 8) by
// the rules SignalDeck grades itself with: close to close, no lookahead, NYSE
// calendar (marketcal), the settled-bar rule of the prediction resolver, and a
// Wilson interval withheld below the platform's 30-row floor.
//
// LICENCE (datalicense.go D1): a member is shown the grade only. Nothing here
// returns, stores or logs an entry price, exit price or realized return.
package memberjournal

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

const (
	// MinN is the resolved-call floor below which no hit rate, interval or
	// baseline is shown: the platform's own withholding rule.
	MinN = 30
	// NoDataSessions: an open call whose bars are still missing this many
	// sessions after its exit session is voided ("no data").
	NoDataSessions = 10
	// stampSlack is the DST/stamp jitter around a daily bar's ET-midnight
	// stamp (pipeline.dstStampSlackSecs): a bar belongs to session D when its
	// ts is in [D 00:00 ET - 6h, D 18:00 ET).
	stampSlack = int64(6 * 3600)
)

// Horizons are the allowed call horizons in trading sessions.
var Horizons = map[int]bool{1: true, 5: true, 21: true}

// Schedule fixes a call's entry and exit sessions at creation. Entry is the
// first session whose CLOSE is strictly after created (a call at 15:59 ET
// enters on that day's close, one at 16:00 on the next session's); exit is
// the close `horizon` sessions after entry. Returned as session-close instants.
func Schedule(created time.Time, horizon int) (entryClose, exitClose time.Time) {
	d := marketcal.SessionDate(created)
	for i := 0; i < 15; i++ {
		if c, ok := marketcal.SessionClose(d); ok && c.After(created) {
			entryClose = c
			break
		}
		d = d.AddDate(0, 0, 1)
	}
	for n := 0; n < horizon; {
		d = d.AddDate(0, 0, 1)
		if marketcal.IsTradingDay(d) {
			n++
		}
	}
	exitClose, _ = marketcal.SessionClose(d)
	return entryClose, exitClose
}

// Grade is the outcome of a close-to-close call: "hit" when the sign of
// exit-entry matches the call, "miss" when it does not, "" (VOID) when the
// close did not move.
func Grade(call string, entry, exit float64) string {
	switch {
	case exit == entry:
		return ""
	case (exit > entry) == (call == "up"):
		return "hit"
	}
	return "miss"
}

// sessionWindow is [start, end) of the daily-bar stamps belonging to the
// session whose close is closeTs.
func sessionWindow(closeTs int64) (int64, int64) {
	mid := marketcal.SessionDate(time.Unix(closeTs, 0)).Unix()
	return mid - stampSlack, mid + 18*3600
}

func sessionBar(ctx context.Context, st *store.Store, symbolID, closeTs int64) (md.Bar, bool, error) {
	lo, hi := sessionWindow(closeTs)
	bars, err := st.Bars(ctx, symbolID, md.TF1d, lo, hi, 1)
	if err != nil || len(bars) == 0 {
		return md.Bar{}, false, err
	}
	return bars[0], true, nil
}

// CanWithdraw: a call may be withdrawn only while open and before its entry
// bar exists (and before its entry close, even if that bar never arrives).
func CanWithdraw(ctx context.Context, st *store.Store, c store.MemberCall, now time.Time) (bool, error) {
	if c.Status != "open" || now.Unix() >= c.EntryTs {
		return false, nil
	}
	lo, _ := sessionWindow(c.EntryTs)
	_, exists, err := st.BarAtOrAfter(ctx, c.SymbolID, md.TF1d, lo)
	return !exists, err
}

// Stats is a member's record: counts always, rates only from MinN up.
type Stats struct {
	Resolved       int      `json:"resolved"`
	Hits           int      `json:"hits"`
	Misses         int      `json:"misses"`
	Open           int      `json:"open"`
	Void           int      `json:"void"`
	Withdrawn      int      `json:"withdrawn"`
	HitRate        *float64 `json:"hitRate"`
	CILow          *float64 `json:"ciLow"`
	CIHigh         *float64 `json:"ciHigh"`
	BaselineUpRate *float64 `json:"baselineUpRate"`
	Withheld       bool     `json:"withheld"`
	MinN           int      `json:"minN"`
}

// Summarize grades a member's calls. The baseline is the share of the SAME
// resolved calls whose symbol went up ("always up"), derived from call and
// outcome alone, so a member can see whether they beat drift.
// ponytail: raw-n Wilson; calls made the same day share one market move, so
// the interval is optimistic for a member who calls many names at once.
// Switch to clusterstat.Grade on entry day if members do that.
func Summarize(calls []store.MemberCall) Stats {
	s := Stats{MinN: MinN}
	ups := 0
	for _, c := range calls {
		switch c.Status {
		case "open":
			s.Open++
		case "void":
			s.Void++
		case "withdrawn":
			s.Withdrawn++
		case "resolved":
			s.Resolved++
			if c.Outcome == "hit" {
				s.Hits++
			} else {
				s.Misses++
			}
			if (c.Call == "up") == (c.Outcome == "hit") {
				ups++
			}
		}
	}
	s.Withheld = s.Resolved < MinN
	if s.Withheld {
		return s
	}
	n := float64(s.Resolved)
	rate, base := float64(s.Hits)/n, float64(ups)/n
	iv := clusterstat.WilsonEffAt(rate, n, 1.959963985)
	s.HitRate, s.CILow, s.CIHigh, s.BaselineUpRate = &rate, &iv.Lo, &iv.Hi, &base
	return s
}

// Resolver is member-call-resolver: after the close on trading days it grades
// every open call whose exit session has passed. Idempotent: a settled call is
// never rewritten (store.SettleMemberCall and the schema trigger).
type Resolver struct {
	St  *store.Store
	Now func() time.Time // test seam
}

func (w *Resolver) Name() string            { return "member-call-resolver" }
func (w *Resolver) Interval() time.Duration { return 24 * time.Hour }

// NextFire is 18:00 ET on trading days, at once after a missed slot.
func (w *Resolver) NextFire(last, now time.Time) time.Time {
	return workers.TradingDayAtETCatchUp(last, now, 18, 0)
}

func (w *Resolver) Run(ctx context.Context) (string, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	due, err := w.St.OpenMemberCallsDue(ctx, now.Unix())
	if err != nil {
		return "", err
	}
	var graded, voided, waiting, failed int
	var firstErr error
	for _, c := range due {
		// One call's store error must not hold up every call queued behind
		// it: count it, keep going, and fail the run at the end.
		outcome, ok, err := w.grade(ctx, c)
		if err != nil {
			failed++
			firstErr = cmp.Or(firstErr, err)
			continue
		}
		if !ok {
			if marketcal.SessionsClosedSince(c.ExitDueTs, now) < NoDataSessions {
				waiting++
				continue
			}
			outcome = "" // no data: void
		}
		done, err := w.St.SettleMemberCall(ctx, c.ID, outcome, now.Unix())
		if err != nil {
			failed++
			firstErr = cmp.Or(firstErr, err)
			continue
		}
		switch {
		case !done:
		case outcome == "":
			voided++
		default:
			graded++
		}
	}
	detail := fmt.Sprintf("graded %d member calls, voided %d, %d waiting on bars", graded, voided, waiting)
	if firstErr != nil {
		return detail, fmt.Errorf("%d member calls failed: %w", failed, firstErr)
	}
	return detail, nil
}

// grade returns the outcome once both bars exist and the exit bar is SETTLED:
// a later bar exists (the prediction resolver's rule; a bar's close is live
// until the next session prints). ok is false while either is missing.
func (w *Resolver) grade(ctx context.Context, c store.MemberCall) (string, bool, error) {
	entry, okE, err := sessionBar(ctx, w.St, c.SymbolID, c.EntryTs)
	if err != nil || !okE {
		return "", false, err
	}
	exit, okX, err := sessionBar(ctx, w.St, c.SymbolID, c.ExitDueTs)
	if err != nil || !okX {
		return "", false, err
	}
	_, hi := sessionWindow(c.ExitDueTs)
	_, settled, err := w.St.BarAtOrAfter(ctx, c.SymbolID, md.TF1d, hi)
	if err != nil || !settled || entry.Close <= 0 || exit.Close <= 0 {
		return "", false, err
	}
	return Grade(c.Call, entry.Close, exit.Close), true, nil
}
