package api

// REGIMES-SIZE (2026-10-02): a member's /api/regimes carries the slice its
// page asks for, never the 3.6 MB full payload; the operator keeps the full
// payload; rows are selected, never changed.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

func TestRegimesMemberViewIsBounded(t *testing.T) {
	ctx := t.Context()
	srv, st, mb, _ := newProductionServer(t, nil, writeRegistry(t, thinWindowRegistry))
	const n = 300
	now := time.Now().Unix()
	for i := 0; i < n; i++ {
		symbol := fmt.Sprintf("RM%03d", i)
		sid, err := st.UpsertSymbol(ctx, symbol, md.Stocks, "")
		if err != nil {
			t.Fatalf("UpsertSymbol %s: %v", symbol, err)
		}
		for _, k := range []structregime.Kind{
			structregime.KindTrend21,
			structregime.KindTrend63,
			structregime.KindLiquidity21,
			structregime.KindVol21,
		} {
			acc := 0.0
			if i%2 == 0 {
				acc = 0.8
			}
			f := structregime.Forecast{
				Kind:               k,
				HorizonDays:        21,
				Regime:             "uptrend",
				Conviction:         float64(i+1) / n,
				HistoricalAccuracy: acc,
				Tier:               "high",
				Rank:               float64(i+1) / n,
				N:                  100,
			}
			if err := st.UpsertRegimeForecast(ctx, sid.ID, now, f); err != nil {
				t.Fatalf("UpsertRegimeForecast %s %s: %v", symbol, k, err)
			}
		}
	}
	cryptoSym := "RMC/USD"
	cid, err := st.UpsertSymbol(ctx, cryptoSym, md.Crypto, "")
	if err != nil {
		t.Fatalf("UpsertSymbol %s: %v", cryptoSym, err)
	}
	cf := structregime.Forecast{
		Kind:               structregime.KindTrendCrypto21,
		HorizonDays:        21,
		Regime:             "uptrend",
		Conviction:         0.5,
		HistoricalAccuracy: 0.9,
		Tier:               "high",
		Rank:               0.5,
		N:                  100,
	}
	if err := st.UpsertRegimeForecast(ctx, cid.ID, now, cf); err != nil {
		t.Fatalf("UpsertRegimeForecast %s crypto: %v", cryptoSym, err)
	}
	member := signupVerified(t, srv, mb, "rmmember", "rmmember@gmail.com")
	owner := ownerClient(t, srv.URL)
	get := func(c *http.Client, path string) (string, map[string]any) {
		code, body := getAs(t, c, srv.URL+path)
		if code != 200 {
			if len(body) > 300 {
				body = body[:300]
			}
			t.Fatalf("GET %s: %d %s", path, code, body)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("JSON %s: %v", path, err)
		}
		return body, m
	}
	rows := func(m map[string]any, kind string) []map[string]any {
		if fc, ok := m["forecasts"].(map[string]any)[kind]; ok {
			if arr, ok := fc.([]any); ok {
				out := make([]map[string]any, len(arr))
				for i, v := range arr {
					if m, ok := v.(map[string]any); ok {
						out[i] = m
					}
				}
				return out
			}
		}
		return nil
	}
	// a) Owner
	bodyO, mO := get(owner, "/api/regimes")
	if len(bodyO) <= 300000 {
		t.Fatalf("Owner body too short: %d", len(bodyO))
	}
	for _, k := range []string{"trend21", "trend63", "liquidity21", "vol21"} {
		if got := len(rows(mO, k)); got != n {
			t.Fatalf("Owner /api/regimes %s: want %d rows, got %d", k, n, got)
		}
	}
	if got := len(rows(mO, "trend21-crypto")); got != 1 {
		t.Fatalf("Owner /api/regimes trend21-crypto: want 1 row, got %d", got)
	}
	if _, ok := mO["kindStats"]; ok {
		t.Fatalf("Owner /api/regimes: unexpected kindStats")
	}
	// b) Member
	bodyM, mM := get(member, "/api/regimes")
	if len(bodyM) >= 300000 {
		t.Fatalf("Member body too long: %d", len(bodyM))
	}
	if got := len(mM["forecasts"].(map[string]any)); got != 4 {
		t.Fatalf("Member /api/regimes: want 4 forecast kinds, got %d", got)
	}
	for _, k := range []string{"trend21", "trend63", "liquidity21", "vol21"} {
		r := rows(mM, k)
		if got := len(r); got != memberRegimesTop {
			t.Fatalf("Member /api/regimes %s: want %d rows, got %d", k, memberRegimesTop, got)
		}
		if got := len(r); got < 2 {
			t.Fatalf("Member /api/regimes %s: need at least 2 rows for symbol check", k)
		}
		if r[0]["symbol"] != "RM299" {
			t.Fatalf("Member /api/regimes %s: row0 symbol want RM299, got %v", k, r[0]["symbol"])
		}
		if r[1]["symbol"] != "RM298" {
			t.Fatalf("Member /api/regimes %s: row1 symbol want RM298, got %v", k, r[1]["symbol"])
		}
		for _, row := range r {
			if row["evidence"] != "backtest" {
				t.Fatalf("Member /api/regimes %s: row evidence want backtest, got %v", k, row["evidence"])
			}
			if caveat, ok := row["evidenceCaveat"].(string); !ok || caveat == "" {
				t.Fatalf("Member /api/regimes %s: row evidenceCaveat missing or not string", k)
			}
		}
		stats := mM["kindStats"].(map[string]any)[k].(map[string]any)
		if got := int(stats["count"].(float64)); got != n {
			t.Fatalf("Member /api/regimes %s kindStats count: want %d, got %d", k, n, got)
		}
		if got := int(stats["measured"].(float64)); got != n/2 {
			t.Fatalf("Member /api/regimes %s kindStats measured: want %d, got %d", k, n/2, got)
		}
		if got := stats["meanHistoricalAccuracy"].(float64); got < 0.8-1e-9 || got > 0.8+1e-9 {
			t.Fatalf("Member /api/regimes %s kindStats meanHistoricalAccuracy: want 0.8, got %v", k, got)
		}
	}
	// c) Member with symbols (the encoded space is trimmed)
	symPath := "/api/regimes?symbols=RM007,%20RM123"
	_, mSym := get(member, symPath)
	for _, k := range []string{"trend21", "trend63", "liquidity21", "vol21"} {
		r := rows(mSym, k)
		if got := len(r); got != 2 {
			t.Fatalf("Member %s %s: want 2 rows, got %d", symPath, k, got)
		}
		if r[0]["symbol"] != "RM123" {
			t.Fatalf("Member %s %s: row0 symbol want RM123, got %v", symPath, k, r[0]["symbol"])
		}
		if r[1]["symbol"] != "RM007" {
			t.Fatalf("Member %s %s: row1 symbol want RM007, got %v", symPath, k, r[1]["symbol"])
		}
		// The totals cover the whole kind, never the member's own list: a total
		// over a watchlist would depend on what else the member watches.
		stats := mSym["kindStats"].(map[string]any)[k].(map[string]any)
		if got := int(stats["count"].(float64)); got != n {
			t.Fatalf("Member %s %s kindStats count: want %d (the whole kind), got %d", symPath, k, n, got)
		}
	}
	// d) Member kind=trend21&offset=25&limit=10
	_, mOff := get(member, "/api/regimes?kind=trend21&offset=25&limit=10")
	if got := len(mOff["forecasts"].(map[string]any)); got != 1 {
		t.Fatalf("Member /api/regimes?kind=trend21&offset=25&limit=10: want 1 forecast kind, got %d", got)
	}
	trend := rows(mOff, "trend21")
	if got := len(trend); got != 10 {
		t.Fatalf("Member /api/regimes?kind=trend21&offset=25&limit=10: want 10 rows, got %d", got)
	}
	expected := []string{"RM274", "RM273", "RM272", "RM271", "RM270", "RM269", "RM268", "RM267", "RM266", "RM265"}
	for i, sym := range expected {
		if trend[i]["symbol"] != sym {
			t.Fatalf("Member /api/regimes?kind=trend21&offset=25&limit=10: row %d symbol want %s, got %v", i, sym, trend[i]["symbol"])
		}
	}
	stats := mOff["kindStats"].(map[string]any)["trend21"].(map[string]any)
	if got := int(stats["count"].(float64)); got != n {
		t.Fatalf("Member /api/regimes?kind=trend21&offset=25&limit=10 kindStats count: want %d, got %d", n, got)
	}
	// e) Member kind=trend21&limit=100000
	_, mLim := get(member, "/api/regimes?kind=trend21&limit=100000")
	trendLim := rows(mLim, "trend21")
	if got := len(trendLim); got != memberRegimesMaxLimit {
		t.Fatalf("Member /api/regimes?kind=trend21&limit=100000: want %d rows, got %d", memberRegimesMaxLimit, got)
	}
	// f) Member kind=trend21&offset=100000
	bodyOffBig, mOffBig := get(member, "/api/regimes?kind=trend21&offset=100000")
	if !strings.Contains(bodyOffBig, `"trend21":[]`) {
		t.Fatalf(`Member /api/regimes?kind=trend21&offset=100000: body does not contain "trend21":[]`)
	}
	statsOffBig := mOffBig["kindStats"].(map[string]any)["trend21"].(map[string]any)
	if got := int(statsOffBig["count"].(float64)); got != n {
		t.Fatalf("Member /api/regimes?kind=trend21&offset=100000 kindStats count: want %d, got %d", n, got)
	}
}

func TestMemberRegimesView_TypedAndPersistedShapes(t *testing.T) {
	// Build typed shape
	fA := store.RegimeForecast{Symbol: "A", Market: string(md.Stocks)}
	fA.HistoricalAccuracy = 0.9
	fB := store.RegimeForecast{Symbol: "B", Market: string(md.Stocks)}
	fB.HistoricalAccuracy = 0.0
	fC := store.RegimeForecast{Symbol: "C", Market: string(md.Stocks)}
	fC.HistoricalAccuracy = 0.7
	fBvol := store.RegimeForecast{Symbol: "B", Market: string(md.Stocks)}
	fBvol.HistoricalAccuracy = 0.6
	typed := map[string]any{
		"forecasts": map[string][]any{
			"trend21": {fA, fB, fC},
			"vol21":   {fBvol},
		},
		"methodology": "m",
	}
	b, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("marshal typed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal typed: %v", err)
	}
	// Test both shapes: typed and persisted (decoded)
	for name, shape := range map[string]map[string]any{
		"typed":     typed,
		"persisted": decoded,
	} {
		// Apply withoutCryptoForecasts (no crypto in shape, so it's a copy)
		filtered := withoutCryptoForecasts(shape)
		// Call memberRegimesView with symbols=B,C
		q := url.Values{"symbols": {"B, C"}}
		out := memberRegimesView(filtered, q)
		// Marshal and unmarshal to normalize
		outb, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal out %s: %v", name, err)
		}
		var outm map[string]any
		if err := json.Unmarshal(outb, &outm); err != nil {
			t.Fatalf("unmarshal out %s: %v", name, err)
		}
		// Check trend21 rows: should be B then C
		trend := outm["forecasts"].(map[string]any)["trend21"].([]any)
		if len(trend) != 2 {
			t.Fatalf("%s: trend21 rows want 2, got %d", name, len(trend))
		}
		if trend[0].(map[string]any)["symbol"] != "B" {
			t.Fatalf("%s: trend21 row0 symbol want B, got %v", name, trend[0].(map[string]any)["symbol"])
		}
		if trend[1].(map[string]any)["symbol"] != "C" {
			t.Fatalf("%s: trend21 row1 symbol want C, got %v", name, trend[1].(map[string]any)["symbol"])
		}
		// Check vol21 rows: should be B only
		vol := outm["forecasts"].(map[string]any)["vol21"].([]any)
		if len(vol) != 1 {
			t.Fatalf("%s: vol21 rows want 1, got %d", name, len(vol))
		}
		if vol[0].(map[string]any)["symbol"] != "B" {
			t.Fatalf("%s: vol21 row0 symbol want B, got %v", name, vol[0].(map[string]any)["symbol"])
		}
		// Check kindStats for trend21
		stats := outm["kindStats"].(map[string]any)["trend21"].(map[string]any)
		// The whole kind (A, B, C), not the symbols asked for.
		if got := int(stats["count"].(float64)); got != 3 {
			t.Fatalf("%s: kindStats trend21 count want 3, got %d", name, got)
		}
		if got := int(stats["measured"].(float64)); got != 2 {
			t.Fatalf("%s: kindStats trend21 measured want 2, got %d", name, got)
		}
		if got := stats["meanHistoricalAccuracy"].(float64); got < 0.8-1e-9 || got > 0.8+1e-9 {
			t.Fatalf("%s: kindStats trend21 meanHistoricalAccuracy want 0.8, got %v", name, got)
		}
		// Check methodology
		if outm["methodology"] != "m" {
			t.Fatalf("%s: methodology want m, got %v", name, outm["methodology"])
		}
	}
	// Check that typed input's forecasts["trend21"] still has 3 elements
	if got := len(typed["forecasts"].(map[string][]any)["trend21"]); got != 3 {
		t.Fatalf("the shared input was modified: trend21 has %d rows, want 3", got)
	}
}
