// Package datasetver content-hashes the exact data a research result was
// computed from, so a claim can be reproduced — or shown to be irreproducible.
//
// # The problem
//
// Providers revise history. A split is applied retroactively, a bad print is
// corrected, an adjustment convention changes. When that happens, a validated
// claim silently stops being reproducible: re-running the same code over the
// same symbol and date range now reads different numbers, and there is no way to
// tell whether a changed result means the code changed, the data changed, or the
// original measurement was wrong. Every walk-forward guarantee in this platform
// is a statement about a specific dataset, and until now that dataset was
// identified only by "whatever was in the table at the time".
//
// # What a version is
//
// A deterministic SHA-256 over the CANONICAL serialization of the rows actually
// used: sorted by timestamp, fixed field order, fixed decimal formatting. Two
// runs over identical data produce an identical hash on any machine; a single
// revised close anywhere in the range produces a different one. The hash is
// recorded alongside the research result, and a later re-run compares.
//
// The formatting is pinned deliberately: %.10g normalizes float noise that
// carries no information (1.0 and 1.00 hash the same) while preserving every
// digit any market data source actually reports. Changing that format would
// invalidate every previously stored hash, which is why it is a documented
// constant rather than a parameter.
package datasetver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// canonicalFloatFmt is the pinned numeric format. Changing it invalidates every
// stored hash — treat it as part of the on-disk schema.
const canonicalFloatFmt = "%.10g"

// Row is one bar reduced to the fields a measurement can depend on.
type Row struct {
	Ts                     int64
	Open, High, Low, Close float64
	Volume                 float64
}

// Version identifies one dataset slice exactly.
type Version struct {
	Symbol    string `json:"symbol"`
	Timeframe string `json:"timeframe"`
	// FirstTs / LastTs are the actual bounds of the rows hashed, not the
	// requested range — a request for 2 years that returned 3 months must not
	// claim to be the 2-year dataset.
	FirstTs int64 `json:"firstTs"`
	LastTs  int64 `json:"lastTs"`
	// N rows hashed.
	N int `json:"n"`
	// Hash is the hex SHA-256 of the canonical serialization.
	Hash string `json:"hash"`
}

// Hash computes the version of a row set. Rows are sorted by timestamp before
// hashing, so retrieval order cannot change the hash; the input slice is not
// mutated. Duplicate timestamps are preserved and hashed in a stable order
// rather than silently collapsed — a duplicate row is itself a data defect this
// hash should expose, not hide.
func Hash(symbol, timeframe string, rows []Row) Version {
	v := Version{Symbol: symbol, Timeframe: timeframe, N: len(rows)}
	if len(rows) == 0 {
		// The empty dataset still gets a stable, distinguishable identity.
		sum := sha256.Sum256([]byte("empty|" + symbol + "|" + timeframe))
		v.Hash = hex.EncodeToString(sum[:])
		return v
	}
	sorted := append([]Row(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Ts != sorted[j].Ts {
			return sorted[i].Ts < sorted[j].Ts
		}
		return sorted[i].Close < sorted[j].Close
	})
	v.FirstTs, v.LastTs = sorted[0].Ts, sorted[len(sorted)-1].Ts

	h := sha256.New()
	// Header binds the hash to its identity: the same bars under a different
	// symbol or timeframe are a different dataset.
	_, _ = fmt.Fprintf(h, "v1|%s|%s|%d\n", symbol, timeframe, len(sorted))
	var b strings.Builder
	for _, r := range sorted {
		b.Reset()
		fmt.Fprintf(&b, "%d|", r.Ts)
		for _, f := range []float64{r.Open, r.High, r.Low, r.Close, r.Volume} {
			fmt.Fprintf(&b, canonicalFloatFmt+"|", f)
		}
		b.WriteByte('\n')
		h.Write([]byte(b.String())) //nolint:errcheck // hash.Write never errors
	}
	v.Hash = hex.EncodeToString(h.Sum(nil))
	return v
}

// HashRecords versions an arbitrary tabular export under the same canonical
// scheme as Hash. It exists for the reproducibility snapshot
// (tools/make_repro_snapshot.py): the grading tallies committed there are not
// OHLCV rows, but they need the same property — one hash on any machine, and a
// single edited cell changes it. The Python side reimplements this
// serialization byte for byte, so the two must never drift; the parity test
// pins a shared vector.
//
// Unlike Hash, records are hashed in the order given and fields are hashed as
// the exact strings supplied — the caller owns row order and numeric
// formatting (numbers must already be rendered with canonicalFloatFmt).
// Name and kind are bound into the hash exactly as symbol and timeframe are
// for bars: the same records under a different identity are a different
// dataset.
func HashRecords(name, kind string, records [][]string) Version {
	v := Version{Symbol: name, Timeframe: kind, N: len(records)}
	if len(records) == 0 {
		sum := sha256.Sum256([]byte("empty|" + name + "|" + kind))
		v.Hash = hex.EncodeToString(sum[:])
		return v
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "v1|%s|%s|%d\n", name, kind, len(records))
	var b strings.Builder
	for _, rec := range records {
		b.Reset()
		for _, f := range rec {
			b.WriteString(f)
			b.WriteByte('|')
		}
		b.WriteByte('\n')
		h.Write([]byte(b.String())) //nolint:errcheck // hash.Write never errors
	}
	v.Hash = hex.EncodeToString(h.Sum(nil))
	return v
}

// Revision describes how a re-read of the same slice differs from what was
// recorded.
type Revision struct {
	// Changed is true when the hash differs — the dataset is not the one the
	// original claim was measured on.
	Changed bool `json:"changed"`
	// Extended is true when rows were only APPENDED: the overlapping range is
	// unchanged and the new version simply reaches further. This is the normal,
	// benign case and must be distinguished from a rewrite of history, because
	// they mean opposite things about reproducibility.
	Extended bool `json:"extended"`
	// RowsBefore / RowsAfter for context.
	RowsBefore int `json:"rowsBefore"`
	RowsAfter  int `json:"rowsAfter"`
	// Reason states the finding in one sentence.
	Reason string `json:"reason"`
}

// Compare determines whether a stored version and a fresh one describe the same
// data. `overlap` is the hash of the fresh rows RESTRICTED to the stored
// version's timestamp range — the caller computes it with Hash over that
// subset. When it matches the stored hash, history was not rewritten and the
// difference is pure extension.
func Compare(stored, fresh Version, overlap string) Revision {
	r := Revision{RowsBefore: stored.N, RowsAfter: fresh.N}
	switch {
	case stored.Hash == fresh.Hash:
		r.Reason = "identical: the dataset is byte-for-byte the one this claim was measured on"
	case overlap != "" && overlap == stored.Hash:
		r.Changed, r.Extended = true, true
		r.Reason = "extended only: the original range is unchanged and newer rows were appended, so the original measurement remains reproducible"
	default:
		r.Changed = true
		r.Reason = "REVISED: the provider changed rows inside the original range, so any claim measured on it is no longer reproducible and must be re-graded"
	}
	return r
}
