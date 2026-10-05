// Tests for classifyPublicationFetch / unavailableReason (src/lib/publicationfetch.ts):
// a daemon that never answered must not render as a grader refusal, and nothing
// but a 2xx carrying status OK may publish.
//   node --test src/lib/publicationfetch.test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { classifyPublicationFetch, unavailableReason } from "./publicationfetch.ts";
import { summarizeRefusal } from "./refusal.ts";

test("timeout is a temporary outage, not a refusal", () => {
	const error = new DOMException("The operation was aborted due to timeout", "TimeoutError");
	const result = classifyPublicationFetch(null, null, error);
	assert.deepEqual(result, {
		kind: "unavailable",
		cause: "timeout",
		detail: "The operation was aborted due to timeout",
	});
});

test("connection refused is unreachable", () => {
	assert.deepEqual(classifyPublicationFetch(null, null, new TypeError("fetch failed")), {
		kind: "unavailable",
		cause: "unreachable",
		detail: "fetch failed",
	});
});

test("proxy 502 with a non-JSON body is unavailable", () => {
	assert.deepEqual(classifyPublicationFetch({ ok: false, status: 502 }, null), {
		kind: "unavailable",
		cause: "http",
		detail: "HTTP 502",
	});
});

test("504 JSON without a status is unavailable", () => {
	assert.deepEqual(classifyPublicationFetch({ ok: false, status: 504 }, { error: "gateway timeout" }), {
		kind: "unavailable",
		cause: "http",
		detail: "HTTP 504",
	});
});

test("429 from the rate limiter is unavailable", () => {
	assert.deepEqual(classifyPublicationFetch({ ok: false, status: 429 }, { error: "rate limited" }), {
		kind: "unavailable",
		cause: "http",
		detail: "HTTP 429",
	});
});

test("a non-2xx carrying status OK is never publishable", () => {
	const result = classifyPublicationFetch({ ok: false, status: 500 }, { status: "OK", rows: [{ live_acc: 0.6 }] });
	assert.deepEqual(result, { kind: "unavailable", cause: "http", detail: "HTTP 500 status OK" });
});

test("an unknown status is not a verdict, even on a 2xx", () => {
	const result = classifyPublicationFetch({ ok: true, status: 200 }, { status: "MAYBE", rows: [] });
	assert.deepEqual(result, { kind: "unavailable", cause: "http", detail: "HTTP 200 status MAYBE" });
});

test("real daemon REFUSED_STALE stays a refusal", () => {
	const body = {
		status: "REFUSED_STALE",
		reason: "last successful grade was 30h0m0s ago (max 26h0m0s)",
		graded_at: "2026-10-01T14:05:18",
	};
	assert.deepEqual(classifyPublicationFetch({ ok: false, status: 503 }, body), {
		kind: "refused",
		status: "REFUSED_STALE",
		reason: "last successful grade was 30h0m0s ago (max 26h0m0s)",
		gradedAt: "2026-10-01T14:05:18",
		refusedSince: undefined,
	});
});

test("REFUSED_UNAVAILABLE is the daemon's own verdict", () => {
	const result = classifyPublicationFetch({ ok: false, status: 503 }, { status: "REFUSED_UNAVAILABLE", reason: "x" });
	assert.equal(result.kind, "refused");
	assert.equal(result.status, "REFUSED_UNAVAILABLE");
});

test("401 is private", () => {
	assert.deepEqual(classifyPublicationFetch({ ok: false, status: 401 }, { error: "unauthorized" }), { kind: "private" });
});

test("2xx with status OK publishes", () => {
	assert.deepEqual(classifyPublicationFetch({ ok: true, status: 200 }, { status: "OK", rows: [] }), {
		kind: "ok",
		body: { status: "OK", rows: [] },
	});
});

test("the banner is worded by cause and survives the refusal summariser", () => {
	const timeout = unavailableReason({ kind: "unavailable", cause: "timeout", detail: "" });
	const http = unavailableReason({ kind: "unavailable", cause: "http", detail: "HTTP 503" });
	const down = unavailableReason({ kind: "unavailable", cause: "unreachable", detail: "" });
	assert.match(timeout, /did not answer in time/);
	assert.match(http, /is temporarily unavailable/);
	assert.match(down, /could not be reached/);
	for (const s of [timeout, http, down]) {
		assert.match(s, /not a refusal by the grader/);
		// RefusalNotice renders summarizeRefusal(reason).headline; the sentence
		// must come through as written, not be rewritten into a grader claim.
		assert.equal(summarizeRefusal(s).headline, s);
	}
});
