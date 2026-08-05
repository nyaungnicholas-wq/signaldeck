package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// WHY THIS TEST EXISTS, and it is not hypothetical.
//
// The spec is built by concatenating raw-string segments around itoa/ftoa calls.
// While writing it, a search-and-replace over backticks broke every one of those
// concatenations, so the JSON carried the literal text `' + itoa(m.StocksTotal) + '`
// where the number belonged. IT COMPILED AND VETTED CLEAN. Nothing in the build
// can tell a raw string containing Go source from a raw string containing JSON.
//
// A prereg record is appended to a hash-chained, append-only log. A malformed or
// literal-source spec written there cannot be edited or withdrawn — only
// superseded by another record explaining the first one was wrong. So the check
// that the spec is real JSON, carrying the real measured numbers, has to run
// before a human is ever asked to type -commit.

func sampleMeasure() dataIntegrityMeasured {
	return dataIntegrityMeasured{
		UniverseRows: 1854289, UniverseDays: 2146,
		StocksTotal: 1774, StocksDelisted: 716,
		SpanYears: 8.0273, DelistRate: 0.0503,
		OutcomeRows: 27273, Resolved: 0, Quarantined: 16082,
		DistinctBlocksMax: 2,
	}
}

func TestDataIntegritySpecIsValidJSON(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal([]byte(dataIntegritySpec(sampleMeasure())), &got); err != nil {
		t.Fatalf("spec is not valid JSON, and it would have been written to an "+
			"append-only chain: %v", err)
	}
	if got["kind"] != DataIntegrityKind {
		t.Errorf("kind = %v, want %v", got["kind"], DataIntegrityKind)
	}
}

func TestDataIntegritySpecCarriesMeasuredNumbersNotSourceText(t *testing.T) {
	spec := dataIntegritySpec(sampleMeasure())

	// The defect that motivated this file: unevaluated Go leaking into the record.
	for _, leak := range []string{"itoa(", "ftoa(", "m.StocksTotal", "m.UniverseRows"} {
		if strings.Contains(spec, leak) {
			t.Fatalf("spec contains unevaluated Go source %q — the string "+
				"concatenation is broken", leak)
		}
	}

	var doc struct {
		CorpusAfter struct {
			StocksTotal       int     `json:"stocksTotal"`
			DelistedNames     int     `json:"delistedNames"`
			DelistRatePerYear float64 `json:"delistRatePerYear"`
			UniverseRows      int     `json:"universeMembershipRows"`
			UniverseDays      int     `json:"universeMembershipDistinctDays"`
		} `json:"corpusAfter"`
		Measured struct {
			Rows        int `json:"regimeOutcomeRows"`
			Resolved    int `json:"resolved"`
			Quarantined int `json:"nullQuarantinedRows"`
			Blocks      int `json:"distinctBlocksAccrued"`
		} `json:"measuredStateAtFiling"`
		ClaimsChanged     bool `json:"claimsChanged"`
		ClaimsRebaselined bool `json:"claimsRebaselined"`
	}
	if err := json.Unmarshal([]byte(spec), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	m := sampleMeasure()
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"stocksTotal", doc.CorpusAfter.StocksTotal, m.StocksTotal},
		{"delistedNames", doc.CorpusAfter.DelistedNames, m.StocksDelisted},
		{"universeRows", doc.CorpusAfter.UniverseRows, m.UniverseRows},
		{"universeDays", doc.CorpusAfter.UniverseDays, m.UniverseDays},
		{"regimeOutcomeRows", doc.Measured.Rows, m.OutcomeRows},
		{"resolved", doc.Measured.Resolved, m.Resolved},
		{"nullQuarantined", doc.Measured.Quarantined, m.Quarantined},
		{"blocksAccrued", doc.Measured.Blocks, m.DistinctBlocksMax},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if doc.CorpusAfter.DelistRatePerYear != m.DelistRate {
		t.Errorf("delistRatePerYear = %v, want %v", doc.CorpusAfter.DelistRatePerYear, m.DelistRate)
	}

	// The record's central promise. If either of these ever reads true, the
	// amendment has stopped being "the corpus moved, the claims did not" and has
	// become a restatement of claims after looking at data.
	if doc.ClaimsChanged || doc.ClaimsRebaselined {
		t.Error("claimsChanged/claimsRebaselined must both be false — this record " +
			"exists precisely because no claim moves")
	}
}

func TestDataIntegrityNoteDisclosesTheResidual(t *testing.T) {
	// The residual is the part a reader is most likely to be misled about, so it
	// is required to survive in the note itself and not only in the spec body.
	for _, want := range []string{"97.9%", "2023-2025", "cannot flatter"} {
		if !strings.Contains(dataIntegrityNote, want) {
			t.Errorf("note no longer discloses %q", want)
		}
	}
}
