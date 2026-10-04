import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { cn } from "./cn";
import { Icon, type IconName } from "./Icon";
import { Spinner } from "./Spinner";

export type Variant = "primary" | "secondary" | "ghost" | "danger" | "dangerSolid";
export type Size = "xs" | "sm" | "md" | "lg";

const base =
  "relative inline-flex items-center justify-center font-semibold whitespace-nowrap rounded-control " +
  "border transition-[background-color,border-color,color,box-shadow] duration-150 " +
  "disabled:opacity-50 disabled:pointer-events-none cursor-pointer select-none";

const sizes: Record<Size, string> = {
  xs: "h-7 px-2.5 text-xs gap-1.5",
  sm: "h-8 px-3 text-sm gap-1.5",
  md: "h-9 px-3.5 text-sm gap-2",
  lg: "h-10 px-4 text-md gap-2",
};

const iconSizes: Record<Size, number> = { xs: 14, sm: 15, md: 16, lg: 17 };

const variants: Record<Variant, string> = {
  primary:
    "border-transparent bg-accent-fill text-onaccent hover:bg-accent-fill-hi " +
    "shadow-[inset_0_1px_0_rgba(255,255,255,0.14),var(--shadow-sm)]",
  secondary:
    "border-edge bg-surface text-fg shadow-[var(--shadow-xs)] hover:border-edge-strong hover:bg-surface2",
  ghost: "border-transparent text-fg2 hover:bg-sunken hover:text-fg",
  danger:
    "border-edge bg-surface text-err shadow-[var(--shadow-xs)] " +
    "hover:border-[color-mix(in_oklab,var(--err)_40%,transparent)] hover:bg-err-soft",
  dangerSolid:
    "border-transparent bg-err text-white hover:bg-err-hi shadow-[inset_0_1px_0_rgba(255,255,255,0.14),var(--shadow-sm)]",
};

/** Class string for anything that should look like a button (links included). */
export function btn(variant: Variant = "secondary", size: Size = "md", extra?: string) {
  return cn(base, sizes[size], variants[variant], extra);
}

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  block?: boolean;
  icon?: IconName;
  /** Shows a spinner in place of the icon and blocks further clicks. */
  loading?: boolean;
  children?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, Props>(function Button(
  { variant = "secondary", size = "md", block, icon, loading, className, children, disabled, type, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type ?? "button"}
      className={cn(btn(variant, size), block && "w-full", className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading ? (
        <Spinner size={iconSizes[size] - 2} />
      ) : (
        icon && <Icon name={icon} size={iconSizes[size]} />
      )}
      {children}
    </button>
  );
});

const iconButtonSizes: Record<Size, string> = {
  xs: "h-7 w-7",
  sm: "h-8 w-8",
  md: "h-9 w-9",
  lg: "h-10 w-10",
};

interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  icon: IconName;
  /** Accessible name, also shown as the native tooltip. */
  label: string;
  variant?: Variant;
  size?: Size;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon, label, variant = "ghost", size = "sm", className, type, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type ?? "button"}
      aria-label={label}
      title={label}
      className={cn(base, variants[variant], iconButtonSizes[size], "px-0", className)}
      {...rest}
    >
      <Icon name={icon} size={iconSizes[size]} />
    </button>
  );
});
