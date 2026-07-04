import React from "react";

export function useSet(initial: string[] = []) {
  const [state, setState] = React.useState<Set<string>>(new Set(initial));

  const add = React.useCallback(
    (id: string) => setState((prev) => new Set(prev).add(id)),
    [],
  );
  const remove = React.useCallback((id: string) => {
    setState((prev) => {
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
  }, []);
  const toggle = React.useCallback((id: string) => {
    setState((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);
  const has = React.useCallback((id: string) => state.has(id), [state]);
  const clear = React.useCallback(() => setState(new Set()), []);

  return { set: state, add, remove, toggle, has, clear, setState };
}
