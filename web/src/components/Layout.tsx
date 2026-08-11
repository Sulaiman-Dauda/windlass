import { NavLink, Outlet } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { useLogout, type User } from "../api/auth";
import { Wordmark } from "../ui/Logo";
import { Icon, type IconName } from "../ui/Icon";
import { ThemeToggle } from "../ui/ThemeToggle";
import { cn } from "../ui/cn";

const nav: { to: string; label: string; icon: IconName }[] = [
  { to: "/", label: "Dashboard", icon: "dashboard" },
  { to: "/projects", label: "Projects", icon: "projects" },
  { to: "/templates", label: "Templates", icon: "templates" },
  { to: "/settings", label: "Settings", icon: "settings" },
];

export default function Layout({ user }: { user: User }) {
  const logout = useLogout();
  const initials = user.email.slice(0, 2).toUpperCase();

  // Same query key Settings uses, so the two stay in sync via the cache.
  const update = useQuery<{ update_available: boolean; version: string }>({
    queryKey: ["system", "update"],
    queryFn: () => api("/system/update"),
    enabled: user.role === "admin",
    retry: false,
    staleTime: 60 * 60 * 1000,
    refetchInterval: 6 * 60 * 60 * 1000,
  });

  return (
    <div className="flex min-h-screen bg-canvas text-fg">
      <aside className="sticky top-0 flex h-screen w-[244px] flex-none flex-col border-r border-chrome-edge bg-chrome p-3 backdrop-blur-xl">
        <div className="flex items-center gap-2.5 px-2 pb-1 pt-2">
          <Wordmark height={19} className="text-fg" />
        </div>

        <nav className="mt-5 flex flex-1 flex-col gap-0.5">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-[10px] px-2.5 py-2 text-sm transition-[background-color,color] duration-200",
                  isActive
                    ? "bg-accent-soft font-semibold text-accent"
                    : "font-medium text-fg2 hover:bg-surface2 hover:text-fg",
                )
              }
            >
              <Icon name={item.icon} size={18} />
              {item.label}
            </NavLink>
          ))}
        </nav>

        {update.data?.update_available && (
          <NavLink
            to="/settings/system#updates"
            className="mb-2 flex items-center gap-2.5 rounded-[10px] bg-accent-soft px-2.5 py-2 text-xs font-semibold text-accent"
          >
            <Icon name="download" size={16} />
            <span className="min-w-0 flex-1 truncate">
              Update available · {update.data.version}
            </span>
          </NavLink>
        )}

        <div className="border-t border-hairline pt-2">
          <div className="flex items-center justify-between px-2 py-2">
            <span className="text-xs text-fg2">Appearance</span>
            <ThemeToggle />
          </div>
          <div className="flex items-center gap-2.5 rounded-[10px] p-2">
            <span className="grid h-8 w-8 flex-none place-items-center rounded-full border border-hairline bg-sunken text-xs font-bold text-fg2">
              {initials}
            </span>
            <span className="min-w-0 flex-1 truncate text-xs text-fg3" title={user.email}>
              {user.email}
            </span>
            <button
              onClick={() => logout.mutate()}
              title="Sign out"
              aria-label="Sign out"
              className="grid h-8 w-8 flex-none place-items-center rounded-[9px] text-fg3 transition-colors duration-200 hover:bg-surface2 hover:text-fg"
            >
              <Icon name="signout" size={17} />
            </button>
          </div>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  );
}
