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
//   - pipeline/researchledger.go — hypothesis --evidenced_by--> each graded
//     evidence row (main grader path).
//   - pipeline/predict.go        — ledgered prediction --generated_by--> each
//     model leg that contributed to its blend.
//
// REMAINING WIRING SITES (documented, deliberately not half-wired):
//   - TODO(lineage): papertrade open/close — trade --traded_as--> prediction
//     (internal/papertrade), closing the prediction→PnL loop.
//   - TODO(lineage): evidence engine — claim --evidenced_by--> hypothesis /
//     feature (internal/evidence.Put callers), replacing the free-text
//     lineage_json column with real edges.
//   - TODO(lineage): researchledger grader feature keys — evidence
//     --tested_in--> feature per key referenced by the grader's Spec JSON
//     (needs Spec parsing; the closures in ledgerGraders are opaque).
//   - TODO(lineage): researchlab (research_hypotheses, "lab:" namespace) and
//     researchengine's decaySweep.
//   - TODO(lineage): datasetver — real dataset_versions rows as nodes once a
//     producer records which version a training run consumed.
package lineage
