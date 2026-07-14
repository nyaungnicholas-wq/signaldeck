package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newSelfAuditServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerSelfAudit(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

type selfAuditResp struct {
	Note        string               `json:"note"`
	GeneratedTs int64                `json:"generatedTs"`
	Empty       bool                 `json:"empty"`
	Findings    []store.SelfAuditRow `json:"findings"`
}

// TestSelfAuditEndpoint: latest finding per metric is returned (newest run
// wins), the note ships, and the empty state is honest.
func TestSelfAuditEndpoint(t *testing.T) {
	srv, st := newSelfAuditServer(t)
	ctx := context.Background()

	get := func(out any) int {
		res, err := newClient(t).Get(srv.URL + "/api/self-audit")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer res.Body.Close() //nolint:errcheck
		if res.StatusCode == 200 {
			if err := json.NewDecoder(res.Body).Decode(out); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
		return res.StatusCode
	}

	// Empty state before any audit: 200 (public read), empty=true, [] not null.
	var empty selfAuditResp
	if code := get(&empty); code != 200 {
		t.Fatalf("status = %d, want 200 (public read)", code)
	}
	if !empty.Empty || len(empty.Findings) != 0 || empty.Note == "" {
		t.Fatalf("empty state: %+v", empty)
	}

	// Two runs of the same metric: the newer status must win.
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{Ts: 1000, Metric: "calibration:1d", Value: 0.20, Status: "ok", Detail: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{Ts: 2000, Metric: "calibration:1d", Value: 0.28, Status: "degrading", Detail: "new"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertSelfAudit(ctx, store.SelfAuditRow{Ts: 2000, Metric: "factor_ic:pressure", Value: 0, Status: "insufficient", Detail: "thin"}); err != nil {
		t.Fatal(err)
	}

	var body selfAuditResp
	if code := get(&body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Empty || body.GeneratedTs != 2000 || len(body.Findings) != 2 {
		t.Fatalf("body: %+v", body)
	}
	var cal store.SelfAuditRow
	for _, f := range body.Findings {
		if f.Metric == "calibration:1d" {
			cal = f
		}
	}
	if cal.Status != "degrading" || cal.Value != 0.28 {
		t.Fatalf("latest calibration must win: %+v", cal)
	}
}
