// This endpoint serves the frozen pre-registration payloads that power the accuracy tables.
// It returns each stored specification verbatim, preserving every field so readers can verify
// exactly what was claimed on the registration date. Decoding into a typed struct would drop
// undeclared fields, leaving readers with blanks where they were told to look.
//
// The endpoint also indicates whether each registration occurred before the first gradable
// date of the structural predictors, but only for kinds where that comparison is defined.
// For other kinds it reports null, avoiding the illusion of a measurement where none was made.
//
// A regression once caused non-structural records to lose critical fields and all records
// to be incorrectly graded against the structural cutoff, turning missing data into false
// findings. This test ensures the payload survives intact and timing flags are applied
// correctly.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
)

func TestPreregServesTheStoredPayloadWholeAndOnlyGradesTimingWhereItApplies(t *testing.T) {
	_, st, d := newTestServer(t, nil)

	if len(prereg.Specs()) == 0 {
		t.Fatal("no structural predictor specs found")
	}
	structural := prereg.Specs()[0].Kind

	first, err := time.Parse("2006-01-02", prereg.FirstGradableOn)
	if err != nil {
		t.Fatalf("parse first gradable date: %v", err)
	}

	structuralSpec := fmt.Sprintf(`{
		"kind":%q,
		"question":"What will happen?",
		"horizonDays":21,
		"bands":[{"minConviction":0.5,"claimedAccuracy":0.8}],
		"auditProbeField":"must survive"
	}`, structural)
	if !json.Valid([]byte(structuralSpec)) {
		t.Fatalf("invalid structural spec JSON: %q", structuralSpec)
	}
	structuralRec := prereg.Record{
		Kind:     structural,
		Ts:       first.Add(-48 * time.Hour).Unix(),
		SpecHash: "hash-structural",
		Note:     "test structural",
		SpecJSON: structuralSpec,
	}
	if _, err := st.AppendPrereg(context.Background(), structuralRec); err != nil {
		t.Fatalf("append structural: %v", err)
	}

	retireSpec := `{
		"model":"some-model",
		"minIndependentN":100,
		"criterion":"some criterion",
		"action":"retire",
		"auditProbeField":"must survive"
	}`
	if !json.Valid([]byte(retireSpec)) {
		t.Fatalf("invalid retire spec JSON: %q", retireSpec)
	}
	retireRec := prereg.Record{
		Kind:     prereg.RetireRuleKind,
		Ts:       first.Add(72 * time.Hour).Unix(),
		SpecHash: "hash-retire",
		Note:     "test retire rule",
		SpecJSON: retireSpec,
	}
	if _, err := st.AppendPrereg(context.Background(), retireRec); err != nil {
		t.Fatalf("append retire rule: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/prereg", nil)
	d.prereg(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Records []struct {
			Kind                string          `json:"kind"`
			Spec                json.RawMessage `json:"spec"`
			BeforeFirstGradable *bool           `json:"beforeFirstGradable"`
		} `json:"records"`
		RegisteredBefore bool `json:"registeredBefore"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode error: %v, body: %s", err, rec.Body.String())
	}

	byKind := make(map[string]struct {
		spec json.RawMessage
		flag *bool
	}, len(got.Records))
	for _, r := range got.Records {
		byKind[r.Kind] = struct {
			spec json.RawMessage
			flag *bool
		}{r.Spec, r.BeforeFirstGradable}
	}
	if _, ok := byKind[structural]; !ok {
		t.Fatalf("missing structural record kind %q", structural)
	}
	if _, ok := byKind[prereg.RetireRuleKind]; !ok {
		t.Fatalf("missing retire rule record kind %q", prereg.RetireRuleKind)
	}

	assertSpecEqual := func(kind string, storedJSON string, served json.RawMessage) {
		var storedMap, servedMap map[string]any
		if err := json.Unmarshal([]byte(storedJSON), &storedMap); err != nil {
			t.Fatalf("failed to unmarshal stored spec for %s: %v", kind, err)
		}
		if err := json.Unmarshal(served, &servedMap); err != nil {
			t.Fatalf("failed to unmarshal served spec for %s: %v", kind, err)
		}
		if !reflect.DeepEqual(storedMap, servedMap) {
			t.Errorf("spec mismatch for %s\nstored: %#v\nserved: %#v", kind, storedMap, servedMap)
		}
		if v, ok := servedMap["auditProbeField"]; !ok || v != "must survive" {
			t.Errorf("registered payload field auditProbeField missing or wrong for %s; got %v", kind, servedMap["auditProbeField"])
		}
	}

	assertSpecEqual(structural, structuralSpec, byKind[structural].spec)
	assertSpecEqual(prereg.RetireRuleKind, retireSpec, byKind[prereg.RetireRuleKind].spec)

	if byKind[structural].flag == nil {
		t.Errorf("structural record's beforeFirstGradable is null; expected true (reader would see missing flag)")
	} else if !*byKind[structural].flag {
		t.Errorf("structural record's beforeFirstGradable is false; expected true (reader would see registration after cutoff)")
	}

	if byKind[prereg.RetireRuleKind].flag != nil {
		t.Errorf("retire rule record's beforeFirstGradable is %v; expected null (reader would see false finding)", *byKind[prereg.RetireRuleKind].flag)
	}

	if !got.RegisteredBefore {
		t.Errorf("registeredBefore is false; expected true (only one record is after cutoff and it is not compared, so aggregate should be true)")
	}
}
