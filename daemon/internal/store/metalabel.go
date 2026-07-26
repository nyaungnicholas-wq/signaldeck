// META-LABELING WAVE (2026-07-25) — persistence for the meta-label study.
//
// The grade is ONE fleet-wide document per horizon, not a per-symbol row, so it
// lives in meta exactly like the feature-redundancy report and the adaptive
// weights. There is deliberately no metalabel table: a per-symbol meta-label
// grade would be measured on a handful of independent days per symbol, which is
// precisely the sample-size illusion this study exists to refuse.
package store

// MetaMetaLabel is the meta key holding the latest meta-labeling grade (JSON,
// one entry per horizon).
const MetaMetaLabel = "metalabel:v1"
