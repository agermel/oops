import assert from "node:assert/strict";
import test from "node:test";

import { shouldAutoExpandFirstServer } from "../src/lib/serverTreeState.ts";

test("auto-expands the first server on the initial project overview render", () => {
  assert.equal(shouldAutoExpandFirstServer({
    authenticated: true,
    view: "project-overview",
    serverCount: 1,
    serversLoading: false,
    expandedCount: 0,
    autoExpandConsumed: false,
  }), true);
});

test("keeps a manually collapsed server collapsed after the initial auto-expand", () => {
  assert.equal(shouldAutoExpandFirstServer({
    authenticated: true,
    view: "project-overview",
    serverCount: 1,
    serversLoading: false,
    expandedCount: 0,
    autoExpandConsumed: true,
  }), false);
});
