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
	for _, n := range []string{"edgar", "cftc"} {
		s, ok := Sources[n]
		if !ok || s.Class != Public || !s.Redistrib {
			t.Fatalf("%s should be public and redistributable: %+v", n, s)
		}
	}
}

// FINRA, Cboe and FRED were retired on 22 Oct 2026 and the Kadoa congress
// dataset's terms are unverified: none may be described as public or
// redistributable. EDGAR is free to reuse but is not called public domain, and
// the Wikimedia pageview counts are CC0.
func TestLicenceRetiredSourcesAreNotRedistributable(t *testing.T) {
	for _, n := range []string{"fred", "finra", "cboe", "kadoa"} {
		s, ok := Sources[n]
		if !ok || s.Class == Public || s.Redistrib {
			t.Errorf("%s must be classified non-public and not redistributable: %+v", n, s)
		}
	}
	for _, n := range []string{"fred", "finra", "cboe"} {
		if !contains(Sources[n].Note, "RETIRED 22 Oct 2026") {
			t.Errorf("%s note must record the retirement: %q", n, Sources[n].Note)
		}
	}
	if !contains(Sources["kadoa"].Note, "RETIRED 23 Oct 2026") {
		t.Errorf("kadoa note must record the retirement: %q", Sources["kadoa"].Note)
	}
	if n := Sources["edgar"].Note; contains(n, "public domain") || !contains(n, "free to access and reuse") {
		t.Errorf("edgar note = %q, want the SEC's reuse wording, not public domain", n)
	}
	if n := Sources["wikimedia"].Note; !contains(n, "CC0") {
		t.Errorf("wikimedia note = %q, want CC0", n)
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

// The route table must refuse vendor data AND must not refuse derived
// analytics. Over-broad is a real failure here, not a safe default: a 451 on a
// derived surface publishes an untrue claim about what the data is.
func TestRestrictedRoutesGovernTheRightThings(t *testing.T) {
	for _, path := range []string{
		"/api/bars", "/api/snaps", "/api/news", "/api/stocktwits",
		"/api/tv-quote", "/api/tv-rating",
		"/api/shorts", "/api/short-interest", "/api/cboe-pc", "/api/macro-series",
		"/api/export/bars.csv", "/api/export/scores.csv", "/api/export/outcomes.csv",
	} {
		src, ok, governed := RouteRedistributable(path)
		if !governed {
			t.Errorf("%s is not governed by the licence table", path)
			continue
		}
		if ok {
			t.Errorf("%s is governed by %q but reports redistributable", path, src)
		}
	}

	// Derived analytics must pass. /api/chart-overlays emits markers -- a
	// timestamp, a type, a label, our own score -- and no vendor price at all.
	for _, path := range []string{
		"/api/chart-overlays", "/api/accuracy", "/api/prereg",
		"/api/vol-forecast/record", "/api/track-record",
	} {
		if _, ok, governed := RouteRedistributable(path); governed || !ok {
			t.Errorf("%s is treated as licensed vendor data; it is derived analytics", path)
		}
	}
}

// Every source named by the route table must exist in Sources. A typo would
// fall through to the unknown-key branch and refuse the route forever, with a
// reason naming a source that does not exist.
func TestRestrictedRoutesNameRealSources(t *testing.T) {
	for path, key := range RestrictedRoutes {
		if _, ok := Sources[key]; !ok {
			t.Errorf("%s names source %q, which is not in Sources", path, key)
		}
	}
}

// An unknown key must fail CLOSED. "We do not know what licence governs this"
// reads as "may not be redistributed", never the reverse.
func TestUnknownSourceFailsClosed(t *testing.T) {
	RestrictedRoutes["/api/__test_unknown"] = "no-such-source"
	defer delete(RestrictedRoutes, "/api/__test_unknown")
	src, ok, governed := RouteRedistributable("/api/__test_unknown")
	if !governed || ok {
		t.Errorf("unknown source %q reported governed=%v redistributable=%v; want governed and refused",
			src, governed, ok)
	}
}

// EVERY NON-REDISTRIBUTABLE SOURCE THAT HAS A SERVING ROUTE MUST BE GOVERNED.
//
// The bug this pins: /api/crypto-perp served Hyperliquid funding, open interest
// and mark price -- a vendor price -- while absent from RestrictedRoutes, so
// RouteRedistributable reported it redistributable and the 451 guard could never
// fire. hyperliquid is class Licensed with its commercial terms recorded as
// UNESTABLISHED, which is precisely the case to withhold on.
//
// It stayed invisible because the table's two failure directions are not
// symmetric. An unknown KEY fails closed (TestUnknownSourceFailsClosed). An
// ungoverned ROUTE fails OPEN: any path missing from the map is reported
// redistributable. A row that is simply absent therefore reads as "allowed",
// which is the direction that does not announce itself.
//
// Listing the serving route per source by hand is deliberate. It cannot be
// derived from the map being tested without asserting that map against itself,
// and a source with no public route at all (cryptohist reaches users only
// through /api/bars, which IS governed) must not be forced to invent one.
func TestEveryNonRedistributableSourceWithARouteIsGoverned(t *testing.T) {
	servingRoute := map[string]string{
		"alpaca":      "/api/bars",
		"cryptolive":  "/api/snaps",
		"news":        "/api/news",
		"stocktwits":  "/api/stocktwits",
		"tvscanner":   "/api/tv-rating",
		"hyperliquid": "/api/crypto-perp",
		"finra":       "/api/short-interest",
		"cboe":        "/api/cboe-pc",
		"fred":        "/api/macro-series",
		// kadoa: /api/congress is withheld from non-operators through
		// NonOperatorSource, not RestrictedRoutes, so the operator can still
		// read the rows stored before the retirement.
		// cryptohist: no route of its own. It is a PriceBarSources member and
		// reaches users only through /api/bars, which is governed as alpaca.
	}

	for key, s := range Sources {
		if s.Redistrib {
			continue // public sources are meant to be servable raw
		}
		route, has := servingRoute[key]
		if !has {
			continue // no serving route; nothing to govern
		}
		src, ok, governed := RouteRedistributable(route)
		if !governed {
			t.Errorf("%s serves non-redistributable source %q but is absent from "+
				"RestrictedRoutes, so the licence guard reports it redistributable and "+
				"451 can never fire for it", route, key)
			continue
		}
		if ok {
			t.Errorf("%s is governed by %q yet reports redistributable", route, src)
		}
		if src != key {
			t.Errorf("%s is governed by %q, want %q -- the refusal would name the wrong provider",
				route, src, key)
		}
	}
}

// TestNonOperatorAllowlistFailsClosed: the non-operator gate is an allowlist.
// Every entry says why its payload is clear, none is also a refused route, and
// a path on no list is withheld under Alpaca's terms rather than served.
func TestNonOperatorAllowlistFailsClosed(t *testing.T) {
	if len(NonOperatorRoutes) < 20 {
		t.Fatalf("only %d rights-cleared routes; the table was truncated", len(NonOperatorRoutes))
	}
	for p, why := range NonOperatorRoutes {
		if why == "" {
			t.Errorf("%s is cleared with no reason", p)
		}
		if src, cleared := NonOperatorSource(p); !cleared || src != "" {
			t.Errorf("NonOperatorSource(%s) = %q, %v; it is on the allowlist", p, src, cleared)
		}
		if src, ok := RestrictedRoutes[p]; ok {
			t.Errorf("%s is cleared for non-operators AND refused under %s", p, src)
		}
	}
	for path, want := range map[string]string{
		"/api/congress":                  "kadoa",
		"/api/bars":                      "alpaca",
		"/api/macro-series":              "fred",
		"/api/accuracy":                  "alpaca",
		"/api/ledger/range":              "alpaca",
		"/api/a-route-nobody-classified": "alpaca",
		"/mcp":                           "alpaca",
	} {
		if src, cleared := NonOperatorSource(path); cleared || src != want {
			t.Errorf("NonOperatorSource(%s) = %q, %v; want %q, withheld", path, src, cleared, want)
		}
	}
	for src := range Sources {
		if n := WithheldNotice(src); !contains(n, "Withheld: ") || !contains(n, "operator") {
			t.Errorf("WithheldNotice(%s) = %q", src, n)
		}
	}
}

// NVIDIA's hosted API was retired 2026-10-23: its trial terms do not permit
// production use. It must stay Restricted, not redistributable, and say why.
func TestNVIDIAIsRetiredAndRestricted(t *testing.T) {
	s, ok := Sources["nvidia"]
	if !ok || s.Class != Restricted || s.Redistrib {
		t.Fatalf("nvidia must be Restricted and not redistributable: %+v", s)
	}
	for _, want := range []string{"RETIRED 2026-10-23", "trial terms", "production use"} {
		if !contains(s.Note, want) {
			t.Errorf("nvidia note must say %q: %q", want, s.Note)
		}
	}
}
