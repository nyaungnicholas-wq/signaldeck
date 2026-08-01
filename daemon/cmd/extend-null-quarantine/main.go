// One-shot: admit the eight already-written, un-baselined structural outcome
// rows into the null quarantine and record the amendment on the prereg chain.
//
// WHY THIS EXISTS. FreezeNullQuarantine is one-shot by design, because a freely
// growable exemption is the guard switched off on a delay. That design assumed
// divergence is transient: deploy the correct binary and no new unmatched rows
// appear. It had no remedy for rows ALREADY written during a divergence window,
// and eight of them (vol21/liquidity21, 2026-07-28..29, written by a binary
// predating revision stamping) permanently blocked regime-outcome-runner. While
// that worker is blocked NOTHING is ever graded -- `correct` and `resolved_at`
// are NULL on all 19,058 outcome rows -- so the measurement the whole system
// exists to produce cannot begin.
//
// The alternatives were worse. Backfilling a label is forbidden outright: a
// persistence baseline computed after the outcome is known is hindsight, not a
// null. Deleting the rows would destroy the record that those calls were made,
// in a system whose stated value is completeness.
//
// So: the rows stay, keep their NULL baseline, keep grading NO BASELINE, and are
// excluded from every structural denominator -- exactly what the original
// quarantine does. The amendment is enumerated (every id named, any drift
// aborts), digest-covered, and appended to the pre-registration chain so it can
// never be a quiet exemption.
//
// Usage (daemon stopped -- this takes the single-writer connection):
//
//	go run ./cmd/extend-null-quarantine -db ../data/signaldeck.db -ids 16221,16282,...
//	go run ./cmd/extend-null-quarantine -db ../data/signaldeck.db -ids ... -apply
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func main() {
	dbPath := flag.String("db", "data/signaldeck.db", "path to signaldeck.db")
	idsCSV := flag.String("ids", "", "comma-separated regime_outcomes.id values to admit (required)")
	apply := flag.Bool("apply", false, "actually write; without it this is a dry run")
	flag.Parse()

	if *idsCSV == "" {
		log.Fatal("-ids is required: an amnesty must enumerate exactly what it forgives")
	}
	var ids []int64
	for _, f := range strings.Split(*idsCSV, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64)
		if err != nil {
			log.Fatalf("bad id %q: %v", f, err)
		}
		ids = append(ids, n)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	before, err := st.UnmatchedNullCount(ctx)
	if err != nil {
		log.Fatalf("count before: %v", err)
	}
	m0, _, err := st.NullQuarantineManifest(ctx)
	if err != nil {
		log.Fatalf("manifest: %v", err)
	}
	fmt.Printf("unmatched before:   %d\n", before)
	fmt.Printf("manifest before:    %d rows, digest %s\n", m0.NRows, m0.Digest)
	fmt.Printf("ids named:          %d %v\n", len(ids), ids)

	if !*apply {
		fmt.Println("\nDRY RUN — nothing written. Re-run with -apply to execute.")
		return
	}

	now := time.Now().Unix()
	m, err := st.ExtendNullQuarantine(ctx, now, ids)
	if err != nil {
		log.Fatalf("extend: %v", err)
	}
	after, err := st.UnmatchedNullCount(ctx)
	if err != nil {
		log.Fatalf("count after: %v", err)
	}
	fmt.Printf("\nmanifest after:     %d rows, digest %s\n", m.NRows, m.Digest)
	fmt.Printf("unmatched after:    %d\n", after)

	if _, ok, err := st.VerifyNullQuarantine(ctx); err != nil || !ok {
		log.Fatalf("post-write verification FAILED: ok=%v err=%v", ok, err)
	}
	fmt.Println("VerifyNullQuarantine: ok")

	// The chain is what makes this an amendment rather than an exemption.
	spec := map[string]any{
		"amendment":   "null-quarantine-extension",
		"reason":      "rows written during a guard-divergence window; un-baselinable without hindsight",
		"outcomeIds":  ids,
		"kinds":       "vol21, liquidity21",
		"window":      "2026-07-28..2026-07-29",
		"priorDigest": m0.Digest,
		"priorNRows":  m0.NRows,
		"digest":      m.Digest,
		"nRows":       m.NRows,
		"relabelled":  false,
		"backfilled":  false,
		"deleted":     false,
		"gradesAs":    "NO BASELINE — excluded from every structural denominator",
		"nonGrowable": "each admitted id is enumerated; any drift in the live set aborts the amendment",
	}
	blob, err := json.Marshal(spec)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	note := fmt.Sprintf("AMENDMENT — null quarantine extended by %d enumerated row(s) written during a "+
		"guard-divergence window (2026-07-28..29). Nothing relabelled, backfilled or deleted; the rows "+
		"keep a NULL naive_label and grade NO BASELINE. Manifest %d rows digest %s (was %d rows %s). "+
		"Unblocks regime-outcome-runner, which had refused every pass and left all 19,058 outcome rows "+
		"ungraded.", len(ids), m.NRows, m.Digest, m0.NRows, m0.Digest)

	r, err := st.AppendPrereg(ctx, prereg.Record{
		Ts: now, Kind: "null-quarantine-manifest", SpecJSON: string(blob),
		SpecHash: fmt.Sprintf("%x", sha256Of(blob)), Note: note,
	})
	if err != nil {
		log.Fatalf("append prereg: %v", err)
	}
	fmt.Printf("prereg appended:    seq %d (entry %.12s...)\n", r.Seq, r.EntryHash)
}

func sha256Of(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
