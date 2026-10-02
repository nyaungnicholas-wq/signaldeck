package api

// Member call journal (plan step 8). A member records their OWN up/down calls
// and sees them graded by SignalDeck's own rules (internal/memberjournal).
// These routes read and write only the caller's rows; they never change or
// personalise SignalDeck's forecasts (docs/PUBLISHER_GUARDRAILS.md rule 10).
//
// LICENCE (datalicense.go D1): no row or aggregate carries an entry price,
// exit price or realized return; rows carry the grade, aggregates counts,
// rates and an interval.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/memberjournal"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const maxJournalNote = 280

type journalRow struct {
	ID          int64  `json:"id"`
	Symbol      string `json:"symbol"`
	Market      string `json:"market"`
	Call        string `json:"call"`
	Horizon     int    `json:"horizon"`
	Note        string `json:"note"` // the owner's own text; this route serves only the owner
	CreatedTs   int64  `json:"createdTs"`
	Status      string `json:"status"`
	Outcome     string `json:"outcome,omitempty"`
	ResolveDate string `json:"resolveDate"` // ET date of the close that grades (or graded) the call
	CanWithdraw bool   `json:"canWithdraw"`
}

type journalCaps struct {
	TodayLeft int `json:"todayLeft"`
	OpenLeft  int `json:"openLeft"`
}

func (d Deps) writeJournal(w http.ResponseWriter, r *http.Request) {
	ctx, uid, now := r.Context(), userID(r), time.Now()
	calls, err := d.St.MemberCalls(ctx, uid)
	if err != nil {
		httpInternal(w, err)
		return
	}
	rows := make([]journalRow, 0, len(calls))
	for _, c := range calls {
		can, err := memberjournal.CanWithdraw(ctx, d.St, c, now)
		if err != nil {
			httpInternal(w, err)
			return
		}
		rows = append(rows, journalRow{
			ID: c.ID, Symbol: c.Symbol, Market: c.Market, Call: c.Call, Horizon: c.Horizon, Note: c.Note,
			CreatedTs: c.CreatedTs, Status: c.Status, Outcome: c.Outcome, CanWithdraw: can,
			ResolveDate: time.Unix(c.ExitDueTs, 0).In(marketcal.Loc()).Format("2006-01-02"),
		})
	}
	today, open, err := d.St.MemberCallCounts(ctx, uid, marketcal.SessionDate(now).Unix())
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"calls": rows,
		"stats": memberjournal.Summarize(calls),
		"caps": journalCaps{
			TodayLeft: max(0, store.MemberCallsPerDay-today),
			OpenLeft:  max(0, store.MemberCallsOpen-open),
		},
	})
}

func (d Deps) journalGet(w http.ResponseWriter, r *http.Request) { d.writeJournal(w, r) }

func (d Deps) journalCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol  string    `json:"symbol"`
		Market  md.Market `json:"market"`
		Call    string    `json:"call"`
		Horizon int       `json:"horizon"`
		Note    string    `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	switch {
	case body.Market == md.Crypto:
		httpErr(w, http.StatusBadRequest, cryptoNotForMembers)
		return
	case body.Market != md.Stocks:
		httpErr(w, http.StatusBadRequest, "the journal covers US stocks and ETFs only")
		return
	case body.Call != "up" && body.Call != "down":
		httpErr(w, http.StatusBadRequest, `call must be "up" or "down"`)
		return
	case !memberjournal.Horizons[body.Horizon]:
		httpErr(w, http.StatusBadRequest, "horizon must be 1, 5 or 21 trading sessions")
		return
	}
	note := strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(note) > maxJournalNote {
		httpErr(w, http.StatusBadRequest, "note is limited to 280 characters")
		return
	}
	s, err := d.St.GetSymbol(r.Context(), strings.ToUpper(strings.TrimSpace(body.Symbol)), md.Stocks)
	if errors.Is(err, sql.ErrNoRows) {
		httpErr(w, 404, "unknown symbol")
		return
	}
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !s.Active {
		httpErr(w, 422, s.Symbol+" is not currently tracked")
		return
	}
	now := time.Now()
	entry, exit := memberjournal.Schedule(now, body.Horizon)
	_, err = d.St.InsertMemberCall(r.Context(), store.MemberCall{
		UserID: userID(r), SymbolID: s.ID, Market: string(md.Stocks), Call: body.Call, Horizon: body.Horizon,
		Note: note, CreatedTs: now.Unix(), EntryTs: entry.Unix(), ExitDueTs: exit.Unix(),
	}, marketcal.SessionDate(now).Unix())
	switch {
	case errors.Is(err, store.ErrCallCapDay):
		httpErr(w, http.StatusTooManyRequests, "you have made 10 calls today (US Eastern); the limit resets at midnight ET")
		return
	case errors.Is(err, store.ErrCallCapOpen):
		httpErr(w, http.StatusTooManyRequests, "you have 50 open calls; new ones open up as those resolve")
		return
	case err != nil:
		httpInternal(w, err)
		return
	}
	d.writeJournal(w, r)
}

func (d Deps) journalWithdraw(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		httpErr(w, 400, `body must be {"id": <call id>}`)
		return
	}
	ctx, uid, now := r.Context(), userID(r), time.Now()
	c, ok, err := d.St.MemberCall(ctx, uid, body.ID)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !ok { // someone else's call answers exactly like a missing one
		httpErr(w, http.StatusNotFound, "no such call")
		return
	}
	can, err := memberjournal.CanWithdraw(ctx, d.St, c, now)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !can {
		httpErr(w, http.StatusConflict, "a call can be withdrawn only before its entry session starts trading")
		return
	}
	if ok, err := d.St.WithdrawMemberCall(ctx, uid, c.ID, now.Unix()); err != nil {
		httpInternal(w, err)
		return
	} else if !ok {
		httpErr(w, http.StatusConflict, "a call can be withdrawn only before its entry session starts trading")
		return
	}
	d.writeJournal(w, r)
}

func (d Deps) registerJournal(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/journal", d.journalGet)
	mux.HandleFunc("POST /api/journal", d.journalCreate)
	mux.HandleFunc("POST /api/journal/withdraw", d.journalWithdraw)
}
