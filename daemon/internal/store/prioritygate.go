package store

// Priority for account writes over the worker fleet's write path.
//
// SQLite lets one connection write at a time and its busy handler is not a
// queue: a waiting connection polls, and a busy connection that commits and
// immediately begins again usually wins. Measured 2026-09-30 on the live box:
// with ~100 workers writing back-to-back through Store.w, an email confirmation
// on its own connection (aw) waited out its full 12s busy_timeout on every
// attempt for more than ten minutes. A separate connection only removed Go's
// pool queue; it could not win the lock.
//
// So the MAIN writer's driver connection is wrapped: before it starts a new
// statement or transaction OUTSIDE an open transaction, it holds off while an
// account write is waiting (at most priorityMaxWait), leaving the SQLite lock
// free for aw at the next transaction boundary. Statements inside an open
// transaction are never delayed, so no transaction is held open longer.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"log/slog"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// priorityMaxWait is the maximum time to wait for a priority account write.
const priorityMaxWait = 3 * time.Second

// priorityGate tracks the number of waiting account writes.
type priorityGate struct {
	waiters atomic.Int32
}

// hold waits while there are waiters, up to priorityMaxWait, respecting ctx.
func (g *priorityGate) hold(ctx context.Context) {
	if g.waiters.Load() == 0 {
		return
	}
	start := time.Now()
	for g.waiters.Load() > 0 && time.Since(start) < priorityMaxWait {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// Priority marks an account write as waiting until the returned func is called.
// Call it before the account write and defer the release.
func (s *Store) Priority() func() {
	if s.gate == nil {
		return func() {}
	}
	s.gate.waiters.Add(1)
	released := &atomic.Bool{}
	return func() {
		if released.CompareAndSwap(false, true) {
			s.gate.waiters.Add(-1)
		}
	}
}

// openGated returns a *sql.DB that uses the priority gate to delay the main writer.
func openGated(drv driver.Driver, dsn string, g *priorityGate) *sql.DB {
	return sql.OpenDB(gatedConnector{drv: drv, dsn: dsn, g: g})
}

// gatedConnector wraps a driver.Driver to inject the priority gate.
type gatedConnector struct {
	drv driver.Driver
	dsn string
	g   *priorityGate
}

// Connect returns a connection that wraps the underlying driver connection.
func (c gatedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	var (
		conn driver.Conn
		err  error
	)
	if dc, ok := c.drv.(driver.DriverContext); ok {
		var connector driver.Connector
		if connector, err = dc.OpenConnector(c.dsn); err != nil {
			return nil, err
		}
		conn, err = connector.Connect(ctx)
	} else {
		conn, err = c.drv.Open(c.dsn)
	}
	if err != nil {
		return nil, err
	}
	// BOTH paths wrap: an unwrapped conn would skip the gate entirely.
	return &gatedConn{Conn: conn, g: c.g}, nil
}

// Driver returns the wrapped driver.
func (c gatedConnector) Driver() driver.Driver {
	return c.drv
}

// gatedConn wraps a driver.Conn to enforce the priority gate.
type gatedConn struct {
	driver.Conn
	g       *priorityGate
	inTx    bool
	txStart time.Time
	txFrom  string
}

// longHold is the write-lock hold worth naming in the log. Account writes wait
// 12s at most, so anything near that locks people out of signing in.
var longHold = 3 * time.Second

// writeCaller names the code that asked for this write: the first frames
// outside database/sql, the runtime and this file.
func writeCaller() string {
	pcs := make([]uintptr, 24)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	var out []string
	for f, more := frames.Next(); more && len(out) < 3; f, more = frames.Next() {
		fn := f.Function
		if strings.HasPrefix(fn, "database/sql") || strings.HasPrefix(fn, "runtime.") || strings.Contains(fn, "store.(*gated") {
			continue
		}
		out = append(out, fn[strings.LastIndex(fn, "/")+1:])
	}
	return strings.Join(out, " <- ")
}

// noteHold logs a write that held the connection past longHold.
func noteHold(kind, from string, start time.Time) {
	if d := time.Since(start); d > longHold {
		slog.Warn("long write-lock hold", "kind", kind, "held", d.Round(time.Millisecond), "from", from)
	}
}

// wait delays if not inside a transaction.
func (c *gatedConn) wait(ctx context.Context) {
	if !c.inTx {
		c.g.hold(ctx)
	}
}

// BeginTx starts a transaction, applying the gate if needed.
func (c *gatedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.wait(ctx)
	var tx driver.Tx
	var err error
	if cb, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = cb.BeginTx(ctx, opts)
	} else {
		//nolint:staticcheck // fallback for drivers without BeginTx
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		return nil, err
	}
	c.inTx = true
	c.txStart, c.txFrom = time.Now(), writeCaller()
	return &gatedTx{Tx: tx, c: c}, nil
}

// PrepareContext prepares a statement, applying the gate if needed.
func (c *gatedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.wait(ctx)
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, query)
	}
	return c.Prepare(query)
}

// ExecContext executes a statement, applying the gate if needed.
func (c *gatedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if ex, ok := c.Conn.(driver.ExecerContext); ok {
		c.wait(ctx)
		if c.inTx {
			return ex.ExecContext(ctx, query, args)
		}
		start := time.Now()
		res, err := ex.ExecContext(ctx, query, args)
		if time.Since(start) > longHold {
			noteHold("statement", writeCaller(), start)
		}
		return res, err
	}
	return nil, driver.ErrSkip
}

// QueryContext queries a statement, applying the gate if needed.
func (c *gatedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if qc, ok := c.Conn.(driver.QueryerContext); ok {
		c.wait(ctx)
		return qc.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// Ping checks the connection.
func (c *gatedConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// ResetSession resets the session.
func (c *gatedConn) ResetSession(ctx context.Context) error {
	if sr, ok := c.Conn.(driver.SessionResetter); ok {
		return sr.ResetSession(ctx)
	}
	return nil
}

// IsValid reports whether the connection is valid.
func (c *gatedConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// CheckNamedValue checks a named value.
func (c *gatedConn) CheckNamedValue(nv *driver.NamedValue) error {
	if chk, ok := c.Conn.(driver.NamedValueChecker); ok {
		return chk.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// gatedTx wraps a driver.Tx to track transaction state.
type gatedTx struct {
	driver.Tx
	c *gatedConn
}

// Commit marks the transaction as not in flight and commits.
func (t *gatedTx) Commit() error {
	t.c.inTx = false
	err := t.Tx.Commit()
	noteHold("transaction", t.c.txFrom, t.c.txStart)
	return err
}

// Rollback marks the transaction as not in flight and rolls back.
func (t *gatedTx) Rollback() error {
	t.c.inTx = false
	err := t.Tx.Rollback()
	noteHold("transaction", t.c.txFrom, t.c.txStart)
	return err
}
