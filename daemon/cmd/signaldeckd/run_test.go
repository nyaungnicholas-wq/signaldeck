package main

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
)

// TestEdgarClientSharedAcrossWaves locks the SEC rate-limit invariant at
// CONSTRUCTION level: the fundamentals fetcher (free-data wave) and the
// filings/13F pollers (signal8 wave) must all pace through the SAME
// *edgar.Client instance — its mutex-serialized min-interval limiter is what
// keeps the whole daemon under SEC's 10 req/s. Two independent clients would
// each be compliant alone yet sum to ~13.3 req/s when their runs overlap.
func TestEdgarClientSharedAcrossWaves(t *testing.T) {
	cfg := config.Config{AlpacaKey: "k", AlpacaSecret: "s"} // HasAlpaca ⇒ clients wired
	shared := edgar.New()

	var ef *pipeline.EdgarFetcher
	for _, w := range freeDataWorkers(cfg, nil, shared) {
		if e, ok := w.(*pipeline.EdgarFetcher); ok {
			ef = e
		}
	}
	if ef == nil || ef.Client == nil {
		t.Fatal("free-data wave did not wire an EDGAR client")
	}
	if ef.Client != shared {
		t.Fatal("edgar-fetcher does not share the daemon-wide EDGAR client (own limiter ⇒ rate cap can be exceeded)")
	}

	var fp *pipeline.FilingsPoller
	var tf *pipeline.ThirteenFPoller
	for _, w := range signal8Workers(cfg, nil, shared) {
		switch v := w.(type) {
		case *pipeline.FilingsPoller:
			fp = v
		case *pipeline.ThirteenFPoller:
			tf = v
		}
	}
	if fp == nil || fp.Client == nil || tf == nil || tf.Client == nil {
		t.Fatal("signal8 wave did not wire its EDGAR clients")
	}
	if fp.Client.Client != shared {
		t.Fatal("filings-poller does not share the daemon-wide EDGAR client")
	}
	if tf.Client.Client != shared {
		t.Fatal("13f-poller does not share the daemon-wide EDGAR client")
	}
	if fp.Client.Client != tf.Client.Client {
		t.Fatal("filings-poller and 13f-poller use different EDGAR limiters")
	}
}
