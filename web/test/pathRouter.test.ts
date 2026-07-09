import assert from "node:assert/strict";
import test from "node:test";

import { parsePathRoute, routeToPath } from "../src/hooks/usePathRouter.ts";

test("settings path routes to global settings view", () => {
  assert.deepEqual(parsePathRoute("/settings"), { view: "settings" });
  assert.equal(routeToPath({ view: "settings" }), "/settings");
});

test("unknown global path falls back to projects", () => {
  assert.deepEqual(parsePathRoute("/unknown"), { view: "projects" });
});
