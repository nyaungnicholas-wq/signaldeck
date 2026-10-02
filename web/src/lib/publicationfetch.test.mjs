// Tests for classifyPublicationFetch (src/lib/publicationfetch.ts): a daemon that
// never answered must not render as a grader refusal.
//   node --test src/lib/publicationfetch.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { classifyPublicationFetch } from "./publicationfetch.ts";

test("timeout", () => {
	const res = null;
	const body = null;
	const error = new DOMException("The operation was aborted due to timeout", "TimeoutError");
	const result = classifyPublicationFetch(res, body, error);
	assert.deepEqual(result, { kind: "unavailable", detail: "The operation was aborted due to timeout" });
	assert.notEqual(result.kind, "refused", "a timeout must not read as a grader refusal");
});

test("connection refused", () => {
	const res = null;
	const body = null;
	const error = new TypeError("fetch failed");
	const result = classifyPublicationFetch(res, body, error);
	assert.deepEqual(result, { kind: "unavailable", detail: "fetch failed" });
});

test("proxy 502 with non-JSON body", () => {
	const res = { ok: false, status: 502 };
	const body = null;
	const result = classifyPublicationFetch(res, body);
	assert.deepEqual(result, { kind: "unavailable", detail: "HTTP 502" });
});

test("504 JSON without status", () => {
	const res = { ok: false, status: 504 };
	const body = { error: "gateway timeout" };
	const result = classifyPublicationFetch(res, body);
	assert.deepEqual(result, { kind: "unavailable", detail: "HTTP 504" });
});

test("real daemon REFUSED_STALE", () => {
	const res = { ok: false, status: 503 };
	const body = {
	  status: "REFUSED_STALE",
	  reason: "last successful grade was 30h0m0s ago (max 26h0m0s)",
	  graded_at: "2026-10-01T14:05:18",
	};
	const result = classifyPublicationFetch(res, body);
	assert.deepEqual(result, {
	  kind: "refused",
	  status: "REFUSED_STALE",
	  reason: "last successful grade was 30h0m0s ago (max 26h0m0s)",
	  gradedAt: "2026-10-01T14:05:18",
	  refusedSince: undefined,
	});
});

test("401", () => {
	const res = { ok: false, status: 401 };
	const body = { error: "unauthorized" };
	const result = classifyPublicationFetch(res, body);
	assert.deepEqual(result, { kind: "private" });
});

test("ok", () => {
	const res = { ok: true, status: 200 };
	const body = { status: "OK", rows: [] };
	const result = classifyPublicationFetch(res, body);
	assert.deepEqual(result, { kind: "ok", body: { status: "OK", rows: [] } });
});

test("200 whose envelope is not OK is still the daemon's verdict", () => {
	const res = { ok: true, status: 200 };
	const body = { status: "REFUSED", reason: "x" };
	const result = classifyPublicationFetch(res, body);
	assert.deepEqual(result, {
	  kind: "refused",
	  status: "REFUSED",
	  reason: "x",
	  gradedAt: undefined,
	  refusedSince: undefined,
	});
});
