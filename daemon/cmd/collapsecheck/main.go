// collapsecheck runs the accuracy PUBLICATION gate once and reports whether the
// window the grader just graded contains a collapsed cross-section.
//
// It exists so ops/accuracy-registry.sh can refuse to publish exactly what
// /api/accuracy refuses to serve. Before it, the HTTP surface returned 503
// REFUSED for this window while the generated live-accuracy block published the
// same rows into README.md and eight other documents.
//
//	go run -C daemon ./cmd/collapsecheck --registry ../data/accuracy_registry.json
//
// Exit status is the interface, so it composes into a shell gate:
//
//	0  publish      — no collapsed day in the graded window
//	1  refuse       — at least one collapsed day; the reason is on stdout
//	2  undetermined — the check could not run; the caller must WITHHOLD
//	                  publication and say the gate was unavailable.
//
// EXIT 2 USED TO MEAN "FAIL OPEN AND PUBLISH", matching what internal/api did
// at the time. Both have been reversed, together, and the reasoning is the same
// in both places: the gate exists to decide whether these rows may be served,
// so an exit that means "it never decided" cannot be spelled the same way as
// "it decided yes". The old contract also made the gate silently
// un-wireable — a renamed flag, a missing binary or a bad --db path all exit 2,
// and every one of them published. ops/accuracy-registry.sh already refuses on
// any non-zero exit from tools/deployment_drift.py for exactly this reason, and
// its comment there spells out the precedent: ops/research-liveness.sh passed a
// flag research_liveness.py never implemented, argparse exited 2, and the check
// "had never produced a verdict".
//
// The cost of the old wiring was never a transient database error. It was that
// nobody would ever find out.
//
// Callers MUST keep 1 and 2 apart on the published surface: 1 is a measured
// scientific refusal, 2 is a check outage, and reporting the second as the
// first invents a verdict.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/api"
	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func main() {
	registry := flag.String("registry", "", "path to accuracy_registry.json (required)")
	dbPath := flag.String("db", "", "database path (default: config)")
	flag.Parse()

	if *registry == "" {
		fmt.Fprintln(os.Stderr, "collapsecheck: --registry is required")
		os.Exit(2)
	}
	path := *dbPath
	if path == "" {
		path = config.Load().DBPath
	}
	st, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collapsecheck: open %s: %v\n", path, err)
		os.Exit(2)
	}
	defer st.Close() //nolint:errcheck

	reason, collapsed, err := api.CollapsedGradingWindow(
		context.Background(), st, *registry, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "collapsecheck: %v\n", err)
		os.Exit(2) // undetermined — the caller WITHHOLDS and says the gate was unavailable
	}
	if collapsed {
		fmt.Println(reason)
		os.Exit(1)
	}
	fmt.Println("no collapsed cross-section in the graded window")
	os.Exit(0)
}
