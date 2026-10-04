import type { ReactNode } from "react";
import { cn } from "./cn";

interface Option<T extends string> {
  value: T;
  label?: ReactNode;
  icon?: ReactNode;
  title?: string;
}

export function Segmented<T extends string>({
  options,
  value,
  onChange,
  size = "md",
  className,
  label,
}: {
  options: Option<T>[];
  value: T;
  onChange: (v: T) => void;
  size?: "sm" | "md";
  className?: string;
  /** Accessible name for the group. */
  label?: string;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className={cn("inline-flex gap-0.5 rounded-control border border-hairline bg-sunken p-0.5", className)}
    >
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            title={o.title}
            aria-label={o.label ? undefined : o.title}
            aria-pressed={on}
            onClick={() => onChange(o.value)}
            className={cn(
              "inline-flex flex-1 items-center justify-center gap-1.5 whitespace-nowrap rounded-[6px] border font-medium transition-[background-color,color,box-shadow] duration-150",
              size === "sm" ? "h-6 px-2 text-xs" : "h-7 px-2.5 text-sm",
              on
                ? "border-hairline bg-surface text-fg shadow-[var(--shadow-sm)]"
                : "border-transparent text-fg3 hover:text-fg",
            )}
          >
            {o.icon}
            {o.label}
          </button>
        );
      })}
    </div>
  );
}
