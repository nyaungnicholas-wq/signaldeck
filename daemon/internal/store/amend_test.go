package store

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestSupersedeAmendedForm4s(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "Zed")
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) int64 {
		tm, _ := time.Parse("2006-01-02", s)
		return tm.Unix()
	}

	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{
		Accession: "o1", SymbolID: sym.ID, Insider: "DOE JOHN", Code: "P",
		Shares: 100, Price: 10, Value: 1000,
		TxTs: day("2026-03-01"), FiledTs: day("2026-03-03") + 20*3600,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{
		Accession: "o2", SymbolID: sym.ID, Insider: "ROE JANE", Code: "S",
		Shares: 50, Price: 10, Value: 500,
		TxTs: day("2026-03-01"), FiledTs: day("2026-03-03") + 15*3600,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{
		Accession: "a1", SymbolID: sym.ID, Insider: "Doe John", Code: "P",
		Shares: 120, Price: 10, Value: 1200,
		TxTs: day("2026-03-01"), FiledTs: day("2026-03-10") + 15*3600,
		AmendsDay: "2026-03-03",
	}); err != nil {
		t.Fatal(err)
	}

	n, amb, err := st.SupersedeAmendedForm4s(ctx, sym.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("first supersede: got n=%d want 1", n)
	}
	if len(amb) != 0 {
		t.Fatalf("first supersede: got amb=%v want empty", amb)
	}

	n, amb, err = st.SupersedeAmendedForm4s(ctx, sym.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second supersede: got n=%d want 0", n)
	}
	if len(amb) != 0 {
		t.Fatalf("second supersede: got amb=%v want empty", amb)
	}

	rows, err := st.InsiderTrades(ctx, sym.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	accSet := make(map[string]bool)
	for _, r := range rows {
		accSet[r.Accession] = true
	}
	wantAcc := map[string]bool{"a1": true, "o2": true}
	if len(accSet) != len(wantAcc) {
		t.Fatalf("InsiderTrades: got %v want %v", accSet, wantAcc)
	}
	for k := range wantAcc {
		if !accSet[k] {
			t.Fatalf("InsiderTrades: missing %s", k)
		}
	}

	buys, sells, nb, ns, err := st.InsiderNetActivity(ctx, sym.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if buys != 1200 {
		t.Fatalf("buys got %f want 1200", buys)
	}
	if nb != 1 {
		t.Fatalf("nb got %d want 1", nb)
	}
	if sells != 500 {
		t.Fatalf("sells got %f want 500", sells)
	}
	if ns != 1 {
		t.Fatalf("ns got %d want 1", ns)
	}
}

func TestSupersedeAmendedForm4sAmbiguous(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "Zed")
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) int64 {
		tm, _ := time.Parse("2006-01-02", s)
		return tm.Unix()
	}

	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{
		Accession: "o1", SymbolID: sym.ID, Insider: "DOE JOHN", Code: "P",
		Shares: 100, Price: 10, Value: 100,
		TxTs: day("2026-03-01"), FiledTs: day("2026-03-03") + 14*3600,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{
		Accession: "o2", SymbolID: sym.ID, Insider: "DOE JOHN", Code: "P",
		Shares: 100, Price: 10, Value: 100,
		TxTs: day("2026-03-01"), FiledTs: day("2026-03-03") + 16*3600,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertInsiderTrade(ctx, InsiderTradeRow{
		Accession: "a1", SymbolID: sym.ID, Insider: "DOE JOHN", Code: "P",
		Shares: 100, Price: 10, Value: 100,
		TxTs: day("2026-03-01"), FiledTs: day("2026-03-12"),
		AmendsDay: "2026-03-03",
	}); err != nil {
		t.Fatal(err)
	}

	n, amb, err := st.SupersedeAmendedForm4s(ctx, sym.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("got n=%d want 0", n)
	}
	if len(amb) != 1 || amb[0] != "a1" {
		t.Fatalf("got amb=%v want [a1]", amb)
	}
}

func TestReplaceInstPeriod(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := st.ReplaceInstPeriod(ctx, "9", "2026-09-30", []InstHoldingRow{
		{Manager: "M", CUSIP: "OLD", Name: "old", Value: 1, Shares: 1},
	}); err != nil {
		t.Fatal(err)
	}

	if err := st.ReplaceInstPeriod(ctx, "9", "2026-09-30", []InstHoldingRow{
		{Manager: "M", CUSIP: "X", Name: "x", Value: 10, Shares: 1},
		{Manager: "M", CUSIP: "X", Name: "x", Value: 5, Shares: 2},
		{Manager: "M", CUSIP: "Y", Name: "y", Value: 1, Shares: 1},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := st.InstHoldingsByManager(ctx, "9", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows want 2", len(got))
	}
	byCUSIP := make(map[string]InstHoldingRow)
	for _, r := range got {
		byCUSIP[r.CUSIP] = r
	}
	x, ok := byCUSIP["X"]
	if !ok {
		t.Fatalf("missing CUSIP X")
	}
	if x.Value != 15 || x.Shares != 3 {
		t.Fatalf("X got Value=%f Shares=%f want 15, 3", x.Value, x.Shares)
	}
	y, ok := byCUSIP["Y"]
	if !ok {
		t.Fatalf("missing CUSIP Y")
	}
	if y.Value != 1 || y.Shares != 1 {
		t.Fatalf("Y got Value=%f Shares=%f want 1, 1", y.Value, y.Shares)
	}
	if _, ok := byCUSIP["OLD"]; ok {
		t.Fatalf("OLD should not exist")
	}
}

// A second 4/A of the same form replaces the first 4/A, so only the newest
// restatement counts.
func TestSupersedeAmendedForm4sChain(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "Zed")
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) int64 { tm, _ := time.Parse("2006-01-02", s); return tm.Unix() }
	for _, r := range []InsiderTradeRow{
		{Accession: "o1", Insider: "DOE JOHN", Code: "P", Value: 1000, FiledTs: day("2026-03-03") + 20*3600},
		{Accession: "a1", Insider: "DOE JOHN", Code: "P", Value: 1200, FiledTs: day("2026-03-10"), AmendsDay: "2026-03-03"},
		{Accession: "a2", Insider: "DOE JOHN", Code: "P", Value: 1300, FiledTs: day("2026-03-20"), AmendsDay: "2026-03-03"},
	} {
		r.SymbolID = sym.ID
		if err := st.InsertInsiderTrade(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	n, amb, err := st.SupersedeAmendedForm4s(ctx, sym.ID)
	if err != nil || n != 2 || len(amb) != 0 {
		t.Fatalf("n=%d amb=%v err=%v, want 2 [] nil", n, amb, err)
	}
	buys, _, nb, _, _ := st.InsiderNetActivity(ctx, sym.ID, 0)
	if buys != 1300 || nb != 1 {
		t.Fatalf("buys=%v n=%d, want 1300 1 (only the newest restatement)", buys, nb)
	}
}

// Arrival order must not matter: O, then its second 4/A, then its first,
// supersede-pass after each insert, ends with only the second counting.
func TestSupersedeAmendedForm4sArrivalOrder(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "ZZZ", md.Stocks, "Zed")
	if err != nil {
		t.Fatal(err)
	}
	day := func(s string) int64 { tm, _ := time.Parse("2006-01-02", s); return tm.Unix() }
	for _, r := range []InsiderTradeRow{
		{Accession: "o1", Insider: "DOE JOHN", Code: "P", Value: 1000, FiledTs: day("2026-03-03") + 20*3600},
		{Accession: "a2", Insider: "DOE JOHN", Code: "P", Value: 1300, FiledTs: day("2026-03-20"), AmendsDay: "2026-03-03"},
		{Accession: "a1", Insider: "DOE JOHN", Code: "P", Value: 1200, FiledTs: day("2026-03-10"), AmendsDay: "2026-03-03"},
		// a parsed-empty same-day row must not make the match ambiguous
		{Accession: "d1", Insider: "DOE JOHN", Code: "", FiledTs: day("2026-03-03") + 21*3600},
	} {
		r.SymbolID = sym.ID
		if err := st.InsertInsiderTrade(ctx, r); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.SupersedeAmendedForm4s(ctx, sym.ID); err != nil {
			t.Fatal(err)
		}
	}
	buys, _, nb, _, _ := st.InsiderNetActivity(ctx, sym.ID, 0)
	if buys != 1300 || nb != 1 {
		t.Fatalf("buys=%v n=%d, want 1300 1", buys, nb)
	}
}
