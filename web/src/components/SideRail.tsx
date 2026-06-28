import { PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { navigation, projectNavigation } from "../types";

export function SideRail({
  activeNav,
  collapsed,
  variant,
  onCollapsedChange,
  onNavChange,
}: {
  activeNav: string;
  collapsed: boolean;
  variant: "global" | "project";
  onCollapsedChange: (collapsed: boolean) => void;
  onNavChange: (id: string) => void;
}) {
  const ToggleIcon = collapsed ? PanelLeftOpen : PanelLeftClose;
  const items = variant === "project" ? projectNavigation : navigation;
  const label = variant === "project" ? "项目" : "工作台";

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
        <span>{label}</span>
      </button>
      <nav className="rail-nav" aria-label={variant === "project" ? "项目导航" : "主导航"}>
        {items.map((item) => (
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
