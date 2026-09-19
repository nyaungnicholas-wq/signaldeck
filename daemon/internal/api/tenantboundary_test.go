package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// CROSS-USER ISOLATION, asserted per personal surface rather than per handler.
//
// The repository had one test of this shape -- TestAlertsAreUserScoped --
// covering one endpoint. Every other personal surface had tests for its own
// behaviour and nothing that put a SECOND user in the room. A scoping bug is
// invisible to a single-user suite by construction: the handler works, the data
// comes back, and it is the wrong person's.
//
// This is a release gate. The question is the one a deployment has to answer
// before strangers can sign in: can user B read user A's things. Each case
// registers two real users through /api/auth/register against a throwaway
// database, gives A something, and requires B to see none of it.
//
// WHY IT ASSERTS "A'S DATA IS ABSENT" RATHER THAN A STATUS CODE. A 200 with an
// empty list and a 403 are both correct here, and different handlers give
// different ones. What must never happen is A's row appearing in B's response.
// Pinning the status would make this fail on a refactor that was not a leak,
// which is how a security test gets relaxed instead of fixed.

func newTenantServer(t *testing.T) (*httptest.Server, *store.Store, Deps) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tenant_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}

	mux := http.NewServeMux()
	d.registerAuth(mux)
	d.registerAlerts(mux)
	// Mounted inline in api.go rather than behind a register* helper, so it is
	// spelled the same way here.
	mux.HandleFunc("GET /api/watchlist", d.watchlist)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st, d
}

// registerUser creates a user and returns a client carrying its session cookie.
func registerUser(t *testing.T, srvURL, name string) (*http.Client, int64) {
	t.Helper()
	c := newClient(t)
	resp := postJSON(t, c, srvURL+"/api/auth/register", map[string]string{
		"username": name, "password": "hunter2secret",
	})
	body := drain(t, resp)
	if resp.StatusCode >= 400 {
		t.Fatalf("register %s: HTTP %d %s", name, resp.StatusCode, body)
	}
	var me struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &me)
	if me.ID == 0 {
		t.Fatalf("register %s returned no id: %s", name, body)
	}
	return c, me.ID
}

func getAs(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp.StatusCode, drain(t, resp)
}

// TestWatchlistIsNotVisibleAcrossUsers. The watchlist is the first personal
// thing a signed-in visitor creates and the most obviously private.
func TestWatchlistIsNotVisibleAcrossUsers(t *testing.T) {
	srv, st, _ := newTenantServer(t)
	ctx := t.Context()

	alice, aliceID := registerUser(t, srv.URL, "alice")
	sym, err := st.UpsertSymbol(ctx, "ZZALICE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUserSymbol(ctx, aliceID, sym.ID); err != nil {
		t.Fatal(err)
	}

	// The fixture has to be real, or the assertion below proves nothing: a test
	// that never wrote the row would "pass" against a totally broken handler.
	if _, body := getAs(t, alice, srv.URL+"/api/watchlist"); !strings.Contains(body, "ZZALICE") {
		t.Fatalf("fixture broken: alice cannot see her own watchlist entry: %s", body)
	}

	bob, _ := registerUser(t, srv.URL, "bob")
	code, body := getAs(t, bob, srv.URL+"/api/watchlist")
	if strings.Contains(body, "ZZALICE") {
		t.Fatalf("bob (HTTP %d) can see alice's watchlist symbol: %s", code, body)
	}
}

// TestAlertsAreNotVisibleAcrossUsers mirrors TestAlertsAreUserScoped but also
// asserts the POSITIVE half -- that alice can see her own. Without it, a handler
// that returned an empty list to everyone would pass the isolation half.
func TestAlertsAreNotVisibleAcrossUsers(t *testing.T) {
	srv, st, _ := newTenantServer(t)
	ctx := t.Context()

	alice, aliceID := registerUser(t, srv.URL, "alice")
	if err := st.InsertAlert(ctx, store.Alert{
		UserID: aliceID, Kind: "breakout", Detail: "alice-only", Ts: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, body := getAs(t, alice, srv.URL+"/api/alerts"); !strings.Contains(body, "alice-only") {
		t.Fatalf("fixture broken: alice cannot see her own alert: %s", body)
	}

	bob, _ := registerUser(t, srv.URL, "bob")
	if code, body := getAs(t, bob, srv.URL+"/api/alerts"); strings.Contains(body, "alice-only") {
		t.Fatalf("bob (HTTP %d) can read alice's alert: %s", code, body)
	}
}

// TestLogoutEndsTheSession. A session that outlives logout is the same leak with
// a longer fuse -- on a shared machine the next person is "user A".
func TestLogoutEndsTheSession(t *testing.T) {
	srv, st, _ := newTenantServer(t)
	ctx := t.Context()

	alice, aliceID := registerUser(t, srv.URL, "alice")
	sym, err := st.UpsertSymbol(ctx, "ZZALICE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUserSymbol(ctx, aliceID, sym.ID); err != nil {
		t.Fatal(err)
	}
	if _, body := getAs(t, alice, srv.URL+"/api/watchlist"); !strings.Contains(body, "ZZALICE") {
		t.Fatalf("fixture broken before logout: %s", body)
	}

	drain(t, postJSON(t, alice, srv.URL+"/api/auth/logout", map[string]string{}))

	code, body := getAs(t, alice, srv.URL+"/api/watchlist")
	if strings.Contains(body, "ZZALICE") {
		t.Fatalf("the watchlist is still readable after logout (HTTP %d): %s", code, body)
	}
}

// TestAnonymousSeesNoPersonalSurfaceAtAll is the other half of the question:
// not "can B read A" but "can nobody-at-all read anyone". security.go lists
// these paths individually; this asserts the list does its job rather than that
// it exists.
func TestAnonymousSeesNoPersonalSurfaceAtAll(t *testing.T) {
	d := Deps{Cfg: baseCfg()}
	for _, p := range []string{
		"/api/watchlist",
		"/api/portfolio",
		"/api/paper/order",
		"/api/alerts",
		"/api/alerts/seen",
		"/api/notify/test",
		"/api/candidates/add",
		"/api/candidates/dismiss",
		"/api/subscribe",
		"/api/unsubscribe",
	} {
		if !d.requiresAuth(p) {
			t.Errorf("%s is reachable with no session -- it is per-user state or an outbound side effect", p)
		}
	}
}
