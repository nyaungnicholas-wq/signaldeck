package api

import (
	"encoding/json"
	"net/http"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/symbolagent"
)

// symbolAgentSkill is one component's measured edge, flattened for the UI.
type symbolAgentSkill struct {
	Component string  `json:"component"`
	HitRate   float64 `json:"hitRate"`
	HasHR     bool    `json:"hasHR"`
	IC        float64 `json:"ic"`
	HasIC     bool    `json:"hasIC"`
	N         int     `json:"n"`
}

// symbolAgentResp is the per-symbol agent view: the honest tier + learning
// progress, the plain-English personality, per-signal skill, and the active
// blend weights — everything the "THIS SYMBOL'S AGENT" panel renders.
type symbolAgentResp struct {
	Symbol        string             `json:"symbol"`
	Market        string             `json:"market"`
	Horizon       string             `json:"horizon"`
	Available     bool               `json:"available"`     // a model row exists yet
	Tier          string             `json:"tier"`          // personal|regime|global|static
	Personal      bool               `json:"personal"`      // tier == personal (own model in use)
	NSamples      int                `json:"nSamples"`      // this symbol's own resolved outcomes
	Threshold     int                `json:"threshold"`     // MinPersonal — samples needed to graduate
	Personality   string             `json:"personality"`   // deterministic plain-English read
	Skill         []symbolAgentSkill `json:"skill"`         // per-component measured edge
	ActiveWeights map[string]float64 `json:"activeWeights"` // blend weights actually in force
	UpdatedTs     int64              `json:"updatedTs"`
}

// symbolAgent serves ONE symbol+horizon's agent. Read-only, gated like every
// other market-data read. Honest by construction: when the symbol hasn't earned
// a personal model the payload reports the fallback tier + "n/threshold"
// progress so the UI can say "still learning → using the global model" instead
// of implying a bespoke model exists.
func (d Deps) symbolAgent(w http.ResponseWriter, r *http.Request) {
	s, err := d.symbolFromQuery(r)
	if err != nil {
		httpErr(w, 404, err.Error())
		return
	}
	h := md.Horizon(r.URL.Query().Get("horizon"))
	if h != md.H1d && h != md.H1w {
		h = md.H1d
	}

	resp := symbolAgentResp{
		Symbol:        s.Symbol,
		Market:        string(s.Market),
		Horizon:       string(h),
		Tier:          symbolagent.TierStatic,
		Threshold:     symbolagent.MinPersonal,
		Skill:         []symbolAgentSkill{}, // "skill": [] even with no model row — never null
		ActiveWeights: map[string]float64{},
	}

	pm, ok, err := d.St.SymbolModel(r.Context(), s.ID, h)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	if !ok {
		// No model row yet (worker hasn't run for it / brand-new symbol): honest
		// "still learning from zero" default, static tier.
		resp.Personality = "Still learning — no resolved outcomes for this symbol yet, so the global model is in use."
		writeJSON(w, resp)
		return
	}

	resp.Available = true
	resp.Tier = pm.Tier
	resp.Personal = pm.Tier == symbolagent.TierPersonal
	resp.NSamples = pm.NSamples
	resp.Personality = pm.Personality
	resp.UpdatedTs = pm.UpdatedTs

	// Skill blob → sorted, flat rows in canonical leg order.
	var skill map[string]symbolagent.Skill
	if pm.Skill != "" {
		_ = json.Unmarshal([]byte(pm.Skill), &skill)
	}
	for _, leg := range legOrder {
		if sk, present := skill[leg]; present {
			resp.Skill = append(resp.Skill, symbolAgentSkill{
				Component: leg, HitRate: sk.HitRate, HasHR: sk.HasHR,
				IC: sk.IC, HasIC: sk.HasIC, N: sk.N,
			})
		}
	}

	// Active weights = the symbol's OWN weights only when it's on the personal
	// tier; otherwise the panel shows the global model is driving the blend (an
	// empty map communicates "using the global weights", matching the tier).
	if resp.Personal && pm.Weights != "" {
		var wts map[string]float64
		if err := json.Unmarshal([]byte(pm.Weights), &wts); err == nil && len(wts) > 0 {
			resp.ActiveWeights = wts
		}
	}

	writeJSON(w, resp)
}

// legOrder is the canonical component order for stable UI rows. Mirrors
// ensemble.LegNames without importing it here (kept local to the handler).
var legOrder = []string{"pressure", "expectancy", "forecast", "sentiment"}
