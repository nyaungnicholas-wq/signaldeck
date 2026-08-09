package store

import (
	"database/sql/driver"
	"fmt"

	sqlite "modernc.org/sqlite"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// settle_day(settle_ts, ts) is md.SettleDay, callable from SQL.
//
// The companion to trading_day for RESOLVED outcome rows. Same reason it exists:
// the day fold is applied on both sides of the wall, and registering the Go
// function as a SQLite scalar keeps ONE implementation rather than letting a
// second spelling drift. Swapping a query's PARTITION BY from trading_day(ts) to
// settle_day(settle_ts, ts) is the whole migration at that call site.
//
// A NULL settle_ts is NOT propagated as NULL: unknown means "fall back to the
// calendar day", which is what md.SettleDay does with 0. Returning NULL here
// would silently drop every not-yet-backfilled row out of its PARTITION and
// quietly shrink the very denominator this function exists to correct.
func init() {
	if err := sqlite.RegisterDeterministicScalarFunction("settle_day", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("settle_day: want 2 args, got %d", len(args))
			}
			var settleTs int64
			if args[0] != nil {
				v, ok := args[0].(int64)
				if !ok {
					return nil, fmt.Errorf("settle_day: want an integer unix timestamp for settle_ts, got %T", args[0])
				}
				settleTs = v
			}
			if args[1] == nil {
				return nil, nil
			}
			ts, ok := args[1].(int64)
			if !ok {
				return nil, fmt.Errorf("settle_day: want an integer unix timestamp, got %T", args[1])
			}
			return md.SettleDay(settleTs, ts), nil
		}); err != nil {
		panic("store: registering settle_day: " + err.Error())
	}
}
