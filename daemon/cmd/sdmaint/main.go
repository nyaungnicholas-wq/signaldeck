// sdmaint — offline database maintenance for SignalDeck.
//
// Run with the daemon STOPPED (zero readers) so it can do in one uncontended
// pass what the hourly workers can't finish under live read pressure:
//   - scores + composite_scores: archive-then-strip the heavy JSON blobs
//     (the exact ScoresCompactor path — archive-before-strip fail-safe, no row
//     deletion unless a row is older than the intraday window)
//   - WAL checkpoint(TRUNCATE) — collapses the WAL file the live governor
//     can never truncate while readers hold it
//   - VACUUM — returns the freed pages to the filesystem
//
// It reuses the daemon's own store + archive packages, so the cold-archive
// format and every safety invariant match production exactly. Reversible: the
// stripped blobs are written to data/archive/*.csv.gz first, and a full DB
// backup predates the run.
//
// Subcommands:
//
//	sdmaint [flags]                     — the compaction pass above (default)
//	sdmaint apply-delistings [flags]    — apply a delisted_at backfill plan
//	sdmaint build-universe [flags]      — materialise the point-in-time
//	                                      universe_membership from daily bars
//	                                      emitted by tools/backfill_delistings.py
//	sdmaint ledger-verify [flags]       — recompute the prediction-ledger chain
//	                                      and re-verify every signed anchor on an
//	                                      arbitrary DB copy (used by
//	                                      ops/restore-rehearsal.sh against the
//	                                      isolated temp restore, H8)
//	sdmaint research-loop [--force]    — run ONE autonomous research-loop pass
//	                                      synchronously and print the grid size,
//	                                      divisor, corrected alpha, judged count
//	                                      and per-gate rejection tally it wrote
//	                                      (the engine was otherwise only
//	                                      reachable by waiting for a UTC day
//	                                      boundary, which is how its tables sat
//	                                      empty unnoticed)
//	sdmaint storage-report [flags]      — per-table dbstat sizes + WAL/backup/log
//	                                      footprint vs declared budgets; exits 1
//	                                      when any budget is exceeded (called
//	                                      nightly by ops/signaldeck-refresh.sh so
//	                                      growth regressions page instead of
//	                                      being discovered at 13GB)
//
// apply-delistings exists so the Python planner can stay strictly read-only
// (mode=ro, like accuracy_registry.py): every write goes through
// store.MarkDelisted on the store's single writer connection, preserving the
// one-writer discipline instead of opening a second writer from Python.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nyaungnicholas-wq/signaldeck/internal/archive"
	"github.com/nyaungnicholas-wq/signaldeck/internal/maintain"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "apply-delistings" {
		if err := applyDelistings(os.Args[2:]); err != nil {
			log.Fatalf("apply-delistings: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "repair-added-at" {
		if err := repairAddedAt(os.Args[2:]); err != nil {
			log.Fatalf("repair-added-at: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "split-reused-tickers" {
		if err := splitReusedTickers(os.Args[2:]); err != nil {
			log.Fatalf("split-reused-tickers: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "purge-bad-bars" {
		if err := purgeBadBars(os.Args[2:]); err != nil {
			log.Fatalf("purge-bad-bars: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "build-universe" {
		if err := buildUniverse(os.Args[2:]); err != nil {
			log.Fatalf("build-universe: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "import-delisted" {
		if err := importDelisted(os.Args[2:]); err != nil {
			log.Fatalf("import-delisted: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "ledger-verify" {
		if err := ledgerVerify(os.Args[2:]); err != nil {
			log.Fatalf("ledger-verify: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "research-loop" {
		if err := researchLoopOnce(os.Args[2:]); err != nil {
			log.Fatalf("research-loop: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "paper-epochs" {
		if err := paperEpochs(os.Args[2:]); err != nil {
			log.Fatalf("paper-epochs: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "confluence-repair" {
		if err := confluenceRepair(os.Args[2:]); err != nil {
			log.Fatalf("confluence-repair: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "adjudicate-delistings" {
		if err := adjudicateDelistings(os.Args[2:]); err != nil {
			log.Fatalf("adjudicate-delistings: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "quarantine-flat-pads" {
		if err := quarantineFlatPadsCmd(os.Args[2:]); err != nil {
			log.Fatalf("quarantine-flat-pads: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "storage-report" {
		over, err := storageReport(os.Args[2:])
		if err != nil {
			log.Fatalf("storage-report: %v", err)
		}
		if over {
			os.Exit(1)
		}
		return
	}

	// UNKNOWN SUBCOMMAND IS FATAL, and this guard is not decoration.
	//
	// The dispatch above is a chain of `if os.Args[1] == "..."` with no else, so
	// an unmatched token used to FALL THROUGH to the default compaction pass —
	// which strips score blobs and VACUUMs. Worse, flag.Parse() stops at the
	// first non-flag argument, so every flag after the unknown token was
	// silently discarded and -db kept its default: the run targeted
	// data/signaldeck.db no matter which database the operator named.
	//
	// This is not hypothetical. On 2026-08-04 a reviewer ran
	//   sdmaint split-reused-tickers -db <a snapshot> -plan <plan> -dry-run
	// against a bin/sdmaint.exe built before that subcommand existed. It
	// compacted and VACUUMed the LIVE database while the daemon was running,
	// took it from 4.54 GB to 4.26 GB, never opened the snapshot named in -db,
	// and honoured neither -plan nor -dry-run. Nothing was lost — the
	// archive-before-strip fail-safe held and quick_check stayed ok — but a
	// stale binary plus a typo should never be able to reach a destructive
	// default. A bare first argument is always a subcommand; if it matched
	// nothing, the operator meant something this binary cannot do.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		log.Fatalf("unknown subcommand %q.\n"+
			"This binary knows: apply-delistings, repair-added-at, build-universe,\n"+
			"split-reused-tickers, purge-bad-bars, import-delisted, ledger-verify,\n"+
			"research-loop, "+
			"paper-epochs, storage-report, quarantine-flat-pads.\n"+
			"If you expected one of these to exist, rebuild: go build -o bin/sdmaint ./cmd/sdmaint\n"+
			"Refusing to fall through to the default compaction pass, which would\n"+
			"strip blobs and VACUUM data/signaldeck.db and ignore every flag you passed.",
			os.Args[1])
	}

	dbPath := flag.String("db", "data/signaldeck.db", "path to signaldeck.db")
	heavyD := flag.Int("heavy", 2, "keep full component/payload blobs this many days")
	intradayD := flag.Int("intraday", 30, "keep intraday rows this many days (daily-downsample beyond)")
	doVacuum := flag.Bool("vacuum", true, "VACUUM after compaction")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close() //nolint:errcheck

	before, walBefore := st.FileSizes()
	fmt.Printf("before: db=%.0fMB wal=%.0fMB\n", mb(before), mb(walBefore))

	arc := archive.New(archive.Dir(*dbPath))
	ctx := context.Background()

	c := &maintain.ScoresCompactor{
		St:           st,
		Arc:          arc,
		KeepHeavy:    time.Duration(*heavyD) * 24 * time.Hour,
		KeepIntraday: time.Duration(*intradayD) * 24 * time.Hour,
	}
	fmt.Println("compacting scores + composite_scores (offline, uncontended)…")
	start := time.Now()
	detail, err := c.Run(ctx)
	if err != nil {
		log.Fatalf("compact: %v", err)
	}
	fmt.Printf("compaction: %s (%.1fs)\n", detail, time.Since(start).Seconds())

	if res, err := st.WALCheckpointTruncate(ctx); err != nil {
		log.Printf("checkpoint: %v", err)
	} else {
		fmt.Printf("wal checkpoint: %+v\n", res)
	}

	if *doVacuum {
		fmt.Println("VACUUM (rewrites the file — this is the slow part)…")
		vs := time.Now()
		if err := st.Vacuum(ctx); err != nil {
			log.Fatalf("vacuum: %v", err)
		}
		fmt.Printf("vacuum done (%.1fs)\n", time.Since(vs).Seconds())

		// VACUUM regrows the WAL it just rewrote through; collapse it again so
		// the sweep hands back a DB with both files at their floor instead of
		// leaving a fresh multi-hundred-MB WAL for the daemon to inherit.
		if res, err := st.WALCheckpointTruncate(ctx); err != nil {
			log.Printf("post-vacuum checkpoint: %v", err)
		} else {
			fmt.Printf("post-vacuum wal checkpoint: %+v\n", res)
		}
	}

	after, walAfter := st.FileSizes()
	fmt.Printf("after:  db=%.0fMB wal=%.0fMB  (freed %.0fMB)\n",
		mb(after), mb(walAfter), mb(before-after))
}

func mb(b int64) float64 { return float64(b) / (1024 * 1024) }

// storageReport prints where the disk actually goes — per-table dbstat MB
// (indexes folded into their owning table), WAL file size, backup dir size,
// log dir size — and checks each surface against a declared budget. It returns
// over=true when any budget is exceeded so main can exit non-zero, which is
// what lets ops/signaldeck-refresh.sh page through the existing notify path
// the night a growth regression starts instead of when the disk fills.
//
// The DB is opened read-only (mode=ro, like the Python audit tools), NOT via
// store.Open: this runs nightly, possibly alongside a live daemon, and must
// never take the writer, apply migrations, or touch the file.
func storageReport(args []string) (over bool, err error) {
	fs := flag.NewFlagSet("storage-report", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	backupsDir := fs.String("backups", "", "backup directory (default: <db dir>/backups)")
	logsDir := fs.String("logs", "", "log directory (default: <db dir>/../logs)")
	top := fs.Int("top", 12, "show this many largest tables")
	// Budgets (MB). Declared here so "what is normal" lives in one place:
	// WAL settles at journal_size_limit (64MB) but a reader-pinned WAL was
	// observed at 256MB, backups are KEEP=2 x ~2GB plus a premaint copy under
	// ops/signaldeck-cleanup.sh, and logs rotate well under 100MB.
	//
	// THE DB BUDGET WAS RAISED 4096 -> 6144 ON 2026-08-05, and the reason has to
	// be on the record because raising a budget is otherwise indistinguishable
	// from silencing a gate.
	//
	// The old comment read "DB floor is ~2.2GB after compaction". That was
	// measured against a ~1,070-symbol universe; the universe is now 2,940
	// symbols carrying 15.4M daily bars, and the floor moved with it. A full
	// offline pass was run — daemon stopped, uncontended — precisely to find
	// out whether 4,762MB was bloat or data:
	//
	//	compaction: stripped 516 scores + 86 composite blobs;
	//	            daily-downsampled 3456 + 0 intraday rows
	//	after:  db=4604MB wal=0MB  (freed 158MB)
	//
	// 158MB. There is no bloat to reclaim: scores 1,408MB and bars 1,149MB are
	// retained data, not garbage. So 4096 was a threshold the system could not
	// meet even in its best possible state, and a budget that can only ever
	// report OVER is not a budget — it is a nightly page that teaches the reader
	// to ignore red, the same failure internal/hud documents for the fleet.
	//
	// 6144 = the measured post-compaction floor (4,606MB) plus ~1.5GB. The
	// headroom is sized on OBSERVED inter-compaction accumulation, which is
	// 158MB (this run) and 280MB (the 2026-08-04 pass, 4.54 -> 4.26GB) — so a
	// surface that genuinely runs away still trips it with an order of magnitude
	// to spare.
	//
	// What this does NOT settle: whether 1.4GB of `scores` and 15.4M bars are
	// worth keeping. That is a retention question for a human, and moving the
	// threshold does not answer it — it just stops the alert from drowning it.
	budDB := fs.Int64("budget-db-mb", 6144, "database file budget, MB")
	budWAL := fs.Int64("budget-wal-mb", 512, "WAL file budget, MB")
	budBak := fs.Int64("budget-backups-mb", 12288, "backup directory budget, MB")
	budLog := fs.Int64("budget-logs-mb", 512, "log directory budget, MB")
	// One rollback copy beside the live database is normal before a migration;
	// five that nobody deleted is the defect. Budget is one DB's worth.
	budSide := fs.Int64("budget-sidecars-mb", 4096, "budget for ad-hoc .bak/.premigration copies beside the db, MB")
	// data/archive is where the compactor writes blobs BEFORE stripping them —
	// the archive-before-strip fail-safe. It therefore grows every time this
	// tool reclaims space, which makes "the DB got smaller" and "the disk got
	// smaller" different statements. It was in no budget at all (384MB at the
	// 2026-08-05 pass), the same unmeasured-surface hole the sidecars line was
	// added to close. Budgeted generously: it is cold, compressed, and the
	// point is to notice a runaway, not to police it.
	archiveDir := fs.String("archive", "", "archive directory (default: <db dir>/archive)")
	budArc := fs.Int64("budget-archive-mb", 2048, "cold-archive directory budget, MB")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *archiveDir == "" {
		*archiveDir = filepath.Join(filepath.Dir(*dbPath), "archive")
	}
	if *backupsDir == "" {
		*backupsDir = filepath.Join(filepath.Dir(*dbPath), "backups")
	}
	if *logsDir == "" {
		*logsDir = filepath.Join(filepath.Dir(*dbPath), "..", "logs")
	}

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", *dbPath))
	if err != nil {
		return false, fmt.Errorf("open ro: %w", err)
	}
	defer db.Close() //nolint:errcheck
	ctx := context.Background()

	// dbstat is per-btree; fold each index into its owning table via
	// sqlite_master so the report answers "which TABLE owns the disk".
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(m.tbl_name, d.name) AS tbl, SUM(d.pgsize) AS bytes
		FROM dbstat d LEFT JOIN sqlite_master m ON m.name = d.name
		GROUP BY tbl ORDER BY bytes DESC`)
	if err != nil {
		return false, fmt.Errorf("dbstat: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	type tblSize struct {
		name  string
		bytes int64
	}
	var tables []tblSize
	var tableTotal int64
	for rows.Next() {
		var t tblSize
		if err := rows.Scan(&t.name, &t.bytes); err != nil {
			return false, err
		}
		tables = append(tables, t)
		tableTotal += t.bytes
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	dbBytes := fileSize(*dbPath)
	walBytes := fileSize(*dbPath + "-wal")
	bakBytes := dirSize(*backupsDir)
	logBytes := dirSize(*logsDir)
	sideBytes := sidecarSize(*dbPath)
	arcBytes := dirSize(*archiveDir)

	fmt.Printf("storage report — %s\n", *dbPath)
	fmt.Printf("%-28s %10s %6s\n", "table (incl. indexes)", "MB", "%")
	shown := len(tables)
	if *top > 0 && shown > *top {
		shown = *top
	}
	var shownBytes int64
	for _, t := range tables[:shown] {
		fmt.Printf("%-28s %10.1f %5.1f%%\n", t.name, mb(t.bytes), 100*float64(t.bytes)/float64(tableTotal))
		shownBytes += t.bytes
	}
	if rest := tableTotal - shownBytes; rest > 0 {
		fmt.Printf("%-28s %10.1f %5.1f%%  (%d tables)\n", "(other)", mb(rest), 100*float64(rest)/float64(tableTotal), len(tables)-shown)
	}

	fmt.Println()
	check := func(name string, bytes, budgetMB int64) {
		status := "ok"
		if mb(bytes) > float64(budgetMB) {
			status = "OVER BUDGET"
			over = true
		}
		fmt.Printf("%-12s %8.0fMB / %6dMB  %s\n", name, mb(bytes), budgetMB, status)
	}
	check("db", dbBytes, *budDB)
	check("wal", walBytes, *budWAL)
	check("backups", bakBytes, *budBak)
	check("sidecars", sideBytes, *budSide)
	check("archive", arcBytes, *budArc)
	check("logs", logBytes, *budLog)
	if over {
		fmt.Println("RESULT: OVER BUDGET — a storage surface outgrew its declared budget")
	} else {
		fmt.Println("RESULT: all storage surfaces within budget")
	}
	return over, nil
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// sidecarSize sums the ad-hoc copies that accumulate BESIDE the live database
// — signaldeck.db.bak-presplit-20260804, .premigration, .loopsnapshot and the
// -wal/-shm each drags along. They are made by hand and by ops scripts before
// a risky migration, and nothing ever removes them.
//
// This surface existed unmeasured. `db` reports only the live file and
// `backups` only <dbdir>/backups, so 15 GB of siblings sat in data/ counted by
// neither, which is how data/ reached 27 GB while the nightly report said
// "backups 6914MB / 12288MB ok". A budget that cannot see the thing that grew
// is not a budget.
//
// The predicate is the "<dbname>." prefix: the live triple is signaldeck.db,
// signaldeck.db-wal and signaldeck.db-shm (hyphen), so it excludes them
// without a special case, and picks up every sibling copy plus its journals.
func sidecarSize(dbPath string) int64 {
	dir, base := filepath.Dir(dbPath), filepath.Base(dbPath)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), base+".") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// dirSize sums regular-file sizes under root, best-effort: a vanished file or
// unreadable subdir must not fail the nightly report, just undercount it.
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// ledgerVerify re-verifies the prediction ledger's DOMAIN invariants on an
// arbitrary database copy: the hash chain recomputes intact, every signed
// anchor still reproduces under recomputation (the auditor's path — derived
// from payloads, ignoring stored hashes), and the chain is at least as long as
// the newest anchor committed to. It exists for ops/restore-rehearsal.sh (H8):
// the weekly drill must prove not just that a backup opens and has rows, but
// that the restored ledger still carries the anteriority evidence the live one
// claims — a restore that drops or truncates prediction_ledger/ledger_anchors
// passes PRAGMA checks and row-count floors while silently destroying exactly
// the record the anchors exist to protect.
//
// Opening through store.Open applies schema+migrations to the copy, which is
// deliberate: the rehearsal target is an isolated temp file, and "the daemon's
// own store can open this restore" is itself part of the drill.
func ledgerVerify(args []string) error {
	fs := flag.NewFlagSet("ledger-verify", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to the database copy to verify")
	allowNoAnchors := fs.Bool("allow-no-anchors", false,
		"pass on a ledger with zero anchors (edit-detection only, no anteriority)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	v, err := st.VerifyLedger(ctx)
	if err != nil {
		return fmt.Errorf("verify chain: %w", err)
	}
	if !v.Intact {
		return fmt.Errorf("ledger chain is NOT intact — broken at seq %d after %d rows", *v.BrokenAtSeq, v.Count)
	}
	fmt.Printf("chain: intact, %d rows, head=%.12s…\n", v.Count, v.HeadHash)

	// recompute=true: derive every anchored head from row payloads from
	// genesis, so a consistently-rewritten chain fails here even though the
	// stored hashes agree with each other.
	av, err := st.VerifyLedgerAnchors(ctx, 0, true)
	if err != nil {
		return fmt.Errorf("verify anchors: %w", err)
	}
	if av.AnchorCount == 0 {
		if *allowNoAnchors {
			fmt.Println("anchors: none (allowed by -allow-no-anchors) — chain proves edit-detection only")
			return nil
		}
		return fmt.Errorf("no ledger anchors in this copy — the live fleet anchors on a 6h cadence, so a restore with an empty ledger_anchors table means the anchor history did not survive backup/restore")
	}
	if av.FailingAnchors > 0 {
		return fmt.Errorf("%d of %d anchors no longer reproduce (first failing at ledger seq %d) — the restored history diverges from what was signed",
			av.FailingAnchors, av.Checked, *av.FirstFailingSeq)
	}
	newest := av.Anchors[0] // LedgerAnchors returns newest-first
	if !newest.SignatureOK {
		return fmt.Errorf("newest anchor (ledger seq %d) signature does not verify under its recorded public key", newest.Record.LedgerSeq)
	}
	if v.Count < newest.Record.LedgerCount {
		return fmt.Errorf("restored ledger has %d rows but the newest signed anchor committed to %d — rows are missing from the restore",
			v.Count, newest.Record.LedgerCount)
	}
	fmt.Printf("anchors: %d/%d verify (recomputed), newest signed count=%d at seq=%d — anteriority holds through seq %d\n",
		av.Checked, av.Checked, newest.Record.LedgerCount, newest.Record.LedgerSeq, *av.ProvenThroughSeq)
	return nil
}

// delistPlan is the JSON emitted by tools/backfill_delistings.py. Only the
// "updates" list is applied — the planner's "conflicts" (ticker reuse) are
// deliberately not even parsed, so a hand-edited plan can't smuggle them in
// under the field name the tooling would trust.
type delistPlan struct {
	Source  string `json:"source"`
	Updates []struct {
		SymbolID   int64  `json:"symbol_id"`
		Symbol     string `json:"symbol"`
		DelistedAt int64  `json:"delisted_at"`
		Date       string `json:"date"`
	} `json:"updates"`
}

// applyDelistings applies a backfill plan through store.MarkDelisted, which
// both rides the single writer connection and refuses to overwrite an existing
// marker (delisted_at IS NULL OR 0 guard) — so re-applying a plan is a no-op,
// and a plan built against a stale DB can't clobber the live detector's work.
func applyDelistings(args []string) error {
	fs := flag.NewFlagSet("apply-delistings", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	planPath := fs.String("plan", "", "plan JSON from tools/backfill_delistings.py")
	dryRun := fs.Bool("dry-run", false, "verify and report without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *planPath == "" {
		return fmt.Errorf("-plan is required")
	}

	raw, err := os.ReadFile(*planPath)
	if err != nil {
		return err
	}
	var plan delistPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}
	if len(plan.Updates) == 0 {
		return fmt.Errorf("plan has no updates")
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	// Snapshot current markers so the report can distinguish "marked by this
	// run" from "marked before it" (MarkDelisted's guard makes the latter
	// silent no-ops, which is correct for the DB but useless in a report).
	bars, err := st.StockLastBars(ctx)
	if err != nil {
		return fmt.Errorf("load current markers: %w", err)
	}
	marked := make(map[int64]bool, len(bars))
	for _, b := range bars {
		if b.DelistedAt != 0 {
			marked[b.SymbolID] = true
		}
	}

	var applied, alreadySet, mismatched, invalid int
	for _, u := range plan.Updates {
		if u.SymbolID <= 0 || u.DelistedAt <= 0 {
			invalid++
			continue
		}
		// Re-verify identity against the LIVE row: the plan carries both id and
		// ticker precisely so that a plan generated against an older copy of the
		// DB cannot stamp a date onto whatever row now owns that id.
		sym, err := st.GetSymbolByID(ctx, u.SymbolID)
		if err != nil || sym.Symbol != u.Symbol {
			mismatched++
			fmt.Printf("  MISMATCH id=%d plan=%q db=%q — skipped\n",
				u.SymbolID, u.Symbol, sym.Symbol)
			continue
		}
		if marked[u.SymbolID] {
			alreadySet++
			continue
		}
		if !*dryRun {
			if err := st.MarkDelisted(ctx, u.SymbolID, u.DelistedAt); err != nil {
				return fmt.Errorf("mark %s (id=%d): %w", u.Symbol, u.SymbolID, err)
			}
		}
		applied++
	}

	mode := "applied"
	if *dryRun {
		mode = "would apply (dry-run)"
	}
	fmt.Printf("delistings %s: %d, already marked: %d, id/ticker mismatches: %d, invalid rows: %d (source: %s)\n",
		mode, applied, alreadySet, mismatched, invalid, plan.Source)
	return nil
}

// researchLoopOnce runs ONE research-loop pass synchronously and prints what it
// judged.
//
// It exists because until now the only way to exercise the loop was to wait for
// a UTC day boundary with the daemon running, and the only readers of its two
// output tables were their own definitions. That combination is how
// research_loop_runs and research_loop_hypotheses stayed at zero rows — against
// a 166k-row research_weeks corpus — through an entire review round without
// anyone noticing. An engine nobody can run on demand and whose output nobody
// reads cannot be audited, including by its author.
//
// It changes no threshold and no verdict: it runs the same worker the scheduler
// runs, and prints the ledger it wrote.
func researchLoopOnce(args []string) error {
	fs := flag.NewFlagSet("research-loop", flag.ExitOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	force := fs.Bool("force", false,
		"clear the once-per-UTC-day gate so a pass runs now (it will overwrite today's row — the same day is the same look, not a second one)")
	maxC := fs.Int("max-candidates", 0, "grid cap (0 = the worker's default)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	if *force {
		// Clearing the day gate does NOT hand back a free look: the pass still
		// charges itself in the search counter, and today's judgment rows are
		// keyed on (day, rule) so a re-run overwrites the same day rather than
		// counting a second night of multiplicity.
		if err := st.SetMeta(ctx, "research_loop_last_day", ""); err != nil {
			return fmt.Errorf("clear day gate: %w", err)
		}
	}

	w := &pipeline.ResearchLoop{St: st, MaxCandidates: *maxC}
	detail, err := w.Run(ctx)
	if err != nil {
		return fmt.Errorf("research-loop pass: %w", err)
	}
	fmt.Println(detail)

	runs, err := st.LoopRuns(ctx, 1)
	if err != nil {
		return fmt.Errorf("read loop runs: %w", err)
	}
	if len(runs) == 0 {
		return fmt.Errorf("pass reported success but wrote no research_loop_runs row")
	}
	r := runs[0]
	fmt.Printf("\nrun %s: grid=%d judged=%d survivors=%d divisor=%d corrected-alpha=%.3g obs=%d\n",
		r.Day, r.GridSize, r.Judged, r.Survivors, r.Divisor, r.CorrectedAlpha, r.ObsCount)
	if r.RefusalReason != "" {
		fmt.Printf("  REFUSED: %s\n", r.RefusalReason)
	}

	// Per-gate tally for THIS pass, read back out of the append-only judgment
	// table rather than out of the in-memory candidates — the point of the
	// command is to prove what actually landed on disk.
	js, err := st.LoopJudgments(ctx, 100000)
	if err != nil {
		return fmt.Errorf("read judgments: %w", err)
	}
	today, gates := 0, map[string]int{}
	for _, j := range js {
		if j.Day != r.Day {
			continue
		}
		today++
		gate := j.RejectedBy
		if gate == "" {
			gate = "(survived)"
		}
		gates[gate]++
	}
	fmt.Printf("append-only judgments for %s: %d (of %d judged)\n", r.Day, today, r.Judged)
	for _, g := range sortedGates(gates) {
		fmt.Printf("  %-20s %d\n", g, gates[g])
	}
	if today != r.Judged {
		return fmt.Errorf("judgment ledger is incomplete: %d rows for %d judged rules",
			today, r.Judged)
	}

	all, err := st.LoopRejectionsByGate(ctx)
	if err != nil {
		return fmt.Errorf("read rejection tally: %w", err)
	}
	total := 0
	for _, n := range all {
		total += n
	}
	fmt.Printf("ledger totals: %d judgments across all days\n", total)
	return nil
}

// sortedGates orders gate names deterministically so two runs of the command
// are diffable.
func sortedGates(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
