package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/memberjournal"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

type journalView struct {
	Calls []map[string]any    `json:"calls"`
	Stats memberjournal.Stats `json:"stats"`
	Caps  journalCaps         `json:"caps"`
}

func journalOf(t *testing.T, c *http.Client, base string) (journalView, string) {
	t.Helper()
	code, body := getAs(t, c, base+"/api/journal")
	if code != 200 {
		t.Fatalf("GET /api/journal: %d %s", code, body)
	}
	var v journalView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	return v, body
}

func TestJournalMemberFlow(t *testing.T) {
	ctx := context.Background()
	srv, st, mb := startPublished(t, nil, true, func(d Deps) http.Handler {
		lim := newRateLimiter(d.Cfg.RateRPS, d.Cfg.RateBurst)
		return d.httpServerWith(d.routes(lim), lim).Handler
	})
	base := srv.URL
	mira := signupVerified(t, srv, mb, "mira", "mira@gmail.com")
	nils := signupVerified(t, srv, mb, "nils", "nils@gmail.com")
	uid := func(name string) int64 {
		u, _, err := st.GetUserByName(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	sym := func(s string, m md.Market) md.Symbol {
		v, err := st.UpsertSymbol(ctx, s, m, s+" Inc")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	aapl := sym("AAPL", md.Stocks)
	// The journal takes a symbol only once it has daily data (what the picker
	// offers): one settled bar long before any call made here.
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: aapl.ID, TF: md.TF1d, Ts: 1_699_920_000,
		Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}}); err != nil {
		t.Fatal(err)
	}
	sym("BTC/USD", md.Crypto)
	post := func(c *http.Client, path string, body any) (int, string) {
		t.Helper()
		resp := postJSON(t, c, base+path, body)
		return resp.StatusCode, drain(t, resp)
	}
	call := func(over map[string]any) map[string]any {
		b := map[string]any{"symbol": "aapl", "market": "stocks", "call": "up", "horizon": 5, "note": "earnings run-up"}
		for k, v := range over {
			b[k] = v
		}
		return b
	}

	// Anonymous: no journal at all.
	if code, _ := getAs(t, newClient(t), base+"/api/journal"); code != http.StatusUnauthorized {
		t.Errorf("anonymous GET /api/journal: %d, want 401", code)
	}

	// Refusals: crypto, other markets, bad call/horizon, long note, unknown symbol.
	for _, c := range []struct {
		body map[string]any
		code int
		msg  string
	}{
		{call(map[string]any{"symbol": "BTC/USD", "market": "crypto"}), 400, cryptoNotForMembers},
		{call(map[string]any{"market": "futures"}), 400, "US stocks and ETFs only"},
		{call(map[string]any{"call": "sideways"}), 400, "call must be"},
		{call(map[string]any{"horizon": 10}), 400, "1, 5 or 21"},
		{call(map[string]any{"note": strings.Repeat("é", 281)}), 400, "280 characters"},
		{call(map[string]any{"symbol": "NOPE"}), 404, "unknown symbol"},
	} {
		if code, body := post(mira, "/api/journal", c.body); code != c.code || !strings.Contains(body, c.msg) {
			t.Errorf("POST %v: %d %s, want %d %q", c.body, code, body, c.code, c.msg)
		}
	}

	// A call: stored with its note, the server's timestamp, no prices.
	before := time.Now().Unix()
	code, body := post(mira, "/api/journal", call(map[string]any{"note": strings.Repeat("é", 280)}))
	if code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	v, _ := journalOf(t, mira, base)
	if len(v.Calls) != 1 {
		t.Fatalf("calls after one create: %+v", v.Calls)
	}
	row := v.Calls[0]
	if row["symbol"] != "AAPL" || row["call"] != "up" || row["horizon"] != float64(5) || row["status"] != "open" ||
		row["canWithdraw"] != true || row["createdTs"].(float64) < float64(before) {
		t.Errorf("row: %+v", row)
	}
	for k := range row {
		switch k {
		case "id", "symbol", "market", "call", "horizon", "note", "createdTs", "status", "outcome", "resolveDate", "canWithdraw":
		default:
			t.Errorf("journal row carries field %q: the licence allows only the grade", k)
		}
	}
	if v.Caps.TodayLeft != 9 || v.Caps.OpenLeft != 49 || !v.Stats.Withheld || v.Stats.Open != 1 || v.Stats.MinN != 30 {
		t.Errorf("caps/stats: %+v %+v", v.Caps, v.Stats)
	}
	miraCall := int64(row["id"].(float64))

	// Another member sees none of it and cannot withdraw it (IDOR): the
	// answer is the same 404 as for a call that does not exist.
	if nv, nb := journalOf(t, nils, base); len(nv.Calls) != 0 || strings.Contains(nb, "AAPL") {
		t.Errorf("nils sees mira's journal: %s", nb)
	}
	if code, body := post(nils, "/api/journal/withdraw", map[string]any{"id": miraCall}); code != 404 || !strings.Contains(body, "no such call") {
		t.Errorf("nils withdrawing mira's call: %d %s, want 404", code, body)
	}
	if code, body := post(nils, "/api/journal/withdraw", map[string]any{"id": 999999}); code != 404 || !strings.Contains(body, "no such call") {
		t.Errorf("withdrawing a missing call: %d %s", code, body)
	}
	if v, _ := journalOf(t, mira, base); v.Calls[0]["status"] != "open" {
		t.Fatalf("mira's call after nils' attempt: %+v", v.Calls[0])
	}

	// Withdraw: allowed before the entry bar, then final.
	if code, body := post(mira, "/api/journal/withdraw", map[string]any{"id": miraCall}); code != 200 || !strings.Contains(body, `"status":"withdrawn"`) {
		t.Fatalf("withdraw: %d %s", code, body)
	}
	if code, _ := post(mira, "/api/journal/withdraw", map[string]any{"id": miraCall}); code != http.StatusConflict {
		t.Errorf("second withdraw: %d, want 409", code)
	}
	// Once the entry session has a bar, a new call cannot be withdrawn.
	if code, body := post(mira, "/api/journal", call(nil)); code != 200 {
		t.Fatalf("create 2: %d %s", code, body)
	}
	v, _ = journalOf(t, mira, base)
	second := v.Calls[0]
	c2, _, err := st.MemberCall(ctx, uid("mira"), int64(second["id"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	entryMid := marketcal.SessionDate(time.Unix(c2.EntryTs, 0)).Unix()
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: aapl.ID, TF: md.TF1d, Ts: entryMid, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1}}); err != nil {
		t.Fatal(err)
	}
	if code, body := post(mira, "/api/journal/withdraw", map[string]any{"id": c2.ID}); code != http.StatusConflict {
		t.Errorf("withdraw after the entry bar: %d %s, want 409", code, body)
	}

	// Daily cap: 10 calls per ET day (two made, one withdrawn: still two made).
	for i := 0; i < 8; i++ {
		if code, body := post(mira, "/api/journal", call(nil)); code != 200 {
			t.Fatalf("call %d: %d %s", i+3, code, body)
		}
	}
	if code, body := post(mira, "/api/journal", call(nil)); code != http.StatusTooManyRequests || !strings.Contains(body, "10 calls today") {
		t.Errorf("11th call today: %d %s, want 429", code, body)
	}
	if v, _ := journalOf(t, mira, base); v.Caps.TodayLeft != 0 || v.Caps.OpenLeft != 41 {
		t.Errorf("caps after 10: %+v", v.Caps)
	}

	// Open cap: 50 open at once (seeded on earlier days so the daily cap is not what bites).
	yesterday := marketcal.SessionDate(time.Now()).Unix() - 3*86400
	for i := 0; i < store.MemberCallsOpen; i++ {
		created := yesterday - int64(4-i/10)*86400 // ascending: ten per earlier day
		if _, err := st.InsertMemberCall(ctx, store.MemberCall{UserID: uid("nils"), SymbolID: aapl.ID, Market: "stocks",
			Call: "down", Horizon: 21, CreatedTs: created, EntryTs: time.Now().Unix() + 86400,
			ExitDueTs: time.Now().Unix() + 40*86400}, created); err != nil {
			t.Fatal(err)
		}
	}
	if code, body := post(nils, "/api/journal", call(nil)); code != http.StatusTooManyRequests || !strings.Contains(body, "50 open calls") {
		t.Errorf("51st open call: %d %s, want 429", code, body)
	}
}

// TestJournalNeverServesPrices: a resolved call graded from bars whose closes
// are distinctive serves its grade and none of the closes or the return.
func TestJournalNeverServesPrices(t *testing.T) {
	ctx := context.Background()
	base, st, member, _ := newAlertPrefsServer(t, nil, false)
	u, _, err := st.GetUserByName(ctx, "mira")
	if err != nil {
		t.Fatal(err)
	}
	s, err := st.UpsertSymbol(ctx, "SNTJ", md.Stocks, "Journal Sentinel")
	if err != nil {
		t.Fatal(err)
	}
	seedResolvedJournalCall(t, st, u.ID, s.ID)
	_, body := journalOf(t, member, base)
	if !strings.Contains(body, `"status":"resolved","outcome":"hit"`) {
		t.Fatalf("no resolved call to scan: %s", body)
	}
	if l := leaks(body, vendorSentinels()); len(l) > 0 {
		t.Errorf("journal leaks vendor values: %v\n%s", l, body)
	}
}

// seedResolvedJournalCall puts sentinel-priced daily bars on three
// consecutive past sessions of symbolID (closes sntC1, sntC2, sntC2: the
// third settles the second), files an up call for uid that enters on the
// first close and exits on the second, and runs member-call-resolver, which
// grades it a hit. It returns the call's id.
func seedResolvedJournalCall(t *testing.T, st *store.Store, uid, symbolID int64) int64 {
	t.Helper()
	ctx := context.Background()
	var sessions []time.Time
	for d := marketcal.SessionDate(time.Now()).AddDate(0, 0, -30); len(sessions) < 3; d = d.AddDate(0, 0, 1) {
		if marketcal.IsTradingDay(d) {
			sessions = append(sessions, d)
		}
	}
	bars := []md.Bar{
		{SymbolID: symbolID, TF: md.TF1d, Ts: sessions[0].Unix(), Open: sntO1, High: sntH1, Low: sntL1, Close: sntC1, Volume: sntV1},
		{SymbolID: symbolID, TF: md.TF1d, Ts: sessions[1].Unix(), Open: sntO2, High: sntH2, Low: sntL2, Close: sntC2, Volume: sntV2},
		{SymbolID: symbolID, TF: md.TF1d, Ts: sessions[2].Unix(), Open: sntO2, High: sntH2, Low: sntL2, Close: sntC2, Volume: sntV2},
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}
	created := sessions[0].Add(10 * time.Hour) // 10:00 ET on the first session
	entry, exit := memberjournal.Schedule(created, 1)
	id, err := st.InsertMemberCall(ctx, store.MemberCall{UserID: uid, SymbolID: symbolID, Market: "stocks", Call: "up",
		Horizon: 1, Note: "seeded", CreatedTs: created.Unix(), EntryTs: entry.Unix(), ExitDueTs: exit.Unix()},
		sessions[0].Unix())
	if err != nil {
		t.Fatal(err)
	}
	r := &memberjournal.Resolver{St: st, Now: func() time.Time { return sessions[2].Add(18 * time.Hour) }}
	if _, err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}
