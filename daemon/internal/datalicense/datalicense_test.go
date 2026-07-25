package datalicense

import "testing"

// This table is a legal boundary expressed as code, so the tests guard the
// property that matters: no price feed may be marked redistributable unless it
// genuinely is, and adding an ingest package without classifying it must be
// visible rather than silent.

func TestNoLicensedSourceIsMarkedRedistributable(t *testing.T) {
	for name, s := range Sources {
		if s.Class == Licensed && s.Redistrib {
			t.Fatalf("%s is Licensed but flagged redistributable — that is the mistake "+
				"this table exists to prevent", name)
		}
		if s.Class == Restricted && s.Redistrib {
			t.Fatalf("%s is Restricted but flagged redistributable", name)
		}
	}
}

func TestRawBarsAreNotRedistributableToday(t *testing.T) {
	// Every price feed currently in use is licensed, so the guard must refuse.
	// If this ever fails, a public-domain price source was added — verify that
	// deliberately rather than by assuming the table is right.
	if BarsRedistributable() {
		t.Fatal("raw bars reported redistributable; confirm a public-domain price " +
			"source was added on purpose before changing this test")
	}
}

func TestEveryPriceSourceIsClassified(t *testing.T) {
	for _, s := range PriceBarSources {
		if _, ok := Sources[s]; !ok {
			t.Fatalf("price source %q has no license classification", s)
		}
	}
}

func TestEverySourceCarriesProviderAndNote(t *testing.T) {
	// A classification nobody can act on is not a classification.
	for name, s := range Sources {
		if s.Provider == "" || s.Note == "" {
			t.Fatalf("%s must name its provider and explain the restriction: %+v", name, s)
		}
		switch s.Class {
		case Public, Licensed, Restricted:
		default:
			t.Fatalf("%s has an unknown class %q", name, s.Class)
		}
	}
}

func TestGovernmentSourcesAreRedistributable(t *testing.T) {
	// The flip side: over-restricting is also wrong. Public-domain regulatory
	// data must stay usable.
	for _, n := range []string{"edgar", "fred", "finra", "cftc"} {
		s, ok := Sources[n]
		if !ok || s.Class != Public || !s.Redistrib {
			t.Fatalf("%s should be public-domain and redistributable: %+v", n, s)
		}
	}
}

func TestNoticeNamesTheProvidersAndTheEscapeHatch(t *testing.T) {
	n := RawDataNotice()
	if len(n) < 80 {
		t.Fatal("the refusal must explain itself, not just deny")
	}
	for _, want := range []string{"Alpaca", "SIGNALDECK_ALLOW_RAW_EXPORT", "does not grant"} {
		if !contains(n, want) {
			t.Fatalf("notice should mention %q: %s", want, n)
		}
	}
}

func contains(h, n string) bool {
	return len(h) >= len(n) && (func() bool {
		for i := 0; i+len(n) <= len(h); i++ {
			if h[i:i+len(n)] == n {
				return true
			}
		}
		return false
	})()
}
