package main

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// DuplicateKind records that three amendment records were filed a SECOND time,
// and why the tool allowed it. It is its own kind because the existing kinds
// now refuse a duplicate by construction, and because the fact being recorded
// is new: not what any amendment said, but that the log states three one-time
// events twice.
const DuplicateKind = "duplicate-amendment-correction"

// duplicateNote follows the chain's AMENDMENT convention: name what happened,
// state that the prior records stand unaltered, and stay legible to a reader
// who trusts nothing else.
const duplicateNote = "AMENDMENT — three amendment records were filed a second time on 2026-08-15 " +
	"and the log therefore states three one-time events twice. The duplicates are gradability-correction, " +
	"protocol-provenance-correction and data-integrity-amendment; each already existed, filed 2026-08-04 " +
	"and 2026-08-05. CAUSE: cmd/prereg-amend verified only each record's PREMISES — whether the facts it " +
	"asserts are true at filing time — and had no check for a record of that kind already existing, so a " +
	"dry run printed a complete, apparently-pending spec for a record already on the chain, and that was " +
	"read as evidence it had not been filed. A clean dry run did not mean not-yet-filed. NOTHING FALSE " +
	"WAS WRITTEN: the later specs differ from the earlier ones only in re-measured live state " +
	"(measuredStateAtFiling, corpusAfter, anomaly), every premise guard re-verified true at filing, and " +
	"the chain verifies INTACT across all rows. NO claim, band table, accuracy figure, baseline, " +
	"threshold, grader hash or verdict changes here, and no consumer counts these kinds — verify_dod.py " +
	"and research_liveness.py test PRESENCE, so their results are unaffected. FIXED at the cause: " +
	"cmd/prereg-amend/duplicate.go now refuses any kind already on the chain, naming the existing " +
	"sequence numbers, before it measures anything; four tests pin it and no override flag exists. The " +
	"duplicate records are deliberately NOT edited and NOT removed: a hash-chained commitment is never " +
	"rewritten, so the explanation is appended and every record remains readable in order."

// dupKind is one duplicated kind as the chain actually holds it.
type dupKind struct {
	Kind      string
	Seqs      []int
	SameStory bool // do the copies open with the same AMENDMENT statement?
}

// duplicates is the live state read at commit time. This record's whole premise
// is that duplicates exist; measuring at filing rather than transcribing an
// earlier count is what keeps the statement true when it is hashed.
type duplicates struct {
	// Kinds holds only the RESTATEMENTS: copies whose notes open with the same
	// statement, i.e. the same one-time fact filed twice.
	Kinds []dupKind
	// Distinct holds kinds that appear more than once with DIFFERENT statements.
	// auto-retire-rule is one (seq 13 and 46): a rule genuinely re-registered
	// with new content is an amendment, not a duplicate, and this record must
	// not describe it as one. Disclosed rather than silently filtered, because
	// a reader counting rows would otherwise find copies this record ignores.
	Distinct []dupKind
	Total    int
}

// measureDuplicates finds every kind the chain holds more than once.
//
// SameStory is measured, not assumed. The record claims the copies RESTATE the
// same one-time fact; if a later copy said something materially different that
// claim would be false, and this record must not assert it. Comparing the
// opening of the notes is enough — the AMENDMENT convention puts the statement
// of what happened at the front.
func measureDuplicates(ctx context.Context, db *sql.DB) (duplicates, error) {
	var d duplicates
	rows, err := db.QueryContext(ctx, `
		SELECT kind, seq, COALESCE(note, '') FROM prereg_records ORDER BY kind, seq`)
	if err != nil {
		return d, fmt.Errorf("read chain: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	seqs := map[string][]int{}
	notes := map[string][]string{}
	for rows.Next() {
		var kind, note string
		var seq int
		if err := rows.Scan(&kind, &seq, &note); err != nil {
			return d, err
		}
		seqs[kind] = append(seqs[kind], seq)
		notes[kind] = append(notes[kind], note)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}

	head := func(s string) string {
		if len(s) > 120 {
			return s[:120]
		}
		return s
	}
	for kind, ss := range seqs {
		if len(ss) < 2 {
			continue
		}
		same := true
		for _, n := range notes[kind][1:] {
			if head(n) != head(notes[kind][0]) {
				same = false
			}
		}
		k := dupKind{Kind: kind, Seqs: ss, SameStory: same}
		if same {
			d.Kinds = append(d.Kinds, k)
			d.Total += len(ss) - 1
		} else {
			d.Distinct = append(d.Distinct, k)
		}
	}
	sort.Slice(d.Kinds, func(i, j int) bool { return d.Kinds[i].Kind < d.Kinds[j].Kind })
	sort.Slice(d.Distinct, func(i, j int) bool { return d.Distinct[i].Kind < d.Distinct[j].Kind })
	return d, nil
}

// duplicateSpec is the machine-readable body, assembled as a plain string so
// what is hashed is exactly what a reader sees.
func duplicateSpec(d duplicates) string {
	var b strings.Builder
	b.WriteString("{\n")
	b.WriteString("  \"kind\": \"" + DuplicateKind + "\",\n")
	b.WriteString("  \"filedOn\": \"2026-08-15\",\n")
	b.WriteString("  \"correctionType\": \"log-hygiene-only\",\n")
	b.WriteString("  \"claimsChanged\": false,\n")
	b.WriteString("  \"gradersChanged\": false,\n")
	b.WriteString("  \"verdictsChanged\": false,\n")
	b.WriteString("  \"cause\": \"prereg-amend verified each record's premises but not whether a " +
		"record of that kind already existed, so a clean dry run was misread as evidence the record " +
		"was still pending\",\n")
	b.WriteString("  \"fixedBy\": \"cmd/prereg-amend/duplicate.go - alreadyFiled() refuses any kind " +
		"already on the chain, naming the existing seqs, before any measurement; 4 tests; no override " +
		"flag\",\n")
	b.WriteString("  \"duplicatedKinds\": [")
	for i, k := range d.Kinds {
		if i > 0 {
			b.WriteString(",")
		}
		ss := make([]string, len(k.Seqs))
		for j, s := range k.Seqs {
			ss[j] = itoa(s)
		}
		b.WriteString("\n    {\"kind\": \"" + k.Kind + "\", \"seqs\": [" + strings.Join(ss, ", ") +
			"], \"copiesRestateTheSameStatement\": " + boolStr(k.SameStory) + "}")
	}
	b.WriteString("\n  ],\n")
	b.WriteString("  \"extraCopies\": " + itoa(d.Total) + ",\n")
	// Disclosed, not filtered. A reader tallying rows per kind will find these
	// copies too; leaving them out would make this record look like it had
	// missed them.
	b.WriteString("  \"kindsAppearingMoreThanOnceWithDIFFERENTstatements\": [")
	for i, k := range d.Distinct {
		if i > 0 {
			b.WriteString(",")
		}
		ss := make([]string, len(k.Seqs))
		for j, sq := range k.Seqs {
			ss[j] = itoa(sq)
		}
		b.WriteString("\n    {\"kind\": \"" + k.Kind + "\", \"seqs\": [" +
			strings.Join(ss, ", ") + "]}")
	}
	b.WriteString("\n  ],\n")
	b.WriteString("  \"noteOnThoseKinds\": \"NOT claimed as duplicates. A kind " +
		"re-registered with different content is an amendment, not a repeated " +
		"filing, and this record makes no assertion about them.\",\n")
	b.WriteString("  \"consumersAffected\": \"none - verify_dod.py and research_liveness.py test " +
		"kind PRESENCE, not count; internal/mcp cites seq 37 by number\",\n")
	b.WriteString("  \"priorRecordsEdited\": false,\n")
	b.WriteString("  \"priorRecordsRemoved\": false\n")
	b.WriteString("}")
	return b.String()
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
