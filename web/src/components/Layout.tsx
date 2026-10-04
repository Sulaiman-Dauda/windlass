import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Link, NavLink, Outlet, useLocation, useMatch } from "react-router";
import { useCan, useLogout, type User } from "../api/auth";
import { useProjects } from "../api/projects";
import { useMetrics, useUpdateCheck } from "../api/system";
import { Wordmark } from "../ui/Logo";
import { Icon, type IconName } from "../ui/Icon";
import { IconButton } from "../ui/Button";
import { Kbd, StatusDot } from "../ui/Badge";
import { Menu, MenuItem, MenuLabel, MenuSeparator } from "../ui/Menu";
import { useModalFocus } from "../ui/Modal";
import { useTheme } from "../ui/theme";
import { ShellContext } from "../ui/shell";
import { cn } from "../ui/cn";
import CommandPalette from "./CommandPalette";

const nav: { to: string; label: string; icon: IconName }[] = [
  { to: "/", label: "Overview", icon: "overview" },
  { to: "/projects", label: "Projects", icon: "projects" },
  { to: "/templates", label: "Templates", icon: "templates" },
  { to: "/settings", label: "Settings", icon: "settings" },
];

export const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

export default function Layout({ user }: { user: User }) {
  const [navOpen, setNavOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const location = useLocation();
  const scroller = useRef<HTMLDivElement>(null);

  // The panel, not the window, scrolls; start each screen at the top.
  useEffect(() => {
    scroller.current?.scrollTo({ top: 0 });
    setNavOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        // Never open over another dialog: Escape would close the one underneath.
        // The palette is itself aria-modal, so Ctrl+K still closes it.
        const layer = document.querySelector('[aria-modal="true"]');
        setSearchOpen((open) => !open && !layer);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  return (
    <ShellContext.Provider value={{ openNav: () => setNavOpen(true), openSearch: () => setSearchOpen(true) }}>
      <div className="flex h-dvh bg-canvas text-fg">
        <aside className="hidden w-[248px] flex-none lg:block">
          <Sidebar user={user} onSearch={() => setSearchOpen(true)} />
        </aside>

        <main className="min-w-0 flex-1 lg:py-2 lg:pr-2">
          <div
            ref={scroller}
            className="h-full overflow-y-auto bg-panel lg:rounded-xl2 lg:border lg:border-hairline lg:shadow-[var(--shadow-sm)]"
          >
            <Outlet />
          </div>
        </main>
      </div>

      {navOpen && <Drawer user={user} onClose={() => setNavOpen(false)} onSearch={() => setSearchOpen(true)} />}
      {searchOpen && <CommandPalette onClose={() => setSearchOpen(false)} />}
    </ShellContext.Provider>
  );
}

function Drawer({ user, onClose, onSearch }: { user: User; onClose: () => void; onSearch: () => void }) {
  const panel = useRef<HTMLDivElement>(null);
  useModalFocus(panel, onClose);

  return createPortal(
    <div className="fixed inset-0 z-40 lg:hidden">
      <div className="absolute inset-0 bg-overlay" style={{ animation: "wl-fade 0.16s var(--ease)" }} onClick={onClose} />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label="Navigation"
        tabIndex={-1}
        className="absolute inset-y-0 left-0 w-[280px] max-w-[85vw] border-r border-hairline bg-canvas shadow-[var(--shadow-lg)] outline-none"
        style={{ animation: "wl-slide-in 0.22s var(--ease)" }}
      >
        <IconButton icon="x" label="Close navigation" onClick={onClose} className="absolute right-3 top-3.5" />
        <Sidebar
          user={user}
          onSearch={() => {
            onClose();
            onSearch();
          }}
        />
      </div>
    </div>,
    document.body,
  );
}

function Sidebar({ user, onSearch }: { user: User; onSearch: () => void }) {
  const can = useCan();
  const projects = useProjects();
  const update = useUpdateCheck(user.role === "admin");
  const list = projects.data ?? [];
  const shown = list.slice(0, 12);

  return (
    <div className="flex h-full flex-col px-3 pb-3 pt-4">
      <Link to="/" className="flex h-7 items-center self-start px-2 text-accent" aria-label="Windlass overview">
        <Wordmark height={19} />
      </Link>

      <InstanceCard />

      <button
        type="button"
        onClick={onSearch}
        className="mt-2 flex h-8 w-full items-center gap-2 rounded-control border border-hairline bg-surface px-2.5 text-sm text-fg3 shadow-[var(--shadow-xs)] transition-colors hover:border-edge hover:text-fg2"
      >
        <Icon name="search" size={15} />
        <span className="flex-1 text-left">Search</span>
        <span className="flex gap-0.5">
          <Kbd>{isMac ? "⌘" : "Ctrl"}</Kbd>
          <Kbd>K</Kbd>
        </span>
      </button>

      <nav aria-label="Main" className="mt-4 flex flex-col gap-0.5">
        {nav.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.to === "/"}
            className={({ isActive }) =>
              cn(
                "flex h-8 items-center gap-2.5 rounded-control border px-2.5 text-sm transition-colors duration-150",
                isActive
                  ? "border-hairline bg-surface font-semibold text-fg shadow-[var(--shadow-xs)]"
                  : "border-transparent font-medium text-fg2 hover:bg-[color-mix(in_oklab,var(--fg)_5%,transparent)] hover:text-fg",
              )
            }
          >
            {({ isActive }) => (
              <>
                <Icon name={item.icon} size={17} className={isActive ? "text-accent" : "text-fg3"} />
                {item.label}
              </>
            )}
          </NavLink>
        ))}
      </nav>

      <div className="mt-6 flex items-center justify-between px-2.5">
        <span className="text-2xs font-semibold uppercase tracking-[0.06em] text-fg3">Projects</span>
        {can("member") && (
          <Link
            to="/projects?new=1"
            aria-label="New project"
            title="New project"
            className="-mr-1 grid h-6 w-6 place-items-center rounded-[6px] text-fg3 transition-colors hover:bg-[color-mix(in_oklab,var(--fg)_6%,transparent)] hover:text-fg"
          >
            <Icon name="plus" size={15} />
          </Link>
        )}
      </div>
      <div className="mt-1 min-h-0 flex-1 overflow-y-auto">
        {shown.map((p) => (
          <ProjectLink key={p.name} name={p.name} />
        ))}
        {list.length > shown.length && (
          <Link to="/projects" className="flex h-8 items-center px-2.5 text-xs font-medium text-fg3 hover:text-fg">
            View all {list.length}
          </Link>
        )}
        {projects.data && list.length === 0 && <p className="px-2.5 py-1.5 text-xs text-fg3">No projects yet</p>}
      </div>

      {update.data?.update_available && (
        <Link
          to="/settings/system#updates"
          className="mb-2 flex items-center gap-2 rounded-control border border-accent-edge bg-accent-soft px-2.5 py-2 text-xs font-semibold text-accent"
        >
          <Icon name="download" size={15} />
          <span className="flex-1">Update available</span>
          <span className="font-mono">{update.data.version}</span>
        </Link>
      )}

      <AccountMenu user={user} />
    </div>
  );
}

function ProjectLink({ name }: { name: string }) {
  const active = useMatch({ path: `/projects/${name}`, end: false });
  return (
    <Link
      to={`/projects/${name}`}
      className={cn(
        "flex h-8 items-center gap-2.5 rounded-control px-2.5 text-sm transition-colors duration-150",
        active ? "bg-[color-mix(in_oklab,var(--fg)_6%,transparent)] font-semibold text-fg" : "font-medium text-fg2 hover:bg-[color-mix(in_oklab,var(--fg)_5%,transparent)] hover:text-fg",
      )}
    >
      <Monogram name={name} />
      <span className="min-w-0 truncate">{name}</span>
    </Link>
  );
}

/** A project's initial on a small tile: a stable visual anchor for scanning lists. */
export function Monogram({ name, size = "sm" }: { name: string; size?: "sm" | "md" | "lg" }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid flex-none place-items-center border border-accent-edge bg-accent-soft font-bold uppercase text-accent",
        size === "sm" && "h-[18px] w-[18px] rounded-[5px] text-2xs",
        size === "md" && "h-8 w-8 rounded-[8px] text-sm",
        size === "lg" && "h-10 w-10 rounded-[10px] text-md",
      )}
    >
      {name.slice(0, 1)}
    </span>
  );
}

function InstanceCard() {
  const metrics = useMetrics(30_000);
  const m = metrics.data;
  return (
    <Link
      to="/"
      className="mt-4 flex items-center gap-2.5 rounded-control border border-hairline bg-surface px-2.5 py-2 shadow-[var(--shadow-xs)] transition-colors hover:border-edge"
    >
      <span className="grid h-7 w-7 flex-none place-items-center rounded-[7px] bg-sunken text-fg2">
        <Icon name="server" size={15} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-semibold text-fg">{m?.node.hostname || "This server"}</span>
        <span className="flex items-center gap-1.5 text-xs text-fg3">
          {metrics.isError ? (
            <>
              <StatusDot tone="err" /> Metrics unavailable
            </>
          ) : m ? (
            <>
              <StatusDot tone="ok" /> {m.containers.running} of {m.containers.total} containers up
            </>
          ) : (
            "Connecting…"
          )}
        </span>
      </span>
    </Link>
  );
}

function AccountMenu({ user }: { user: User }) {
  const logout = useLogout();
  const { mode, setMode } = useTheme();
  const handle = user.email.split("@")[0];

  return (
    <Menu
      side="top"
      align="start"
      width={232}
      trigger={(props) => (
        <button
          {...props}
          type="button"
          aria-label="Account menu"
          className="flex w-full items-center gap-2.5 rounded-control p-1.5 text-left transition-colors hover:bg-[color-mix(in_oklab,var(--fg)_5%,transparent)]"
        >
          <span className="grid h-8 w-8 flex-none place-items-center rounded-full border border-accent-edge bg-accent-soft text-xs font-bold uppercase text-accent">
            {handle.slice(0, 2)}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-semibold text-fg">{handle}</span>
            <span className="block truncate text-xs capitalize text-fg3">{user.role}</span>
          </span>
          <Icon name="chevronsUpDown" size={15} className="text-fg3" />
        </button>
      )}
    >
      <MenuLabel>
        <span className="block truncate normal-case tracking-normal">{user.email}</span>
      </MenuLabel>
      <MenuItem icon="shield" to="/settings/auth">
        Sign-in and security
      </MenuItem>
      <MenuSeparator />
      <MenuLabel>Theme</MenuLabel>
      {(
        [
          ["light", "Light", "sun"],
          ["dark", "Dark", "moon"],
          ["system", "Match system", "monitor"],
        ] as const
      ).map(([value, label, icon]) => (
        <MenuItem key={value} icon={icon} onSelect={() => setMode(value)} hint={mode === value ? <Icon name="check" size={15} className="text-accent" /> : undefined}>
          {label}
        </MenuItem>
      ))}
      <MenuSeparator />
      <MenuItem icon="signout" onSelect={() => logout.mutate()}>
        Sign out
      </MenuItem>
    </Menu>
  );
}
