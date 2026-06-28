import { Bot } from "lucide-react";
import { StreamingText } from "./StreamingText";

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
