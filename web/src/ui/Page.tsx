import type { ReactNode } from "react";
import { cn } from "./cn";

export function Page({
  title,
  subtitle,
  actions,
  children,
  width = "90%",
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  width?: number | string;
}) {
  return (
    <>
      <header className="sticky top-0 z-20 flex min-h-[72px] items-center justify-between gap-4 border-b border-chrome-edge bg-chrome px-6 py-4 backdrop-blur-xl md:px-10">
        <div className="min-w-0">
          <h1 className="truncate text-xl font-semibold tracking-[-0.016em]">{title}</h1>
          {subtitle && (
            <div className="mt-0.5 truncate font-mono text-xs tracking-[-0.01em] text-fg3">
              {subtitle}
            </div>
          )}
        </div>
        {actions && <div className="flex flex-none items-center gap-2">{actions}</div>}
      </header>
      <div className={cn("mx-auto px-6 pb-20 pt-8 md:px-10")} style={{ maxWidth: width }}>
        {children}
      </div>
    </>
  );
}

export function SectionHead({
  title,
  actions,
  className,
}: {
  title: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mb-3.5 mt-9 flex items-center justify-between first:mt-0", className)}>
      <h2 className="text-lg font-semibold tracking-[-0.012em]">{title}</h2>
      {actions}
    </div>
  );
}

export function EmptyState({
  icon,
  title,
  desc,
}: {
  icon?: ReactNode;
  title: ReactNode;
  desc?: ReactNode;
}) {
  return (
    <div className="grid place-items-center rounded-[13px] border border-dashed border-edge px-5 py-12 text-center">
      {icon && <div className="mb-2.5 text-fg3 opacity-70">{icon}</div>}
      <div className="text-md font-medium text-fg2">{title}</div>
      {desc && <div className="mt-1 text-sm text-fg3">{desc}</div>}
    </div>
  );
}
