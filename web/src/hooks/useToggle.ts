import React from "react";

export function useToggle(initial = false) {
  const [on, setOn] = React.useState(initial);
  const toggle = React.useCallback(() => setOn((p) => !p), []);
  const set = React.useCallback((v: boolean) => setOn(v), []);
  return [on, { on: () => set(true), off: () => set(false), toggle, set }] as const;
}
