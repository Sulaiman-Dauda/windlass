import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "./cn";

export function Card({ className, children, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("rounded-card border border-hairline bg-surface shadow-[var(--shadow-xs)]", className)}
      {...rest}
    >
      {children}
    </div>
  );
}

export function CardHeader({
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
    <div className={cn("flex flex-wrap items-start justify-between gap-x-4 gap-y-2 border-b border-hairline px-5 py-4", className)}>
      <div className="min-w-0 flex-1">
        <h3 className="text-md font-semibold tracking-[-0.01em] text-fg">{title}</h3>
        {description && <p className="mt-0.5 max-w-[72ch] text-sm text-fg3">{description}</p>}
      </div>
      {actions && <div className="flex flex-none flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

/** Footer strip: a note on the left, actions on the right. */
export function CardFooter({
  note,
  children,
  className,
}: {
  note?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center justify-between gap-3 rounded-b-card border-t border-hairline bg-surface2 px-5 py-3",
        className,
      )}
    >
      <div className="min-w-0 flex-1 text-xs text-fg3">{note}</div>
      {children && <div className="flex flex-none items-center gap-2">{children}</div>}
    </div>
  );
}
