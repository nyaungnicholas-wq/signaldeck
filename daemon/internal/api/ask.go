package api

// "Ask the data" (plan step 10): POST /api/ask answers a question ONLY from
// SignalDeck's own tables, through the fixed catalog in internal/copilot, and
// every claim cites a returned row. The model never writes SQL and never
// chooses whose rows it reads (a Scoped entry binds the session's user id).
//
// Members: off unless SIGNALDECK_MEMBER_COPILOT=1, and then limited to the
// catalog's member entries (derived or public-domain data only, datalicense.go
// D1). The route sits in memberRoutes either way and gates inside the handler,
// so the member-surface and sentinel tests keep probing it.
//
// GET /api/ask reports whether the caller may ask and the catalog they would
// ask through; it spends nothing and is the same for every member.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nyaungnicholas-wq/signaldeck/internal/copilot"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
)

// Daily ask caps per account (each ask is two LLM calls). The LLM client's own
// daily call cap still bounds the total.
const (
	askCapMember   = 20
	askCapOperator = 200
	// askMemberPool caps member asks per UTC day across ALL members, persisted
	// under uid 0 (never a user id), so members cannot spend the LLM budget
	// the operator's features run on.
	askMemberPool    = 300
	askMemberPoolUID = 0
	// askMemberHeadroom: members are refused once the LLM layer's own daily
	// counter passes this share of its cap, leaving the rest to the operator.
	askMemberHeadroom = 0.70
)

// askInFlight holds the users with an ask running: one at a time per user.
var askInFlight sync.Map

const askMemberOff = "Ask the data is not available to member accounts yet"

type askCatalogEntry struct {
	Name   string          `json:"name"`
	Desc   string          `json:"description"`
	Params []copilot.Param `json:"params"`
}

// askAccess is who may ask: the tier, or a refusal status and message. The
// operator tier needs the ACTUAL admin (or the API token, which resolves to
// the admin), on any deployment: before publish "not a member" covered every
// signed-in account, and an account that is not the admin gets the member
// tier here, so only when the member flag is on.
func (d Deps) askAccess(r *http.Request) (copilot.Tier, int, string) {
	uid := userID(r)
	admin := uid != 0 && d.isAdminUID(r.Context(), uid)
	switch {
	case uid == 0:
		return "", http.StatusUnauthorized, "sign in to ask the data"
	case !admin && !d.Cfg.MemberCopilot:
		return "", http.StatusForbidden, askMemberOff
	case d.LLM == nil || !d.LLM.Enabled():
		return "", http.StatusServiceUnavailable, "Ask the data needs the AI layer, which is not configured on this deployment"
	case !admin:
		return copilot.TierMember, 0, ""
	}
	return copilot.TierOperator, 0, ""
}

// memberHeadroomGone is true once the LLM layer's counter for TODAY is past
// askMemberHeadroom of its cap. Until the first LLM call of a process the
// counter reads empty (it loads the persisted count on that call); the
// persisted askMemberPool budget is what bounds members in that window.
func (d Deps) memberHeadroomGone() bool {
	s := d.LLM.Stats()
	return s.DailyCap > 0 && s.Day == time.Now().UTC().Format("2006-01-02") &&
		float64(s.Calls) >= askMemberHeadroom*float64(s.DailyCap)
}

func (d Deps) askStatus(w http.ResponseWriter, r *http.Request) {
	tier, code, msg := d.askAccess(r)
	if code != 0 {
		writeJSON(w, map[string]any{"available": false, "reason": msg})
		return
	}
	cat := []askCatalogEntry{}
	for _, q := range copilot.For(tier) {
		cat = append(cat, askCatalogEntry{q.Name, q.Desc, q.Params})
	}
	limit := askCapOperator
	if tier == copilot.TierMember {
		limit = askCapMember
	}
	writeJSON(w, map[string]any{"available": true, "dailyLimit": limit, "maxQuestion": copilot.MaxQuestion, "catalog": cat})
}

func (d Deps) ask(w http.ResponseWriter, r *http.Request) {
	tier, code, msg := d.askAccess(r)
	if code != 0 {
		httpErr(w, code, msg)
		return
	}
	var body struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, `body must be {"question": "..."}`)
		return
	}
	q := strings.TrimSpace(body.Question)
	if q == "" || utf8.RuneCountInString(q) > copilot.MaxQuestion {
		httpErr(w, http.StatusBadRequest, "ask a question of 1 to 500 characters")
		return
	}
	uid, ctx, day := userID(r), r.Context(), askDay()
	if _, busy := askInFlight.LoadOrStore(uid, struct{}{}); busy {
		httpErr(w, http.StatusTooManyRequests, "one question at a time: wait for your last answer")
		return
	}
	defer askInFlight.Delete(uid)
	member := tier == copilot.TierMember
	if member && d.memberHeadroomGone() {
		httpErr(w, http.StatusTooManyRequests, "Ask the data is busy today; try again after UTC midnight")
		return
	}
	// The daily caps count every ask that reaches the model, including a plan
	// the catalog then refuses (it cost an LLM call). Everything above (401,
	// 403, 503, a 400 question, one already in flight, no headroom) refused
	// before any LLM call and is not counted; nor is a pool refusal below,
	// which hands both counts back.
	//
	// The pool is decided on the value its own increment returns, never on a
	// read before it: with check-then-increment, N members asking at once at
	// askMemberPool-1 all read a free slot and all ran (H-9). It is taken only
	// after the member's own cap passes, so a capped member never holds a slot,
	// and the hand-back ignores the request's cancellation: an abandoned ask
	// must not keep a slot it never used.
	limit := askCapOperator
	if member {
		limit = askCapMember
	}
	n, err := d.St.IncrAskCount(ctx, uid, day)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if n > limit {
		httpErr(w, http.StatusTooManyRequests, "you have used today's questions; the limit resets at UTC midnight")
		return
	}
	if member {
		pool, err := d.St.IncrAskCount(ctx, askMemberPoolUID, day)
		if err != nil || pool > askMemberPool {
			undo := context.WithoutCancel(ctx)
			if err == nil {
				d.undoAsk(undo, askMemberPoolUID, day)
			}
			d.undoAsk(undo, uid, day)
			if err != nil {
				httpInternal(w, err)
				return
			}
			httpErr(w, http.StatusTooManyRequests, "today's member questions are used up; they reset at UTC midnight")
			return
		}
	}
	// Opened only now: every refusal above costs no connection.
	db, err := d.St.OpenQueryOnly()
	if err != nil {
		httpInternal(w, err)
		return
	}
	defer db.Close() //nolint:errcheck
	res, err := copilot.Asker{LLM: d.LLM, DB: db, Tier: tier, UID: uid}.Ask(ctx, q)
	switch {
	case err == nil:
		writeJSON(w, res)
	case errors.Is(err, copilot.ErrPlan):
		httpErr(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, llm.ErrCapReached):
		httpErr(w, http.StatusTooManyRequests, aiErr(err))
	case errors.Is(err, llm.ErrDisabled):
		httpErr(w, http.StatusServiceUnavailable, aiErr(err))
	default:
		slog.Warn("ask the data failed", "err", err)
		httpErr(w, http.StatusBadGateway, "could not answer that right now; try again shortly")
	}
}

// undoAsk hands back one count (a member's own, or the pool's under
// askMemberPoolUID) for an ask refused before any LLM call. A failed undo only
// refuses early, never late, so it is logged rather than turned into an error.
func (d Deps) undoAsk(ctx context.Context, uid int64, day string) {
	if err := d.St.UndoAskCount(ctx, uid, day); err != nil {
		slog.Warn("ask the data: ask count not handed back", "uid", uid, "err", err)
	}
}

// askDay is the UTC day the cap counts against.
func askDay() string { return time.Now().UTC().Format("2006-01-02") }

func (d Deps) registerAsk(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ask", d.askStatus)
	mux.HandleFunc("POST /api/ask", d.ask)
}
