package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	sqlite3 "modernc.org/sqlite/lib"
)

// codedErr stands in for *sqlite.Error, whose fields the driver keeps
// unexported. isSQLiteBusy matches the Code() interface, so this is the same
// path production takes.
type codedErr struct{ code int }

func (e codedErr) Error() string { return "coded sqlite error" }
func (e codedErr) Code() int     { return e.code }

type fakeAppender struct {
	errs  []error // returned in order, one per call
	calls int
}

func (f *fakeAppender) AppendLedger(ctx context.Context, e store.LedgerEntry) (store.LedgerEntry, error) {
	f.calls++
	if f.calls <= len(f.errs) {
		return store.LedgerEntry{}, f.errs[f.calls-1]
	}
	return store.LedgerEntry{Seq: int64(f.calls)}, nil
}

func TestAppendLedgerBusyRetry_RetriesBusyThenSucceeds(t *testing.T) {
	f := &fakeAppender{errs: []error{codedErr{sqlite3.SQLITE_BUSY}, codedErr{sqlite3.SQLITE_LOCKED}}}
	entry, err := appendLedgerBusyRetry(context.Background(), f, store.LedgerEntry{})
	if err != nil {
		t.Fatalf("want success after contention, got %v", err)
	}
	if f.calls != 3 {
		t.Fatalf("want 3 attempts, got %d", f.calls)
	}
	if entry.Seq == 0 {
		t.Fatal("want the appended entry back")
	}
}

func TestAppendLedgerBusyRetry_DoesNotRetryOtherErrors(t *testing.T) {
	// A constraint violation is a real defect: retrying it would only delay
	// the dq event that should record it.
	f := &fakeAppender{errs: []error{codedErr{sqlite3.SQLITE_CONSTRAINT}, nil}}
	if _, err := appendLedgerBusyRetry(context.Background(), f, store.LedgerEntry{}); err == nil {
		t.Fatal("want the constraint error surfaced immediately")
	}
	if f.calls != 1 {
		t.Fatalf("want exactly 1 attempt, got %d", f.calls)
	}
	f2 := &fakeAppender{errs: []error{errors.New("plain")}}
	if _, err := appendLedgerBusyRetry(context.Background(), f2, store.LedgerEntry{}); err == nil {
		t.Fatal("want the plain error surfaced")
	}
	if f2.calls != 1 {
		t.Fatalf("want exactly 1 attempt, got %d", f2.calls)
	}
}

func TestAppendLedgerBusyRetry_IsBoundedAndReturnsLastError(t *testing.T) {
	busy := []error{}
	for i := 0; i < 20; i++ {
		busy = append(busy, codedErr{sqlite3.SQLITE_BUSY})
	}
	f := &fakeAppender{errs: busy}
	if _, err := appendLedgerBusyRetry(context.Background(), f, store.LedgerEntry{}); err == nil {
		t.Fatal("want the last busy error returned so the dq event names it")
	}
	if f.calls != ledgerBusyAttempts {
		t.Fatalf("want %d attempts, got %d", ledgerBusyAttempts, f.calls)
	}
}

func TestAppendLedgerBusyRetry_StopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeAppender{errs: []error{codedErr{sqlite3.SQLITE_BUSY}, nil}}
	if _, err := appendLedgerBusyRetry(ctx, f, store.LedgerEntry{}); err == nil {
		t.Fatal("want the busy error returned when the context is done")
	}
	if f.calls != 1 {
		t.Fatalf("want 1 attempt on a cancelled context, got %d", f.calls)
	}
}
