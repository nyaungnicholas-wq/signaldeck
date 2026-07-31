// One-shot: append grading-protocol record seq 19 restoring the four fields
// dropped by seq 18 (minDistinctBlocks, maxAlpha, clusterUnit, multiplicityRule).
//
// The record's spec_json is produced by prereg.GradingProtocol() with the
// committed grader's SHA-256 and commit, so it is byte-for-byte identical to
// what the grader's own constants produce. CheckAppendable will accept it
// because the candidate is at least as strict as StrictestProtocol (which
// preserves seq 14's floors via the fold).
//
// Run with the daemon stopped: it takes the store's single-writer connection.
//
// Usage:
//
//	go run ./cmd/append-prereg19 -db ../data/signaldeck.db
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func main() {
	dbPath := flag.String("db", "data/signaldeck.db", "path to signaldeck.db")
	graderSHA256 := flag.String("sha256", "", "SHA-256 of the grader file at the pinned commit")
	graderCommit := flag.String("commit", "", "git commit that contains the grader at the pinned digest")
	flag.Parse()

	if *graderSHA256 == "" || *graderCommit == "" {
		log.Fatal("-sha256 and -commit are required; obtain them from the committed grader")
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close() //nolint:errcheck

	ctx := context.Background()

	proto := prereg.GradingProtocol(*graderSHA256, *graderCommit)
	blob, err := json.Marshal(proto)
	if err != nil {
		log.Fatalf("marshal protocol: %v", err)
	}

	// Read the current chain head for the note.
	recs, err := st.PreregRecords(ctx)
	if err != nil {
		log.Fatalf("read chain: %v", err)
	}
	var lastSeq int64
	if len(recs) > 0 {
		lastSeq = recs[len(recs)-1].Seq
	}

	note := fmt.Sprintf("APPEND-19 — restore minDistinctBlocks=10, maxAlpha=0.05, clusterUnit, "+
		"multiplicityRule dropped by seq 18. Chain head at seq %d. Frozen floors are now "+
		"internally monotone again.", lastSeq)

	r, err := st.AppendPrereg(ctx, prereg.Record{
		Ts:       time.Now().Unix(),
		Kind:     prereg.ProtocolKind,
		SpecJSON: string(blob),
		SpecHash: proto.Hash(),
		Note:     note,
	})
	if err != nil {
		log.Fatalf("append prereg: %v", err)
	}

	fmt.Printf("appended seq %d (entry_hash %.12s…)\n", r.Seq, r.EntryHash)
	fmt.Printf("protocol hash: %s\n", proto.Hash())
	fmt.Printf("grader commit: %s\n", proto.GraderCommit)
	fmt.Printf("grader SHA-256: %s\n", proto.GraderSHA256)
	fmt.Printf("minDistinctBlocks: %d\n", proto.MinDistinctBlocks)
	fmt.Printf("maxAlpha: %g\n", proto.MaxAlpha)
	fmt.Printf("clusterUnit: %s\n", proto.ClusterUnit)
	fmt.Printf("multiplicityRule present: %v\n", proto.MultiplicityRule != "")
}
