import {
  forwardRef,
  useId,
  type InputHTMLAttributes,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
  type ReactNode,
} from "react";
import { cn } from "./cn";

// Note for callers: cn() does no Tailwind conflict resolution, so never pass a
// class that competes with one below (a second height, width or padding).
// Size a control with `controlSize`, and width it with a wrapper element.
const control =
  "w-full rounded-control border border-edge bg-surface text-fg shadow-[var(--shadow-xs)] " +
  "placeholder:text-fg3 outline-none transition-[border-color,box-shadow] duration-150 " +
  "hover:border-edge-strong focus:border-accent focus:shadow-[0_0_0_3px_var(--color-ring)] " +
  "focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-55 " +
  "aria-[invalid=true]:border-err";

type ControlSize = "sm" | "md" | "lg";

const heights: Record<ControlSize, string> = {
  sm: "h-8 px-2.5 text-sm",
  md: "h-9 px-3 text-sm",
  lg: "h-10 px-3.5 text-md",
};

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  controlSize?: ControlSize;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { className, controlSize = "md", ...rest },
  ref,
) {
  return <input ref={ref} className={cn(control, heights[controlSize], className)} {...rest} />;
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  function Textarea({ className, ...rest }, ref) {
    return (
      <textarea
        ref={ref}
        className={cn(control, "min-h-[104px] resize-y px-3 py-2.5 text-sm leading-relaxed", className)}
        {...rest}
      />
    );
  },
);

const caret =
  "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='%23858b96' stroke-width='2.2' stroke-linecap='round' stroke-linejoin='round'><path d='m7 10 5 5 5-5'/></svg>\")";

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  controlSize?: ControlSize;
}

export function Select({ className, children, controlSize = "md", ...rest }: SelectProps) {
  return (
    <select
      className={cn(control, heights[controlSize], "cursor-pointer appearance-none bg-no-repeat pr-8", className)}
      style={{ backgroundImage: caret, backgroundPosition: "right 10px center" }}
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
  as: Tag = "label",
}: {
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  children: ReactNode;
  className?: string;
  /** Use "div" when the content holds buttons: a label would forward clicks to them. */
  as?: "label" | "div";
}) {
  return (
    <Tag className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      {label && <span className="text-xs font-semibold text-fg2">{label}</span>}
      {children}
      {error ? (
        <span className="text-xs text-err">{error}</span>
      ) : (
        hint && <span className="text-xs text-fg3">{hint}</span>
      )}
    </Tag>
  );
}

/** An on/off control that saves nothing by itself: the caller decides when. */
export function Switch({
  checked,
  onChange,
  label,
  description,
  disabled,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label?: ReactNode;
  description?: ReactNode;
  disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className="flex items-start gap-3">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={cn(
          "relative mt-0.5 inline-flex h-5 w-9 flex-none cursor-pointer items-center rounded-full border transition-colors duration-150 disabled:opacity-50",
          checked ? "border-transparent bg-accent-fill" : "border-edge bg-sunken",
        )}
      >
        <span
          className={cn(
            "inline-block h-4 w-4 rounded-full bg-white shadow-[0_1px_2px_rgba(0,0,0,0.25)] transition-transform duration-150",
            checked ? "translate-x-[17px]" : "translate-x-[1px]",
          )}
        />
      </button>
      {(label || description) && (
        <label htmlFor={id} className="min-w-0 cursor-pointer select-none">
          {label && <span className="block text-sm font-medium text-fg">{label}</span>}
          {description && <span className="mt-0.5 block text-xs text-fg3">{description}</span>}
        </label>
      )}
    </div>
  );
}
