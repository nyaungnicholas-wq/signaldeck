package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// proxyKeyRT adds the loopback web proxy's key header to every request, as the
// private launcher does when it forwards a tunnelled visitor to the daemon.
type proxyKeyRT struct{ key string }

func (p proxyKeyRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-Signaldeck-Local", p.key)
	return http.DefaultTransport.RoundTrip(r)
}

// TestMemberFINRAOperatorOnlyByDefault: FINRA was retired on 22 Oct 2026
// because its terms do not permit this use. /api/shorts and /api/short-interest
// are no longer member routes, so SIGNALDECK_MEMBER_FINRA=1 no longer opens them,
// and on a published box the licence guard refuses them to the operator too. The
// tunnel-proxied posture is the live one: a stranger's request reaches the
// daemon on loopback carrying the proxy key, and must still meet the member
// gate (published(), not ReachablePrivately(), decides).
func TestMemberFINRAOperatorOnlyByDefault(t *testing.T) {
	for _, posture := range []struct {
		name    string
		mutate  func(*config.Config)
		proxied bool
	}{
		{"public-surface", nil, false},
		{"tunnel-proxied", func(c *config.Config) {
			c.PublicSurface, c.PublicReads, c.LocalProxyKey = false, false, "finra-proxy-key"
		}, true},
	} {
		for _, flag := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/flag=%v", posture.name, flag), func(t *testing.T) {
				srv, st, mb, _ := newProductionServer(t, func(c *config.Config) {
					c.MemberFINRA = flag
					if posture.mutate != nil {
						posture.mutate(c)
					}
				}, writeRegistry(t, thinWindowRegistry))
				if _, err := st.UpsertSymbol(t.Context(), "SNTL", md.Stocks, "Sentinel Corp"); err != nil {
					t.Fatal(err)
				}
				member := signupVerified(t, srv, mb, "mira", "mira@gmail.com")
				owner := ownerClient(t, srv.URL)
				anon := newClient(t)
				if posture.proxied {
					for _, c := range []*http.Client{member, owner, anon} {
						c.Transport = proxyKeyRT{"finra-proxy-key"}
					}
				}
				for _, u := range []string{"/api/shorts", "/api/short-interest?symbol=SNTL&market=stocks"} {
					if code, body := getAs(t, anon, srv.URL+u); code != http.StatusUnauthorized {
						t.Errorf("anonymous %s: %d %.300s, want 401", u, code, body)
					}
					if code, body := getAs(t, owner, srv.URL+u); code != http.StatusUnavailableForLegalReasons ||
						!strings.Contains(body, "finra licence") {
						t.Errorf("operator %s: %d %.300s, want 451 under the finra licence", u, code, body)
					}
					if code, body := getAs(t, member, srv.URL+u); code != http.StatusForbidden ||
						!strings.Contains(body, "not available to member accounts") {
						t.Errorf("member %s (flag %v): %d %.300s, want 403", u, flag, code, body)
					}
				}
				if code, body := getAs(t, member, srv.URL+"/api/auth/me"); code != http.StatusOK ||
					!strings.Contains(body, `"memberFinra":false`) {
					t.Errorf("member /api/auth/me (flag %v): %d %.300s, want memberFinra false", flag, code, body)
				}
				// The refusal is specific to the FINRA routes, not a broken session.
				if code, body := getAs(t, member, srv.URL+"/api/watchlist"); code != http.StatusOK {
					t.Errorf("member /api/watchlist: %d %.300s, want 200", code, body)
				}
			})
		}
	}
}
