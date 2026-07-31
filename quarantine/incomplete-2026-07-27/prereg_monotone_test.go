package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// While the grading-protocol chain is not monotone in strictness there is no
// single frozen protocol, so the two surfaces that publish graded evidence
// refuse rather than serve under whichever record was written last. The
// refusal must NAME the seq pair — a 409 that says only "conflict" would be
// the same silence in a different colour.
func TestGradedSurfacesRefuseOnARetrogradeProtocolChain(t *testing.T) {
	d := Deps{ProtocolWeakenings: []string{
		"seq 18 weakens seq 14: minDistinctBlocks was 10, now absent (0) (frozen by seq 14)"}}
	for name, h := range map[string]http.HandlerFunc{
		"/api/prereg":       d.prereg,
		"/api/model-health": d.modelHealth,
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", name, nil))
		if rec.Code != http.StatusConflict {
			t.Errorf("%s served %d while the chain weakens; want 409", name, rec.Code)
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(rec.Body.String(), "seq 18 weakens seq 14") {
			t.Errorf("%s refusal does not name the seq pair: %s", name, rec.Body.String())
		}
	}

	// The guard is inert on a monotone chain — it may only ever suppress.
	clean := Deps{}
	rec := httptest.NewRecorder()
	if clean.refuseOnProtocolWeakening(rec) {
		t.Error("a monotone chain must not be refused")
	}
}
