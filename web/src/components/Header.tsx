import { Search, CircleHelp, Bell, Sparkles } from "lucide-react";
import { navigation } from "../types";

export function Header({ activeNav, onNavChange }: { activeNav: string; onNavChange: (id: string) => void }) {
  return (
    <header className="global-header">
      <div className="elastic-brand" aria-label="Oops Panel">
        <span className="elastic-flower" aria-hidden="true">
          <i />
          <i />
          <i />
          <i />
        </span>
        <span>Oops</span>
      </div>
      <nav className="top-nav" aria-label="主导航">
        {navigation.map((item) => (
          <button
            key={item.id}
            className={activeNav === item.id ? "active" : ""}
            onClick={() => onNavChange(item.id)}
          >
            <item.icon size={15} />
            <span>{item.label}</span>
          </button>
        ))}
      </nav>
      <label className="global-search">
        <Search size={18} />
        <input placeholder="搜索（即将推出）" disabled title="搜索功能即将推出" />
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
