package pipeline

import (
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// The heavy fleet is marked for the spread boot start, its cadence is what it
// was, and the light, critical workers (resolvers, scorers, market data) and
// the research loop are not marked: they still start within the first minute.
func TestHeavyFleetMarkedWithItsCadenceUnchanged(t *testing.T) {
	heavy := []struct {
		w  workers.Worker
		iv time.Duration
	}{
		{&GBMTrainer{}, time.Hour}, {&PerSymbolLearner{}, time.Hour}, {&PressureTrainer{}, time.Hour},
		{&AdaptiveWeightsWorker{}, 6 * time.Hour}, {&ModelHealthWorker{}, time.Hour},
		{&ExpectancyRunner{}, time.Hour}, {&MetaLabelRunner{}, 12 * time.Hour},
		{&AlphaXTrainer{}, 6 * time.Hour}, {&ExpectancyTrainer{}, time.Hour},
		{&FeatureRedundancyRunner{}, 24 * time.Hour}, {&CanaryRunner{}, time.Hour},
		{&SmartMoneyScorer{}, time.Hour}, {&ForecastTrainer{}, time.Hour},
		{&FeatureHealthGrader{}, 6 * time.Hour},
	}
	for _, h := range heavy {
		hw, ok := h.w.(workers.HeavyWorker)
		if !ok || !hw.Heavy() {
			t.Errorf("%s is not marked heavy", h.w.Name())
		}
		if got := h.w.Interval(); got != h.iv {
			t.Errorf("%s interval %v, want %v unchanged", h.w.Name(), got, h.iv)
		}
	}
	for _, w := range []workers.Worker{&PredictionResolver{}, &PredictionRunner{}, &SignalRunner{},
		&CompositeScorer{}, &StockBars{}, &CryptoBars{}, &ResearchLoop{}} {
		if hw, ok := w.(workers.HeavyWorker); ok && hw.Heavy() {
			t.Errorf("%s is marked heavy; it must start with the fleet", w.Name())
		}
	}
	if got := (&ResearchLoop{}).Interval(); got != 24*time.Hour {
		t.Errorf("research-loop interval %v, want 24h (never raised)", got)
	}
}
