package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
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

// TestLicenceRetiredWorkersAreNotWired: FINRA (Reg SHO volume and short
// interest), Cboe put/call and FRED were retired on 22 Oct 2026, and the
// congress-poller (whose every stored row came from Kadoa) on 23 Oct 2026,
// because their terms do not permit SignalDeck's use. The waves that used to
// build their pollers must not build them any more.
func TestLicenceRetiredWorkersAreNotWired(t *testing.T) {
	retired := map[string]bool{"finra-shorts": true, "finra-shortint": true, "cboe-pc": true, "fred-poller": true,
		"congress-poller": true}
	cfg := config.Config{AlpacaKey: "k", AlpacaSecret: "s"}
	var fleet []workers.Worker
	fleet = append(fleet, freeDataWorkers(cfg, nil, edgar.New())...)
	fleet = append(fleet, finraShortsWorkers(nil)...)
	fleet = append(fleet, dataExpansionWorkers(nil)...)
	fleet = append(fleet, congressWorkers(nil)...)
	if len(fleet) == 0 {
		t.Fatal("no workers built; the scan would pass vacuously")
	}
	for _, w := range fleet {
		if retired[w.Name()] {
			t.Errorf("%s is still wired; its source was retired for licence reasons", w.Name())
		}
	}
	// run() also appends workers inline and through ~40 other waves, so the
	// constructors above are not the whole fleet. No source file of this
	// package may construct a retired poller at all.
	ctor := regexp.MustCompile(`pipeline\.(ShortVolPoller|ShortInterestPoller|CboePCPoller|FredPoller|CongressPoller)\{`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil || strings.HasSuffix(f, "_test.go") {
			continue
		}
		if m := ctor.Find(src); m != nil {
			t.Errorf("%s constructs %s; its source was retired for licence reasons", f, m)
		}
	}
	if len(files) < 5 {
		t.Fatalf("scanned only %d files; the glob is broken", len(files))
	}
}
