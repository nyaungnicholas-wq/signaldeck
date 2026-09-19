package api

import (
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"testing"
)

func TestVolatilityRecordPublicWithoutOpeningPrivateReads(t *testing.T) {
	for _, published := range []bool{false, true} {
		d := Deps{Cfg: config.Config{PublicReads: false, PublicSurface: published}}
		if d.requiresAuth("/api/vol-forecast/record") {
			t.Errorf("published=%v: public volatility record requires login", published)
		}
		for _, path := range []string{"/api/watchlist", "/api/paper", "/api/bars", "/api/ai/chat"} {
			if !d.requiresAuth(path) {
				t.Errorf("published=%v: private route %s opened", published, path)
			}
		}
	}
}
