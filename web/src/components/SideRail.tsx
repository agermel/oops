import { Menu } from "lucide-react";
import { navigation } from "../types";

export function SideRail({ activeNav, onNavChange }: { activeNav: string; onNavChange: (id: string) => void }) {
  return (
    <aside className="side-rail">
      <button className="rail-menu" title="菜单">
        <Menu size={23} />
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
          </button>
        ))}
      </nav>
    </aside>
  );
}
