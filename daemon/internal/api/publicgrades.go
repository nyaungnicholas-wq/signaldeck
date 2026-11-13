package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
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
	Compared int      `json:"compared,omitempty"` // rows both rates are measured on
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
		out = append(out, gradeOf(t))
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

// gradeOf turns one kind's tally into what a visitor sees: a rate only past
// the day floor, and with a baseline, both rates on the same rows.
func gradeOf(t store.RegimeKindTally) publicGrade {
	g := publicGrade{Kind: t.Kind, Open: t.Open, Graded: t.Graded, Days: t.Days}
	switch {
	case t.Graded == 0:
		g.Status = "no forecast window has closed yet"
	case t.Days < publicGradeMinDays:
		g.Status = "too few graded days to show a rate"
	default:
		// With a baseline, both rates come from the SAME rows (the ones
		// that froze a baseline), and the day floor applies to those rows:
		// the page says "on the same forecasts" and shows "compared".
		if t.BaselineN > 0 {
			if t.BaselineDays < publicGradeMinDays {
				g.Status = "too few graded days with a baseline to show a rate"
				break
			}
			acc := float64(t.CorrectOnBaseline) / float64(t.BaselineN)
			base := float64(t.BaselineCorrect) / float64(t.BaselineN)
			g.Accuracy, g.Baseline, g.Compared = &acc, &base, t.BaselineN
		} else {
			acc := float64(t.Correct) / float64(t.Graded)
			g.Accuracy = &acc
		}
		g.Status = "graded"
	}
	return g
}
