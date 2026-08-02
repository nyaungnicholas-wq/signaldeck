// Package cryptolive ingests TickStream's live consolidated crypto view.
//
// TickStream stays its own process (its packages are internal/ by design);
// SignalDeck consumes its dashboard JSON at /api/snapshot once per second —
// exactly the snapshots_1s cadence — which also isolates failure domains: if
// tickstreamd is down, SignalDeck records a data-quality event and keeps
// serving everything else. Live crypto BARS are not fabricated from these
// mids; the Kraken OHLC refresher supplies real OHLCV (see cmd wiring).
package cryptolive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// tickstreamDTO mirrors the fields we consume from tickstream's /api/snapshot.
type tickstreamDTO struct {
	Valid             bool    `json:"valid"`
	BidPrice          string  `json:"bidPrice"`
	AskPrice          string  `json:"askPrice"`
	Spread            string  `json:"spread"`
	Mid               float64 `json:"mid"`
	WeightedMid       float64 `json:"weightedMid"`
	ImbSigned         float64 `json:"imbSigned"`
	ApplyLatencyNanos int64   `json:"applyLatencyNanos"`
	PublishUnixNanos  int64   `json:"publishUnixNanos"`
}

// clockSkewTolerance is how far a publish timestamp may sit in the future
// before the snapshot is refused. Sub-second differences are ordinary process
// scheduling; anything past this is a real clock disagreement and the snapshot
// cannot be trusted to land in the right 1s bucket.
const clockSkewTolerance = time.Second

// Ingestor is the long-running crypto snapshot worker (Interval 0).
type Ingestor struct {
	st       *store.Store
	url      string
	symbolID int64
	http     *http.Client

	consecFails int
	lastDQEmit  time.Time
	written     int64
}

// New builds the ingestor for one consolidated crypto symbol.
func New(st *store.Store, tickstreamURL string, symbolID int64) *Ingestor {
	return &Ingestor{
		st:       st,
		url:      tickstreamURL,
		symbolID: symbolID,
		http:     &http.Client{Timeout: 3 * time.Second},
	}
}

// Name implements workers.Worker.
func (g *Ingestor) Name() string { return "crypto-live" }

// Interval implements workers.Worker: 0 = long-running stream.
func (g *Ingestor) Interval() time.Duration { return 0 }

// Run polls once per second until ctx ends. It returns an error after
// sustained failure so the run log shows the outage; the runner restarts it.
func (g *Ingestor) Run(ctx context.Context) (string, error) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Sprintf("stopped; %d snapshots written", g.written), nil
		case <-t.C:
			if err := g.poll(ctx); err != nil {
				g.consecFails++
				g.maybeEmitDQ(ctx, err)
				if g.consecFails >= 60 {
					return fmt.Sprintf("%d snapshots written this run", g.written),
						fmt.Errorf("tickstream unreachable for %ds: %w", g.consecFails, err)
				}
				continue
			}
			g.consecFails = 0
		}
	}
}

func (g *Ingestor) poll(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url, nil)
	if err != nil {
		return err
	}
	res, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("tickstream: HTTP %d", res.StatusCode)
	}
	var dto tickstreamDTO
	if err := json.NewDecoder(res.Body).Decode(&dto); err != nil {
		return err
	}
	if !dto.Valid {
		return fmt.Errorf("tickstream: snapshot not valid yet")
	}
	if dto.PublishUnixNanos <= 0 {
		return fmt.Errorf("tickstream: snapshot carries no publish timestamp")
	}
	// A publish older than 10s means tickstream is up but its feeds stalled.
	//
	// The age is checked in BOTH directions. `age > 10s` alone fails open: any
	// skew putting the publish timestamp ahead of this process's clock makes
	// age negative, which is never > 10s, so a fully stalled feed would read as
	// perpetually fresh. Today tickstream shares this machine's clock so the
	// two agree, but a clock resync mid-run — or moving tickstream to another
	// host — breaks that silently. internal/backup/backup.go:140 already
	// guards its own age the same way.
	age := time.Since(time.Unix(0, dto.PublishUnixNanos))
	if age < -clockSkewTolerance {
		return fmt.Errorf("tickstream: publish timestamp is %s in the future — "+
			"clock skew between tickstream and signaldeckd",
			(-age).Round(time.Millisecond))
	}
	if age > 10*time.Second {
		return fmt.Errorf("tickstream: snapshot stale by %s", age.Round(time.Second))
	}
	bid, _ := strconv.ParseFloat(dto.BidPrice, 64)
	ask, _ := strconv.ParseFloat(dto.AskPrice, 64)
	spread, _ := strconv.ParseFloat(dto.Spread, 64)
	err = g.st.InsertSnap1s(ctx, md.Snap1s{
		SymbolID: g.symbolID,
		// The publisher's timestamp, not local receipt time. Receipt time makes
		// every row inherit this machine's clock error — a 77s-slow clock (as
		// measured 2026-08-02) files each 1s snapshot ~77 buckets early and
		// misaligns it with provider-stamped equity bars.
		Ts:         time.Unix(0, dto.PublishUnixNanos).Unix(),
		Bid:        bid,
		Ask:        ask,
		Mid:        dto.Mid,
		WMid:       dto.WeightedMid,
		ImbSigned:  dto.ImbSigned,
		Spread:     spread,
		ApplyLatNs: dto.ApplyLatencyNanos,
	})
	if err == nil {
		g.written++
	}
	return err
}

// maybeEmitDQ records the outage as a data-quality event, rate-limited to
// one per 5 minutes so a long outage doesn't flood the table.
func (g *Ingestor) maybeEmitDQ(ctx context.Context, cause error) {
	if time.Since(g.lastDQEmit) < 5*time.Minute {
		return
	}
	g.lastDQEmit = time.Now()
	_ = g.st.InsertDQ(ctx, md.DQEvent{
		SymbolID: &g.symbolID,
		Ts:       time.Now().Unix(),
		Kind:     "stale",
		Detail:   "crypto live feed: " + cause.Error(),
	})
}
