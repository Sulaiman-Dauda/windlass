import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  type Ref,
} from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router";
import { Icon, type IconName } from "./Icon";
import { cn } from "./cn";

interface TriggerProps {
  ref: Ref<HTMLButtonElement>;
  onClick: () => void;
  "aria-haspopup": "menu";
  "aria-expanded": boolean;
}

const MenuCtx = createContext<() => void>(() => {});

/**
 * Dropdown menu. The panel is portalled and positioned against the trigger,
 * so it is never clipped by a scrolling or overflow-hidden ancestor. It
 * closes on outside press, Escape, scroll and resize.
 */
export function Menu({
  trigger,
  children,
  align = "end",
  side = "bottom",
  width = 224,
}: {
  trigger: (props: TriggerProps) => ReactNode;
  children: ReactNode;
  align?: "start" | "end";
  side?: "bottom" | "top";
  width?: number;
}) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const close = (refocus = true) => {
    setOpen(false);
    setPos(null);
    if (refocus) triggerRef.current?.focus();
  };

  useLayoutEffect(() => {
    if (!open || !triggerRef.current || !menuRef.current) return;
    const r = triggerRef.current.getBoundingClientRect();
    const h = menuRef.current.offsetHeight;
    const gap = 6;
    const fitsBelow = r.bottom + gap + h <= window.innerHeight - 8;
    const fitsAbove = r.top - gap - h >= 8;
    const placeTop = side === "top" ? fitsAbove || !fitsBelow : !fitsBelow && fitsAbove;
    const top = placeTop ? r.top - gap - h : r.bottom + gap;
    let left = align === "end" ? r.right - width : r.left;
    left = Math.max(8, Math.min(left, window.innerWidth - width - 8));
    setPos({ top, left });
  }, [open, align, side, width]);

  // Focus the first item only once the menu is placed: until then it is
  // visibility: hidden, which cannot take focus.
  const placed = pos !== null;
  useEffect(() => {
    if (placed) menuRef.current?.querySelector<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])')?.focus();
  }, [placed]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (menuRef.current?.contains(t) || triggerRef.current?.contains(t)) return;
      close(false);
    };
    // Close only when the scroll moves the trigger. A log pane following new
    // lines elsewhere on the page scrolls constantly and must not shut it.
    const onScroll = (e: Event) => {
      const t = e.target as Node;
      if (menuRef.current?.contains(t)) return;
      if (t.contains(triggerRef.current)) close(false);
    };
    const onResize = () => close(false);
    document.addEventListener("pointerdown", onDown);
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", onResize);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onResize);
    };
  }, [open]);

  const onKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const items = Array.from(
      menuRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])') ?? [],
    );
    const i = items.indexOf(document.activeElement as HTMLElement);
    if (e.key === "ArrowDown") {
      e.preventDefault();
      items[(i + 1) % items.length]?.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      items[(i - 1 + items.length) % items.length]?.focus();
    } else if (e.key === "Home") {
      e.preventDefault();
      items[0]?.focus();
    } else if (e.key === "End") {
      e.preventDefault();
      items[items.length - 1]?.focus();
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close();
    } else if (e.key === "Tab") {
      close(false);
    }
  };

  return (
    <>
      {trigger({
        ref: triggerRef,
        onClick: () => (open ? close(false) : setOpen(true)),
        "aria-haspopup": "menu",
        "aria-expanded": open,
      })}
      {open &&
        createPortal(
          <MenuCtx.Provider value={() => close()}>
            <div
              ref={menuRef}
              role="menu"
              onKeyDown={onKeyDown}
              className="fixed z-50 rounded-card border border-hairline bg-surface p-1 shadow-[var(--shadow-lg)]"
              style={{
                width,
                top: pos?.top ?? -9999,
                left: pos?.left ?? -9999,
                visibility: pos ? "visible" : "hidden",
                animation: "wl-drop 0.14s var(--ease)",
              }}
            >
              {children}
            </div>
          </MenuCtx.Provider>,
          document.body,
        )}
    </>
  );
}

const itemClass =
  "flex h-8 w-full cursor-pointer items-center gap-2.5 rounded-[6px] px-2.5 text-left text-sm font-medium outline-none " +
  "transition-colors duration-100 focus-visible:outline-none";

export function MenuItem({
  icon,
  children,
  onSelect,
  to,
  href,
  tone,
  disabled,
  hint,
}: {
  icon?: IconName;
  children: ReactNode;
  onSelect?: () => void;
  to?: string;
  href?: string;
  tone?: "danger";
  disabled?: boolean;
  hint?: ReactNode;
}) {
  const close = useContext(MenuCtx);
  const className = cn(
    itemClass,
    disabled
      ? "cursor-not-allowed text-fg3 opacity-60"
      : tone === "danger"
        ? "text-err hover:bg-err-soft focus:bg-err-soft"
        : "text-fg hover:bg-sunken focus:bg-sunken",
  );
  const content = (
    <>
      {icon && <Icon name={icon} size={16} className={tone === "danger" ? "" : "text-fg3"} />}
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {hint && <span className="text-xs text-fg3">{hint}</span>}
    </>
  );

  if (to && !disabled) {
    return (
      <Link role="menuitem" to={to} className={className} onClick={() => close()}>
        {content}
      </Link>
    );
  }
  if (href && !disabled) {
    return (
      <a role="menuitem" href={href} className={className} onClick={() => close()}>
        {content}
      </a>
    );
  }
  return (
    <button
      type="button"
      role="menuitem"
      aria-disabled={disabled || undefined}
      className={className}
      onClick={() => {
        if (disabled) return;
        close();
        onSelect?.();
      }}
    >
      {content}
    </button>
  );
}

export function MenuSeparator() {
  return <div role="separator" className="-mx-1 my-1 h-px bg-hairline" />;
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <div className="px-2.5 pb-1 pt-1.5 text-2xs font-semibold uppercase tracking-[0.05em] text-fg3">{children}</div>;
}
