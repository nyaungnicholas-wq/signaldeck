package api

import "time"

// sharedResearchLedgerSWR body-caches GET /api/research-ledger.
//
// It is the slowest route on the ANONYMOUS surface. It is listed in
// publicRoutes, and it was registered bare while its slow neighbours
// (/api/screener, /api/symbol, /api/paper, /api/honesty) all sit behind an SWR
// body cache. Measured 2026-09-13: 11.2s warm on an otherwise quiet box; the
// audit measured 53s cold under worker load. The API read pool is 4
// connections, so a few concurrent anonymous requests are enough to hold all
// of them for the length of a full scan.
//
// 5 minutes, not the 60s the screener uses: this payload is assembled from the
// research ledger, which the loop updates on a cadence of hours, so a longer
// TTL costs nothing in freshness and removes far more of the scan. Stale-while-
// revalidate means the first visitor after expiry still gets the old body
// immediately while the rebuild runs behind them.
var sharedResearchLedgerSWR = newSWRBodyCache(5 * time.Minute)
