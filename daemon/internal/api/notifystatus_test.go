package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
)

// newNotifyServer wires only the notify-status route behind the real
// middleware (kept separate from newTestServer so parallel edits to the
// shared test harness never collide with this wave).
func newNotifyServer(t *testing.T, n *notify.Notifier) *httptest.Server {
	t.Helper()
	srv, _, d := newTestServer(t, nil)
	d.Notifier = n
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/notify-status", d.notifyStatus)
	srv.Config.Handler = d.secure(mux)
	return srv
}

type notifyStatusBody struct {
	Transports []notifyTransportRow `json:"transports"`
	Email      string               `json:"email"`
	Note       string               `json:"note"`
}

func getNotifyStatus(t *testing.T, srv *httptest.Server) notifyStatusBody {
	t.Helper()
	res, err := newClient(t).Get(srv.URL + "/api/notify-status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body notifyStatusBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestNotifyStatusNilNotifier: minimal wiring (nil notifier) must still be
// honest — the LOCAL desktop row describes the platform the daemon is actually
// running on, all remote transports unconfigured with their enabling env vars,
// and the no-email note present.
//
// This used to assert a hardcoded macos row. That assertion is what let the
// endpoint keep claiming local delivery worked after the move to Windows, where
// `osascript` does not exist and every alert died in the log — the test was
// pinning the bug. It now asserts the invariant that actually matters: the row
// names this platform's real channel, and `configured` is true only where one
// exists.
func TestNotifyStatusNilNotifier(t *testing.T) {
	body := getNotifyStatus(t, newNotifyServer(t, nil))
	if len(body.Transports) != 4 {
		t.Fatalf("transports = %d, want 4 (local desktop + discord + telegram + webhook)", len(body.Transports))
	}
	byName := map[string]notifyTransportRow{}
	for _, tr := range body.Transports {
		byName[tr.Name] = tr
	}
	wantLocal := notify.LocalTransport()
	if wantLocal == "" {
		wantLocal = "local-desktop"
	}
	local, ok := byName[wantLocal]
	if !ok {
		t.Fatalf("no %q row for GOOS=%s; rows = %+v", wantLocal, runtime.GOOS, body.Transports)
	}
	if local.Configured != notify.LocalSupported() {
		t.Errorf("%s configured = %v, want %v (a channel must claim configured only where it exists)",
			wantLocal, local.Configured, notify.LocalSupported())
	}
	if local.Note == "" {
		t.Errorf("%s row = %+v, want an honest note about untracked delivery", wantLocal, local)
	}
	for _, name := range []string{"discord", "telegram", "webhook"} {
		tr := byName[name]
		if tr.Configured {
			t.Errorf("%s configured with nil notifier", name)
		}
		if !strings.Contains(tr.Env, "SIGNALDECK_") {
			t.Errorf("%s missing env hint: %+v", name, tr)
		}
	}
	if !strings.Contains(body.Email, "not supported") {
		t.Errorf("email note = %q, want an honest not-supported note", body.Email)
	}
}

// TestNotifyStatusConfiguredWithDelivery: a configured webhook transport that
// has delivered must show configured=true + lastOk, and the response must
// NEVER echo the secret URL anywhere.
func TestNotifyStatusConfiguredWithDelivery(t *testing.T) {
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer hook.Close()
	n := &notify.Notifier{WebhookURL: hook.URL + "/secret-path-token"}
	n.Send(context.Background(), notify.Message{Title: "t", Kind: "alerts"})

	body := getNotifyStatus(t, newNotifyServer(t, n))
	var webhook notifyTransportRow
	for _, tr := range body.Transports {
		if tr.Name == "webhook" {
			webhook = tr
		}
	}
	if !webhook.Configured || webhook.LastOK == 0 || webhook.LastError != "" {
		t.Errorf("webhook row = %+v, want configured + lastOk set", webhook)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "secret-path-token") {
		t.Errorf("secret webhook URL leaked into /api/notify-status: %s", raw)
	}
}
