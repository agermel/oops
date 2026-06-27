import { Bot } from "lucide-react";
import { Streamdown } from "streamdown";
import { useTypewriter } from "../hooks/useTypewriter";

function StreamingText({ text, animate }: { text: string; animate: boolean }) {
  const displayed = useTypewriter(text, animate);
  const done = !animate || displayed.length >= text.length;
  return (
    <>
      <Streamdown className="stream-markdown" mode={animate ? "streaming" : "static"} controls={false}>{displayed}</Streamdown>
      {!done && <span className="cursor-blink">|</span>}
    </>
  );
}

export function AnswerBlock({ content, animate }: { content: string; animate: boolean }) {
  return (
    <div className="chat-msg assistant">
      <div className="chat-avatar">
        <Bot size={16} />
      </div>
      <div className="chat-content">
        <StreamingText text={content} animate={animate} />
      </div>
    </div>
  );
}
