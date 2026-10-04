import { Fragment, type ReactNode } from "react";
import { Link } from "react-router";
import { cn } from "./cn";
import { Icon, type IconName } from "./Icon";
import { IconButton } from "./Button";
import { Wordmark } from "./Logo";
import { useShell } from "./shell";

export interface Crumb {
  label: ReactNode;
  to?: string;
}

/**
 * The frame every signed-in screen uses: a sticky bar with breadcrumbs, a
 * header with title, description and actions, optional tabs, then content.
 */
export function Page({
  title,
  titleAdornment,
  description,
  meta,
  actions,
  crumbs,
  tabs,
  children,
}: {
  title: ReactNode;
  titleAdornment?: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  crumbs?: Crumb[];
  tabs?: ReactNode;
  children: ReactNode;
}) {
  const shell = useShell();
  const trail = crumbs ?? [{ label: title }];

  return (
    <>
      <div className="sticky top-0 z-20 flex h-12 items-center gap-2 border-b border-chrome-edge bg-chrome px-4 backdrop-blur-xl lg:px-8">
        <IconButton icon="menu" label="Open navigation" className="-ml-1.5 lg:hidden" onClick={shell.openNav} />
        <Link to="/" className="mr-1 text-accent lg:hidden" aria-label="Windlass home">
          <Wordmark height={16} />
        </Link>
        <nav aria-label="Breadcrumb" className="min-w-0 flex-1">
          <ol className="flex min-w-0 items-center gap-1.5 text-sm">
            {trail.map((c, i) => {
              const last = i === trail.length - 1;
              return (
                <Fragment key={i}>
                  {i > 0 && (
                    <li aria-hidden="true" className="text-fg3">
                      <Icon name="chevronRight" size={14} />
                    </li>
                  )}
                  <li className={cn("min-w-0", !last && "hidden sm:block")}>
                    {c.to && !last ? (
                      <Link to={c.to} className="block truncate font-medium text-fg3 transition-colors hover:text-fg">
                        {c.label}
                      </Link>
                    ) : (
                      <span aria-current={last ? "page" : undefined} className="block truncate font-semibold text-fg">
                        {c.label}
                      </span>
                    )}
                  </li>
                </Fragment>
              );
            })}
          </ol>
        </nav>
        <IconButton icon="search" label="Search" className="lg:hidden" onClick={shell.openSearch} />
      </div>

      <div className="mx-auto w-full max-w-[100rem]">
        <header className={cn("px-4 pt-7 sm:px-6 lg:px-8", tabs ? "border-b border-hairline" : "pb-2")}>
          <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
                <h1 className="min-w-0 truncate text-2xl font-semibold tracking-[-0.022em] text-fg">{title}</h1>
                {titleAdornment}
              </div>
              {description && <p className="mt-1 max-w-[80ch] text-sm text-fg3">{description}</p>}
              {meta && <div className="mt-2.5 flex flex-wrap items-center gap-x-5 gap-y-1.5 text-sm text-fg2">{meta}</div>}
            </div>
            {actions && <div className="flex flex-none flex-wrap items-center gap-2">{actions}</div>}
          </div>
          {tabs && <div className="mt-5">{tabs}</div>}
        </header>
        <div className="px-4 pb-20 pt-6 sm:px-6 lg:px-8">{children}</div>
      </div>
    </>
  );
}

/** One item in a page header's meta row: icon plus text. */
export function MetaItem({ icon, children, className }: { icon: IconName; children: ReactNode; className?: string }) {
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1.5", className)}>
      <Icon name={icon} size={15} className="text-fg3" />
      <span className="min-w-0 truncate">{children}</span>
    </span>
  );
}

export function SectionHead({
  title,
  description,
  actions,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mb-3 flex flex-wrap items-end justify-between gap-3", className)}>
      <div className="min-w-0">
        <h2 className="text-md font-semibold tracking-[-0.01em] text-fg">{title}</h2>
        {description && <p className="mt-0.5 text-sm text-fg3">{description}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

export function EmptyState({
  icon,
  title,
  desc,
  actions,
  className,
}: {
  icon?: IconName;
  title: ReactNode;
  desc?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center rounded-card border border-dashed border-edge px-6 py-14 text-center",
        className,
      )}
    >
      {icon && (
        <div className="mb-4 grid h-11 w-11 place-items-center rounded-[10px] border border-hairline bg-surface text-fg2 shadow-[var(--shadow-sm)]">
          <Icon name={icon} size={20} />
        </div>
      )}
      <h3 className="text-md font-semibold text-fg">{title}</h3>
      {desc && <p className="mt-1 max-w-[52ch] text-sm text-fg3">{desc}</p>}
      {actions && <div className="mt-5 flex flex-wrap justify-center gap-2">{actions}</div>}
    </div>
  );
}

type CalloutTone = "info" | "ok" | "warn" | "err";

const calloutStyle: Record<CalloutTone, { box: string; icon: IconName; iconClass: string }> = {
  info: { box: "border-accent-edge bg-accent-soft", icon: "info", iconClass: "text-accent" },
  ok: { box: "border-[color-mix(in_oklab,var(--ok)_28%,transparent)] bg-ok-soft", icon: "checkCircle", iconClass: "text-ok" },
  warn: { box: "border-[color-mix(in_oklab,var(--warn)_30%,transparent)] bg-warn-soft", icon: "warning", iconClass: "text-warn" },
  err: { box: "border-[color-mix(in_oklab,var(--err)_28%,transparent)] bg-err-soft", icon: "xCircle", iconClass: "text-err" },
};

/** Inline message box with a tone, for state that needs reading, not a toast. */
export function Callout({
  tone = "info",
  title,
  children,
  onClose,
  className,
}: {
  tone?: CalloutTone;
  title?: ReactNode;
  children?: ReactNode;
  onClose?: () => void;
  className?: string;
}) {
  const s = calloutStyle[tone];
  return (
    <div role={tone === "err" ? "alert" : undefined} className={cn("flex items-start gap-3 rounded-card border px-4 py-3", s.box, className)}>
      <Icon name={s.icon} size={17} className={cn("mt-px", s.iconClass)} />
      <div className="min-w-0 flex-1 text-sm text-fg2">
        {title && <div className="font-semibold text-fg">{title}</div>}
        {children && <div className={cn(Boolean(title) && "mt-0.5")}>{children}</div>}
      </div>
      {onClose && (
        <button
          type="button"
          onClick={onClose}
          aria-label="Dismiss"
          className="-mr-1 grid h-6 w-6 flex-none place-items-center rounded-[6px] text-fg3 hover:bg-[color-mix(in_oklab,var(--fg)_8%,transparent)] hover:text-fg"
        >
          <Icon name="x" size={14} />
        </button>
      )}
    </div>
  );
}

/** Small inline error line under a form. */
export function FormError({ error, fallback = "Something went wrong" }: { error: unknown; fallback?: string }) {
  if (!error) return null;
  return (
    <p role="alert" className="text-sm text-err">
      {error instanceof Error && error.message ? error.message : fallback}
    </p>
  );
}
