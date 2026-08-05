// Command signaldeckd is the SignalDeck data daemon: ingest workers (crypto +
// stocks), the signal/expectancy/insight engines, trader-hud sync, and the
// JSON API — one binary, many in-app agents (see internal/workers).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
	"github.com/nyaungnicholas-wq/signaldeck/internal/logrotate"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const version = "0.1.0-dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	sicBulk := flag.Bool("sic-bulk-sync", false,
		"run ONE forced SIC bulk sync (SEC EDGAR bulk submissions.zip, ~1.5 GB streamed to SIGNALDECK_TMP) against the configured DB, print the result, and exit — stop the daemon first")
	flag.Parse()
	fmt.Printf("signaldeckd v%s\n", version)
	if *showVersion {
		return
	}

	cfg := config.Load()

	// Log slog to BOTH stderr and a size-capped rotating file (20 MB x 3).
	// SIGNALDECK_LOG_FILE overrides the path; set it to "" to disable file
	// logging entirely (stderr only, e.g. when launchd redirection is enough).
	logPath, hasLogEnv := os.LookupEnv("SIGNALDECK_LOG_FILE")
	if !hasLogEnv {
		logPath = filepath.Join(filepath.Dir(filepath.Dir(cfg.DBPath)), "logs", "signaldeckd.log")
	}
	if logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			slog.Warn("create log dir failed — logging to stderr only", "dir", filepath.Dir(logPath), "err", err)
		} else if lw, err := logrotate.New(logPath, logrotate.DefaultMaxMB, logrotate.DefaultKeep); err != nil {
			slog.Warn("open rotating log failed — logging to stderr only", "path", logPath, "err", err)
		} else {
			defer lw.Close() //nolint:errcheck
			slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, lw), nil)))
			slog.Info("file logging enabled", "path", logPath, "maxMB", logrotate.DefaultMaxMB, "keep", logrotate.DefaultKeep)
		}
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		slog.Error("create data dir", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		slog.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close() //nolint:errcheck

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Lineage spine (Layers 2+8): record the running build's VCS revision
	// (embedded by the Go toolchain — no git exec at runtime) so the DB knows
	// which code versions have operated on it; research producers stamp the
	// same rev into the lineage edges they write.
	if err := lineage.RecordBuildRevision(ctx, st); err != nil {
		slog.Warn("lineage: record build revision", "err", err)
	}

	// REFUSE to run an unattributable build. Every row this process freezes is
	// stamped with lineage.RevisionStamp(); when the stamp is empty or carries
	// the "+dirty" suffix, tools/accuracy_registry.py's revision_resolvable()
	// permanently refuses those rows, so the daemon would spend days producing
	// evidence no one can grade or reproduce. Same doctrine as store.Open
	// refusing an uncontracted schema: a worker whose output cannot be audited
	// must not report success. SIGNALDECK_ALLOW_DIRTY_BUILD is the development
	// override (`go run`, local iteration) and is deliberately explicit.
	if lineage.BuildModified() || lineage.BuildRevision() == "" {
		if _, dev := os.LookupEnv("SIGNALDECK_ALLOW_DIRTY_BUILD"); !dev {
			slog.Error("refusing to start: build is unattributable — rows it writes cannot be graded",
				"revision_stamp", lineage.RevisionStamp(),
				"vcs_modified", lineage.BuildModified(),
				"fix", "deploy via ops/signaldeck-ctl.sh deploy (builds from `git archive HEAD`)",
				"override", "SIGNALDECK_ALLOW_DIRTY_BUILD=1 for development only")
			os.Exit(1)
		}
		slog.Warn("SIGNALDECK_ALLOW_DIRTY_BUILD set: running an unattributable build; rows it writes are ungradable",
			"revision_stamp", lineage.RevisionStamp())
	}

	// REFUSE a build whose commit no longer exists. The check above asks "was
	// my tree dirty"; this one asks "is my commit still here", and they are
	// different failures. A clean build stamps a real 40-hex commit, and then a
	// rebase or amend removes it — from that moment the daemon writes rows
	// naming source nobody can produce, and the grader's revision gate refuses
	// the whole predictor forever. Three such stamps put 946 rows into the
	// ledger on 2026-08-02, and nothing noticed until an audit read the gate.
	// Only a PROVEN absence stops startup: when git cannot be consulted the
	// question is unanswerable here, and an unanswerable question must not be
	// read as a refusal.
	if ok, checked := lineage.BuildReachable(ctx, ""); checked && !ok {
		if _, dev := os.LookupEnv("SIGNALDECK_ALLOW_DIRTY_BUILD"); !dev {
			slog.Error("refusing to start: this build's commit is not in the repository — "+
				"history was rewritten under it, so rows it writes cannot be graded",
				"revision", lineage.BuildRevision(),
				"fix", "rebuild from a commit that exists: ops/signaldeck-ctl.sh deploy",
				"override", "SIGNALDECK_ALLOW_DIRTY_BUILD=1 for development only")
			os.Exit(1)
		}
		slog.Warn("SIGNALDECK_ALLOW_DIRTY_BUILD set: this build's commit is absent from the repository; rows it writes are ungradable",
			"revision", lineage.BuildRevision())
	}

	// Manual one-shot: SIC bulk sync (Stage 4). Runs the sic-bulk-sync worker
	// once with the gate forced, prints its honest detail line, and exits —
	// the fleet is never started.
	if *sicBulk {
		detail, err := runSICBulkOnce(ctx, st)
		if err != nil {
			slog.Error("sic-bulk-sync", "err", err)
			os.Exit(1)
		}
		fmt.Println(detail)
		return
	}

	slog.Info("signaldeckd started", "db", cfg.DBPath, "http", cfg.HTTPAddr,
		"alpaca", cfg.HasAlpaca(), "hud", cfg.HudURL)

	// Workers + API are wired in as their packages land (see run.go).
	run(ctx, cfg, st)

	// Why this is not just `return`: the daemon has two ways to stop and they
	// mean opposite things, but both used to exit 0 and neither was logged, so
	// a dead daemon left no evidence of which had happened.
	//
	//  1. The shutdown context fired — an operator stop, or on Windows any
	//     console control event (CTRL_CLOSE/CTRL_LOGOFF are delivered as
	//     os.Interrupt). Exit 0 is correct.
	//  2. run() returned while ctx is still live — an internal path bailed out
	//     (failed user bootstrap, fatal wiring error). Those paths log and
	//     `return`, so the process exited 0 and Task Scheduler read a fatal
	//     fault as "completed successfully" — which is why the configured
	//     restart-on-failure policy (RestartCount=999) had never once fired.
	//
	// Exiting non-zero on case 2 is what makes that existing policy work.
	if ctx.Err() != nil {
		slog.Info("signaldeckd stopped: shutdown signal received", "cause", ctx.Err())
		return
	}
	slog.Error("signaldeckd stopped WITHOUT a shutdown signal — internal fault; " +
		"exiting non-zero so the supervisor restarts it")
	os.Exit(1)
}
