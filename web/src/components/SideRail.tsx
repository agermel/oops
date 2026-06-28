import { PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { navigation } from "../types";

export function SideRail({
  activeNav,
  collapsed,
  onCollapsedChange,
  onNavChange,
}: {
  activeNav: string;
  collapsed: boolean;
  onCollapsedChange: (collapsed: boolean) => void;
  onNavChange: (id: string) => void;
}) {
  const ToggleIcon = collapsed ? PanelLeftOpen : PanelLeftClose;

  return (
    <aside className="side-rail">
      <button
        className="rail-menu"
        title={collapsed ? "展开侧边栏" : "收起侧边栏"}
        aria-label={collapsed ? "展开侧边栏" : "收起侧边栏"}
        aria-expanded={!collapsed}
        onClick={() => onCollapsedChange(!collapsed)}
      >
        <ToggleIcon size={20} />
        <span>工作台</span>
      </button>
      <nav className="rail-nav" aria-label="主导航">
        {navigation.map((item) => (
          <button
            key={item.id}
            className={activeNav === item.id ? "active" : ""}
            title={item.label}
            onClick={() => onNavChange(item.id)}
          >
            <item.icon size={19} />
            <span>{item.label}</span>
          </button>
        ))}
      </nav>
    </aside>
  );
}
