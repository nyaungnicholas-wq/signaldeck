package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
)

// The default provider (NVIDIA's hosted API) was retired 2026-10-23: its trial
// terms do not permit production use. The on-demand AI endpoints must say so,
// not tell the operator to set a key that can no longer help.
func TestAIEndpointsReportRetiredProvider(t *testing.T) {
	d := Deps{LLM: llm.New([]string{"nvapi-test"}, "https://integrate.api.nvidia.com/v1", "m", "", "", 10)}
	rec := httptest.NewRecorder()
	if d.aiReady(rec) {
		t.Fatal("a retired provider must not be ready")
	}
	if body := rec.Body.String(); rec.Code != 503 || !strings.Contains(body, "AI is retired: NVIDIA trial API terms") {
		t.Fatalf("aiReady = %d %q, want 503 naming the retirement", rec.Code, body)
	}

	rec = httptest.NewRecorder()
	d.aiStatus(rec, httptest.NewRequest("GET", "/api/ai/status", nil))
	var out struct {
		Enabled bool   `json:"enabled"`
		Retired string `json:"retired"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Enabled || !strings.Contains(out.Retired, "NVIDIA trial API terms") {
		t.Fatalf("aiStatus = %+v, want disabled with the retirement reason", out)
	}
}
