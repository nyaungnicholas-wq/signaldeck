package structregime

import "sync/atomic"

// backtestOff silences every backtest table in this package.
//
// The accuracy bands and mean forward returns hard-coded here were MEASURED ON
// ALPACA BARS. An instance fed by another price source has no such measurement
// of its own, and Alpaca's licence does not let figures derived from its data
// be shown to third parties. So on that instance a forecast carries no
// historical accuracy (0 = not measured on this data) and no trade plan is
// built, until the tables are re-measured on that source's bars.
var backtestOff atomic.Bool

// DisableBacktestTables is called once at daemon start, before any worker runs,
// on an instance whose public price source is not Alpaca.
func DisableBacktestTables() { backtestOff.Store(true) }
