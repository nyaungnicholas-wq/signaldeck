package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptolive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// The retired sources (crypto 2026-10-21: no licensed data source; StockTwits
// and TradingView: their terms; the LLM sentiment tagger 2026-10-23: NVIDIA's
// trial API terms) still compile as worker types, so one line in
// run.go would put a worker back with no build error. The fleet is assembled
// inline in run(), which needs live clients, so this reads the package source:
// no file of package main may reference a type whose Name() is a retired
// worker name.
func TestFleetRegistersNoRetiredSourceWorker(t *testing.T) {
	retired := map[string]workers.Worker{ // as written in package main -> the worker it builds
		"cryptolive.Ingestor":        &cryptolive.Ingestor{},
		"cryptolive.New":             &cryptolive.Ingestor{},
		"pipeline.CryptoBars":        &pipeline.CryptoBars{},
		"pipeline.CryptoPerpPoller":  &pipeline.CryptoPerpPoller{},
		"pipeline.StocktwitsFetcher": &pipeline.StocktwitsFetcher{},
		"pipeline.TVRatingPoller":    &pipeline.TVRatingPoller{},
		"pipeline.TVQuotesPoller":    &pipeline.TVQuotesPoller{},
		"pipeline.SentimentTagger":   &pipeline.SentimentTagger{},
	}
	// Retired-source CLIENTS: not workers, but e.g. a Kraken client handed to
	// NewBackfiller would silently re-enable crypto backfills.
	retiredClients := map[string]bool{"cryptohist.New": true, "hyperliquid.New": true, "cryptolive.New": true}
	banned := map[string]bool{"crypto-live": true, "crypto-bars": true, "crypto-perp": true, "tv-rating": true, "tv-quotes": true, "sentiment-tagger": true}
	for ref, w := range retired { // pins the table to the names it stands for
		if n := w.Name(); !banned[n] && !strings.HasPrefix(n, "stocktwits") {
			t.Fatalf("%s builds worker %q, not a retired name: update this table", ref, n)
		}
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && retiredClients[x.Name+"."+sel.Sel.Name] {
				t.Errorf("%s: references %s.%s, a retired-source client", fset.Position(sel.Pos()), x.Name, sel.Sel.Name)
			}
			if x, ok := sel.X.(*ast.Ident); ok && retired[x.Name+"."+sel.Sel.Name] != nil {
				t.Errorf("%s: references %s.%s, which builds the retired %q worker",
					fset.Position(sel.Pos()), x.Name, sel.Sel.Name, retired[x.Name+"."+sel.Sel.Name].Name())
			}
			return true
		})
	}
}
