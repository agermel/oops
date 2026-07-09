import assert from "node:assert/strict";
import test from "node:test";

import { nextChatStateAfterCreateError } from "../src/lib/chatRequestState.ts";

test("restores submitted input after run creation failure", () => {
  assert.deepEqual(
    nextChatStateAfterCreateError({ input: "", loading: true }, "/skill:missing", false),
    { input: "/skill:missing", loading: false },
  );
});

test("restores submitted input after network failure before run creation", () => {
  assert.deepEqual(
    nextChatStateAfterCreateError({ input: "", loading: true }, "/skill:diagnose inspect", false),
    { input: "/skill:diagnose inspect", loading: false },
  );
});

test("keeps input cleared after stream setup failure following run creation", () => {
  assert.deepEqual(
    nextChatStateAfterCreateError({ input: "", loading: true }, "/skill:diagnose inspect", true),
    { input: "", loading: false },
  );
});
