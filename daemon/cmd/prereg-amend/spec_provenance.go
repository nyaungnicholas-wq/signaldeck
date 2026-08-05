package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// jsonString quotes and escapes a value read from the database. The offending
// note is transcribed verbatim into the spec, and it contains punctuation that
// would otherwise break the hand-assembled JSON.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// ProvenanceKind records HOW an earlier grading-protocol record entered the
// chain, never what it said. Amending the grading-protocol kind itself would
// re-hash the protocol and read as "the protocol moved"; nothing about the
// thresholds, the grader or any claim changes here. The chain already carries
// the anomaly — this record carries its explanation, so a reader who trusts
// nothing else can tell a repair from a rewrite without leaving the chain.
const ProvenanceKind = "protocol-provenance-correction"

// provenanceNote follows the chain's AMENDMENT convention: name what happened,
// state that the prior record stands unaltered, and be legible to a hostile
// reader.
const provenanceNote = "AMENDMENT — one grading-protocol record entered outside the amendment path. " +
	"Every other protocol record after the genesis registration carries an AMENDMENT note written by " +
	"prereg-amend; this one was written by the single-purpose cmd/append-prereg19 and its note begins " +
	"APPEND-19. An audit criterion that forbids hand re-pinning of a grading protocol is right to flag " +
	"it, so the explanation belongs on the chain rather than in a commit message. What it did was " +
	"RESTORE frozen floors that the immediately preceding record had dropped to null: the multiplicity " +
	"rule, the maximum alpha, the cluster unit and the minimum distinct blocks. Restoring a Bonferroni " +
	"correction and a minimum block count makes a verdict HARDER to obtain, never easier, so the change " +
	"cannot have flattered any result. NO claim, band table, accuracy figure, baseline, threshold or " +
	"grader hash changes here. The offending record is deliberately NOT edited and NOT removed: a " +
	"hash-chained commitment is never rewritten, and both records remain readable in order."

// provenance is the live state read at commit time. The premise of this record
// is a fact about the chain as it stands, so it is measured at filing rather
// than transcribed — if the anomaly has already been explained or the chain has
// moved on, the record must not be filed at all.
type provenance struct {
	GenesisSeq     int
	OffendingSeq   int
	OffendingNote  string
	OffendingKind  string
	PriorSeq       int
	RestoredFields []string
	DroppedFields  []string
	ProtocolCount  int
}

// The floors whose presence or absence decides whether the offending record
// tightened the protocol or loosened it. This is the whole defence of the
// record, so it is checked against the database rather than asserted.
var frozenFloors = []string{
	"multiplicityRule",
	"maxAlpha",
	"clusterUnit",
	"minDistinctBlocks",
}

// measureProvenance finds the first grading-protocol record after the genesis
// registration whose note does not follow the AMENDMENT convention, and works
// out which frozen floors it restored relative to the record before it.
func measureProvenance(ctx context.Context, db *sql.DB) (provenance, error) {
	var p provenance

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MIN(seq), 0)
		  FROM prereg_records WHERE kind = 'grading-protocol'`).Scan(&p.ProtocolCount, &p.GenesisSeq); err != nil {
		return p, fmt.Errorf("count grading-protocol records: %w", err)
	}
	if p.GenesisSeq == 0 {
		return p, fmt.Errorf("no grading-protocol record exists")
	}

	// The genesis registration is not an amendment and is not expected to say so.
	err := db.QueryRowContext(ctx, `
		SELECT seq, kind, note
		  FROM prereg_records
		 WHERE kind = 'grading-protocol' AND seq > ? AND note NOT LIKE 'AMENDMENT%'
		 ORDER BY seq LIMIT 1`, p.GenesisSeq).Scan(&p.OffendingSeq, &p.OffendingKind, &p.OffendingNote)
	if err == sql.ErrNoRows {
		return p, nil // no anomaly; main refuses to file
	}
	if err != nil {
		return p, fmt.Errorf("find non-amendment protocol record: %w", err)
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0)
		  FROM prereg_records WHERE kind = 'grading-protocol' AND seq < ?`,
		p.OffendingSeq).Scan(&p.PriorSeq); err != nil {
		return p, fmt.Errorf("find preceding protocol record: %w", err)
	}

	// A floor is "restored" when the preceding record carried no value for it and
	// the offending record does. json_extract yields NULL both for an absent key
	// and for a JSON null, and for this purpose those mean the same thing: the
	// floor was not in force.
	for _, field := range frozenFloors {
		var priorSet, offendingSet bool
		if err := db.QueryRowContext(ctx, `
			SELECT json_extract(spec_json, '$.'||?) IS NOT NULL
			  FROM prereg_records WHERE seq = ?`, field, p.PriorSeq).Scan(&priorSet); err != nil {
			return p, fmt.Errorf("read %s from seq %d: %w", field, p.PriorSeq, err)
		}
		if err := db.QueryRowContext(ctx, `
			SELECT json_extract(spec_json, '$.'||?) IS NOT NULL
			  FROM prereg_records WHERE seq = ?`, field, p.OffendingSeq).Scan(&offendingSet); err != nil {
			return p, fmt.Errorf("read %s from seq %d: %w", field, p.OffendingSeq, err)
		}
		switch {
		case !priorSet && offendingSet:
			p.RestoredFields = append(p.RestoredFields, field)
		case priorSet && !offendingSet:
			p.DroppedFields = append(p.DroppedFields, field)
		}
	}

	return p, nil
}

func quotedList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return `"` + strings.Join(items, `", "`) + `"`
}

// provenanceSpec is the machine-readable body, assembled as a plain string so
// that what is hashed is exactly what a reader sees.
func provenanceSpec(p provenance) string {
	return `{
  "kind": "protocol-provenance-correction",
  "filedOn": "2026-08-04",
  "amends": "nothing — this record explains the provenance of prereg seq ` + itoa(p.OffendingSeq) + `, and alters no commitment",
  "correctionType": "provenance-only",
  "claimsChanged": false,
  "thresholdsChanged": false,
  "graderChanged": false,
  "anomaly": {
    "seq": ` + itoa(p.OffendingSeq) + `,
    "recordKind": "` + p.OffendingKind + `",
    "genesisSeq": ` + itoa(p.GenesisSeq) + `,
    "precedingProtocolSeq": ` + itoa(p.PriorSeq) + `,
    "totalProtocolRecords": ` + itoa(p.ProtocolCount) + `,
    "defect": "The record was appended by cmd/append-prereg19, a single-purpose tool, rather than by cmd/prereg-amend. Its note begins APPEND-19 instead of AMENDMENT, which is the convention every other post-genesis grading-protocol record follows. The record is therefore indistinguishable, by note convention alone, from a hand-written re-pin of the grading protocol.",
    "note": ` + jsonString(p.OffendingNote) + `
  },
  "effect": {
    "floorsRestored": [` + quotedList(p.RestoredFields) + `],
    "floorsDropped": [` + quotedList(p.DroppedFields) + `],
    "direction": "STRICTLY TIGHTENING",
    "why": "Each restored field constrains when a verdict may be published. multiplicityRule reintroduces the Bonferroni correction over family and looks, which can only ever WIDEN a published interval. maxAlpha reinstates the 0.05 ceiling the correction divides. minDistinctBlocks reinstates the requirement of 10 non-overlapping horizon blocks before any interval is published at all. clusterUnit reinstates the independence unit that prevents pooled rows inflating n. A protocol that is harder to satisfy cannot manufacture a passing verdict, so the restoration cannot have flattered any result — which is the question an auditor is actually asking of a record that entered off-path."
  },
  "verification": {
    "chainIntact": "verified by prereg-amend pre-flight, which recomputes every entry_hash with prereg.HashEntry and checks prev_hash linkage before appending",
    "recomputeFormula": "entry_hash = sha256(prev_hash || 0x1E || \"ts=<ts>|kind=<kind>|specHash=<specHash>|note=<note>\")",
    "reproduce": "python tools/verify_dod.py --repo . — the NO_MANUAL_REPIN and CHAIN_INTACT checks read this same chain"
  },
  "doesNotChange": [
    "every frozen band table and claimed accuracy",
    "every threshold, floor and refusal rule now in force",
    "the registered grader and its sha256",
    "the multiplicity rule, which this record only describes",
    "prereg seq ` + itoa(p.OffendingSeq) + ` itself, which stands unaltered above this record"
  ],
  "consequence": "The chain now carries its own explanation of the one record that entered off-path. An auditor reading the chain alone can tell that the anomaly was a repair of dropped floors rather than a re-pin that loosened them, without needing to trust a commit message, a report or the operator."
}`
}
