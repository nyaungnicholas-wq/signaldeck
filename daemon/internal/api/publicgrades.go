package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
)

// publicGradeMinDays is how many distinct graded days a kind needs before any
// rate is shown for it. Below it a hit rate is an anecdote: the windows overlap
// heavily, so a few days of calls are close to one observation. The research
// registry uses the same floor (min_distinct_blocks) for its intervals.
const publicGradeMinDays = 10

// publicGrade is one kind's record as a visitor sees it. Accuracy and Baseline
// are absent (omitted) until the day floor is met.
type publicGrade struct {
	Kind     string   `json:"kind"`
	Open     int      `json:"open"`
	Graded   int      `json:"graded"`
	Days     int      `json:"days"`
	Accuracy *float64 `json:"accuracy,omitempty"`
	Baseline *float64 `json:"baseline,omitempty"`
	Status   string   `json:"status"`
}

// publicGrades is the visitor's view of how the public (displayable-source)
// record is doing: per kind, how many calls are still open, how many have been
// graded, and, once enough distinct days exist, how often the call was right
// next to how often the "nothing changes" baseline was right on the same rows.
//
// Like publicForecasts it is an allowlist built from this instance's own rows.
// It carries no interval and no verdict: this is a raw tally with its sample
// size, and the page says so.
func (d Deps) publicGrades(w http.ResponseWriter, r *http.Request) {
	src := d.Cfg.PublicPriceSource
	if !datalicense.PublicPriceCleared(src) {
		writeWithheld(w, "alpaca")
		return
	}
	tallies, err := d.St.RegimeKindTallies(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	out := make([]publicGrade, 0, len(tallies))
	for _, t := range tallies {
		g := publicGrade{Kind: t.Kind, Open: t.Open, Graded: t.Graded, Days: t.Days}
		switch {
		case t.Graded == 0:
			g.Status = "no forecast window has closed yet"
		case t.Days < publicGradeMinDays:
			g.Status = "too few graded days to show a rate"
		default:
			acc := float64(t.Correct) / float64(t.Graded)
			g.Accuracy = &acc
			if t.BaselineN > 0 {
				base := float64(t.BaselineCorrect) / float64(t.BaselineN)
				g.Baseline = &base
			}
			g.Status = "graded"
		}
		out = append(out, g)
	}
	body := map[string]any{
		"source":  src,
		"minDays": publicGradeMinDays,
		"kinds":   out,
		"note": "Raw tallies on overlapping forecast windows, shown with their sample size. " +
			"Not a significance test and not evidence of skill; a rate is compared with the " +
			"\"nothing changes\" baseline on the same forecasts.",
	}
	if src == "iexhist" {
		body["citation"] = datalicense.IEXCitation
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, body)
}
