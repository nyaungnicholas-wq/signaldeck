package store

// StructSettledEpochTS is 2026-11-16T00:00:00-05:00 (05:00:00Z), the start of
// Monday 16 November 2026 in New York. Structural regime calls made at or after
// it are built from, frozen against and resolved on SETTLED daily bars only
// (pipeline/structsettled.go). Calls before it keep the rule they were frozen
// under. Registered on the pre-registration chain as
// "structural-settled-bar" (cmd/prereg-amend/spec_structsettled.go) before the
// first call it governs existed. Owner decision, 13 Nov 2026.
const StructSettledEpochTS int64 = 1794805200
