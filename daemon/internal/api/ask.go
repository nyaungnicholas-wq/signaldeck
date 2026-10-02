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
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
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
)

const askMemberOff = "Ask the data is not available to member accounts yet"

type askCatalogEntry struct {
	Name   string          `json:"name"`
	Desc   string          `json:"description"`
	Params []copilot.Param `json:"params"`
}

// askAccess is who may ask: the tier, or a refusal status and message.
func (d Deps) askAccess(r *http.Request) (copilot.Tier, int, string) {
	switch {
	case userID(r) == 0:
		return "", http.StatusUnauthorized, "sign in to ask the data"
	case d.isMember(r) && !d.Cfg.MemberCopilot:
		return "", http.StatusForbidden, askMemberOff
	case d.LLM == nil || !d.LLM.Enabled():
		return "", http.StatusServiceUnavailable, "Ask the data needs the AI layer, which is not configured on this deployment"
	case d.isMember(r):
		return copilot.TierMember, 0, ""
	}
	return copilot.TierOperator, 0, ""
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
	limit := askCapOperator
	if tier == copilot.TierMember {
		limit = askCapMember
	}
	n, err := d.St.IncrAskCount(r.Context(), userID(r), time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		httpInternal(w, err)
		return
	}
	if n > limit {
		httpErr(w, http.StatusTooManyRequests, "you have used today's questions; the limit resets at UTC midnight")
		return
	}
	db, err := d.St.OpenQueryOnly()
	if err != nil {
		httpInternal(w, err)
		return
	}
	defer db.Close() //nolint:errcheck
	res, err := copilot.Asker{LLM: d.LLM, DB: db, Tier: tier, UID: userID(r)}.Ask(r.Context(), q)
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

func (d Deps) registerAsk(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ask", d.askStatus)
	mux.HandleFunc("POST /api/ask", d.ask)
}
