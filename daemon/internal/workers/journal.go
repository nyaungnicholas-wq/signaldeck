package workers

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// The run journal makes worker-run bookkeeping unlosable.
//
// Every worker start and finish is a write to worker_runs through the store's
// SINGLE write connection (SetMaxOpenConns(1)), shared by the whole fleet
// against a multi-gigabyte database. Doing that write inline, under a fixed
// deadline with one retry, meant a busy write queue silently dropped the row —
// measured at 300+ expiries per day, each one leaving a run permanently stuck
// at 'running'.
//
// A dropped completion is not a cosmetic log gap. ResearchLoop derives the
// Bonferroni multiplicity divisor from a max over meta, worker_runs and the
// durable tables; under-counting looks yields a smaller divisor and therefore a
// LOOSER multiple-comparison correction. So the journal never gives up: ops go
// into a buffered channel drained by ONE goroutine that retries with backoff
// until the write succeeds. Submitters block only when the buffer is full —
// back-pressure, never loss.
const (
	journalBuffer     = 8192
	journalWriteLimit = 2 * time.Minute // per-attempt deadline, not a give-up point
)

// Retry backoff bounds (vars only so tests can shorten them).
var (
	journalRetryMin = 250 * time.Millisecond
	journalRetryMax = 30 * time.Second
)

// journalOp is one bookkeeping write. A zero runID means "open a run"; the id
// is returned on reply. A non-zero runID means "close that run".
type journalOp struct {
	worker string
	runID  int64
	status string
	detail string
	reply  chan int64
	// barrier ops carry no write; the drain loop just closes reply, which
	// proves every op queued ahead of it has already landed.
	barrier bool
}

// runJournal is the single-goroutine drain over the store's write connection.
type runJournal struct {
	st  storeWriter
	ops chan journalOp

	startOnce sync.Once
	doneOnce  sync.Once
	drained   chan struct{}
}

// storeWriter is the slice of *store.Store the journal needs (kept narrow so
// the drain loop is testable without a database).
type storeWriter interface {
	StartWorkerRun(ctx context.Context, worker string) (int64, error)
	FinishWorkerRun(ctx context.Context, id int64, status, detail string) error
}

func newRunJournal(st storeWriter) *runJournal {
	return &runJournal{st: st, ops: make(chan journalOp, journalBuffer), drained: make(chan struct{})}
}

// journal returns the runner's journal, starting its drain goroutine once.
func (r *Runner) journal() *runJournal {
	r.mu.Lock()
	if r.jrnl == nil {
		r.jrnl = newRunJournal(r.st)
	}
	j := r.jrnl
	r.mu.Unlock()
	j.startOnce.Do(func() { go j.drain() })
	return j
}

// submit enqueues an op, blocking if the buffer is full. It never drops.
func (j *runJournal) submit(op journalOp) { j.ops <- op }

// open records the start of a run and returns its id, waiting for the journal
// to land the INSERT. It waits on ctx so shutdown is not blocked forever; a
// zero id means "unrecorded", which runOnce already treats as "do not finish".
func (j *runJournal) open(ctx context.Context, worker string) int64 {
	reply := make(chan int64, 1)
	select {
	case j.ops <- journalOp{worker: worker, reply: reply}:
	case <-ctx.Done():
		return 0
	}
	select {
	case id := <-reply:
		return id
	case <-ctx.Done():
		return 0
	}
}

// close stops accepting ops and waits for the queue to drain, so a clean
// shutdown leaves no run non-terminal that the process could have finished.
func (j *runJournal) close(wait time.Duration) {
	j.doneOnce.Do(func() { close(j.ops) })
	select {
	case <-j.drained:
	case <-time.After(wait):
		slog.Error("worker journal: drain did not finish before deadline", "wait", wait)
	}
}

func (j *runJournal) drain() {
	defer close(j.drained)
	for op := range j.ops {
		if op.barrier {
			close(op.reply)
			continue
		}
		j.apply(op)
	}
}

// FlushRunJournal blocks until every bookkeeping write submitted so far has
// landed. The queue is FIFO and single-drained, so a barrier that reaches the
// front proves everything ahead of it was already written. Callers that need
// to READ worker_runs right after a run (the health watchdog's tests, any
// synchronous inspection) use this instead of assuming the write was inline.
func (r *Runner) FlushRunJournal(ctx context.Context) error {
	j := r.journal()
	reply := make(chan int64)
	select {
	case j.ops <- journalOp{barrier: true, reply: reply}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// apply retries one op until it succeeds. There is deliberately no attempt
// cap: the alternative to "keep trying" is a lost row, and a lost row loosens
// the multiplicity correction downstream.
func (j *runJournal) apply(op journalOp) {
	backoff := journalRetryMin
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), journalWriteLimit)
		var err error
		if op.runID == 0 {
			var id int64
			id, err = j.st.StartWorkerRun(ctx, op.worker)
			if err == nil {
				if op.reply != nil {
					op.reply <- id
				}
				cancel()
				return
			}
		} else {
			err = j.st.FinishWorkerRun(ctx, op.runID, op.status, op.detail)
			if err == nil {
				cancel()
				return
			}
		}
		cancel()
		slog.Warn("worker journal: write failed, retrying",
			"worker", op.worker, "runID", op.runID, "attempt", attempt, "err", err)
		time.Sleep(backoff)
		if backoff *= 2; backoff > journalRetryMax {
			backoff = journalRetryMax
		}
	}
}
