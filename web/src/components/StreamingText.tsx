import { Streamdown } from "streamdown";
import { useTypewriter } from "../hooks/useTypewriter";

export function StreamingText({ text, animate }: { text: string; animate: boolean }) {
  const displayed = useTypewriter(text, animate);
  const done = !animate || displayed.length >= text.length;
  return (
    <>
      <Streamdown className="stream-markdown" mode={animate ? "streaming" : "static"} controls={false}>{displayed}</Streamdown>
      {!done && <span className="cursor-blink">|</span>}
    </>
  );
}
