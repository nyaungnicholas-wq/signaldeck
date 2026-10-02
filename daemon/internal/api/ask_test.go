package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	fake := &echoLLM{plan: planFor(`{"query":"prereg_chain","params":{}}`)}
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
	fake := &echoLLM{plan: planFor(`{"query":"prereg_chain","params":{}}`)}
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
	// crypto regime flip (labels only) and a pre-registration record.
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
	if len(calls) < 6 {
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

// statsLLM is echoLLM with a settable daily usage and an optional gate that
// holds the planner turn until released.
type statsLLM struct {
	echoLLM
	stats   llm.Stats
	entered chan struct{}
	release chan struct{}
}

func (s *statsLLM) Stats() llm.Stats { return s.stats }
func (s *statsLLM) Complete(ctx context.Context, sys string, m []llm.Message, n int) (string, error) {
	if s.release != nil && strings.HasPrefix(sys, "You route questions") {
		s.entered <- struct{}{}
		<-s.release
	}
	return s.echoLLM.Complete(ctx, sys, m, n)
}

// askDirect drives the handler as uid on a PRIVATE (unpublished) deployment.
func askDirect(t *testing.T, d Deps, uid int64, q string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"question": q})
	rec := httptest.NewRecorder()
	d.ask(rec, withUser(httptest.NewRequest("POST", "/api/ask", bytes.NewReader(b)), uid))
	return rec.Code, rec.Body.String()
}

func privateAskServer(t *testing.T, fake llm.Client) (Deps, int64, int64) {
	t.Helper()
	_, st, d := newTestServer(t, nil)
	d.LLM = fake
	owner, err := st.CreateUser(context.Background(), "owner", "x", true)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := st.CreateUser(context.Background(), "guest", "x", false)
	if err != nil {
		t.Fatal(err)
	}
	return d, owner, guest
}

// TestAskOperatorTierNeedsTheAdmin: before publish every signed-in account was
// "the operator"; the copilot's operator tier needs the actual admin, and any
// other account is a member, so it asks only with the member flag on.
func TestAskOperatorTierNeedsTheAdmin(t *testing.T) {
	fake := &echoLLM{plan: planFor(`{"query":"worker_health","params":{}}`)}
	d, owner, guest := privateAskServer(t, fake)
	if d.published() {
		t.Fatal("the fixture must be a private deployment")
	}
	if code, body := askDirect(t, d, guest, "worker health?"); code != 403 {
		t.Errorf("non-admin on a private box, flag off: %d %s", code, body)
	}
	if code, body := askDirect(t, d, owner, "worker health?"); code != 200 {
		t.Errorf("admin: %d %s", code, body)
	}
	d.Cfg.MemberCopilot = true
	if code, body := askDirect(t, d, guest, "worker health?"); code != 422 {
		t.Errorf("non-admin with the flag on asked an operator entry: %d %s", code, body)
	}
	rec := httptest.NewRecorder()
	d.askStatus(rec, withUser(httptest.NewRequest("GET", "/api/ask", nil), guest))
	if strings.Contains(rec.Body.String(), "worker_health") || !strings.Contains(rec.Body.String(), `"dailyLimit":20`) {
		t.Errorf("non-admin status must be the member catalog: %s", rec.Body)
	}
}

// TestAskMemberBudgets: one ask in flight per user; a global member pool per
// day; and members are refused once the LLM layer passes 70% of its cap. The
// operator is refused by none of these.
func TestAskMemberBudgets(t *testing.T) {
	ctx := context.Background()
	today := time.Now().UTC().Format("2006-01-02")
	fake := &statsLLM{echoLLM: echoLLM{plan: planFor(`{"query":"prereg_chain","params":{}}`)}}
	d, owner, guest := privateAskServer(t, fake)
	d.Cfg.MemberCopilot = true

	// Headroom: at 70% of the LLM cap members stop, the operator does not.
	fake.stats = llm.Stats{Day: today, DailyCap: 2000, Calls: 1400}
	if code, body := askDirect(t, d, guest, "vol record?"); code != 429 || !strings.Contains(body, "busy today") {
		t.Errorf("member at 70%% of the LLM cap: %d %s", code, body)
	}
	if code, _ := askDirect(t, d, owner, "vol record?"); code != 200 {
		t.Errorf("operator at 70%% of the LLM cap: %d", code)
	}
	fake.stats.Calls = 1399
	if code, body := askDirect(t, d, guest, "vol record?"); code != 200 {
		t.Errorf("member under 70%%: %d %s", code, body)
	}
	fake.stats = llm.Stats{Day: "2000-01-01", DailyCap: 2000, Calls: 1999} // yesterday's counter is not today's
	if code, body := askDirect(t, d, guest, "vol record?"); code != 200 {
		t.Errorf("member with a stale counter: %d %s", code, body)
	}
	if n, _ := d.St.AskCount(ctx, askMemberPoolUID, today); n != 2 {
		t.Errorf("member pool counted %d member asks, want 2 (the refusal is not one)", n)
	}

	// The pool: once spent, members stop and the operator does not.
	if err := d.St.SetMeta(ctx, "copilot_ask:"+today+":0", fmt.Sprint(askMemberPool)); err != nil {
		t.Fatal(err)
	}
	before, _ := d.St.AskCount(ctx, guest, today)
	if code, body := askDirect(t, d, guest, "vol record?"); code != 429 || !strings.Contains(body, "member questions are used up") {
		t.Errorf("member with the pool spent: %d %s", code, body)
	}
	if after, _ := d.St.AskCount(ctx, guest, today); after != before {
		t.Errorf("a pool refusal counted against the member: %d -> %d", before, after)
	}
	if code, _ := askDirect(t, d, owner, "vol record?"); code != 200 {
		t.Errorf("operator with the member pool spent: %d", code)
	}
	if err := d.St.SetMeta(ctx, "copilot_ask:"+today+":0", "0"); err != nil {
		t.Fatal(err)
	}

	// In flight: a second ask by the same user while one runs is refused;
	// another user is not blocked.
	fake.entered, fake.release = make(chan struct{}), make(chan struct{})
	done := make(chan int)
	go func() {
		code, _ := askDirect(t, d, guest, "vol record?")
		done <- code
	}()
	<-fake.entered
	if code, body := askDirect(t, d, guest, "again?"); code != 429 || !strings.Contains(body, "one question at a time") {
		t.Errorf("second ask in flight: %d %s", code, body)
	}
	go func() { <-fake.entered }() // the owner's ask passes the gate below
	go func() { fake.release <- struct{}{}; fake.release <- struct{}{} }()
	if code, _ := askDirect(t, d, owner, "vol record?"); code != 200 {
		t.Errorf("another user blocked by an ask in flight: %d", code)
	}
	if code := <-done; code != 200 {
		t.Errorf("the held ask: %d", code)
	}
	fake.entered, fake.release = nil, nil
	if code, _ := askDirect(t, d, guest, "after?"); code != 200 {
		t.Errorf("the in-flight slot was not released: %d", code)
	}
}
