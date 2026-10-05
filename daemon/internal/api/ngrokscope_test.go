package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestNgrokHostReachesOnlyTheWebhook: the weekday ngrok tunnel points straight
// at the daemon. Before 2026-10-05 every route answered through it, so the
// operator token worked from the internet. Now an ngrok Host gets the
// TradingView webhook and a 403 for everything else, token or not.
func TestNgrokHostReachesOnlyTheWebhook(t *testing.T) {
	const ngrok = "spearfish-dwindle-module.ngrok-free.dev"
	cfg := baseCfg()
	cfg.APIToken = "operator-token"
	cfg.AllowedHosts = []string{"127.0.0.1:8322", ngrok}
	st, err := store.Open(filepath.Join(t.TempDir(), "ngrok.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reached := false
	d := Deps{St: st, Cfg: cfg}
	h := d.secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusTeapot)
	}))
	do := func(method, host, path string) int {
		reached = false
		req := httptest.NewRequest(method, path, nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer operator-token")
		req.Header.Set(csrfHeader, "1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, path := range []string{"/api/health", "/api/version", "/api/export/bars.csv", "/api/watchlist"} {
		if code := do("GET", ngrok, path); code != http.StatusForbidden || reached {
			t.Errorf("GET %s via ngrok: %d (handler reached %v), want 403", path, code, reached)
		}
	}
	if code := do("GET", ngrok, "/api/tv-webhook"); code != http.StatusForbidden {
		t.Errorf("GET /api/tv-webhook via ngrok: %d, want 403 (POST only)", code)
	}
	if do("POST", ngrok, "/api/tv-webhook"); !reached {
		t.Error("POST /api/tv-webhook via ngrok was blocked before its handler")
	}
	if do("GET", "127.0.0.1:8322", "/api/version"); !reached {
		t.Error("a loopback request was blocked by the ngrok scope")
	}
}
