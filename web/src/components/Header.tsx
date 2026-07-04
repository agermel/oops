import { Sparkles, LogOut } from "lucide-react";
import { navigation } from "../types";
import { authPaths } from "../lib/paths";

export function Header({ activeNav, onNavChange }: { activeNav: string; onNavChange: (id: string) => void }) {
  async function handleLogout() {
    try {
      await fetch(authPaths.token, { method: "DELETE" });
    } catch {
      // 即使请求失败也清除本地状态，token 可能已过期或基于 cookie
    }
    window.location.reload();
  }
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
      <div className="global-actions">
        <button title="智能助手" onClick={() => onNavChange("chat")} className={activeNav === "chat" ? "chat-active" : ""}>
          <Sparkles size={20} />
        </button>
        <button title="登出" onClick={handleLogout}>
          <LogOut size={20} />
        </button>
      </div>
    </header>
  );
}
