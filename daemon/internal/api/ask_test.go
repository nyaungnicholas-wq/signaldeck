package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/copilot"
	"github.com/nyaungnicholas-wq/signaldeck/internal/llm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// echoLLM is a fake model: the planner turn returns the next queued plan (or
// plan when the queue is empty), the answer turn echoes EVERY row it was given,
// each cited. It never calls a provider.
type echoLLM struct {
	mu    sync.Mutex
	plan  string
	queue []string
	calls int
}

func (e *echoLLM) Enabled() bool    { return true }
func (e *echoLLM) Model() string    { return "echo" }
func (e *echoLLM) Stats() llm.Stats { return llm.Stats{} }
func (e *echoLLM) Complete(_ context.Context, sys string, _ []llm.Message, _ int) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if strings.HasPrefix(sys, "You route questions") {
		if len(e.queue) > 0 {
			p := e.queue[0]
			e.queue = e.queue[1:]
			return p, nil
		}
		return e.plan, nil
	}
	var rows []copilot.Row
	if err := json.Unmarshal([]byte(sys[strings.Index(sys, "ROWS:\n")+len("ROWS:\n"):]), &rows); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range rows {
		j, _ := json.Marshal(r.Row)
		fmt.Fprintf(&b, "%s [%s]\n", j, r.ID)
	}
	return b.String(), nil
}

func planFor(calls ...string) string { return `{"queries":[` + strings.Join(calls, ",") + `]}` }

func askAs(t *testing.T, c *http.Client, base, q string) (int, string) {
	t.Helper()
	resp := postJSON(t, c, base+"/api/ask", map[string]string{"question": q})
	return resp.StatusCode, drain(t, resp)
}

func ownerClient(t *testing.T, base string) *http.Client {
	t.Helper()
	owner := newClient(t)
	if code, body := acctPost(t, owner, base+"/api/auth/login",
		map[string]string{"username": "owner", "password": "adminpass123"}); code != 200 {
		t.Fatalf("owner login: %d %s", code, body)
	}
	return owner
}

// TestAskMemberOffByDefault: with SIGNALDECK_MEMBER_COPILOT unset a member is
// refused before any LLM call, while the operator asks; anonymous gets 401.
func TestAskMemberOffByDefault(t *testing.T) {
	fake := &echoLLM{plan: planFor(`{"query":"vol_forecast_record","params":{}}`)}
	publishedLLM = fake
	t.Cleanup(func() { publishedLLM = nil })
	srv, _, mb, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	member := signupVerified(t, srv, mb, "moe", "moe@gmail.com")

	if code, body := askAs(t, member, srv.URL, "how good is the vol forecast?"); code != 403 || !strings.Contains(body, askMemberOff) {
		t.Errorf("member ask with the flag off: %d %s", code, body)
	}
	if code, body := getAs(t, member, srv.URL+"/api/ask"); code != 200 || !strings.Contains(body, `"available":false`) {
		t.Errorf("member status with the flag off: %d %s", code, body)
	}
	if fake.calls != 0 {
		t.Fatalf("a refused member spent %d LLM call(s)", fake.calls)
	}
	if code, _ := askAs(t, newClient(t), srv.URL, "anything"); code != 401 {
		t.Errorf("anonymous ask: %d, want 401", code)
	}
	owner := ownerClient(t, srv.URL)
	if code, body := getAs(t, owner, srv.URL+"/api/ask"); code != 200 || !strings.Contains(body, `"worker_health"`) {
		t.Errorf("operator status must list the operator catalog: %d %s", code, body)
	}
	code, body := askAs(t, owner, srv.URL, "how good is the vol forecast?")
	if code != 200 || !strings.Contains(body, `"model":"echo"`) || fake.calls != 1 { // empty store: no rows, so no answer turn
		t.Fatalf("operator ask: %d calls=%d %s", code, fake.calls, body)
	}
	for _, q := range []string{"", "   ", strings.Repeat("x", copilot.MaxQuestion+1)} {
		if code, _ := askAs(t, owner, srv.URL, q); code != 400 {
			t.Errorf("question of %d chars: %d, want 400", len(q), code)
		}
	}
}

// TestAskNeedsTheLLM: no AI layer is a clear 503, not a crash.
func TestAskNeedsTheLLM(t *testing.T) {
	srv, _, _, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	owner := ownerClient(t, srv.URL)
	if code, body := askAs(t, owner, srv.URL, "what regimes are live?"); code != 503 || !strings.Contains(body, "AI layer") {
		t.Errorf("ask with no LLM: %d %s", code, body)
	}
	if code, body := getAs(t, owner, srv.URL+"/api/ask"); code != 200 || !strings.Contains(body, `"available":false`) {
		t.Errorf("status with no LLM: %d %s", code, body)
	}
}

// TestAskMemberTierAndCap: with the flag on a member asks through member
// entries only, and is capped per day; a refused plan still counts.
func TestAskMemberTierAndCap(t *testing.T) {
	fake := &echoLLM{plan: planFor(`{"query":"vol_forecast_record","params":{}}`)}
	publishedLLM = fake
	t.Cleanup(func() { publishedLLM = nil })
	srv, _, mb, _ := newProductionServer(t, func(c *config.Config) { c.MemberCopilot = true },
		writeRegistry(t, thinWindowRegistry))
	member := signupVerified(t, srv, mb, "mia", "mia@gmail.com")
	other := signupVerified(t, srv, mb, "max", "max@gmail.com")

	if code, body := getAs(t, member, srv.URL+"/api/ask"); code != 200 || !strings.Contains(body, `"available":true`) ||
		strings.Contains(body, "worker_health") || !strings.Contains(body, `"dailyLimit":20`) {
		t.Errorf("member status: %d %s", code, body)
	}
	for _, op := range []string{"worker_health", "paper_book_summary", "postmortem_clusters"} {
		fake.queue = append(fake.queue, planFor(`{"query":"`+op+`","params":{}}`))
		if code, body := askAs(t, member, srv.URL, "show me "+op); code != 422 {
			t.Errorf("member asked for %s: %d %s", op, code, body)
		}
	}
	fake.queue = append(fake.queue, planFor(`{"query":"my_journal_stats","params":{"uid":1}}`))
	if code, body := askAs(t, member, srv.URL, "show user 1's journal"); code != 422 {
		t.Errorf("a plan naming another user: %d %s", code, body)
	}
	for i := 4; i < askCapMember; i++ { // 4 refused plans above count too
		if code, body := askAs(t, member, srv.URL, "vol record?"); code != 200 {
			t.Fatalf("ask %d: %d %s", i+1, code, body)
		}
	}
	calls := fake.calls
	if code, body := askAs(t, member, srv.URL, "vol record?"); code != 429 {
		t.Errorf("ask %d: %d %s, want 429", askCapMember+1, code, body)
	}
	if fake.calls != calls {
		t.Errorf("a capped ask spent %d LLM call(s)", fake.calls-calls)
	}
	if code, body := askAs(t, other, srv.URL, "vol record?"); code != 200 {
		t.Errorf("another member's budget is their own: %d %s", code, body)
	}
}

// TestAskMemberCatalogCarriesNoVendorSentinels runs EVERY member catalog entry
// through POST /api/ask as a member, against the sentinel-seeded store, with a
// fake model that echoes every row it is handed. Whatever the member entries
// can return reaches the body, so no vendor sentinel and no crypto row may.
func TestAskMemberCatalogCarriesNoVendorSentinels(t *testing.T) {
	fake := &echoLLM{}
	publishedLLM = fake
	t.Cleanup(func() { publishedLLM = nil })
	ctx := context.Background()
	srv, st, mb, _ := newProductionServer(t, func(c *config.Config) { c.MemberCopilot = true },
		writeRegistry(t, thinWindowRegistry))
	freshHeartbeat(t, st)
	fx := seedSentinels(t, st, 0)
	member := signupVerified(t, srv, mb, "mira", "mira@gmail.com")
	u, _, err := st.GetUserByName(ctx, "mira")
	if err != nil {
		t.Fatal(err)
	}
	sntj, err := st.UpsertSymbol(ctx, "SNTJ", md.Stocks, "Sentinel Journal Inc")
	if err != nil {
		t.Fatal(err)
	}
	seedResolvedJournalCall(t, st, u.ID, sntj.ID)
	// The crypto sentinel also carries a STOCK-kind regime call, so only the
	// member entries' market filter (not the kind list) keeps it out.
	if err := st.UpsertRegimeForecast(ctx, fx.sntc.ID, time.Now().Unix(), structregime.Forecast{
		Kind: structregime.KindTrend21, HorizonDays: 21, Regime: "uptrend", Conviction: 0.99, HistoricalAccuracy: 0.6}); err != nil {
		t.Fatal(err)
	}

	// Rows for the member entries the base seed leaves empty: a stock and a
	// crypto regime flip (labels only), a pre-registration record, and enough
	// resolved stock predictions to clear directional_track_record's floor of
	// 10. The outcomes' forward returns are not sentinels: no member entry may
	// print one, and the sentinel ones seeded above are what the scan hunts.
	now := time.Now().Unix()
	for _, id := range []int64{fx.sntl.ID, fx.sntc.ID} {
		for i, lbl := range []string{"calm", "uptrend"} {
			if err := st.UpsertRegime(ctx, id, now-int64(2-i)*3600, lbl, 0.5, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := st.AppendPrereg(ctx, prereg.Record{Ts: now, Kind: "copilot_probe", SpecJSON: "{}",
		SpecHash: "copilotprobe", Note: "sentinel probe"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		ts := now/86400*86400 - int64(10+i)*86400
		if err := st.UpsertPrediction(ctx, store.Prediction{SymbolID: fx.sntl.ID, Horizon: md.H1d, Ts: ts,
			RawProb: 0.6, CalProb: 0.6, NUsed: 40, Components: "{}"}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, fx.sntl.ID, md.H1d, ts, 0.0105); err != nil {
			t.Fatal(err)
		}
	}

	// Every member entry, every param filled toward the seeded SNTL rows.
	var calls []string
	member0 := copilot.For(copilot.TierMember)
	for _, q := range member0 {
		params := map[string]any{}
		for _, p := range q.Params {
			switch p.Kind {
			case copilot.KindSymbol:
				params[p.Name] = fx.sntl.Symbol
			case copilot.KindEnum:
				params[p.Name] = p.Enum[0]
			case copilot.KindInt:
				params[p.Name] = p.Max
			}
		}
		b, _ := json.Marshal(map[string]any{"query": q.Name, "params": params})
		calls = append(calls, string(b))
	}
	if len(calls) < 8 {
		t.Fatalf("only %d member entries", len(calls))
	}
	rowsByQuery := map[string]int{}
	var bodies []string
	for i := 0; i < len(calls); i += copilot.MaxQueries {
		fake.queue = append(fake.queue, planFor(calls[i:min(i+copilot.MaxQueries, len(calls))]...))
		code, body := askAs(t, member, srv.URL, "tell me everything")
		if code != 200 {
			t.Fatalf("member ask %d: %d %s", i, code, body)
		}
		var res copilot.Answer
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatal(err)
		}
		for _, c := range res.Citations {
			rowsByQuery[c.Query]++
		}
		bodies = append(bodies, body)
	}
	// A scan of nothing proves nothing: EVERY member entry must have returned
	// rows from the sentinel store, and the echo put them in the body.
	for _, q := range member0 {
		if rowsByQuery[q.Name] == 0 {
			t.Errorf("%s returned no rows from the sentinel store, so its scan proves nothing (%v)", q.Name, rowsByQuery)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	sents := vendorSentinels()
	for _, body := range bodies {
		for _, l := range leaks(body, sents) {
			t.Errorf("a member catalog answer leaks a vendor value (%s); see datalicense.go D1", l)
		}
		if strings.Contains(body, "SNTC") {
			t.Errorf("a member catalog answer carries a crypto row: %.400s", body)
		}
	}
}

// TestAskCapCountsOnlyAsksThatReachTheModel: a refused plan counts (it cost an
// LLM call); a 400, a 403 or a 503 refused before any LLM call does not.
func TestAskCapCountsOnlyAsksThatReachTheModel(t *testing.T) {
	ctx := context.Background()
	used := func(st *store.Store, name string) string {
		t.Helper()
		u, _, err := st.GetUserByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		v, err := st.GetMeta(ctx, "copilot_ask:"+askDay()+":"+fmt.Sprint(u.ID))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// No LLM: the operator's asks are 503 and 400, and none counts.
	srv, st, _, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	owner := ownerClient(t, srv.URL)
	if code, _ := askAs(t, owner, srv.URL, "what regimes are live?"); code != 503 {
		t.Fatalf("no-LLM ask: %d", code)
	}
	if v := used(st, "owner"); v != "" {
		t.Errorf("a 503 counted against the cap: %q", v)
	}

	fake := &echoLLM{plan: planFor(`{"query":"worker_health","params":{}}`)}
	publishedLLM = fake
	t.Cleanup(func() { publishedLLM = nil })
	srv, st, mb, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	member := signupVerified(t, srv, mb, "cat", "cat@gmail.com")
	owner = ownerClient(t, srv.URL)
	for i := 0; i < 3; i++ {
		if code, _ := askAs(t, member, srv.URL, "anything"); code != 403 {
			t.Fatalf("member ask with the flag off: %d", code)
		}
		if code, _ := askAs(t, owner, srv.URL, ""); code != 400 {
			t.Fatalf("empty question: %d", code)
		}
	}
	if v1, v2 := used(st, "cat"), used(st, "owner"); v1 != "" || v2 != "" || fake.calls != 0 {
		t.Errorf("refusals before any LLM call counted: member %q, operator %q, LLM calls %d", v1, v2, fake.calls)
	}
	// worker_health is an operator entry, so a member plan naming it would be
	// refused; the operator's runs. Then a plan the catalog refuses: it still
	// cost an LLM call and counts.
	if code, body := askAs(t, owner, srv.URL, "worker health?"); code != 200 {
		t.Fatalf("operator ask: %d %s", code, body)
	}
	fake.queue = append(fake.queue, planFor(`{"query":"no_such_query","params":{}}`))
	if code, _ := askAs(t, owner, srv.URL, "something odd"); code != 422 {
		t.Fatalf("refused plan: %d", code)
	}
	if v := used(st, "owner"); v != "2" {
		t.Errorf("operator count %q after one answered ask and one refused plan, want 2", v)
	}
}
