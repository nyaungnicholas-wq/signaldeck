// GET /api/prereg — what each structural predictor claimed, frozen and chained
// before its forecasts began resolving.
//
// This is the endpoint that makes the accuracy tables falsifiable. Anywhere
// else on the platform, a claimed number is just what the code says today; here
// it is what the code said on a recorded date, with a hash chain that turns a
// later edit into a detectable break rather than a matter of trust.
//
// The payload leads with whether the chain VERIFIES, and with whether the
// registration beat the first gradable date — because a pre-registration
// written after results started arriving is worth very little, and the reader
// should not have to compare two dates to discover that.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
)

func (d Deps) prereg(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	recs, err := d.St.PreregRecords(ctx)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "prereg: "+err.Error())
		return
	}
	ok, brokenAt, err := d.St.VerifyPrereg(ctx)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "prereg verify: "+err.Error())
		return
	}

	// Decode each stored spec so the page renders the FROZEN text, not a
	// re-read of today's constants — which would defeat the purpose entirely.
	out := make([]map[string]any, 0, len(recs))
	registeredBefore := true
	firstGradable, _ := time.Parse("2006-01-02", prereg.FirstGradableOn)
	for _, rec := range recs {
		// The grading-protocol record carries a Protocol payload, not a Spec —
		// decode it as what it is so its frozen text renders too.
		var spec any
		if rec.Kind == prereg.ProtocolKind {
			var p prereg.Protocol
			_ = json.Unmarshal([]byte(rec.SpecJSON), &p)
			spec = p
		} else {
			var s prereg.Spec
			_ = json.Unmarshal([]byte(rec.SpecJSON), &s)
			spec = s
		}
		when := time.Unix(rec.Ts, 0).UTC()
		before := when.Before(firstGradable)
		if !before {
			registeredBefore = false
		}
		out = append(out, map[string]any{
			"seq": rec.Seq, "ts": rec.Ts, "kind": rec.Kind,
			"spec": spec, "specHash": rec.SpecHash,
			"prevHash": rec.PrevHash, "entryHash": rec.EntryHash,
			"note":                rec.Note,
			"registeredOn":        when.Format("2006-01-02"),
			"beforeFirstGradable": before,
		})
	}

	writeJSON(w, map[string]any{
		"records":          out,
		"chainVerified":    ok,
		"brokenAtSeq":      brokenAt,
		"firstGradableOn":  prereg.FirstGradableOn,
		"registeredBefore": registeredBefore,
		"whatThisIs": "The claim each structural predictor made, frozen before any of its forecasts " +
			"resolved. Every accuracy number these predictors advertise is a BACKTEST result until the " +
			"live record starts arriving on " + prereg.FirstGradableOn + ". Recording the claims first, " +
			"hashed and chained, is what makes the eventual comparison a measurement instead of a story.",
		"whyChained": "A single stored record could be replaced wholesale. Each record links to the " +
			"previous by hash, so rewriting an old claim breaks every link after it and the break is " +
			"found by recomputation rather than by trust. chainVerified false means exactly that " +
			"happened, and brokenAtSeq is where.",
		"howToUseIt": "After the first grades land, compare each kind's live accuracy against the band " +
			"claimed HERE, not against whatever the code says at that time. If the two disagree, the " +
			"frozen record is the one that counts.",
		"appendOnly": "An amended claim is a new record, never an update — a change stays visible as a " +
			"change. Nothing in this chain is ever edited or deleted.",
	})
}

func (d Deps) registerPrereg(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/prereg", d.prereg)
}
