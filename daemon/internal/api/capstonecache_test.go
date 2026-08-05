package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

type capstoneCacheFakeLLM struct {
	callCount int
	response  string
}

func (f *capstoneCacheFakeLLM) Enabled() bool { return true }

func (f *capstoneCacheFakeLLM) Complete(_ context.Context, _ string, _ []llm.Message, _ int) (string, error) {
	f.callCount++
	return f.response, nil
}

func (f *capstoneCacheFakeLLM) Model() string    { return "test" }
func (f *capstoneCacheFakeLLM) Stats() llm.Stats { return llm.Stats{} }

func newCapstoneCacheServer(t *testing.T, fake llm.Client) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "capstone_cache_test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := Deps{St: st, LLM: fake, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerCapstones(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv, st
}

func TestProfileCacheKeyIsStableAndInputSensitive(t *testing.T) {
	sym := "AAPL"
	model := "gpt-4"
	charter := "Investor-focused"
	digest := "abc123"

	key1 := profileCacheKey(sym, model, charter, digest)
	key2 := profileCacheKey(sym, model, charter, digest)

	if key1 != key2 {
		t.Fatalf("same inputs produced different keys: %q vs %q", key1, key2)
	}

	if !strings.HasPrefix(key1, profileCachePrefix) {
		t.Errorf("key %q missing prefix %q", key1, profileCachePrefix)
	}
	if !strings.Contains(key1, sym) {
		t.Errorf("key %q does not contain symbol %q", key1, sym)
	}

	type testCase struct {
		name    string
		sym     string
		model   string
		charter string
		digest  string
	}

	cases := []testCase{
		{"different sym", "MSFT", model, charter, digest},
		{"different model", sym, "gpt-3.5", charter, digest},
		{"different charter", sym, model, "Founder-focused", digest},
		{"different digest", sym, model, charter, "def456"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			other := profileCacheKey(tc.sym, tc.model, tc.charter, tc.digest)
			if key1 == other {
				t.Errorf("changing %s produced identical key", tc.name)
			}
		})
	}
}

func TestDeleteMetaPrefixExceptKeepsOnlyTheLiveKey(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "capstone_delete_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Seed three keys under the same prefix and one unrelated key.
	prefix := "capstone_profile:AAPL:"
	keep := prefix + "ccc"
	if err := st.SetMeta(ctx, prefix+"aaa", "aaa"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := st.SetMeta(ctx, prefix+"bbb", "bbb"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := st.SetMeta(ctx, keep, "ccc"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := st.SetMeta(ctx, "llm_spend:2026-08-05", "spend"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	if err := st.DeleteMetaPrefixExcept(ctx, prefix, keep); err != nil {
		t.Fatalf("DeleteMetaPrefixExcept: %v", err)
	}

	// ccc should survive.
	if v, err := st.GetMeta(ctx, keep); err != nil || v != "ccc" {
		t.Errorf("kept key: got %q, %v; want %q", v, err, "ccc")
	}

	// aaa and bbb should be gone.
	for _, k := range []string{prefix + "aaa", prefix + "bbb"} {
		if v, err := st.GetMeta(ctx, k); err != nil || v != "" {
			t.Errorf("expected deleted key %q to be gone, got %q, %v", k, v, err)
		}
	}

	// Unrelated key must remain.
	if v, err := st.GetMeta(ctx, "llm_spend:2026-08-05"); err != nil || v != "spend" {
		t.Errorf("unrelated key was pruned; pruning must never reach outside its namespace: got %q, %v", v, err)
	}
}

func TestCompanyProfileServesCacheWithoutCallingLLM(t *testing.T) {
	ctx := context.Background()
	fake := &capstoneCacheFakeLLM{response: "Test company profile content"}
	srv, st := newCapstoneCacheServer(t, fake)

	if _, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple Inc"); err != nil {
		t.Fatalf("UpsertSymbol: %v", err)
	}

	get := func(what string) string {
		resp, err := http.Get(srv.URL + "/api/company/profile?symbol=AAPL&summary=1")
		if err != nil {
			t.Fatalf("%s GET: %v", what, err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s GET: status %d", what, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("%s decode: %v", what, err)
		}
		b, _ := json.Marshal(out)
		return string(b)
	}

	first := get("first")
	if fake.callCount != 1 {
		t.Fatalf("first request should generate the profile once, got %d call(s)", fake.callCount)
	}
	if !strings.Contains(first, "Test company profile content") {
		t.Fatalf("first response is missing the generated profile: %s", first)
	}

	second := get("second")
	if fake.callCount != 1 {
		t.Errorf("identical facts must be served from cache: the LLM was called %d times across two "+
			"requests. An uncached summary re-pays for identical facts on every page load — this is the "+
			"regression that exhausted a 2000-call daily budget.", fake.callCount)
	}
	if !strings.Contains(second, "profileCached") {
		t.Errorf("second response must be marked profileCached, got: %s", second)
	}
}
