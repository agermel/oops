import React from "react";

export function useTypewriter(text: string, enabled: boolean): string {
  const [displayed, setDisplayed] = React.useState("");
  const timerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  React.useEffect(() => {
    setDisplayed("");
  }, [text]);

  React.useEffect(() => {
    if (!enabled) {
      setDisplayed(text);
      return;
    }
    if (displayed.length >= text.length) return;

    const remaining = text.length - displayed.length;

    // 自适应分块：剩余越少块越小，收尾平滑。
    let chunk: number;
    if (remaining <= 12) chunk = 2;
    else if (remaining <= 48) chunk = 4;
    else if (remaining <= 96) chunk = 8;
    else chunk = Math.min(256, Math.ceil(remaining / 4));

    // 标点对齐：在 chunk 附近找标点或空格，停顿更自然。
    const end = Math.min(displayed.length + chunk, text.length);
    const snapPat = /[\s.,!?;:)\]]/;
    let snap = end;
    for (let i = end; i < Math.min(end + 8, text.length); i++) {
      if (snapPat.test(text[i])) { snap = i + 1; break; }
    }
    if (snap - displayed.length > chunk * 2) snap = end;

    timerRef.current = setTimeout(() => {
      setDisplayed(text.slice(0, snap));
    }, 24);

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [displayed, text, enabled]);

  return enabled ? displayed : text;
}
