import type { HTMLAttributes, ReactNode } from "react";
import { Link } from "react-router-dom";
import { cn } from "./cn";

export function Card({ className, children, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "rounded-[13px] border border-hairline bg-surface shadow-[var(--shadow-sm)]",
        className,
      )}
      {...rest}
    >
      {children}
    </div>
  );
}

export function CardLink({
  to,
  className,
  children,
}: {
  to: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <Link
      to={to}
      className={cn(
        "block rounded-[13px] border border-hairline bg-surface shadow-[var(--shadow-sm)] no-underline",
        "transition-[transform,box-shadow,border-color] duration-200",
        "hover:-translate-y-[2px] hover:border-edge hover:shadow-[var(--shadow-md)]",
        className,
      )}
    >
      {children}
    </Link>
  );
}
