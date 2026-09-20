// Regression tests for the accuracy page's two-read binding (audit R01).
//   node --test src/lib/accuracybinding.test.mjs
//
// The page asks the daemon for approval, then reads the registry from disk.
// These pin the two failure shapes the audit named: the registry being swapped
// between the reads, and a registry row with no counterpart in the API
// response.

import { test } from "node:test";
import assert from "node:assert/strict";
import { bindingMismatch, labelOfPublished, unapprovedLabels } from "./accuracybinding.ts";

const GRADE_A = { graded_at: "2026-09-13T14:42:10", grader_sha256: "6908c6f9446ab440" };
const APPROVAL_A = { gradedAt: "2026-09-13T14:42:10", graderSha256: "6908c6f9446ab440" };

test("the matching pair renders", () => {
	assert.equal(bindingMismatch(GRADE_A, APPROVAL_A), null);
});

test("a registry swapped between the two reads fails closed", () => {
	// The grader replaced data/accuracy_registry.json after /api/accuracy
	// approved the previous one. Same grader binary, later grade.
	const swapped = { graded_at: "2026-09-20T09:01:00", grader_sha256: GRADE_A.grader_sha256 };
	const why = bindingMismatch(swapped, APPROVAL_A);
	assert.ok(why, "a later grade paired with an older approval must not render");
	assert.match(why, /different grade/);
	assert.match(why, /2026-09-20T09:01:00/);
	assert.match(why, /2026-09-13T14:42:10/);
});

test("a registry written by a different grader fails closed", () => {
	const reforged = { graded_at: GRADE_A.graded_at, grader_sha256: "deadbeefdeadbeef" };
	const why = bindingMismatch(reforged, APPROVAL_A);
	assert.ok(why);
	assert.match(why, /different grader/);
});

test("an unidentifiable artifact is a mismatch, not a pass", () => {
	// "unknown" must not resolve to "publish" on either side.
	assert.ok(bindingMismatch({}, APPROVAL_A));
	assert.ok(bindingMismatch(GRADE_A, {}));
	assert.ok(bindingMismatch({ graded_at: "   " }, APPROVAL_A));
	assert.ok(bindingMismatch(null, APPROVAL_A));
	assert.ok(bindingMismatch(GRADE_A, null));
});

test("the daemon omitting grader_sha256 does not block a matching grade", () => {
	// The API omits it on paths that publish no rows; graded_at still binds.
	assert.equal(bindingMismatch(GRADE_A, { gradedAt: GRADE_A.graded_at }), null);
});

test("a registry row absent from the API response is withheld by name", () => {
	const registryRows = [
		{ predictor: "directional-ensemble (1d)" },
		{ predictor: "directional-ensemble (1w)" },
		{ predictor: "ghost-model (1d)" },
	];
	const published = [
		{ predictor: "directional-ensemble", horizon: "1d", variant: "" },
		{ predictor: "directional-ensemble", horizon: "1w", variant: "" },
	];
	assert.deepEqual(unapprovedLabels(registryRows, published), ["ghost-model (1d)"]);
});

test("the label rebuild matches the registry's own spelling", () => {
	assert.equal(
		labelOfPublished({ predictor: "directional-ensemble", horizon: "1d", variant: "high conviction" }),
		"directional-ensemble (1d, high conviction)",
	);
	assert.equal(labelOfPublished({ predictor: "directional-ensemble", horizon: "1d" }),
		"directional-ensemble (1d)");
	assert.equal(labelOfPublished({ predictor: "solo" }), "solo");
});

test("every approved row stays approved", () => {
	const registryRows = [
		{ predictor: "directional-ensemble (1d)" },
		{ predictor: "directional-ensemble (1d, high conviction)" },
	];
	const published = [
		{ predictor: "directional-ensemble", horizon: "1d", variant: "" },
		{ predictor: "directional-ensemble", horizon: "1d", variant: "high conviction" },
	];
	assert.deepEqual(unapprovedLabels(registryRows, published), []);
});
