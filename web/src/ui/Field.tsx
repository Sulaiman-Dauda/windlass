import type {
  InputHTMLAttributes,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
  ReactNode,
} from "react";
import { cn } from "./cn";

// Comfortable controls: generous vertical padding, 15px text, hairline border,
// accent focus ring.
const control =
  "w-full rounded-[10px] border border-edge bg-surface text-md text-fg placeholder:text-fg3 " +
  "px-3.5 py-2.5 outline-none transition-[border-color,box-shadow] duration-200 " +
  "focus:border-accent focus:shadow-[0_0_0_3.5px_var(--color-ring)] disabled:opacity-50";

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn(control, className)} {...rest} />;
}

export function Textarea({ className, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn(control, "min-h-[104px] resize-y leading-relaxed", className)} {...rest} />;
}

const caret =
  "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='%23878c97' stroke-width='2.2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>\")";

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(control, "cursor-pointer appearance-none bg-no-repeat pr-9", className)}
      style={{ backgroundImage: caret, backgroundPosition: "right 12px center" }}
      {...rest}
    >
      {children}
    </select>
  );
}

export function Field({
  label,
  hint,
  error,
  children,
  className,
}: {
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <label className={cn("flex flex-col gap-1.5", className)}>
      {label && <span className="text-xs font-semibold text-fg2">{label}</span>}
      {children}
      {error ? (
        <span className="text-xs text-err">{error}</span>
      ) : (
        hint && <span className="text-xs text-fg3">{hint}</span>
      )}
    </label>
  );
}
