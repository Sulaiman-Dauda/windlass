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
}: {
  options: Option<T>[];
  value: T;
  onChange: (v: T) => void;
  size?: "sm" | "md";
  className?: string;
}) {
  return (
    <div
      role="group"
      className={cn(
        "inline-flex gap-0.5 rounded-[10px] border border-hairline bg-sunken p-1",
        className,
      )}
    >
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            title={o.title}
            aria-pressed={on}
            onClick={() => onChange(o.value)}
            className={cn(
              "inline-flex items-center justify-center gap-2 rounded-[7px] font-medium transition-[background-color,color,box-shadow] duration-200",
              size === "sm" ? "px-2.5 py-1 text-sm" : "px-3.5 py-1.5 text-sm",
              !o.label && "px-2.5",
              on
                ? "bg-surface text-accent shadow-[var(--shadow-sm)] border border-hairline"
                : "border border-transparent text-fg3 hover:text-fg",
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
