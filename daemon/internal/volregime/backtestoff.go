package volregime

import "sync/atomic"

// backtestOff silences AccuracyForConviction: its bands were measured on Alpaca
// bars and must not ride along on forecasts built from another source. See
// structregime.DisableBacktestTables.
var backtestOff atomic.Bool

// DisableBacktestTables is called once at daemon start on an instance whose
// public price source is not Alpaca.
func DisableBacktestTables() { backtestOff.Store(true) }
