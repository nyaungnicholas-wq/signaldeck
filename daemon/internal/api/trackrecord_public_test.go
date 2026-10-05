package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// asOperator signs a test request in as user 1. On the unpublished test
// posture (baseCfg) any signed-in user is the operator (isOperator).
func asOperator(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userKey{}, int64(1)))
}

// TestTrackRecordRegimesAndPaperAreOperatorOnly pins the 2026-10-05 release
// audit: the anonymous /api/track-record carried a `regimes` block with
// intervals the registry withholds and a `paper` return figure for the retired
// 1d model. A non-operator now receives neither, and stripping them must not
// touch the payload the operator is served.
func TestTrackRecordRegimesAndPaperAreOperatorOnly(t *testing.T) {
	d, _ := newWave2Deps(t)
	get := func(r *http.Request) map[string]any {
		rec := httptest.NewRecorder()
		d.trackRecord(rec, r)
		if rec.Code != 200 {
			t.Fatalf("track-record status %d: %s", rec.Code, rec.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	op := get(asOperator(httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil)))
	if _, ok := op["regimes"]; !ok {
		t.Fatal("the operator view lost its regimes block; the test cannot tell stripping from absence")
	}
	anon := get(httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil))
	for _, k := range []string{"regimes", "paper"} {
		if _, ok := anon[k]; ok {
			t.Errorf("anonymous track-record still carries %q", k)
		}
	}
	if _, ok := get(asOperator(httptest.NewRequest("GET", "/api/track-record?horizon=1d", nil)))["regimes"]; !ok {
		t.Error("serving the public view removed regimes from the shared payload")
	}
}
