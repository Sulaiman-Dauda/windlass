import { NavLink, Outlet } from "react-router-dom";
import { useLogout, type User } from "../api/auth";

const nav = [
  { to: "/", label: "Dashboard" },
  { to: "/projects", label: "Projects" },
  { to: "/templates", label: "Templates" },
  { to: "/settings", label: "Settings" },
];

export default function Layout({ user }: { user: User }) {
  const logout = useLogout();

  return (
    <div className="flex min-h-screen bg-zinc-950 text-zinc-100">
      <aside className="flex w-52 flex-col border-r border-zinc-900 p-4">
        <div className="mb-8 px-2 text-lg font-semibold tracking-tight">
          Windlass
        </div>
        <nav className="flex flex-1 flex-col gap-1">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              className={({ isActive }) =>
                `rounded-md px-2 py-1.5 text-sm ${
                  isActive
                    ? "bg-zinc-800 text-zinc-100"
                    : "text-zinc-400 hover:bg-zinc-900 hover:text-zinc-200"
                }`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="border-t border-zinc-900 pt-3">
          <div className="truncate px-2 text-xs text-zinc-500">{user.email}</div>
          <button
            onClick={() => logout.mutate()}
            className="mt-1 w-full rounded-md px-2 py-1.5 text-left text-sm text-zinc-400 hover:bg-zinc-900 hover:text-zinc-200"
          >
            Sign out
          </button>
        </div>
      </aside>
      <main className="flex-1 overflow-auto p-8">
        <Outlet />
      </main>
    </div>
  );
}
