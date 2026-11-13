package store

// WHY: a WAL frame cannot be checkpointed past the oldest read snapshot still
// open. On 2026-11-12 the storage governor's TRUNCATE stalled at ONE frame for
// 10-20 minutes at a time (689621, then 1103121, then 1170381) while the WAL
// reached 4.8 GB, and nothing named the reader: the store logs long WRITE holds
// (prioritygate.go) but no long reads. This wrapper times every read on the
// read pool and names any snapshot held past longReadHold.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

var longReadHold = 60 * time.Second // a var so a test can lower it

// readCallerPCs captures the caller of the wrapper method that calls it. Only
// program counters: every read pays for this, so symbolising waits until a
// hold is actually long (readCaller).
func readCallerPCs() []uintptr {
	pcs := make([]uintptr, 24)
	return pcs[:runtime.Callers(3, pcs)]
}

// readCaller names the code that asked for a read, with writeCaller's filter.
func readCaller(pcs []uintptr) string {
	frames := runtime.CallersFrames(pcs)
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

// openReads opens a read pool whose connections time every snapshot.
func openReads(dsn string) (*sql.DB, error) {
	base, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(gatedReadConnector{drv: base.Driver(), dsn: dsn}), nil
}

type gatedReadConnector struct {
	drv driver.Driver
	dsn string
}

func (c gatedReadConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if dc, ok := c.drv.(driver.DriverContext); ok {
		connector, err := dc.OpenConnector(c.dsn)
		if err != nil {
			return nil, err
		}
		conn, err := connector.Connect(ctx)
		if err != nil {
			return nil, err
		}
		return &gatedReadConn{Conn: conn}, nil
	}
	conn, err := c.drv.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &gatedReadConn{Conn: conn}, nil
}

func (c gatedReadConnector) Driver() driver.Driver { return c.drv }

type gatedReadConn struct {
	driver.Conn
}

func (c *gatedReadConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	from, start := readCallerPCs(), time.Now()
	rows, err := qc.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &gatedReadRows{Rows: rows, from: from, start: start}, nil
}

func (c *gatedReadConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if ex, ok := c.Conn.(driver.ExecerContext); ok {
		return ex.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *gatedReadConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, query)
	}
	return c.Prepare(query)
}

func (c *gatedReadConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	from, start := readCallerPCs(), time.Now()
	var tx driver.Tx
	var err error
	if cb, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = cb.BeginTx(ctx, opts)
	} else {
		tx, err = c.Conn.Begin() //nolint:staticcheck // fallback for drivers without BeginTx
	}
	if err != nil {
		return nil, err
	}
	return &gatedReadTx{Tx: tx, from: from, start: start}, nil
}

func (c *gatedReadConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *gatedReadConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *gatedReadConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *gatedReadConn) CheckNamedValue(nv *driver.NamedValue) error {
	if nvc, ok := c.Conn.(driver.NamedValueChecker); ok {
		return nvc.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type gatedReadTx struct {
	driver.Tx
	from  []uintptr
	start time.Time
}

func (t *gatedReadTx) Commit() error {
	err := t.Tx.Commit()
	noteReadHold("transaction", t.from, t.start)
	return err
}

func (t *gatedReadTx) Rollback() error {
	err := t.Tx.Rollback()
	noteReadHold("transaction", t.from, t.start)
	return err
}

type gatedReadRows struct {
	driver.Rows
	from   []uintptr
	start  time.Time
	closed bool
}

func (r *gatedReadRows) Close() error {
	err := r.Rows.Close()
	if !r.closed {
		r.closed = true
		noteReadHold("query", r.from, r.start)
	}
	return err
}

// noteReadHold logs a read snapshot held past longReadHold, naming its caller.
func noteReadHold(kind string, from []uintptr, start time.Time) {
	if d := time.Since(start); d > longReadHold {
		slog.Warn("long read hold", "kind", kind, "held", d.Round(time.Millisecond), "from", readCaller(from),
			"effect", "WAL frames newer than this snapshot could not be checkpointed while it was open")
	}
}
