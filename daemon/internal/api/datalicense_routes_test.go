package api

// The licence guard fails OPEN by construction: datalicense.RouteRedistributable
// reports any path absent from RestrictedRoutes as redistributable. A vendor
// route added without a row is therefore silently allowed, and the 2026-07-26
// audit found exactly that on /api/export/bars.csv. An unknown source KEY fails
// closed; an ungoverned ROUTE does not announce itself. These tests scan the
// registered route literals so the omission fails CI instead of an audit.

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
)

// registeredAPIRoutes collects every literal "<METHOD> /api/..." pattern in the
// package's non-test sources. Dynamically built patterns are not seen; the
// route literals in api.go and the register* helpers are all string constants.
func registeredAPIRoutes(t *testing.T) map[string]bool {
	t.Helper()
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	re := regexp.MustCompile(`"(GET|POST|PUT|DELETE|PATCH) (/api/[^" ]+)"`)
	routes := map[string]bool{}
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range re.FindAllSubmatch(data, -1) {
			routes[string(m[2])] = true
		}
	}
	return routes
}

// A route cannot be both anonymous-public and licence-restricted: the 451 would
// fire on the published product's own surface.
func TestPublicRoutesNeverGoverned(t *testing.T) {
	for path := range publicRoutes {
		if _, _, governed := datalicense.RouteRedistributable(path); governed {
			t.Errorf("%s is in publicRoutes AND RestrictedRoutes; an anonymous caller would get 451 on the public surface", path)
		}
	}
}

func TestEveryRegisteredVendorRouteIsGoverned(t *testing.T) {
	vendorHints := []string{"/bars", "/snaps", "/news", "/export/", "/tv-", "/stocktwits", "/crypto-perp", "/quote"}
	derivedExceptions := map[string]string{
		"/api/chart-overlays": "annotations only: timestamp, marker, label, own score; no OHLC, volume or vendor price (see datalicense.go)",
		"/api/news-trends":    "headline COUNTS per day and a z-score; no headline text, no publisher rows",
		"/api/tv-status":      "tunnel reachability and webhook totals; no rating, quote or signal rows",
		"/api/tv-webhook":     "inbound POST from TradingView; serves nothing",
	}
	for path := range registeredAPIRoutes(t) {
		vendor := false
		for _, hint := range vendorHints {
			if strings.Contains(path, hint) {
				vendor = true
				break
			}
		}
		if !vendor {
			continue
		}
		if _, exempt := derivedExceptions[path]; exempt {
			continue
		}
		if _, governed := datalicense.RestrictedRoutes[path]; !governed {
			t.Errorf("%s looks like it serves vendor rows and is absent from RestrictedRoutes, so "+
				"RouteRedistributable reports it redistributable and 451 can never fire; either add it "+
				"to RestrictedRoutes or, if it is genuinely derived analytics, add it to derivedExceptions "+
				"with the reason", path)
		}
	}
}

// The reverse: a stale entry for a route that no longer exists is a guard
// nobody is checking.
func TestRestrictedRoutesAreRegistered(t *testing.T) {
	routes := registeredAPIRoutes(t)
	for path := range datalicense.RestrictedRoutes {
		if !routes[path] {
			t.Errorf("stale licence entry: %s is in RestrictedRoutes but no handler registers it", path)
		}
	}
}

// Guards the scanner itself: a regexp that matches nothing would make the two
// tests above pass vacuously.
func TestRouteScannerFindsRoutes(t *testing.T) {
	if n := len(registeredAPIRoutes(t)); n < 20 {
		t.Fatalf("route scan found only %d routes; the regexp is probably broken", n)
	}
}
