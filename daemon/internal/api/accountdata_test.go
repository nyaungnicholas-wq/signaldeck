package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestMemberCanExportAndDeleteTheirAccount pins AUD-05 (2026-10-05): a member
// downloads what is held about them (no password hash, no tokens) and erases
// the account with their password; the operator account cannot be erased this
// way, and a wrong password erases nothing.
func TestMemberCanExportAndDeleteTheirAccount(t *testing.T) {
	srv, st, mb := startPublished(t, nil, true, func(d Deps) http.Handler {
		mux := http.NewServeMux()
		d.registerAuth(mux)
		d.registerAccountData(mux)
		return d.secure(mux)
	})
	member := signupVerified(t, srv, mb, "dora", "dora@gmail.com")
	ctx := context.Background()
	u, ok, err := st.GetUserByName(ctx, "dora")
	if err != nil || !ok {
		t.Fatalf("member not created: %v", err)
	}
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMemberSymbol(ctx, u.ID, sym.ID); err != nil {
		t.Fatal(err)
	}

	resp, err := member.Get(srv.URL + "/api/account/export")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %d %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	if strings.Contains(string(raw), "pass_hash") || strings.Contains(string(raw), u.PassHash) {
		t.Fatal("the export carries the password hash")
	}
	var exp struct {
		Account struct {
			Username string `json:"username"`
			Email    string `json:"email"`
		} `json:"account"`
		Watchlist []map[string]any `json:"member_symbols"`
	}
	if err := json.Unmarshal(raw, &exp); err != nil {
		t.Fatal(err)
	}
	if exp.Account.Username != "dora" || exp.Account.Email == "" || len(exp.Watchlist) != 1 {
		t.Fatalf("export = %+v", exp)
	}

	if code, body := acctPostWith(t, member, srv.URL+"/api/account/delete", map[string]string{"password": "wrong-password"}); code != http.StatusForbidden {
		t.Fatalf("delete with a wrong password: %d %s, want 403", code, body)
	}
	if _, ok, _ := st.GetUserByName(ctx, "dora"); !ok {
		t.Fatal("a wrong password erased the account")
	}
	if code, body := acctPostWith(t, member, srv.URL+"/api/account/delete", map[string]string{"password": "correcthorse1"}); code != 200 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if _, ok, _ := st.GetUserByName(ctx, "dora"); ok {
		t.Fatal("the account survived its deletion")
	}
	if syms, err := st.ListMemberSymbols(ctx, u.ID); err == nil && len(syms) != 0 {
		t.Fatalf("the watchlist survived the account: %v", syms)
	}
	if resp, err := member.Get(srv.URL + "/api/account/export"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the deleted account's session still works: %v %v", err, resp.StatusCode)
	}

	admin := newClient(t)
	if code, _ := acctPostWith(t, admin, srv.URL+"/api/auth/login", map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("admin sign-in: %d", code)
	}
	if code, _ := acctPostWith(t, admin, srv.URL+"/api/account/delete", map[string]string{"password": "adminpass123"}); code != http.StatusForbidden {
		t.Fatalf("operator self-delete: %d, want 403", code)
	}
}

func acctPostWith(t *testing.T, c *http.Client, url string, body map[string]string) (int, string) {
	t.Helper()
	resp := postJSON(t, c, url, body)
	return resp.StatusCode, drain(t, resp)
}
