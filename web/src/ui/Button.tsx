import type { ButtonHTMLAttributes, ReactNode } from "react";
import { cn } from "./cn";

export type Variant = "primary" | "secondary" | "ghost" | "danger" | "dangerSolid";
export type Size = "sm" | "md" | "lg";

const base =
  "inline-flex items-center justify-center gap-2 font-semibold whitespace-nowrap rounded-[10px] " +
  "border border-transparent transition-[background-color,border-color,box-shadow,transform] duration-200 " +
  "active:translate-y-[0.5px] disabled:opacity-45 disabled:pointer-events-none cursor-pointer select-none";

const sizes: Record<Size, string> = {
  sm: "text-sm px-3.5 py-2 gap-1.5",
  md: "text-md px-4 py-2.5",
  lg: "text-md px-5 py-3",
};

const variants: Record<Variant, string> = {
  primary: "bg-accent-fill text-onaccent shadow-[var(--shadow-sm)] hover:bg-accent-fill-hi",
  secondary:
    "bg-surface text-fg border-edge shadow-[var(--shadow-sm)] hover:bg-surface2 hover:border-edge-strong",
  ghost: "text-fg2 hover:bg-surface2 hover:text-fg",
  danger:
    "text-err border-[color-mix(in_oklab,var(--err)_42%,transparent)] hover:bg-err-soft",
  dangerSolid: "bg-err text-white hover:bg-err-hi",
};

export function btn(variant: Variant = "secondary", size: Size = "md", extra?: string) {
  return cn(base, sizes[size], variants[variant], extra);
}

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  block?: boolean;
  children: ReactNode;
}

export function Button({ variant = "secondary", size = "md", block, className, children, ...rest }: Props) {
  return (
    <button className={cn(btn(variant, size), block && "w-full", className)} {...rest}>
      {children}
    </button>
  );
}
