package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func vrcDay(m time.Month, d int) int64 { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC).Unix() }
func vrcAt(m time.Month, d, hh, mm int) int64 {
	return time.Date(2026, m, d, hh, mm, 0, 0, time.UTC).Unix()
}
func vrcKey(sym int64, h string, ts int64) string { return fmt.Sprintf("%d|%s|%d", sym, h, ts) }

type vrcFixture struct{ btc, eth, sol, xrp, aapl int64 }

type vrcCase struct {
	name   string
	sym    func(vrcFixture) int64
	ts     int64
	void   []string // horizons the resolver can never settle
	settle []string // open horizons it can
}

// The fixture's forecast groups, each hand-derived from the resolver's rules.
// BTC bars: 08-25..10-12 daily, hole 09-14..09-18. ETH: 09-28..10-11, hole 10-06.
// XRP: 09-01..09-15. SOL: no bars (nothing to date its exit from).
var vrcCases = []vrcCase{
	{"R1 pre-settled-base rule", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.September, 1, 12, 0), nil, []string{"1d", "1d#pm", "1w", "1w#pm"}},
	{"R2 forming base steps back; 1w forward bar after the hole", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.September, 10, 0, 30), nil, []string{"1d", "1d#pm", "1w", "1w#pm"}},
	{"R3 1d forward bar across the hole (gap guard)", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.September, 14, 12, 0), []string{"1d", "1d#pm"}, []string{"1w", "1w#pm"}},
	{"R4 1w has no bar at/after ts+1w", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.October, 5, 0, 10), []string{"1w", "1w#pm"}, []string{"1d", "1d#pm"}},
	{"R5 forward bar is the last stored bar / none", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.October, 11, 0, 10), []string{"1d", "1d#pm", "1w", "1w#pm"}, nil},
	{"R6 1d settles on the last bar; 1w cannot", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.October, 10, 0, 10), []string{"1w", "1w#pm"}, []string{"1d", "1d#pm"}},
	{"R7 issue-day bar missing (label base)", func(f vrcFixture) int64 { return f.eth }, vrcAt(time.October, 6, 0, 10), []string{"1d", "1d#pm", "1w", "1w#pm"}, nil},
	{"R8 between cutoffs, 1w forward bar after the hole", func(f vrcFixture) int64 { return f.eth }, vrcAt(time.October, 1, 0, 10), nil, []string{"1d", "1d#pm", "1w", "1w#pm"}},
	{"R9 first bar unsettled at issue, nothing before it", func(f vrcFixture) int64 { return f.eth }, vrcAt(time.September, 28, 6, 0), []string{"1d", "1d#pm", "1w", "1w#pm"}, nil},
	{"R12 1d has forward and successor bars but none at/after ts+1d (queue gate only)", func(f vrcFixture) int64 { return f.xrp }, vrcAt(time.September, 14, 12, 0), []string{"1d", "1d#pm", "1w", "1w#pm"}, nil},
	{"R11 1d forward bar is the last bar, no successor (settlement guard only)", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.October, 11, 0, 0), []string{"1d", "1d#pm", "1w", "1w#pm"}, nil},
	{"R10 1d already graded; twin and 1w open", func(f vrcFixture) int64 { return f.btc }, vrcAt(time.September, 20, 12, 0), nil, []string{"1d#pm", "1w", "1w#pm"}},
}

func vrcExpected(f vrcFixture) (void, settle map[string]bool) {
	void, settle = map[string]bool{}, map[string]bool{}
	for _, c := range vrcCases {
		for _, h := range c.void {
			void[vrcKey(c.sym(f), h, c.ts)] = true
		}
		for _, h := range c.settle {
			settle[vrcKey(c.sym(f), h, c.ts)] = true
		}
	}
	return void, settle
}

func vrcSeed(t *testing.T, path string) vrcFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sym := func(s string, m md.Market) int64 {
		v, err := st.UpsertSymbol(ctx, s, m, "")
		if err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	f := vrcFixture{btc: sym("BTC/USD", md.Crypto), eth: sym("ETH/USD", md.Crypto), sol: sym("SOL/USD", md.Crypto), xrp: sym("XRP/USD", md.Crypto), aapl: sym("AAPL", md.Stocks)}
	bars := func(id int64, from, to time.Time, hour int, skip func(time.Time) bool) {
		var bs []md.Bar
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			if skip(d) {
				continue
			}
			px := float64(100 + len(bs))
			bs = append(bs, md.Bar{SymbolID: id, TF: md.TF1d, Ts: d.Add(time.Duration(hour) * time.Hour).Unix(), Open: px, High: px, Low: px, Close: px, Volume: 1000})
		}
		if err := st.UpsertBars(ctx, bs); err != nil {
			t.Fatal(err)
		}
	}
	utc := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	bars(f.btc, utc(time.August, 25), utc(time.October, 12), 0, func(d time.Time) bool {
		return d.Month() == time.September && d.Day() >= 14 && d.Day() <= 18
	})
	bars(f.xrp, utc(time.September, 1), utc(time.September, 15), 0, func(time.Time) bool { return false })
	bars(f.eth, utc(time.September, 28), utc(time.October, 11), 0, func(d time.Time) bool {
		return d.Month() == time.October && d.Day() == 6
	})
	bars(f.aapl, utc(time.September, 1), utc(time.October, 9), 4, func(d time.Time) bool {
		return d.Weekday() == time.Saturday || d.Weekday() == time.Sunday
	})
	group := func(id, ts int64) {
		for _, h := range []md.Horizon{md.H1d, md.H1w} {
			if err := st.UpsertPrediction(ctx, store.Prediction{SymbolID: id, Horizon: h, Ts: ts, RawProb: 0.6, CalProb: 0.6, NUsed: 1, Components: "{}"}); err != nil {
				t.Fatal(err)
			}
			if err := st.SeedBenchmarkOutcome(ctx, id, h+"#pm", ts, 0.6); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range vrcCases {
		group(c.sym(f), c.ts)
	}
	r10 := vrcCases[len(vrcCases)-1].ts
	if err := st.ResolvePrediction(ctx, f.btc, md.H1d, r10, 0.01); err != nil {
		t.Fatal(err)
	}
	group(f.aapl, vrcAt(time.October, 8, 15, 0))
	if _, err := st.AppendLedger(ctx, store.LedgerEntry{PredictedAt: vrcCases[0].ts, SymbolID: f.btc, Horizon: md.H1d,
		BarTs: vrcDay(time.September, 1), RawProb: 0.6, CalProb: 0.6, FeatureHash: "fixture", ModelVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db := vrcOpenRaw(t, path)
	if _, err := db.Exec(`INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime, conviction, historical_accuracy, rank, naive_label)
		VALUES (?, 'trend21-crypto', ?, ?, 21, 'up', 0.9, 0.9, 1, 'up')`, f.btc, vrcAt(time.October, 5, 0, 10), vrcDay(time.October, 5)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return f
}

func vrcOpenRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func vrcFileHash(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// vrcDump renders a query's full result so a table can be compared before/after.
func vrcDump(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&b, vals)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func vrcOutcome(t *testing.T, db *sql.DB, key string) (resolved, up sql.NullInt64, fwd sql.NullFloat64, settle sql.NullInt64) {
	t.Helper()
	var sym, ts int64
	var h string
	parts := strings.Split(key, "|")
	if _, err := fmt.Sscan(parts[0], &sym); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscan(parts[2], &ts); err != nil {
		t.Fatal(err)
	}
	h = parts[1]
	if err := db.QueryRow(`SELECT resolved_at, up, fwd_return, settle_ts FROM prediction_outcomes WHERE symbol_id=? AND horizon=? AND ts=?`,
		sym, h, ts).Scan(&resolved, &up, &fwd, &settle); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return
}

func vrcSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The mirror in vrcSettleable must agree with pipeline.PredictionResolver: the
// mirror classifies the fixture, then the REAL resolver runs on the same rows
// and must grade exactly the settleable ones and none of the void ones.
func TestVoidRetiredCryptoMirrorsResolver(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mirror.db")
	f := vrcSeed(t, path)
	void, settle := vrcExpected(f)

	plan, n, err := vrcRun(ctx, path, false, "", time.Now().Unix(), io.Discard)
	if err != nil || n != 0 {
		t.Fatalf("dry run: n=%d err=%v", n, err)
	}
	got := map[string]bool{}
	for _, r := range plan.void {
		got[vrcKey(r.sym, r.horizon, r.ts)] = true
	}
	for k := range void {
		if !got[k] {
			t.Errorf("mirror left %s open; the resolver can never settle it", k)
		}
	}
	for k := range got {
		if !void[k] {
			t.Errorf("mirror would void %s, which is not in the expected void set", k)
		}
	}
	if plan.settleable != len(settle) {
		t.Errorf("settleable = %d, want %d", plan.settleable, len(settle))
	}
	if s := vrcByHorizon(plan.byHorizon); s != "1d=6,1d#pm=6,1w=7,1w#pm=7" {
		t.Errorf("by_horizon = %s", s)
	}

	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := (&pipeline.PredictionResolver{St: st}).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db := vrcOpenRaw(t, path)
	for _, k := range vrcSorted(settle) {
		if _, up, _, _ := vrcOutcome(t, db, k); !up.Valid {
			t.Errorf("resolver did not grade %s, which the mirror calls settleable (resolver: %s)", k, out)
		}
	}
	for _, k := range vrcSorted(void) {
		if resolved, _, _, _ := vrcOutcome(t, db, k); resolved.Valid {
			t.Errorf("resolver graded %s, which the mirror would void (resolver: %s)", k, out)
		}
	}
}

func TestVoidRetiredCryptoApply(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "apply.db")
	f := vrcSeed(t, path)
	void, settle := vrcExpected(f)
	now := vrcAt(time.October, 21, 12, 0)

	const (
		qLedger = "SELECT seq, predicted_at, symbol_id, horizon, bar_ts, raw_prob, cal_prob, feature_hash, model_version, prev_hash, entry_hash FROM prediction_ledger ORDER BY seq"
		qPreds  = "SELECT symbol_id, horizon, ts, raw_prob, cal_prob, n_used FROM predictions ORDER BY 1,2,3"
		qStocks = "SELECT o.symbol_id, o.horizon, o.ts, o.prob, o.up, o.fwd_return, o.resolved_at FROM prediction_outcomes o JOIN symbols s ON s.id=o.symbol_id WHERE s.market='stocks' ORDER BY 1,2,3"
	)
	db := vrcOpenRaw(t, path)
	ledger, preds, stocks := vrcDump(t, db, qLedger), vrcDump(t, db, qPreds), vrcDump(t, db, qStocks)
	var dqCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dq_events`).Scan(&dqCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// (a) Dry run: writes nothing, and its handle cannot write.
	h0 := vrcFileHash(t, path)
	var buf bytes.Buffer
	if _, n, err := vrcRun(ctx, path, false, "", now, &buf); err != nil || n != 0 {
		t.Fatalf("dry run: n=%d err=%v", n, err)
	}
	if h := vrcFileHash(t, path); h != h0 {
		t.Fatal("dry run changed the database file")
	}
	for _, want := range []string{
		"VOID-RETIRED-CRYPTO dry_run=true voided=26 by_horizon=1d=6,1d#pm=6,1w=7,1w#pm=7 settleable_unresolved=21 regime_open=1 delisted_stamped=3 active_crypto=4",
		"DRY RUN: nothing written",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("dry-run output lacks %q:\n%s", want, buf.String())
		}
	}
	ro, err := vrcOpen(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Exec(`INSERT INTO dq_events (ts, kind, detail) VALUES (1, 'x', 'y')`); err == nil {
		t.Fatal("the dry-run handle accepted a write; it must open the database read-only")
	}
	_ = ro.Close()

	// (b) -apply without -log refuses before touching the database.
	if _, _, err := vrcRun(ctx, path, true, "", now, io.Discard); err == nil || !strings.Contains(err.Error(), "-log") {
		t.Fatalf("apply without -log: err=%v", err)
	}
	// (c) -apply refuses while crypto is still active (the paused build is not live).
	logPath := filepath.Join(dir, "void.csv")
	if _, n, err := vrcRun(ctx, path, true, logPath, now, io.Discard); err == nil || n != 0 || !strings.Contains(err.Error(), "still active") {
		t.Fatalf("apply while active: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("refused apply created the log: %v", err)
	}
	if h := vrcFileHash(t, path); h != h0 {
		t.Fatal("a refused apply changed the database file")
	}

	// (d) Pause crypto the way cmd/signaldeckd pauseCrypto does.
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{f.btc, f.eth, f.sol, f.xrp} {
		if err := st.SetSymbolActive(ctx, id, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// (e) Apply.
	buf.Reset()
	if _, n, err := vrcRun(ctx, path, true, logPath, now, &buf); err != nil || n != 26 {
		t.Fatalf("apply: n=%d err=%v\n%s", n, err, buf.String())
	}
	for _, want := range []string{"VOID-RETIRED-CRYPTO dry_run=false voided=26", "delisted_stamped=3"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("apply output lacks %q:\n%s", want, buf.String())
		}
	}
	db2 := vrcOpenRaw(t, path)
	for _, k := range vrcSorted(void) {
		resolved, up, fwd, stl := vrcOutcome(t, db2, k)
		if !resolved.Valid || resolved.Int64 != now || up.Valid || fwd.Valid || stl.Valid {
			t.Errorf("%s: want a void (resolved_at=%d, up/fwd/settle NULL), got resolved=%v up=%v fwd=%v settle=%v", k, now, resolved, up, fwd, stl)
		}
	}
	for _, k := range vrcSorted(settle) {
		if resolved, _, _, _ := vrcOutcome(t, db2, k); resolved.Valid {
			t.Errorf("%s is settleable and must stay open for the resolver", k)
		}
	}
	r10 := vrcCases[len(vrcCases)-1].ts
	if _, up, _, _ := vrcOutcome(t, db2, vrcKey(f.btc, "1d", r10)); !up.Valid {
		t.Error("the already graded R10 1d row lost its label")
	}
	if got := vrcDump(t, db2, qStocks); got != stocks {
		t.Errorf("stock outcomes changed:\nbefore %s\nafter  %s", stocks, got)
	}
	if got := vrcDump(t, db2, qPreds); got != preds {
		t.Error("predictions changed")
	}
	if got := vrcDump(t, db2, qLedger); got != ledger {
		t.Error("prediction_ledger changed")
	}
	for id, want := range map[int64]sql.NullInt64{
		f.btc: {Int64: vrcDay(time.October, 12), Valid: true},
		f.eth: {Int64: vrcDay(time.October, 11), Valid: true},
		f.xrp: {Int64: vrcDay(time.September, 15), Valid: true},
		f.sol: {},
	} {
		var got sql.NullInt64
		if err := db2.QueryRow(`SELECT delisted_at FROM symbols WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("symbol %d delisted_at = %v, want %v", id, got, want)
		}
	}
	var n2 int
	var kind, detail string
	if err := db2.QueryRow(`SELECT COUNT(*) FROM dq_events`).Scan(&n2); err != nil {
		t.Fatal(err)
	}
	if err := db2.QueryRow(`SELECT kind, detail FROM dq_events ORDER BY id DESC LIMIT 1`).Scan(&kind, &detail); err != nil {
		t.Fatal(err)
	}
	if n2 != dqCount+1 || kind != "predictions_voided_source_retired" || !strings.Contains(detail, "source_retired") || !strings.Contains(detail, logPath) {
		t.Errorf("dq_events: count %d (want %d), last %q %q", n2, dqCount+1, kind, detail)
	}
	lf, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(lf).ReadAll()
	_ = lf.Close()
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]int{}
	for _, r := range recs[1:] {
		tables[r[0]]++
	}
	if len(recs) != 1+26+3 || recs[0][0] != "table" || tables["prediction_outcomes"] != 26 || tables["symbols"] != 3 {
		t.Errorf("log: %d records, header %v, by table %v", len(recs), recs[0], tables)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := st.VerifyLedger(ctx)
	if err != nil || !v.Intact {
		t.Errorf("VerifyLedger after apply: %+v err=%v", v, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// (f) Idempotent: a second apply finds nothing and writes nothing.
	log2 := filepath.Join(dir, "void2.csv")
	buf.Reset()
	if _, n, err := vrcRun(ctx, path, true, log2, now+60, &buf); err != nil || n != 0 || !strings.Contains(buf.String(), "dry_run=false voided=0") {
		t.Fatalf("second apply: n=%d err=%v\n%s", n, err, buf.String())
	}
	if _, err := os.Stat(log2); !os.IsNotExist(err) {
		t.Fatalf("second apply created a log: %v", err)
	}
	if err := db2.QueryRow(`SELECT COUNT(*) FROM dq_events`).Scan(&n2); err != nil {
		t.Fatal(err)
	}
	if n2 != dqCount+1 {
		t.Errorf("second apply wrote a dq_events row: %d, want %d", n2, dqCount+1)
	}
}
