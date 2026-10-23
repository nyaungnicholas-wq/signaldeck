// These tests pin the demo-build guard of lib/demoUpstream.ts: a demo build
// (non-empty SIGNALDECK_DEMO_BANNER) must refuse every daemon answer that lacks
// the demo stub's `x-signaldeck-demo: synthetic` header, and any accuracy
// registry not marked demo: true, so a demo build launched any other way than
// ops/demo-run.ps1 can never show real figures under a banner calling them synthetic.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { isNotDemoData, daemonFetch, isUsableRegistry, NOT_DEMO_DATA } from "./demoUpstream.ts";

let stamp = false;
let hits = 0;
let server;
let base;
let GET;

server = http.createServer((req, res) => {
	hits++;
	const headers = { "content-type": "application/json" };
	if (stamp) headers["x-signaldeck-demo"] = "synthetic";
	res.writeHead(200, headers);
	res.end('{"ok":true}');
});

before(async () => {
	await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
	base = `http://127.0.0.1:${server.address().port}`;
	process.env.SIGNALDECK_DAEMON = base;
	({ GET } = await import("../app/api/[...path]/route.ts"));
});

after(() => {
	server.closeAllConnections();
	server.close();
	delete process.env.SIGNALDECK_DAEMON;
});

async function withBanner(banner, fn) {
	const prev = process.env.SIGNALDECK_DEMO_BANNER;
	if (banner === "") delete process.env.SIGNALDECK_DEMO_BANNER;
	else process.env.SIGNALDECK_DEMO_BANNER = banner;
	try {
		await fn();
	} finally {
		if (prev === undefined) delete process.env.SIGNALDECK_DEMO_BANNER;
		else process.env.SIGNALDECK_DEMO_BANNER = prev;
	}
}

function proxyGet() {
	const url = "http://127.0.0.1/api/health";
	const req = Object.assign(new Request(url), { nextUrl: new URL(url) });
	return GET(req, { params: Promise.resolve({ path: ["health"] }) });
}

const hdr = (h) => new Response("", { headers: h });

test("outside a demo build nothing is refused", () => {
	assert.equal(isNotDemoData(hdr({}), ""), false);
	assert.equal(isUsableRegistry({ rows: [] }, ""), true);
	assert.equal(isUsableRegistry(null, ""), true);
});

test("a demo build refuses an answer without the stub's stamp", async () => {
	await withBanner("B", async () => {
		assert.equal(isNotDemoData(hdr({}), "B"), true);
		assert.equal(isNotDemoData(hdr({ "x-signaldeck-demo": "synthetic" }), "B"), false);
		assert.equal(isNotDemoData(hdr({ "x-signaldeck-demo": "real" }), "B"), true);
	});
});

test("a demo build reads only a registry marked demo: true", async () => {
	await withBanner("B", async () => {
		assert.equal(isUsableRegistry({ demo: true, rows: [] }, "B"), true);
		assert.equal(isUsableRegistry({ demo: "true" }, "B"), false);
		assert.equal(isUsableRegistry({ rows: [] }, "B"), false);
		assert.equal(isUsableRegistry(null, "B"), false);
	});
});

test("the proxy refuses a non-stub upstream in a demo build", async () => {
	await withBanner("B", async () => {
		stamp = false;
		const before = hits;
		const res = await proxyGet();
		assert.equal(hits, before + 1);
		assert.equal(res.status, 502);
		assert.deepEqual(await res.json(), { error: NOT_DEMO_DATA });
	});
});

test("the proxy passes the stub's answers in a demo build", async () => {
	await withBanner("B", async () => {
		stamp = true;
		const res = await proxyGet();
		assert.equal(res.status, 200);
		assert.deepEqual(await res.json(), { ok: true });
	});
});

test("the proxy is unchanged outside a demo build", async () => {
	await withBanner("", async () => {
		stamp = false;
		const res = await proxyGet();
		assert.equal(res.status, 200);
		assert.deepEqual(await res.json(), { ok: true });
	});
});

test("daemonFetch throws on a non-stub answer in a demo build", async () => {
	await withBanner("B", async () => {
		stamp = false;
		await assert.rejects(daemonFetch(`${base}/api/accuracy`), /not demo data/);
		stamp = true;
		assert.equal((await daemonFetch(`${base}/api/accuracy`)).status, 200);
	});
});

test("every server-side daemon read goes through the guard", () => {
	const src = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");
	const files = fs.readdirSync(src, { recursive: true })
		.filter((f) => (f.endsWith(".ts") || f.endsWith(".tsx")) && !f.includes(".test."));
	const readers = files.filter((f) => fs.readFileSync(path.join(src, f), "utf8").includes("SIGNALDECK_DAEMON"));
	const offenders = readers.filter((f) => {
		const text = fs.readFileSync(path.join(src, f), "utf8");
		return !text.includes("daemonFetch(") && !text.includes("isNotDemoData(");
	});
	assert.ok(readers.length >= 4, `found only ${readers.length} daemon readers`);
	assert.deepEqual(offenders, []);
	// ...and none of them still calls plain fetch() on the daemon URL.
	const raw = readers.filter((f) => /\bfetch\(\s*`\$\{\s*daemon\b/i.test(fs.readFileSync(path.join(src, f), "utf8")));
	assert.deepEqual(raw, []);
	// The page that reads the registry file from disk filters it through isUsableRegistry.
	const regReaders = files.filter((f) => {
		const text = fs.readFileSync(path.join(src, f), "utf8");
		return text.includes("readFile") && text.includes("accuracy_registry.json");
	});
	assert.ok(regReaders.length >= 1, "no registry reader found");
	for (const f of regReaders) assert.ok(fs.readFileSync(path.join(src, f), "utf8").includes("isUsableRegistry("), f);
});