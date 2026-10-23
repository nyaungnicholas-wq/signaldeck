package recommendation

import (
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/composite"
)

// Since 2026-10-23 the sentiment tile is retired (its LLM ratings ran on
// NVIDIA's trial API). The news agent must state that reason, not claim that
// fresh headlines were missing.
func TestNewsViewStatesTheRetiredTileReason(t *testing.T) {
	in := Inputs{Factors: []composite.Factor{{Key: composite.FactorSentiment, Gated: true, GateReason: composite.SentimentRetiredReason}}}
	v := newsView(in)
	if v.Stance != stanceNeutral || !strings.Contains(v.View, composite.SentimentRetiredReason) || strings.Contains(v.View, "no fresh rated headlines") {
		t.Fatalf("newsView = %+v", v)
	}
}
