package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ── schema verification ─────────────────────────────────────────────────
//
// WHY THIS EXISTS. schema.sql is applied with CREATE TABLE IF NOT EXISTS, so a
// column added to a CREATE statement instead of to migrate() is a PERMANENT
// NO-OP on any database that already has the table. A fresh test database gets
// the column (the CREATE runs for real); the production database never does.
// Tests pass, the writer's INSERT silently drops the value or errors into a
// swallowed path, and nothing in the system can tell the two databases apart.
// That is the exact mechanism by which frozen research judgments and frozen
// nulls were written into columns production did not have.
//
// verifySchema closes that hole mechanically: it reads the DECLARED shape out
// of the embedded schema.sql and compares it to the shape the opened database
// actually has, and refuses to hand back a Store when they differ. It can only
// ever PREVENT work — it never edits a threshold, a label, or a result.

// declaredTable is one CREATE TABLE statement's declared shape.
type declaredTable struct {
	name    string
	columns []string
}

var (
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["'` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)["'` + "`" + `\]]?\s*\(`)
	identRe       = regexp.MustCompile(`^["'` + "`" + `\[]?([A-Za-z_][A-Za-z0-9_]*)`)
)

// tableConstraintKeywords open a table-level constraint clause rather than a
// column definition, so the token after them is not a column name.
var tableConstraintKeywords = map[string]bool{
	"primary": true, "unique": true, "check": true, "foreign": true,
	"constraint": true,
}

// stripSQLComments removes `--` line comments (schema.sql is heavily annotated;
// a comment body must never be mistaken for a column).
func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// parseDeclaredSchema extracts every CREATE TABLE and its column names from SQL
// text. Only tables are parsed: indexes carry no units a writer can lose.
func parseDeclaredSchema(sqlText string) []declaredTable {
	src := stripSQLComments(sqlText)
	var out []declaredTable
	for _, m := range createTableRe.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[2]:m[3]]
		// Body runs from the opening paren (end of the match) to its match.
		depth := 1
		i := m[1]
		start := i
		for ; i < len(src) && depth > 0; i++ {
			switch src[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth != 0 {
			continue // unbalanced; refuse to guess
		}
		body := src[start : i-1]
		out = append(out, declaredTable{name: name, columns: parseColumns(body)})
	}
	return out
}

// parseColumns splits a CREATE TABLE body on top-level commas and takes the
// leading identifier of each definition that is not a table-level constraint.
func parseColumns(body string) []string {
	var cols []string
	depth := 0
	start := 0
	flush := func(seg string) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return
		}
		m := identRe.FindStringSubmatch(seg)
		if m == nil {
			return
		}
		// A table-level constraint may be written with or without a space
		// before its paren ("UNIQUE(a,b)"), so compare the leading IDENTIFIER,
		// never the whitespace-split first field.
		if tableConstraintKeywords[strings.ToLower(m[1])] {
			return
		}
		cols = append(cols, m[1])
	}
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				flush(body[start:i])
				start = i + 1
			}
		}
	}
	flush(body[start:])
	return cols
}

// liveColumns returns the columns the opened database actually has for table,
// and whether the table exists at all.
func liveColumns(q interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}, table string) (map[string]bool, bool, error) {
	var n int
	if err := q.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
		return nil, false, err
	}
	if n == 0 {
		return nil, false, nil
	}
	rows, err := q.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close() //nolint:errcheck
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, true, err
		}
		cols[c] = true
	}
	return cols, true, rows.Err()
}

// verifySchema compares the embedded declaration against the opened database
// and returns a hard error naming EVERY declared-but-absent table and column.
// Called immediately after migrate(): at that point the two must agree, and any
// disagreement means a declared unit has nowhere to be recorded.
func verifySchema(w *sql.DB) error {
	var missing []string
	for _, t := range parseDeclaredSchema(schemaSQL) {
		cols, exists, err := liveColumns(w, t.name)
		if err != nil {
			return err
		}
		if !exists {
			missing = append(missing, "table "+t.name)
			continue
		}
		for _, c := range t.columns {
			if !cols[c] {
				missing = append(missing, t.name+"."+c)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("schema divergence: %d declared object(s) absent from the database "+
		"(a column added to schema.sql instead of to migrate() never lands on an existing DB): %s",
		len(missing), strings.Join(missing, ", "))
}

// ── the worker/grader schema contract ───────────────────────────────────

// SchemaContract maps a worker (or grader) name to the storage objects it reads
// or writes, as "table" or "table.column". A worker whose objects are absent
// cannot record what it claims to judge: its writes no-op or error into a
// status='ok' run, which is indistinguishable from honest work. Rather than
// start it, the daemon refuses to REGISTER it and says so.
//
// This is the generalization of the per-worker readiness check that was
// previously written by hand for one worker. Entries name only objects the
// worker genuinely depends on — an over-broad entry would disable a worker that
// is in fact able to do its job, which is its own kind of dishonesty.
var SchemaContract = map[string][]string{
	"research-loop": {
		"research_loop_runs.corpus_coverage",
		"research_loop_hypotheses.null_p0",
		"research_loop_hypotheses.null_weeks",
		"research_loop_judgments",
	},
	"regime-outcome-runner": {
		"regime_outcomes.naive_label",
		"regime_outcomes.revision",
		"regime_outcomes.superseded_by",
		"regime_outcome_quarantine",
		"regime_outcome_quarantine_manifest",
	},
	// Both nulls are NOT NULL in the schema, so naming them here means the
	// worker is DROPPED at boot on a database that predates them rather than
	// running and failing every write. The revision column is what ties a row
	// to the binary that produced it.
	"rv-forecast-runner": {
		"rv_forecasts.null_rw",
		"rv_forecasts.null_ewma",
		"rv_forecasts.revision",
	},
	"rv-outcome-runner": {
		"rv_forecasts.actual",
		"rv_forecasts.ungradable",
	},
	"prediction-runner":   {"prediction_ledger.revision"},
	"prediction-resolver": {"prediction_ledger.revision"},
}

// AuditRecordWorkers names the workers whose ENTIRE output is an audit record.
// For an ordinary worker a missing object costs a feature: the daemon can drop
// it, keep running, and the loss is visible as an absent surface. For these,
// the record IS the work — a research pass that judges 48 rules over 166k
// observations and stores nothing leaves a status='ok' run narrating an honest
// null it wrote down nowhere, indistinguishable from a disciplined engine
// returning a true null. De-registration is too quiet for that: it hides the
// gap behind a worker that merely "isn't running". So an unmet contract here
// ABORTS startup naming the object, the same way verifySchema aborts on a
// declared/actual column mismatch. Like verifySchema, this gate can only ever
// STOP the process — it cannot change a threshold, a label, or any result.
var AuditRecordWorkers = map[string]bool{
	"research-loop":         true,
	"regime-outcome-runner": true,
}

// VerifyAuditContract returns a hard error if any object contracted by an
// audit-record worker is absent. Called from Open: at that moment the schema
// has been applied and migrated, so an absence is real and permanent for this
// database, and boot is the only moment it can still be acted on.
func (s *Store) VerifyAuditContract(ctx context.Context) error {
	var workers []string
	for w := range AuditRecordWorkers {
		workers = append(workers, w)
	}
	sort.Strings(workers)
	var problems []string
	for _, w := range workers {
		missing, err := s.MissingObjects(ctx, SchemaContract[w])
		if err != nil {
			return err
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			problems = append(problems, w+" requires "+strings.Join(missing, ", "))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("audit-record schema contract unmet — refusing to start, because these workers "+
		"would run, judge, store nothing, and still report status='ok': %s",
		strings.Join(problems, "; "))
}

// SchemaContractMetaKey holds the boot-time refusal set as JSON
// (worker → missing objects); "{}" means every contracted worker was able to
// register. /api/health publishes it, so a refusal is visible without reading
// the daemon log.
const SchemaContractMetaKey = "schema_contract_refusals"

// MissingObjects returns, for each named object, "" if present or a reason if
// absent. Objects are "table" or "table.column".
func (s *Store) MissingObjects(ctx context.Context, objects []string) ([]string, error) {
	var missing []string
	for _, o := range objects {
		table, col, hasCol := strings.Cut(o, ".")
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			missing = append(missing, o)
			continue
		}
		if !hasCol {
			continue
		}
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			missing = append(missing, o)
		}
	}
	return missing, nil
}

// ContractViolations checks every worker in SchemaContract that appears in
// names, returning worker → the objects it needs and the database lacks. An
// empty result means every listed worker can record what it judges.
func (s *Store) ContractViolations(ctx context.Context, names []string) (map[string][]string, error) {
	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}
	out := map[string][]string{}
	for worker, objs := range SchemaContract {
		if !present[worker] {
			continue
		}
		missing, err := s.MissingObjects(ctx, objs)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			out[worker] = missing
		}
	}
	return out, nil
}
