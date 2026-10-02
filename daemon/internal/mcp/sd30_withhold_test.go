package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
)

// SD-30 on the MCP track record, following api/sd30_withhold_test.go: each test
// sets the flag itself, checks the figures are withheld with the reason while
// the retirement stays stated, and the twin restores them with the flag off.

func sd30Set(t *testing.T, v bool) {
	t.Helper()
	old := publication.SD30Withheld
	publication.SD30Withheld = v
	t.Cleanup(func() { publication.SD30Withheld = old })
}

func sd30Off(t *testing.T) { t.Helper(); sd30Set(t, false) }
func sd30On(t *testing.T)  { t.Helper(); sd30Set(t, true) }

// Both the graded rows (model_health meta) and the documented fallback.
var sd30Sources = map[string]stubSource{
	"meta": {health: map[string]string{
		"directional-ensemble-1d": `{"model":"directional-ensemble-1d","verdict":"retired",` +
			`"emitting":false,"accuracy":0.467,"baseline":0.52,"observations":8191}`,
		"directional-ensemble-1w": `{"model":"directional-ensemble-1w","verdict":"retired",` +
			`"emitting":false,"accuracy":0.451,"baseline":0.55,"observations":1200}`,
	}},
	"fallback": {},
}

func sd30Directional(t *testing.T, src stubSource) ([]map[string]any, string) {
	t.Helper()
	s := newTestServer(t, Options{ReachablePrivately: true}, src)
	out, rerr := call(t, s, fullClient(), "get_track_record", map[string]any{})
	if rerr != nil {
		t.Fatalf("%v", rerr)
	}
	blob, _ := json.Marshal(out)
	raw, _ := out["directional"].([]any)
	rows := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		rows = append(rows, r.(map[string]any))
	}
	if len(rows) == 0 {
		t.Fatal("no directional record: silence reads as no bad news")
	}
	return rows, string(blob)
}

func TestSD30_MCPTrackRecordWithholdsDirectionalFigures(t *testing.T) {
	sd30On(t)
	for name, src := range sd30Sources {
		rows, blob := sd30Directional(t, src)
		for _, row := range rows {
			for _, k := range []string{"liveAccuracy", "baselineAccuracy", "brierSkill"} {
				if v, present := row[k]; present {
					t.Errorf("%s %v: %s served under SD-30: %v", name, row["horizon"], k, v)
				}
			}
			if row["figuresWithheld"] != publication.SD30Reason {
				t.Errorf("%s %v: figuresWithheld = %v, want the SD-30 reason", name, row["horizon"], row["figuresWithheld"])
			}
			if row["verdict"] != "retired" || row["emitting"] != false {
				t.Errorf("%s %v: the retirement must stay stated: %v", name, row["horizon"], row)
			}
			if row["note"] != publication.SD30Reason {
				t.Errorf("%s %v: note = %v, want the SD-30 reason in place of the figures in prose", name, row["horizon"], row["note"])
			}
		}
		for _, fig := range []string{"0.467", "0.451", "-0.252", "below the naive baseline", "Brier skill is negative"} {
			if strings.Contains(blob, fig) {
				t.Errorf("%s: withheld figure %s still in the payload", name, fig)
			}
		}
	}
}

func TestSD30_MCPTrackRecordOffRestoresFigures(t *testing.T) {
	sd30Off(t)
	rows, _ := sd30Directional(t, sd30Sources["meta"])
	if len(rows) != 2 || rows[0]["liveAccuracy"] != 0.467 || rows[0]["baselineAccuracy"] != 0.52 ||
		rows[1]["liveAccuracy"] != 0.451 {
		t.Fatalf("flag off must restore the graded figures: %v", rows)
	}
	fb, _ := sd30Directional(t, sd30Sources["fallback"])
	if fb[0]["liveAccuracy"] != 0.467 || fb[0]["brierSkill"] != -0.252 {
		t.Fatalf("flag off must restore the figure of record: %v", fb[0])
	}
	for _, row := range append(rows, fb...) {
		if _, present := row["figuresWithheld"]; present {
			t.Fatalf("flag off still marks the row withheld: %v", row)
		}
		if row["note"] != directionalNote {
			t.Fatalf("flag off must restore the retirement note: %v", row["note"])
		}
	}
	if d := trackRecordToolDescription(t); !strings.Contains(d, "NEGATIVE live result") || strings.Contains(d, "SD-30") {
		t.Fatalf("flag off must restore the tool description: %q", d)
	}
}

// The tool description is a claim too: "NEGATIVE live result" names figures the
// tool does not serve while SD-30 withholds them.
func TestSD30_MCPTrackRecordDescriptionWithheld(t *testing.T) {
	sd30On(t)
	d := trackRecordToolDescription(t)
	if strings.Contains(d, "NEGATIVE") || !strings.Contains(d, "SD-30") || !strings.Contains(d, "retirement stands") {
		t.Fatalf("withheld description must drop the figure claim, keep the retirement and name SD-30: %q", d)
	}
}

func trackRecordToolDescription(t *testing.T) string {
	t.Helper()
	for _, d := range toolDescriptors(fullClient()) {
		if d["name"] == "get_track_record" {
			s, _ := d["description"].(string)
			return s
		}
	}
	t.Fatal("get_track_record is not listed")
	return ""
}
