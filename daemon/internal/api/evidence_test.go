// Evidence Engine endpoint tests (separate harness file so parallel edits
// never collide).
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/evidence"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newEvidenceServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerEvidence(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

func fp(v float64) *float64 { return &v }

func seedEvidence(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	claims := []evidence.Claim{
		{
			ID: "ev-active", Text: "active moderate claim",
			Scope: evidence.Scope{DateFrom: "2026-07-01", DateTo: "2026-07-26"},
			Items: []evidence.Item{{Kind: "live-record", Value: 0.56, NEffective: 150,
				Method: "walk-forward", CILow: fp(0.53), CIHigh: fp(0.59), Baseline: fp(0.52)}},
			Tier: evidence.TierModerate, Status: evidence.StatusActive,
			LastValidated: time.Now().Unix(), RevalidateBy: time.Now().Add(720 * time.Hour).Unix(),
			Lineage: evidence.Lineage{FeatureKeys: []string{"trend21"}},
		},
		{
			ID: "ev-retired", Text: "refuted retired claim",
			Scope: evidence.Scope{DateFrom: "2026-07-01", DateTo: "2026-07-26"},
			Items: []evidence.Item{{Kind: "live-record", Value: 0.48, NEffective: 800,
				Method: "live-forward", CILow: fp(0.45), CIHigh: fp(0.51), Baseline: fp(0.55)}},
			Tier: evidence.TierRefuted, Status: evidence.StatusRetired,
			LastValidated: time.Now().Unix(), RevalidateBy: time.Now().Add(720 * time.Hour).Unix(),
		},
	}
	for _, c := range claims {
		if err := evidence.Put(ctx, st, c); err != nil {
			t.Fatalf("seed %s: %v", c.ID, err)
		}
	}
}

type evidenceListResp struct {
	Claims []evidence.Claim `json:"claims"`
	Count  int              `json:"count"`
	Note   string           `json:"note"`
}

func TestEvidenceListAndFilters(t *testing.T) {
	srv, st := newEvidenceServer(t)
	seedEvidence(t, st)

	get := func(url string) evidenceListResp {
		t.Helper()
		resp, err := http.Get(srv.URL + url)
		if err != nil {
			t.Fatalf("get %s: %v", url, err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get %s: status %d", url, resp.StatusCode)
		}
		var out evidenceListResp
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
		return out
	}

	if got := get("/api/evidence"); got.Count != 2 || got.Note == "" {
		t.Fatalf("unfiltered: count=%d note=%q", got.Count, got.Note)
	}
	if got := get("/api/evidence?status=retired"); got.Count != 1 || got.Claims[0].ID != "ev-retired" {
		t.Fatalf("status filter: %+v", got)
	}
	if got := get("/api/evidence?feature=trend21"); got.Count != 1 || got.Claims[0].ID != "ev-active" {
		t.Fatalf("feature filter: %+v", got)
	}
	if got := get("/api/evidence?feature=nosuch"); got.Count != 0 {
		t.Fatalf("bogus feature filter matched: %+v", got)
	}
}

func TestEvidenceByID(t *testing.T) {
	srv, st := newEvidenceServer(t)
	seedEvidence(t, st)

	resp, err := http.Get(srv.URL + "/api/evidence/ev-active")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Claim evidence.Claim `json:"claim"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Claim.ID != "ev-active" || len(out.Claim.Items) != 1 {
		t.Fatalf("unexpected claim: %+v", out.Claim)
	}
	if out.Claim.Items[0].NEffective != 150 || out.Claim.Items[0].Method != "walk-forward" {
		t.Fatalf("item lost fields: %+v", out.Claim.Items[0])
	}

	miss, err := http.Get(srv.URL + "/api/evidence/no-such-claim")
	if err != nil {
		t.Fatalf("get miss: %v", err)
	}
	defer miss.Body.Close() //nolint:errcheck
	if miss.StatusCode != http.StatusNotFound {
		t.Fatalf("missing claim: status %d, want 404", miss.StatusCode)
	}
}
