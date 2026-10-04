import { forwardRef, useEffect, useImperativeHandle, useRef, type ReactNode } from "react";
import { cn } from "../ui/cn";

/**
 * Dark output pane for streamed text. It follows the bottom while the reader
 * is at the bottom, and stays put once they scroll up to read something.
 */
export const TermPane = forwardRef<
  HTMLDivElement,
  { children: ReactNode; follow?: boolean; tick: number; wrap?: boolean; className?: string }
>(function TermPane({ children, follow = true, tick, wrap = true, className }, outer) {
  const pane = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  useImperativeHandle(outer, () => pane.current!);

  useEffect(() => {
    const el = pane.current;
    if (el && follow && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [tick, follow]);

  return (
    <div
      ref={pane}
      onScroll={(e) => {
        const el = e.currentTarget;
        atBottom.current = el.scrollTop + el.clientHeight >= el.scrollHeight - 24;
      }}
      className={cn(
        "wl-term overflow-auto bg-term px-4 py-3 font-mono text-xs leading-[1.7] text-term-fg",
        wrap ? "whitespace-pre-wrap break-words" : "whitespace-pre",
        className,
      )}
    >
      {children}
    </div>
  );
});
