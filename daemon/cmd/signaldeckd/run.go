package main

import (
	"context"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// run wires workers + API. Placeholder while engine packages land; replaced
// at integration.
func run(ctx context.Context, cfg config.Config, st *store.Store) {
	<-ctx.Done()
}
