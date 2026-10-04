import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router";
import { useCan, useLogout, type Role } from "../api/auth";
import { useProjects } from "../api/projects";
import { Icon, type IconName } from "../ui/Icon";
import { Kbd } from "../ui/Badge";
import { useTheme } from "../ui/theme";
import { cn } from "../ui/cn";

interface Command {
  id: string;
  group: string;
  label: string;
  icon: IconName;
  hint?: string;
  /** Extra words that should match, not shown. */
  keywords?: string;
  /** Only offered once the person starts typing, to keep the first view short. */
  searchOnly?: boolean;
  run: () => void;
}

const projectPages: [string, string, IconName, Role?][] = [
  ["deployments", "Deployments", "deploy"],
  ["logs", "Logs", "logs"],
  ["terminal", "Terminal", "terminal", "member"],
  ["env", "Environment", "key"],
  ["files", "Files", "file"],
  ["domains", "Domains", "globe"],
  ["git", "Git", "gitBranch"],
  ["backups", "Backups", "archive"],
];

/** Jump anywhere and run common actions from the keyboard (Ctrl/Cmd+K). */
export default function CommandPalette({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const projects = useProjects();
  const can = useCan();
  const logout = useLogout();
  const { setMode } = useTheme();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const list = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);

  const commands = useMemo<Command[]>(() => {
    const go = (to: string) => () => navigate(to);
    const out: Command[] = [];
    for (const p of projects.data ?? []) {
      out.push({ id: `p:${p.name}`, group: "Projects", label: p.name, icon: "projects", hint: "Project", keywords: p.git_repo, run: go(`/projects/${p.name}`) });
      for (const [path, label, icon, role] of projectPages) {
        if (role && !can(role)) continue;
        out.push({
          id: `p:${p.name}:${path}`,
          group: "Project pages",
          label: `${p.name} / ${label}`,
          icon,
          searchOnly: true,
          run: go(`/projects/${p.name}/${path}`),
        });
      }
    }
    out.push(
      { id: "go:overview", group: "Go to", label: "Overview", icon: "overview", keywords: "dashboard home metrics", run: go("/") },
      { id: "go:projects", group: "Go to", label: "Projects", icon: "projects", run: go("/projects") },
      { id: "go:templates", group: "Go to", label: "Templates", icon: "templates", keywords: "database postgres app", run: go("/templates") },
      { id: "go:settings", group: "Go to", label: "Settings", icon: "settings", run: go("/settings/general") },
      { id: "go:users", group: "Go to", label: "Settings / Users and sign-in", icon: "users", keywords: "2fa totp oauth github app", searchOnly: true, run: go("/settings/auth") },
    );
    if (can("admin")) {
      out.push(
        { id: "go:git", group: "Go to", label: "Settings / Git connections", icon: "gitBranch", searchOnly: true, run: go("/settings/git") },
        { id: "go:registries", group: "Go to", label: "Settings / Container registries", icon: "package", keywords: "ghcr docker login", searchOnly: true, run: go("/settings/registries") },
        { id: "go:system", group: "Go to", label: "Settings / System and updates", icon: "server", keywords: "update prune images storage", searchOnly: true, run: go("/settings/system") },
      );
    }
    if (can("member")) {
      out.push({ id: "act:new", group: "Actions", label: "Create a project", icon: "plus", keywords: "new", run: go("/projects?new=1") });
    }
    out.push(
      { id: "act:light", group: "Actions", label: "Use light theme", icon: "sun", keywords: "appearance", searchOnly: true, run: () => setMode("light") },
      { id: "act:dark", group: "Actions", label: "Use dark theme", icon: "moon", keywords: "appearance", searchOnly: true, run: () => setMode("dark") },
      { id: "act:system", group: "Actions", label: "Match system theme", icon: "monitor", keywords: "appearance", searchOnly: true, run: () => setMode("system") },
      { id: "act:signout", group: "Actions", label: "Sign out", icon: "signout", keywords: "logout", searchOnly: true, run: () => logout.mutate() },
    );
    return out;
  }, [projects.data, can, navigate, setMode, logout]);

  const results = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return commands.filter((c) => !c.searchOnly);
    const terms = q.split(/\s+/);
    return commands
      .filter((c) => {
        const hay = `${c.label} ${c.group} ${c.keywords ?? ""}`.toLowerCase();
        return terms.every((t) => hay.includes(t));
      })
      .sort((a, b) => Number(b.label.toLowerCase().startsWith(q)) - Number(a.label.toLowerCase().startsWith(q)));
  }, [commands, query]);

  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    input.current?.focus();
  }, []);
  useEffect(() => {
    list.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: "nearest" });
  }, [active]);

  const run = (c: Command | undefined) => {
    if (!c) return;
    onClose();
    c.run();
  };

  let lastGroup = "";

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-overlay px-4 pt-[12vh] backdrop-blur-[2px]"
      style={{ animation: "wl-fade 0.14s var(--ease)" }}
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Search"
        className="w-full max-w-[640px] overflow-hidden rounded-xl2 border border-hairline bg-surface shadow-[var(--shadow-lg)]"
        style={{ animation: "wl-pop 0.18s var(--ease)" }}
      >
        <div className="flex h-12 items-center gap-3 border-b border-hairline px-4">
          <Icon name="search" size={18} className="text-fg3" />
          <input
            ref={input}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setActive((i) => Math.min(i + 1, results.length - 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setActive((i) => Math.max(i - 1, 0));
              } else if (e.key === "Enter") {
                e.preventDefault();
                run(results[active]);
              } else if (e.key === "Escape") {
                e.preventDefault();
                onClose();
              }
            }}
            placeholder="Search projects, pages and actions"
            aria-label="Search projects, pages and actions"
            role="combobox"
            aria-expanded="true"
            aria-controls="wl-palette-list"
            aria-activedescendant={results[active] ? `wl-cmd-${active}` : undefined}
            className="h-full min-w-0 flex-1 bg-transparent text-md text-fg outline-none placeholder:text-fg3 focus-visible:outline-none"
          />
          <Kbd>Esc</Kbd>
        </div>

        <div ref={list} id="wl-palette-list" role="listbox" className="max-h-[min(60vh,420px)] overflow-y-auto p-1.5">
          {results.length === 0 && <p className="px-3 py-8 text-center text-sm text-fg3">Nothing matches “{query}”.</p>}
          {results.map((c, i) => {
            const header = c.group !== lastGroup ? c.group : null;
            lastGroup = c.group;
            return (
              <div key={c.id}>
                {header && <div className="px-2.5 pb-1 pt-2.5 text-2xs font-semibold uppercase tracking-[0.06em] text-fg3">{header}</div>}
                <button
                  type="button"
                  id={`wl-cmd-${i}`}
                  role="option"
                  aria-selected={i === active}
                  data-index={i}
                  onMouseMove={() => setActive(i)}
                  onClick={() => run(c)}
                  className={cn(
                    "flex h-9 w-full items-center gap-3 rounded-control px-2.5 text-left text-sm",
                    i === active ? "bg-sunken text-fg" : "text-fg2",
                  )}
                >
                  <Icon name={c.icon} size={16} className={i === active ? "text-accent" : "text-fg3"} />
                  <span className="min-w-0 flex-1 truncate font-medium">{c.label}</span>
                  {c.hint && <span className="text-xs text-fg3">{c.hint}</span>}
                  {i === active && <Icon name="chevronRight" size={14} className="text-fg3" />}
                </button>
              </div>
            );
          })}
        </div>

        <div className="flex items-center gap-4 border-t border-hairline bg-surface2 px-4 py-2 text-xs text-fg3">
          <span className="flex items-center gap-1.5">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> to move
          </span>
          <span className="flex items-center gap-1.5">
            <Kbd>↵</Kbd> to open
          </span>
        </div>
      </div>
    </div>,
    document.body,
  );
}
