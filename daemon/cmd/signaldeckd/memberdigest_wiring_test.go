package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/memberdigest"
	"github.com/nyaungnicholas-wq/signaldeck/internal/notify"
)

// The digest's link origin is SIGNALDECK_PUBLIC_URL only: a quick-tunnel URL
// (which the API's publicBase falls back to) changes on every restart, so a
// link mailed with it dies. No configured URL = no email and no links.
func TestMemberDigestUsesConfiguredPublicURLOnly(t *testing.T) {
	log := filepath.Join(t.TempDir(), "tunnel.log")
	if err := os.WriteFile(log, []byte("|  https://abc-def.trycloudflare.com  |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := func(cfg config.Config) string {
		t.Helper()
		ws := memberDigestWorkers(nil, &notify.Notifier{}, cfg)
		w, ok := ws[0].(*memberdigest.Worker)
		if !ok {
			t.Fatalf("first worker is %T", ws[0])
		}
		return w.Base()
	}
	if got := base(config.Config{TunnelLog: log}); got != "" {
		t.Errorf("tunnel-only deployment: digest base %q, want none", got)
	}
	if got := base(config.Config{TunnelLog: log, PublicURL: "https://sd.example"}); got != "https://sd.example" {
		t.Errorf("configured URL: digest base %q", got)
	}
}
