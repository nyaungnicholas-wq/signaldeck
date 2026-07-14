package api

import (
	"encoding/json"
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/aiagents/analyst"
	"github.com/nyaungnicholas-wq/signaldeck/internal/aiagents/chat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/aiagents/filingmind"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
)

// aiStatus reports whether the AI layer is enabled + the daily spend so far.
func (d Deps) aiStatus(w http.ResponseWriter, r *http.Request) {
	if d.LLM == nil {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	s := d.LLM.Stats()
	out := map[string]any{
		"enabled": d.LLM.Enabled(),
		"model":   d.LLM.Model(),
		"stats":   s,
		"charters": map[string]string{
			"analyst":    analyst.Charter,
			"chat":       chat.Charter,
			"filingmind": filingmind.Charter,
		},
	}
	// Surface the tier/pool details when the concrete client supports them
	// (never the keys themselves — only the count).
	if t, ok := d.LLM.(llm.Tiered); ok {
		out["deepModel"] = t.DeepModel()
		out["fastModel"] = t.FastModel()
		out["keyCount"] = t.KeyCount()
	}
	writeJSON(w, out)
}

// aiAnalyst runs the analyst on demand and returns its brief (also persisted
// to the insights feed by the hourly worker).
func (d Deps) aiAnalyst(w http.ResponseWriter, r *http.Request) {
	if !d.aiReady(w) {
		return
	}
	b, err := analyst.Run(r.Context(), d.LLM, d.St)
	if err != nil {
		httpErr(w, 502, aiErr(err))
		return
	}
	writeJSON(w, b)
}

// aiChat answers a question from a pre-fetched data snapshot (read-only).
func (d Deps) aiChat(w http.ResponseWriter, r *http.Request) {
	if !d.aiReady(w) {
		return
	}
	var body struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	ans, err := chat.Ask(r.Context(), d.LLM, d.St, body.Question)
	if err != nil {
		httpErr(w, 400, aiErr(err))
		return
	}
	writeJSON(w, ans)
}

// aiFiling analyzes pasted filing text into a cited thesis.
func (d Deps) aiFiling(w http.ResponseWriter, r *http.Request) {
	if !d.aiReady(w) {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	res, err := filingmind.Analyze(r.Context(), d.LLM, body.Text)
	if err != nil {
		httpErr(w, 400, aiErr(err))
		return
	}
	writeJSON(w, res)
}

// aiReady 503s when the AI layer has no key configured.
func (d Deps) aiReady(w http.ResponseWriter) bool {
	if d.LLM == nil || !d.LLM.Enabled() {
		httpErr(w, 503, "AI is disabled — set SIGNALDECK_NVIDIA_KEY in daemon/.env and restart")
		return false
	}
	return true
}

// aiErr maps llm sentinel errors to friendly messages (never leaks the key).
func aiErr(err error) string {
	switch err {
	case llm.ErrCapReached:
		return "daily AI call limit reached — resets at UTC midnight"
	case llm.ErrDisabled:
		return "AI is disabled (no key)"
	default:
		return err.Error()
	}
}

// registerAI wires the AI routes. A pasted 10-K (<=60k chars, see
// filingmind.MaxFilingChars) fits comfortably under the middleware's 128KB
// body cap.
func (d Deps) registerAI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/status", d.aiStatus)
	mux.HandleFunc("GET /api/ai/analyst", d.aiAnalyst)
	mux.HandleFunc("POST /api/ai/chat", d.aiChat)
	mux.HandleFunc("POST /api/ai/filing", d.aiFiling)
}
