// node --test src/lib/calibration.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { brierSkillSub } from "./calibration.ts";

const SD30 =
	"withheld: label partly realised at issue (SD-30); a corrected label is pending a preregistration decision";

test("a withheld skill shows the daemon's reason, gated or not", () => {
	assert.equal(brierSkillSub({ brierSkill: null, brierNote: SD30 }), SD30);
	assert.equal(brierSkillSub({ brierSkill: null, brierNote: SD30, gated: true }), SD30);
});

test("with no stated reason the generic line stays", () => {
	assert.match(brierSkillSub({ brierSkill: null }), /not gradable yet/);
	assert.match(brierSkillSub(null), /not gradable yet/);
});

test("a measured skill still reads against the base rate", () => {
	assert.equal(
		brierSkillSub({ brierSkill: -0.2, baseRate: 0.56, brierNote: "ignored" }),
		"vs always forecasting the 56.0% base rate; negative = worse than the constant",
	);
});
