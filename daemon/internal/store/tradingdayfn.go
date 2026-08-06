package store

import (
	"database/sql/driver"
	"fmt"

	sqlite "modernc.org/sqlite"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// trading_day(ts) is md.TradingDay, callable from SQL.
//
// The day fold is the platform's unit of independent evidence, and it is
// applied on BOTH sides of the wall: Go code folds slices of timestamps, SQL
// folds with PARTITION BY / COUNT(DISTINCT). Those two used to be independent
// spellings of `ts/86400`, which meant the definition could drift on one side
// only — and the whole point of the fold is that the surface which TRAINS a leg
// and the surface which JUDGES it agree about what one observation is.
//
// Registering the Go function as a SQLite scalar removes the second spelling
// entirely: there is now one implementation and SQL calls it. That also carries
// the floor-division fix across, which an inline `(ts-18000)/86400` could not —
// SQLite truncates toward zero exactly like Go, so an inline form would fold
// days -1 and 0 together for timestamps below the offset.
//
// ponytail: a scalar call per row is slower than inline arithmetic on a big
// scan; if a day-folded query ever shows up hot, inline the arithmetic THERE
// and leave this as the definition everything else shares.
func init() {
	if err := sqlite.RegisterDeterministicScalarFunction("trading_day", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("trading_day: want 1 arg, got %d", len(args))
			}
			// A NULL timestamp folds to NULL rather than to day zero: a row with
			// no time is not an observation on the epoch.
			if args[0] == nil {
				return nil, nil
			}
			ts, ok := args[0].(int64)
			if !ok {
				return nil, fmt.Errorf("trading_day: want an integer unix timestamp, got %T", args[0])
			}
			return md.TradingDay(ts), nil
		}); err != nil {
		panic("store: registering trading_day: " + err.Error())
	}
}
