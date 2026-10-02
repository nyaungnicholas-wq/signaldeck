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

// TestMemberFINRAOperatorOnlyByDefault: FINRA's terms may not permit passing
// its short data on to members (owner's call, 2026-10-02), so /api/shorts and
// /api/short-interest are operator-only unless SIGNALDECK_MEMBER_FINRA=1. The
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
					if code, body := getAs(t, owner, srv.URL+u); code != http.StatusOK {
						t.Errorf("operator %s: %d %.300s, want 200", u, code, body)
					}
					code, body := getAs(t, member, srv.URL+u)
					switch {
					case !flag && (code != http.StatusForbidden || !strings.Contains(body, "not available to member accounts")):
						t.Errorf("member %s with the flag off: %d %.300s, want 403", u, code, body)
					case flag && code != http.StatusOK:
						t.Errorf("member %s with the flag on: %d %.300s, want 200", u, code, body)
					}
				}
				if code, body := getAs(t, member, srv.URL+"/api/auth/me"); code != http.StatusOK ||
					!strings.Contains(body, fmt.Sprintf(`"memberFinra":%v`, flag)) {
					t.Errorf("member /api/auth/me: %d %.300s, want memberFinra %v", code, body, flag)
				}
				// The refusal is specific to the FINRA routes, not a broken session.
				if code, body := getAs(t, member, srv.URL+"/api/watchlist"); code != http.StatusOK {
					t.Errorf("member /api/watchlist: %d %.300s, want 200", code, body)
				}
			})
		}
	}
}
