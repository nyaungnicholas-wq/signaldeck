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
//	2  undetermined — the check could not run; the caller must FAIL OPEN and
//	                  publish, matching the HTTP handler, which ignores this
//	                  error rather than wedging the surface shut on a transient
//	                  database failure.
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
		os.Exit(2) // undetermined — the caller publishes
	}
	if collapsed {
		fmt.Println(reason)
		os.Exit(1)
	}
	fmt.Println("no collapsed cross-section in the graded window")
	os.Exit(0)
}
