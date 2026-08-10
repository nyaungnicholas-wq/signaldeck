package api

// ── WEEKLY DIGEST (#16) read route ───────────────────────────────────────────
//
// GET /api/digest serves the latest weekly digest text the weekly-digest
// worker composed (meta digest_last_text), with when it was generated and —
// when a notify transport was configured at compose time — when it was handed
// off for delivery. available:false with a plain note before the first Sunday
// gate fires; sentAt:0 means composed but never delivered (no transport).

import (
	"net/http"
	"strconv"

	"github.com/nyaungnicholas-wq/signaldeck/internal/briefing"
)

func (d Deps) digest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	text, err := d.St.GetMeta(ctx, briefing.MetaDigestLastText)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if text == "" {
		writeJSON(w, map[string]any{
			"available": false,
			"note":      "no digest composed yet — the weekly-digest worker fires once per NY week, Sunday from 17:00 ET",
		})
		return
	}
	atoi := func(k string) int64 {
		v, _ := d.St.GetMeta(ctx, k)
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	weekKey, _ := d.St.GetMeta(ctx, briefing.MetaDigestLastWeek)
	writeJSON(w, map[string]any{
		"available":   true,
		"text":        text,
		"generatedAt": atoi(briefing.MetaDigestLastTs),
		"sentAt":      atoi(briefing.MetaDigestSentAt), // 0 = composed but never delivered (no transport)
		"weekKey":     weekKey,
	})
}

// registerDigest wires the weekly-digest read route.
func (d Deps) registerDigest(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/digest", d.digest)
}
