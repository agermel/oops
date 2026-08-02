import assert from "node:assert/strict";
import test from "node:test";

import {
  applySkillSuggestion,
  handleSkillSuggestionKey,
  parseSkillInvocationSummary,
  skillCommandState,
  skillInvocationDisplayText,
  skillSuggestions,
} from "../src/lib/skillSlashCommand.ts";
import type { Skill } from "../src/types.ts";

const skills: Skill[] = [
  skill("inspect", "Inspect containers"),
  skill("diagnose", "Diagnose problems"),
  skill("redis-diagnose", "Redis diagnosis"),
  skill("disabled", "Disabled", false),
];

test("detects command state only at input start", () => {
  assert.deepEqual(skillCommandState("/dia"), { active: true, token: "dia", query: "dia" });
  assert.deepEqual(skillCommandState("/skill:dia"), { active: true, token: "skill:dia", query: "skill:dia" });
  assert.equal(skillCommandState("hello /dia").active, false);
  assert.equal(skillCommandState(" /dia").active, false);
  assert.equal(skillCommandState("/dia args").active, false);
});

test("suggests enabled skills by command text and name with deterministic prefix before substring order", () => {
  assert.deepEqual(
    skillSuggestions("/dia", skills).map((item) => item.command),
    ["skill:diagnose", "skill:redis-diagnose"],
  );
  assert.deepEqual(
    skillSuggestions("/s", skills).map((item) => item.command),
    ["skill:diagnose", "skill:inspect", "skill:redis-diagnose"],
  );
  assert.deepEqual(
    skillSuggestions("/skill", skills).map((item) => item.command),
    ["skill:diagnose", "skill:inspect", "skill:redis-diagnose"],
  );
  assert.deepEqual(
    skillSuggestions("/skill:dia", skills).map((item) => item.command),
    ["skill:diagnose"],
  );
  assert.deepEqual(
    skillSuggestions("/", skills).map((item) => item.command),
    ["skill:diagnose", "skill:inspect", "skill:redis-diagnose"],
  );
});

test("completion inserts canonical skill command", () => {
  const applied = applySkillSuggestion("/dia", { name: "diagnose", command: "skill:diagnose", description: "" });
  assert.deepEqual(applied, { value: "/skill:diagnose ", cursor: "/skill:diagnose ".length });
});

test("keyboard helper selects, moves, closes, and respects composing state", () => {
  assert.deepEqual(handleSkillSuggestionKey("Enter", 0, 3), { action: "select", nextIndex: 0 });
  assert.deepEqual(handleSkillSuggestionKey("Tab", 1, 3), { action: "select", nextIndex: 1 });
  assert.deepEqual(handleSkillSuggestionKey("ArrowDown", 2, 3), { action: "move", nextIndex: 0 });
  assert.deepEqual(handleSkillSuggestionKey("ArrowUp", 0, 3), { action: "move", nextIndex: 2 });
  assert.deepEqual(handleSkillSuggestionKey("Escape", 0, 3), { action: "close", nextIndex: 0 });
  assert.deepEqual(handleSkillSuggestionKey("Enter", 0, 3, true), { action: "none", nextIndex: 0 });
});

test("parses compact skill invocation summary", () => {
  const text = `<skill name="diagnose" location="/workspace/skills/diagnose/SKILL.md">
References are relative to /workspace/skills/diagnose.

Use &lt;probe&gt;.
</skill>

write <draft> & explain "why"`;

  assert.deepEqual(parseSkillInvocationSummary(text), {
    name: "diagnose",
    instructions: `write <draft> & explain "why"`,
  });
  assert.equal(skillInvocationDisplayText(text), `skill:diagnose write <draft> & explain "why"`);
});

test("malformed skill block falls back to plain text", () => {
  const text = `<skill name="diagnose" location="/workspace/skills/diagnose/SKILL.md">missing close`;
  assert.equal(parseSkillInvocationSummary(text), null);
  assert.equal(skillInvocationDisplayText(text), text);
});

function skill(name: string, description: string, enabled = true): Skill {
  return {
    name,
    description,
    content: "",
    icon: "",
    label: "",
    color: "",
    enabled,
  };
}
