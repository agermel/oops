import React from "react";

/**
 * 打字机效果 Hook。
 *
 * 当 text 变化时，自动从已显示内容的末尾继续，仅对新增部分应用
 * 逐字动画——避免增量更新时全文重打造成的闪烁。
 */
export function useTypewriter(text: string, enabled: boolean): string {
  const [displayed, setDisplayed] = React.useState("");
  const timerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  // 用 ref 跟踪最新 text，消除闭包过期问题。
  const textRef = React.useRef(text);
  textRef.current = text;

  const clearTimer = React.useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  // disabled 时立即展示全部文本。
  React.useEffect(() => {
    if (!enabled) {
      clearTimer();
      setDisplayed(text);
    }
  }, [text, enabled, clearTimer]);

  // text 变化时，保留已展示的公共前缀，只重置差异部分。
  React.useEffect(() => {
    setDisplayed((prev) => {
      if (prev === text) return prev;
      // 新文本以旧文本开头 → 保留已显示部分。
      if (text.startsWith(prev)) return prev;
      // 完全不匹配，重置。
      return "";
    });
    clearTimer();
  }, [text, clearTimer]);

  // 打字机定时推进。
  React.useEffect(() => {
    if (!enabled) return;
    if (displayed.length >= text.length) return;

    const remaining = text.length - displayed.length;

    // 自适应分块：剩余越少块越小，收尾平滑。
    let chunk: number;
    if (remaining <= 12) chunk = 2;
    else if (remaining <= 48) chunk = 4;
    else if (remaining <= 96) chunk = 8;
    else chunk = Math.min(256, Math.ceil(remaining / 4));

    // 标点对齐：在 chunk 附近找标点或空格，停顿更自然（含中英文标点）。
    const end = Math.min(displayed.length + chunk, text.length);
    const snapPat = /[\s.,!?;:)\]，。！？；：）】、》""]/;
    let snap = end;
    for (let i = end; i < Math.min(end + 8, text.length); i++) {
      if (snapPat.test(text[i])) { snap = i + 1; break; }
    }
    if (snap - displayed.length > chunk * 2) snap = end;

    timerRef.current = setTimeout(() => {
      // 使用 textRef 避免在 setTimeout 回调中读到过期 text。
      setDisplayed(textRef.current.slice(0, snap));
    }, 24);

    return clearTimer;
  }, [displayed, text, enabled, clearTimer]);

  return enabled ? displayed : text;
}
