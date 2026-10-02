package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestMemberCallsImmutableCappedAndOwned(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "calls.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ann, err := st.CreateVerifiedUser(ctx, "ann", "ann@gmail.com", "x")
	must(err)
	bob, err := st.CreateVerifiedUser(ctx, "bob", "bob@gmail.com", "x")
	must(err)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	must(err)
	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC).Unix()
	call := func(uid, created int64) MemberCall {
		return MemberCall{UserID: uid, SymbolID: sym.ID, Market: "stocks", Call: "up", Horizon: 1,
			Note: "n", CreatedTs: created, EntryTs: created + 3600, ExitDueTs: created + 90000}
	}

	// Daily cap: 10 per member per day; the 11th is refused, another member is unaffected.
	var ids []int64
	for i := 0; i < MemberCallsPerDay; i++ {
		id, err := st.InsertMemberCall(ctx, call(ann, day+int64(i)), day)
		must(err)
		ids = append(ids, id)
	}
	if _, err := st.InsertMemberCall(ctx, call(ann, day+100), day); !errors.Is(err, ErrCallCapDay) {
		t.Fatalf("11th call today: %v, want ErrCallCapDay", err)
	}
	if _, err := st.InsertMemberCall(ctx, call(bob, day+100), day); err != nil {
		t.Fatalf("bob's first call: %v", err)
	}
	// Open cap: 50 open across days. Ann has 10 open; 40 more on later days fill it.
	for i := 0; i < MemberCallsOpen-MemberCallsPerDay; i++ {
		next := day + int64(1+i/MemberCallsPerDay)*86400
		_, err := st.InsertMemberCall(ctx, call(ann, next+int64(i)), next)
		must(err)
	}
	later := day + 30*86400
	if _, err := st.InsertMemberCall(ctx, call(ann, later), later); !errors.Is(err, ErrCallCapOpen) {
		t.Fatalf("51st open call: %v, want ErrCallCapOpen", err)
	}

	// Ownership: bob cannot read or withdraw ann's call.
	if _, ok, err := st.MemberCall(ctx, bob, ids[0]); err != nil || ok {
		t.Fatalf("bob read ann's call: ok=%v err=%v", ok, err)
	}
	if ok, err := st.WithdrawMemberCall(ctx, bob, ids[0], day); err != nil || ok {
		t.Fatalf("bob withdrew ann's call: ok=%v err=%v", ok, err)
	}
	if c, ok, err := st.MemberCall(ctx, ann, ids[0]); err != nil || !ok || c.Status != "open" || c.Symbol != "AAPL" {
		t.Fatalf("ann's call after bob's attempt: %+v ok=%v err=%v", c, ok, err)
	}

	// Immutable: what was called never changes, open or not.
	for _, q := range []string{
		`UPDATE member_calls SET call='down' WHERE id=?`,
		`UPDATE member_calls SET note='edited' WHERE id=?`,
		`UPDATE member_calls SET horizon=21 WHERE id=?`,
		`UPDATE member_calls SET created_ts=created_ts-86400 WHERE id=?`,
		`UPDATE member_calls SET entry_ts=0 WHERE id=?`,
	} {
		if _, err := st.w.ExecContext(ctx, q, ids[0]); err == nil {
			t.Errorf("%s succeeded on an open call", q)
		}
	}
	// Settled is final: a resolved, voided or withdrawn call never changes again.
	if ok, err := st.SettleMemberCall(ctx, ids[0], "hit", day); err != nil || !ok {
		t.Fatalf("settle: ok=%v err=%v", ok, err)
	}
	if ok, err := st.SettleMemberCall(ctx, ids[0], "miss", day); err != nil || ok {
		t.Fatalf("re-settle rewrote a graded call: ok=%v err=%v", ok, err)
	}
	if _, err := st.w.ExecContext(ctx, `UPDATE member_calls SET outcome='miss' WHERE id=?`, ids[0]); err == nil {
		t.Error("a resolved call's outcome was rewritten")
	}
	if ok, err := st.WithdrawMemberCall(ctx, ann, ids[0], day); err != nil || ok {
		t.Fatalf("withdrew a resolved call: ok=%v err=%v", ok, err)
	}
	// Not at or after the entry close, whatever the caller checked a moment earlier.
	if ok, err := st.WithdrawMemberCall(ctx, ann, ids[1], call(ann, day+1).EntryTs); err != nil || ok {
		t.Fatalf("withdrew at the entry close: ok=%v err=%v", ok, err)
	}
	if ok, err := st.WithdrawMemberCall(ctx, ann, ids[1], day); err != nil || !ok {
		t.Fatalf("withdraw open call: ok=%v err=%v", ok, err)
	}
	if ok, err := st.SettleMemberCall(ctx, ids[1], "hit", day); err != nil || ok {
		t.Fatalf("graded a withdrawn call: ok=%v err=%v", ok, err)
	}
	if ok, err := st.SettleMemberCall(ctx, ids[2], "", day); err != nil || !ok {
		t.Fatalf("void: ok=%v err=%v", ok, err)
	}
	calls, err := st.MemberCalls(ctx, ann)
	must(err)
	got := map[int64]MemberCall{}
	for _, c := range calls {
		if c.UserID != ann {
			t.Fatalf("MemberCalls(ann) returned user %d's call", c.UserID)
		}
		got[c.ID] = c
	}
	if c := got[ids[0]]; c.Status != "resolved" || c.Outcome != "hit" || c.Call != "up" || c.Note != "n" {
		t.Errorf("resolved call: %+v", c)
	}
	if c := got[ids[1]]; c.Status != "withdrawn" || c.WithdrawnTs != day {
		t.Errorf("withdrawn call: %+v", c)
	}
	if c := got[ids[2]]; c.Status != "void" || c.Outcome != "" {
		t.Errorf("void call: %+v", c)
	}

	// Purge removes a stale unverified account's calls with its other rows.
	eve, err := st.CreateUserWithEmail(ctx, "eve", "eve@gmail.com", "x")
	must(err)
	_, err = st.InsertMemberCall(ctx, call(eve, day), day)
	must(err)
	must(st.PurgeStaleUnverified(ctx, time.Now().Add(time.Hour)))
	var n int
	must(st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM member_calls WHERE user_id=?`, eve).Scan(&n))
	if n != 0 {
		t.Errorf("purge left %d of eve's calls", n)
	}
	must(st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM member_calls WHERE user_id=?`, ann).Scan(&n))
	if n != MemberCallsOpen {
		t.Errorf("purge touched ann's calls: %d left", n)
	}
}
