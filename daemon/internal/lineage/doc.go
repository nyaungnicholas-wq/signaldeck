// Package lineage is the research lineage spine (ARCHITECTURE_EV.md Layers
// 2+8): one typed edge graph connecting the three hypothesis registries,
// dataset windows, model legs, ledgered predictions, paper trades and
// evidence claims, so "which feature produced the most PnL" becomes one
// graph walk instead of three joins across three ID schemes.
//
// Node IDs are namespaced strings so the three hypothesis registries can
// coexist without a shared ID scheme:
//
//	hypothesis  "loop:<id>"      research_loop_hypotheses
//	hypothesis  "ledger:<id>"    research_ledger_hypotheses (e.g. "ledger:H008")
//	hypothesis  "lab:<id>"       research_hypotheses (Research Lab)
//	experiment  "loop-run:<day>" one research-loop grid search
//	dataset_version  "research_weeks:<fromTs>-<toTs>" the graded window
//	model       "<model>:<horizon>" e.g. "gbm:1w" (store.Model* names)
//	prediction  "<ledger seq>"   prediction_ledger seq
//	trade       "<paper_trades id>"
//	claim       "<evidence_claims id>" or "ledger-evidence:<hyp>@<ts>:<kind>"
//	feature     "<feature key>"  a feature-vector key, e.g. "trend21"
//
// Every edge written by a research producer stamps the build's VCS revision
// (RecordBuildRevision at daemon startup; RevMeta on each edge) so an
// experiment is tied to the exact code version that ran it.
//
// WIRED PRODUCERS (write edges at write time):
//   - pipeline/researchloop.go   — hypothesis --tested_in--> dataset window,
//     hypothesis --graded_by--> the loop-run experiment.
//   - pipeline insertLedgerEvidence (researchledger.go) — hypothesis
//     --evidenced_by--> research-ledger evidence, for EVERY writer: the live
//     grader, the research engine's era grades and discoveries, the live
//     fresh-window replication, and the one-shot seed and correction rows
//     (those already ran on the live DB, before this wiring, so they carry no
//     edges there). TestLedgerEvidenceWritersLinkLineage fails if a writer
//     bypasses it. The claim id is per (hypothesis, ts, kind), not per row, so
//     rows written in one pass with the same kind share one claim node.
//   - pipeline/predict.go        — ledgered prediction --generated_by--> each
//     model leg that contributed to its blend.
//   - evidence.Put, and evidence.EnsureSeeds for claims stored before this
//     wiring — feature --evidenced_by--> claim, one edge per
//     Lineage.FeatureKeys entry (lineage_json stays as the claim's own record).
//     Those keys are the structural-regime kinds the claims are about
//     ("trend21", "liquidity21"), the "feature" ids this table names. No other
//     producer writes feature nodes yet. Lineage.Models is not linked: those
//     names ("structural-regime") are not "<model>:<horizon>" model nodes.
//
// NOT WIRED, and why (an absent edge means not-yet-wired, not not-related):
//   - papertrade open/close — trade --traded_as--> prediction. Three gaps:
//     store.ApplyPaperStep assigns trade ids inside its transaction and returns
//     none, so the trade node id needs a store API change; the trader holds a
//     predictions row while the prediction node is a prediction_ledger seq, and
//     re-predicting a bar appends a second seq for it; and only probability-flip
//     exits act on a prediction (barrier, kill-switch and deactivation closes do
//     not), so which closes link is an owner decision, not a wiring one.
//   - research-ledger grader feature keys — evidence --tested_in--> feature.
//     The keys live inside the opaque filter/call closures in ledgerGraders; a
//     hand-kept key list beside them would drift and answer traces wrongly.
//     Needs the graders expressed as Spec JSON first.
//   - researchlab ("lab:" namespace) — the lab grades on pooled labeled
//     features, which have no dataset_version id, and has no experiment-id
//     scheme; minting either is a lineage-semantics decision. researchengine's
//     decaySweep writes only derived peak/last-grade fields on existing
//     hypotheses, so it has no new node to link (the engine's evidence rows
//     are wired above).
//   - datasetver — no producer records which dataset version a training run
//     consumed (dataset_versions is written only by the honesty-gap
//     price-revision check), so there is no consumption fact to link.
package lineage
