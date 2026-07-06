import assert from "node:assert/strict";
import test from "node:test";

import {
  containerStatusPresentation,
  shouldAutoExpandFirstServer,
} from "../src/lib/serverTreeState.ts";

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

test("marks cached containers as unknown when the parent nodelet is stale", () => {
  assert.deepEqual(containerStatusPresentation({
    containerState: "running",
    stale: true,
  }), {
    alive: false,
    unknown: true,
    disabled: true,
    title: "状态未知",
  });
});

test("uses container runtime state only when the nodelet data is fresh", () => {
  assert.deepEqual(containerStatusPresentation({
    containerState: "running",
    stale: false,
  }), {
    alive: true,
    unknown: false,
    disabled: false,
    title: "运行中",
  });
});
