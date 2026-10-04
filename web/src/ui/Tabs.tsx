import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import { cn } from "./cn";

export interface TabItem {
  to: string;
  label: string;
  end?: boolean;
  badge?: ReactNode;
}

/** Route-backed tabs: each tab is a link, so every view has its own URL. */
export function RouteTabs({ items, label }: { items: TabItem[]; label: string }) {
  return (
    <nav aria-label={label} className="-mb-px flex gap-1 overflow-x-auto [scrollbar-width:none]">
      {items.map((t) => (
        <NavLink
          key={t.label}
          to={t.to}
          end={t.end}
          className={({ isActive }) =>
            cn(
              "group relative flex-none pb-2.5 text-sm transition-colors duration-150",
              isActive
                ? "font-semibold text-fg after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-fg"
                : "font-medium text-fg3 hover:text-fg",
            )
          }
        >
          <span className="inline-flex h-8 items-center gap-1.5 rounded-[6px] px-2.5 transition-colors group-hover:bg-sunken">
            {t.label}
            {t.badge}
          </span>
        </NavLink>
      ))}
    </nav>
  );
}
