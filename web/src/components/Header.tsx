import { Search, CircleHelp, Bell, Sparkles } from "lucide-react";

export function Header({ activeNav, onNavChange }: { activeNav: string; onNavChange: (id: string) => void }) {
  return (
    <header className="global-header">
      <div className="elastic-brand" aria-label="Elastic">
        <span className="elastic-flower" aria-hidden="true">
          <i />
          <i />
          <i />
          <i />
        </span>
        <span>elastic</span>
      </div>
      <label className="global-search">
        <Search size={18} />
        <input placeholder="搜索 Ops Plane" />
      </label>
      <div className="global-actions">
        <button title="帮助">
          <CircleHelp size={20} />
        </button>
        <button title="通知">
          <Bell size={20} />
        </button>
        <button title="智能助手" onClick={() => onNavChange("chat")} className={activeNav === "chat" ? "chat-active" : ""}>
          <Sparkles size={20} />
        </button>
        <button className="avatar" title="用户">
          o
        </button>
      </div>
    </header>
  );
}
