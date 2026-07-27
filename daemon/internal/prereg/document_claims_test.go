// PREREGISTRATION.md §0 says the chain governs where prose and chain disagree,
// but nothing here has ever MEASURED whether they disagree. So the document has
// been free to assert registrations that were never appended: a reader
// performing §7 step 1 finds a mismatch, and a reader trusting the prose finds
// nothing at all. This file makes the document unable to claim a registration
// the chain does not carry.
//
// It reads the chain, never writes it, and can only fail a build. It never
// repairs the document, never re-pins a digest, and never touches a verdict.
package prereg

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// A kind claim: "…under the kind `X`" / "registered under `X`". The document is
// asserting that a record of kind X exists on the chain.
var kindClaimRe = regexp.MustCompile("(?:under the kind|registered under) `([a-z0-9][a-z0-9-]*)`")

// A field claim: "frozen on the chain in the `K` record (`prereg.Protocol.A`,
// `prereg.Protocol.B`)". The document is asserting that the newest record of
// kind K actually carries those fields.
var fieldClaimRe = regexp.MustCompile("frozen on the chain in the `([a-z0-9][a-z0-9-]*)` record")

var protocolFieldRe = regexp.MustCompile("`prereg\\.Protocol\\.([A-Za-z0-9]+)`")

// "…alongside the existing `minIndependentN`, `minDistinctDays` and
// `minDistinctBlocks` floors" — same assertion in the document's other voice:
// these are JSON field names, claimed to be on the same record.
var alongsideRe = regexp.MustCompile("alongside the existing ((?:`[A-Za-z0-9]+`(?:, | and )?)+)")

var backtickedRe = regexp.MustCompile("`([A-Za-z0-9]+)`")

func docPath() string { return filepath.Join("..", "..", "..", "PREREGISTRATION.md") }
func testDBPath() string {
	if p := os.Getenv("SIGNALDECK_DB"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "..", "data", "signaldeck.db")
}

// whitespace-normalized document text: the claims wrap across lines, and a
// claim that only holds because it happened not to wrap is not a check.
func documentText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(docPath())
	if err != nil {
		t.Fatalf("PREREGISTRATION.md not readable: %v", err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

// chainKinds returns every kind on the chain, and the newest spec_json per kind.
func chainKinds(t *testing.T) (map[string]bool, map[string]map[string]any) {
	t.Helper()
	path := testDBPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("live DB not present at %s (set SIGNALDECK_DB) — the document's chain assertions cannot be checked", path)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open %s read-only: %v", path, err)
	}
	defer db.Close() //nolint:errcheck

	rows, err := db.Query(`SELECT kind, spec_json FROM prereg_records
		WHERE seq IN (SELECT MAX(seq) FROM prereg_records GROUP BY kind)`)
	if err != nil {
		t.Fatalf("read prereg chain: %v", err)
	}
	defer rows.Close() //nolint:errcheck

	kinds := map[string]bool{}
	newest := map[string]map[string]any{}
	for rows.Next() {
		var kind, specJSON string
		if err := rows.Scan(&kind, &specJSON); err != nil {
			t.Fatalf("scan: %v", err)
		}
		kinds[kind] = true
		var m map[string]any
		if err := json.Unmarshal([]byte(specJSON), &m); err != nil {
			t.Errorf("chain record kind %q holds unparseable spec_json: %v", kind, err)
			continue
		}
		newest[kind] = m
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate prereg chain: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatal("the pre-registration chain is empty, so every registration PREREGISTRATION.md asserts is unbacked")
	}
	return kinds, newest
}

// Every kind the document says is registered must exist on the chain. A
// mechanism described in prose but never appended is indistinguishable, to a
// reader, from one that was — which is exactly what §0 promises cannot happen.
func TestDocumentClaimedKindsExistOnTheChain(t *testing.T) {
	text := documentText(t)
	kinds, _ := chainKinds(t)

	claimed := map[string]bool{}
	for _, m := range kindClaimRe.FindAllStringSubmatch(text, -1) {
		claimed[m[1]] = true
	}
	if len(claimed) == 0 {
		t.Fatal("no `registered under the kind` claims parsed out of PREREGISTRATION.md — either the " +
			"document stopped asserting its registrations or this parser no longer matches it; both mean " +
			"the assertions are unchecked")
	}
	for _, kind := range sortedKeys(claimed) {
		if !kinds[kind] {
			t.Errorf("PREREGISTRATION.md says a record is registered under the kind %q, but the chain "+
				"carries no record of that kind (%d kinds present). Append the record — do NOT delete the "+
				"sentence: the fix is registering the mechanism, not describing fewer of them.",
				kind, len(kinds))
		}
	}
}

// Where the document names the exact record FIELD that freezes a parameter, the
// newest record of that kind must actually carry it. A record that predates the
// amendment describing it freezes nothing the amendment claims.
func TestDocumentClaimedProtocolFieldsAreOnTheNewestRecord(t *testing.T) {
	text := documentText(t)
	_, newest := chainKinds(t)
	protoType := reflect.TypeOf(Protocol{})

	locs := fieldClaimRe.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		t.Fatal("no `frozen on the chain in the … record` claims parsed out of PREREGISTRATION.md — the " +
			"document's field-level registration assertions are going unchecked")
	}
	for i, loc := range locs {
		kind := text[loc[2]:loc[3]]
		// The claim runs to the start of the next claim, or to the end of the
		// paragraph it opens — whichever comes first.
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		clause := text[loc[1]:end]
		if p := strings.Index(clause, "**"); p >= 0 { // next bolded lead-in ends the paragraph
			clause = clause[:p]
		}

		want := map[string]string{} // json field -> how the document named it
		for _, m := range protocolFieldRe.FindAllStringSubmatch(clause, -1) {
			sf, ok := protoType.FieldByName(m[1])
			if !ok {
				t.Errorf("PREREGISTRATION.md names `prereg.Protocol.%s`, which is not a field of the "+
					"Protocol struct", m[1])
				continue
			}
			tag := strings.Split(sf.Tag.Get("json"), ",")[0]
			if tag == "" {
				tag = sf.Name
			}
			want[tag] = "prereg.Protocol." + m[1]
		}
		for _, a := range alongsideRe.FindAllStringSubmatch(clause, -1) {
			for _, n := range backtickedRe.FindAllStringSubmatch(a[1], -1) {
				want[n[1]] = n[1]
			}
		}
		if len(want) == 0 {
			continue
		}
		rec, ok := newest[kind]
		if !ok {
			t.Errorf("PREREGISTRATION.md freezes %d field(s) in the %q record, but the chain has no "+
				"record of that kind", len(want), kind)
			continue
		}
		for _, f := range sortedKeys(want) {
			v, present := rec[f]
			if !present || v == nil || v == "" {
				t.Errorf("PREREGISTRATION.md says %s is frozen on the chain in the %q record, but the "+
					"NEWEST %q record does not carry %q (value %#v, present=%v). The registrar has not "+
					"appended the amended record yet, so the document describes a protocol nothing "+
					"registered. Fix by registering it — never by softening the sentence.",
					want[f], kind, kind, f, v, present)
			}
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
