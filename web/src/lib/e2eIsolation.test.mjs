// The signing-in e2e specs write to the daemon; they must never reach
// production (127.0.0.1:8322), which is also the web tier's default.
//   node --test src/lib/e2eIsolation.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { isolatedDaemon } from "../../e2e/isolation.ts";

test("writing e2e specs refuse the production daemon and the default", () => {
  assert.throws(() => isolatedDaemon({}), /REFUSED/);
  assert.throws(() => isolatedDaemon({ SIGNALDECK_DAEMON: "http://127.0.0.1:8322" }), /REFUSED/);
  assert.throws(() => isolatedDaemon({ SIGNALDECK_DAEMON: "http://localhost:8322/" }), /REFUSED/);
  assert.throws(() => isolatedDaemon({ SIGNALDECK_DAEMON: "http://127.0.0.1" }), /REFUSED/);
  assert.throws(() => isolatedDaemon({ SIGNALDECK_DAEMON: "not a url" }), /REFUSED/);
  assert.equal(isolatedDaemon({ SIGNALDECK_DAEMON: "http://127.0.0.1:18322" }), "http://127.0.0.1:18322");
});
