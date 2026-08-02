import type { Skill } from "../types";

export type SkillSuggestion = {
  name: string;
  command: string;
  description: string;
};

export type SkillCommandState = {
  active: boolean;
  token: string;
  query: string;
};

export type SkillSuggestionKeyResult = {
  action: "none" | "select" | "close" | "move";
  nextIndex: number;
};

export type SkillInvocationSummary = {
  name: string;
  instructions: string;
};

export function skillCommandState(input: string): SkillCommandState {
  if (!input.startsWith("/") || input.startsWith(" /")) {
    return { active: false, token: "", query: "" };
  }
  const firstWhitespace = input.search(/\s/);
  if (firstWhitespace !== -1) {
    return { active: false, token: "", query: "" };
  }
  const token = input.slice(1);
  if (token.includes("/")) {
    return { active: false, token: "", query: "" };
  }
  return { active: true, token, query: token };
}

export function skillSuggestions(input: string, skills: Skill[], limit = 8): SkillSuggestion[] {
  const state = skillCommandState(input);
  if (!state.active) return [];
  const enabled = skills.filter((skill) => skill.enabled);
  const query = state.query;
  const prefix: SkillSuggestion[] = [];
  const substring: SkillSuggestion[] = [];
  for (const skill of enabled) {
    const item = {
      name: skill.name,
      command: `skill:${skill.name}`,
      description: skill.description || "",
    };
    if (query === "" || skill.name.startsWith(query) || item.command.startsWith(query)) {
      prefix.push(item);
    } else if (skill.name.includes(query) || item.command.includes(query)) {
      substring.push(item);
    }
  }
  return [...sortSuggestions(prefix), ...sortSuggestions(substring)].slice(0, limit);
}

export function applySkillSuggestion(input: string, suggestion: SkillSuggestion): { value: string; cursor: number } {
  const value = `/skill:${suggestion.name} `;
  return { value, cursor: value.length };
}

export function handleSkillSuggestionKey(
  key: string,
  currentIndex: number,
  count: number,
  composing = false,
): SkillSuggestionKeyResult {
  if (composing || count <= 0) return { action: "none", nextIndex: currentIndex };
  if (key === "Enter" || key === "Tab") return { action: "select", nextIndex: clampIndex(currentIndex, count) };
  if (key === "Escape") return { action: "close", nextIndex: currentIndex };
  if (key === "ArrowDown") return { action: "move", nextIndex: (clampIndex(currentIndex, count) + 1) % count };
  if (key === "ArrowUp") return { action: "move", nextIndex: (clampIndex(currentIndex, count) - 1 + count) % count };
  return { action: "none", nextIndex: currentIndex };
}

export function parseSkillInvocationSummary(text: string): SkillInvocationSummary | null {
  const match = text.match(/^<skill name="([^"]*)" location="[^"]*">\n[\s\S]*?\n<\/skill>(?:\n\n([\s\S]*))?$/);
  if (!match) return null;
  return {
    name: unescapeXML(match[1] || ""),
    instructions: (match[2] || "").trim(),
  };
}

export function skillInvocationDisplayText(text: string): string {
  const summary = parseSkillInvocationSummary(text);
  if (!summary) return text;
  return summary.instructions ? `skill:${summary.name} ${summary.instructions}` : `skill:${summary.name}`;
}

function sortSuggestions(items: SkillSuggestion[]): SkillSuggestion[] {
  return [...items].sort((a, b) => a.name.localeCompare(b.name));
}

function clampIndex(index: number, count: number): number {
  if (index < 0) return 0;
  if (index >= count) return count - 1;
  return index;
}

function unescapeXML(value: string): string {
  return value
    .replace(/&quot;/g, "\"")
    .replace(/&#34;/g, "\"")
    .replace(/&apos;/g, "'")
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&");
}
