package api

import "sync/atomic"

// lruTick returns a strictly increasing stamp used to order cache entries for
// eviction.
//
// Wall-clock time cannot do this job. time.Now() has coarse resolution (tens of
// microseconds up to ~15ms on Windows), so a burst of gets can share a single
// instant. evictLRULocked then finds several entries with an identical stamp
// and breaks the tie by Go's randomized map iteration order — which can evict
// the continuously-used hot entry that the LRU policy exists to protect, handing
// the next real visitor a cold build. A counter has no ties.
var lruSeq atomic.Uint64

func lruTick() uint64 { return lruSeq.Add(1) }
