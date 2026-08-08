// Broad-universe wave: /api/universe exposes the HOT/BROAD split — how many
// symbols are in the live STREAMED hot set vs the broad DAILY-ONLY universe,
// alongside the caps that govern each. Read-only; gated like every other read
// (public when SIGNALDECK_PUBLIC_READS=true). Makes the "wide+free" claim
// visible: hundreds of daily-only symbols, a small streamed set within the
// free ws cap.
package api

import (
	"net/http"

	"github.com/nyaungnicholas-wq/signaldeck/internal/discovery"
	"github.com/nyaungnicholas-wq/signaldeck/internal/universe"
)

func (d Deps) universe(w http.ResponseWriter, r *http.Request) {
	streamed, err := d.St.StreamedSymbolCount(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	daily, err := d.St.DailyUniverseCount(r.Context())
	if err != nil {
		httpInternal(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"streamed":     streamed,                 // live ws + full 1m pipeline
		"streamCap":    discovery.SymbolCap(),    // SIGNALDECK_STREAM_CAP
		"dailyOnly":    daily,                    // REST daily bars only (broad)
		"universeCap":  universe.UniverseCap(),   // SIGNALDECK_UNIVERSE_CAP
		"universeSeed": len(universe.Curated(0)), // deduped curated seed size
	})
}
