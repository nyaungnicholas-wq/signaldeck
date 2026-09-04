package volprereg

import (
	"strings"
	"testing"
)

// The digest must not move without someone deciding it should. A
// pre-registration whose hash drifts silently is not a registration, and this
// is the same pinned-digest discipline prereg.AutoRetireRule already carries.
//
// If this fails, the spec text changed. That is either a mistake or an
// AMENDMENT -- and an amendment is a decision to be filed on the chain, never
// a test to be updated.
func TestRegistrationDigestIsPinned(t *testing.T) {
	got := Registration().Hash()
	const want = "f44e22b2e6ee7a77634c5fad609b7083f70eabd0821fff2018563eb43151f9e8"
	if got != want {
		t.Errorf("spec digest moved:\n got  %s\n want %s\n"+
			"If the change was deliberate it is an AMENDMENT and belongs on the "+
			"chain, not in this test.", got, want)
	}
}

// canonical must be sensitive to every field. A field left out of the digest
// is a field that can be edited after filing with no visible trace, which is
// exactly what a pre-registration exists to prevent.
func TestCanonicalCoversEveryField(t *testing.T) {
	base := Registration()
	baseHash := base.Hash()

	mutate := map[string]func(*RVSpec){
		"TestID":               func(s *RVSpec) { s.TestID += "x" },
		"Question":             func(s *RVSpec) { s.Question += "x" },
		"Estimand":             func(s *RVSpec) { s.Estimand += "x" },
		"Horizons":             func(s *RVSpec) { s.Horizons = append(s.Horizons, 22) },
		"Model":                func(s *RVSpec) { s.Model += "x" },
		"Nulls":                func(s *RVSpec) { s.Nulls += "x" },
		"Losses":               func(s *RVSpec) { s.Losses += "x" },
		"Control":              func(s *RVSpec) { s.Control += "x" },
		"UnitOfObservation":    func(s *RVSpec) { s.UnitOfObservation += "x" },
		"TestStatistic":        func(s *RVSpec) { s.TestStatistic += "x" },
		"Headline":             func(s *RVSpec) { s.Headline += "x" },
		"FamilySize":           func(s *RVSpec) { s.FamilySize++ },
		"LooksSpent":           func(s *RVSpec) { s.LooksSpent++ },
		"MinDistinctDays":      func(s *RVSpec) { s.MinDistinctDays++ },
		"MinSymbolsPerDay":     func(s *RVSpec) { s.MinSymbolsPerDay++ },
		"StartRule":            func(s *RVSpec) { s.StartRule += "x" },
		"DecisionRule":         func(s *RVSpec) { s.DecisionRule += "x" },
		"KnownWeakness":        func(s *RVSpec) { s.KnownWeakness += "x" },
		"WhatThisCannotChange": func(s *RVSpec) { s.WhatThisCannotChange += "x" },
		"BacktestAtFiling":     func(s *RVSpec) { s.BacktestAtFiling += "x" },
	}
	for name, mut := range mutate {
		s := Registration()
		mut(&s)
		if s.Hash() == baseHash {
			t.Errorf("%s is NOT covered by the digest: it can be edited after "+
				"filing with no visible trace", name)
		}
	}
}

// The things that decide the verdict must be stated, not implied. Each of
// these is a rule this repository has been burned by leaving vague.
func TestRegistrationStatesTheRulesThatDecide(t *testing.T) {
	s := Registration()
	for _, want := range []struct{ field, phrase string }{
		{"UnitOfObservation", "trading DAY"},
		{"UnitOfObservation", "never the (symbol, day)"},
		{"Headline", "ONE cell"},
		{"DecisionRule", "INSUFFICIENT"},
		{"DecisionRule", "NO SKILL DEMONSTRATED"},
		{"DecisionRule", "ESTIMATOR ARTIFACT"},
		{"DecisionRule", "BEATS THE NULLS"},
		{"StartRule", "No backfill, ever"},
		{"Control", "RV^CC"},
		{"KnownWeakness", "not elicitable"},
		{"BacktestAtFiling", "THIS IS A BACKTEST"},
	} {
		var got string
		switch want.field {
		case "UnitOfObservation":
			got = s.UnitOfObservation
		case "Headline":
			got = s.Headline
		case "DecisionRule":
			got = s.DecisionRule
		case "StartRule":
			got = s.StartRule
		case "Control":
			got = s.Control
		case "KnownWeakness":
			got = s.KnownWeakness
		case "BacktestAtFiling":
			got = s.BacktestAtFiling
		}
		if !strings.Contains(got, want.phrase) {
			t.Errorf("%s does not state %q", want.field, want.phrase)
		}
	}
	if s.LooksSpent < 2 {
		t.Errorf("LooksSpent = %d; two exploratory looks were taken before the "+
			"harness existed and must be charged", s.LooksSpent)
	}
	if s.FamilySize < 24 {
		t.Errorf("FamilySize = %d; 2 horizons x 3 nulls x 2 losses x 2 proxies is 24", s.FamilySize)
	}
}
