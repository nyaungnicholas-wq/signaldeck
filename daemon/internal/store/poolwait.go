package store

import (
	"fmt"
	"time"
)

// Connection-pool contention, measured by database/sql rather than by anything
// this project counts itself. WaitCount and WaitDuration are how often, and for
// how long in total, a caller blocked waiting for a free connection.
//
// Because the write pool is capped at one connection (SetMaxOpenConns(1)), its
// WaitDuration IS SQLite writer contention for this process. There is no
// separate lock worth instrumenting.
//
// LIMIT OF THE MEASUREMENT, stated because it is easy to over-read: these
// counters are process-wide and cumulative across every goroutine, so a delta
// taken over one worker's run measures how long the WHOLE FLEET waited during
// that window, not how long that one worker was blocked. It identifies the
// saturated resource; it does not apportion the wait to a caller.

// PoolWait holds connection pool statistics for both writer and reader pools.
type PoolWait struct {
	WriterWaits int64
	WriterWait  time.Duration
	WriterInUse int
	ReaderWaits int64
	ReaderWait  time.Duration
	ReaderInUse int
}

// PoolWaits returns the current connection pool statistics for both the
// writer and reader database pools.
func (s *Store) PoolWaits() PoolWait {
	wStats := s.w.Stats()
	rStats := s.db.Stats()
	return PoolWait{
		WriterWaits: wStats.WaitCount,
		WriterWait:  wStats.WaitDuration,
		WriterInUse: wStats.InUse,
		ReaderWaits: rStats.WaitCount,
		ReaderWait:  rStats.WaitDuration,
		ReaderInUse: rStats.InUse,
	}
}

// Since returns the difference between two PoolWait snapshots.
// For counters (Waits, Wait), it calculates a - b. For gauges (InUse),
// it returns the value from 'a' unchanged.
func (a PoolWait) Since(b PoolWait) PoolWait {
	return PoolWait{
		WriterWaits: a.WriterWaits - b.WriterWaits,
		WriterWait:  a.WriterWait - b.WriterWait,
		// InUse is a gauge, not a counter, so we report the current value from 'a'.
		WriterInUse: a.WriterInUse,
		ReaderWaits: a.ReaderWaits - b.ReaderWaits,
		ReaderWait:  a.ReaderWait - b.ReaderWait,
		// InUse is a gauge, not a counter, so we report the current value from 'a'.
		ReaderInUse: a.ReaderInUse,
	}
}

func (p PoolWait) String() string {
	if p.WriterWaits == 0 && p.ReaderWaits == 0 {
		return ""
	}

	writerStr := ""
	if p.WriterWaits > 0 {
		writerStr = "writer " + p.WriterWait.Round(time.Millisecond*100).String() + "/" + fmt.Sprint(p.WriterWaits)
	}

	readerStr := ""
	if p.ReaderWaits > 0 {
		readerStr = "reader " + p.ReaderWait.Round(time.Millisecond*100).String() + "/" + fmt.Sprint(p.ReaderWaits)
	}

	if writerStr != "" && readerStr != "" {
		return writerStr + " " + readerStr
	}
	if writerStr != "" {
		return writerStr
	}
	return readerStr // Should only happen if writerStr is empty and readerStr is not
}
