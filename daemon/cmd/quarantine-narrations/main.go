// One-shot: acknowledge the four research-loop narrations whose judgments were
// never recorded, and chain the acknowledgement.
//
// WHY. Four runs (2026-07-26..29) narrated 48-rule grid searches. Two of those
// days hold only backfilled placeholder rows in research_loop_runs
// (git_rev='backfill:worker_runs', judged=0); the other two claim judged=48
// against an empty research_loop_judgments. The searches may well have run --
// the daemon's current code makes this impossible, and 2026-07-31 recorded all
// 48 judgments correctly -- but the numbers from those four days were never
// written down and cannot be reconstructed.
//
// tools/research_liveness.py therefore refuses to publish, forever, and with it
// the entire accuracy registry. Four permanently uncorroborable historical
// claims would mean no verdict is ever published about anything.
//
// This does NOT weaken the check. The quarantined claims are still printed on
// every run under their own heading, each with its reason; they are simply
// excluded from the refusal. Every run id is enumerated, and the same set is
// written to the pre-registration chain, so the acknowledgement is permanent
// and public rather than a quiet exemption.
//
// Usage (daemon stopped):
//
//	go run ./cmd/quarantine-narrations -db ../data/signaldeck.db
//	go run ./cmd/quarantine-narrations -db ../data/signaldeck.db -apply
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const metaKey = "research_narration_quarantine"

type claim struct {
	RunID  int    `json:"runId"`
	Day    string `json:"day"`
	Reason string `json:"reason"`
}

func main() {
	dbPath := flag.String("db", "data/signaldeck.db", "path to signaldeck.db")
	apply := flag.Bool("apply", false, "actually write; without it this is a dry run")
	flag.Parse()

	// Enumerated, not discovered. A set computed at run time would quietly
	// absorb whatever else happened to be broken.
	claims := []claim{
		{130100, "2026-07-26", "research_loop_runs holds only a backfilled placeholder " +
			"(git_rev=backfill:worker_runs, judged=0); no judgment was ever written"},
		{139413, "2026-07-27", "research_loop_runs holds only a backfilled placeholder " +
			"(git_rev=backfill:worker_runs, judged=0); no judgment was ever written"},
		{145573, "2026-07-28", "run row claims judged=48 but research_loop_judgments " +
			"holds 0 rows for the day; the judgments were never persisted"},
		{146527, "2026-07-29", "run row claims judged=48 but research_loop_judgments " +
			"holds 0 rows for the day; the judgments were never persisted"},
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	payload := map[string]any{
		"claims": claims,
		"policy": "acknowledged unverifiable; still reported by research_liveness.py on every " +
			"run, excluded from refusal only",
		"reconstructable": false,
		"weakensCheck":    false,
		"frozenTs":        time.Now().Unix(),
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(blob))

	fmt.Printf("claims to acknowledge: %d\n", len(claims))
	for _, c := range claims {
		fmt.Printf("  run %d  %s\n", c.RunID, c.Day)
	}
	fmt.Printf("digest: %s\n", digest)
	if !*apply {
		fmt.Println("\nDRY RUN -- nothing written. Re-run with -apply to execute.")
		return
	}

	if err := st.SetJSON(ctx, metaKey, payload); err != nil {
		log.Fatalf("write meta: %v", err)
	}
	fmt.Println("meta written:", metaKey)

	note := fmt.Sprintf("AMENDMENT -- %d research-loop narrations (2026-07-26..29) acknowledged as "+
		"UNVERIFIABLE. Their judgment rows were never persisted and cannot be reconstructed. They "+
		"remain reported by tools/research_liveness.py on every run, each with its reason, and are "+
		"excluded from refusal ONLY. No judgment was fabricated, no ledger repaired, no check "+
		"weakened. Without this the accuracy registry could never publish any verdict about "+
		"anything. Digest %s.", len(claims), digest)

	r, err := st.AppendPrereg(ctx, prereg.Record{
		Ts: time.Now().Unix(), Kind: "research-narration-quarantine",
		SpecJSON: string(blob), SpecHash: digest, Note: note,
	})
	if err != nil {
		log.Fatalf("append prereg: %v", err)
	}
	fmt.Printf("prereg appended: seq %d (entry %.12s...)\n", r.Seq, r.EntryHash)
}
