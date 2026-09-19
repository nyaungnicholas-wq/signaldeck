package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// /api/hud mirrors a SEPARATE project's live brokerage account -- cash,
// equity, buying power, open positions, trade history and the full tuned
// strategy configuration -- stored verbatim and served without parsing.
//
// These pin that the route is OWNER-only, not merely authenticated. Before
// requireAdmin existed, IsAdmin was set at signup, stored, and returned to the
// browser while being checked by nothing: every account had identical
// authority, so any second user could read the first's account.

// TestHud_AnonymousIsRefused runs under baseCfg, which sets PublicReads:true --
// the LOOPBACK default. That combination is exactly the case requiresAuth does
// NOT cover: with reads open, an anonymous GET reached this handler, so before
// requireAdmin the route was readable by anything that could open a socket to
// 127.0.0.1. The live daemon answers 401 only because its own config closes
// PublicReads (A9).
//
// So this asserts the property that matters: /api/hud is shut under EVERY
// combination of PublicSurface and PublicReads, rather than depending on a
// flag whose default differs between the loopback and published deployments.
func TestHud_AnonymousIsRefused(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	c := newClient(t)

	resp, err := c.Get(srv.URL + "/api/hud")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got = %v, want %v (%s)", resp.StatusCode, http.StatusUnauthorized, drain(t, resp))
	}
}

// TestHud_NonAdminUserIsRefused is the finding itself: a second, ordinary
// account must not be able to read the owner's brokerage account. Both users
// hold a valid session, so only the admin check can separate them.
func TestHud_NonAdminUserIsRefused(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)

	// First registration becomes admin.
	admin := newClient(t)
	resp := postJSON(t, admin, srv.URL+"/api/auth/register",
		map[string]string{"username": "owner", "password": "hunter2secret"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register owner: %v %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	// Second registration is an ordinary user.
	other := newClient(t)
	resp = postJSON(t, other, srv.URL+"/api/auth/register",
		map[string]string{"username": "intruder", "password": "hunter2secret"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register intruder: %v %s", resp.StatusCode, drain(t, resp))
	}
	if body := drain(t, resp); !strings.Contains(body, `"isAdmin":false`) {
		t.Fatalf("second user should not be admin, got %s", body)
	}

	// The ordinary account is authenticated and still refused.
	resp, err := other.Get(srv.URL + "/api/hud")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin got = %v, want %v (%s)", resp.StatusCode, http.StatusForbidden, drain(t, resp))
	}
	drain(t, resp)

	// The owner still reaches it -- a gate that refuses everyone is not a fix.
	resp, err = admin.Get(srv.URL + "/api/hud")
	if err != nil {
		t.Fatalf("get as admin: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin got = %v, want %v (%s)", resp.StatusCode, http.StatusOK, drain(t, resp))
	}
	drain(t, resp)
}

// TestHud_NoAdminRowFailsClosed covers the inversion case. AdminUserID returns
// (0, nil) -- not an error -- when no users row has is_admin=1, which a
// restored or half-migrated database can produce. Reading that as "no admin is
// configured, so let everyone through" would open the route at exactly the
// moment it should shut.
func TestHud_NoAdminRowFailsClosed(t *testing.T) {
	srv, st, _ := newTestServer(t, nil)
	ctx := context.Background()

	// Build the account directly so it is created WITHOUT the admin flag;
	// registering through the API would make the first user an admin.
	hash, err := bcrypt.GenerateFromPassword([]byte("hunter2secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := st.CreateUser(ctx, "nobody", string(hash), false); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if id, err := st.AdminUserID(ctx); err != nil || id != 0 {
		t.Fatalf("precondition: AdminUserID = (%v, %v), want (0, nil)", id, err)
	}

	c := newClient(t)
	resp := postJSON(t, c, srv.URL+"/api/auth/login",
		map[string]string{"username": "nobody", "password": "hunter2secret"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %s", resp.StatusCode, drain(t, resp))
	}
	drain(t, resp)

	resp, err = c.Get(srv.URL + "/api/hud")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got = %v, want %v (%s)", resp.StatusCode, http.StatusForbidden, drain(t, resp))
	}
}
