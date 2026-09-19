package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// maxEmailLen is the RFC 5321 maximum for a forward path.
const maxEmailLen = 254

// normaliseEmail trims, lowercases and validates. ok=false means "do not
// store this", and the reason is deliberately not returned to the caller
// beyond a generic message.
//
// This is a TRUST BOUNDARY: the only unauthenticated write on the published
// deployment. The validation is intentionally strict-and-boring rather than a
// clever regex. It is NOT trying to decide whether an address is deliverable
// -- that is undecidable without sending mail -- only that it cannot be used
// to smuggle something into a log line, a header, or a future mail envelope.
//
// The control-character check is the one that actually matters: an address
// containing CR or LF is the classic header-injection payload
// ("a@b.c\r\nBcc: everyone"). Refusing it here means no downstream consumer
// has to remember to.
func normaliseEmail(raw string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || len(e) > maxEmailLen {
		return "", false
	}
	// No control characters anywhere, and no spaces. Checked over BYTES
	// because a multi-byte rune cannot contain a stray 0x0A and this is
	// cheaper and harder to get wrong than a rune loop.
	for i := 0; i < len(e); i++ {
		if e[i] < 0x20 || e[i] == 0x7f || e[i] == ' ' {
			return "", false
		}
	}
	at := strings.IndexByte(e, '@')
	if at <= 0 || at != strings.LastIndexByte(e, '@') || at == len(e)-1 {
		return "", false
	}
	// A domain with no dot is not routable on the public internet, and
	// accepting one only produces a bounce later.
	if !strings.Contains(e[at+1:], ".") || strings.HasSuffix(e, ".") {
		return "", false
	}
	return e, true
}

type waitlistReq struct {
	Email string `json:"email"`
	// HP is a honeypot: a field hidden from humans in the browser. Anything
	// non-empty here is a bot, and the response is a cheerful 200 that stores
	// nothing -- telling a scraper it was detected just teaches it to stop
	// filling the field.
	HP     string `json:"hp"`
	Source string `json:"source"`
}

// waitlistAdd handles POST /api/waitlist.
//
// It answers 200 for a new address AND for one already on the list. The
// difference is list-membership disclosure: an endpoint that says "already
// subscribed" lets anyone test whether a given person signed up.
func (d Deps) waitlistAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, 405, "POST only")
		return
	}
	var req waitlistReq
	// The body is already capped upstream by http.MaxBytesReader in secure().
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpErr(w, 400, "expected a JSON body")
		return
	}
	if strings.TrimSpace(req.HP) != "" {
		// Bot. Say yes, store nothing.
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	email, ok := normaliseEmail(req.Email)
	if !ok {
		httpErr(w, 400, "that does not look like an email address")
		return
	}
	source := req.Source
	if len(source) > 64 {
		source = source[:64]
	}
	if _, err := d.St.AddWaitlist(r.Context(), email, source, time.Now()); err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
