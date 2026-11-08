import { forwardRef, useEffect, useImperativeHandle, useRef, type ReactNode } from "react";
import { cn } from "../ui/cn";

/**
 * Whether the pane should keep following the bottom after a scroll event.
 * Only an upward move (scrollTop decreased) away from the bottom turns
 * following off. Content growing under a pinned bottom also moves the
 * bottom away, but without a scrollTop decrease it must not count as the
 * reader leaving, or the pane stops following on its own output.
 */
export function nextAtBottom(
  wasAtBottom: boolean,
  prevScrollTop: number,
  el: { scrollTop: number; clientHeight: number; scrollHeight: number },
): boolean {
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 24) return true;
  return el.scrollTop < prevScrollTop ? false : wasAtBottom;
}

/** Follow state kept across scroll events: the bottom flag and the last scrollTop seen. */
export type ScrollState = { atBottom: boolean; lastScrollTop: number };

/** The onScroll handler body: updates the follow state from one scroll event. */
export function onPaneScroll(
  state: ScrollState,
  el: { scrollTop: number; clientHeight: number; scrollHeight: number },
): void {
  state.atBottom = nextAtBottom(state.atBottom, state.lastScrollTop, el);
  state.lastScrollTop = el.scrollTop;
}

/**
 * Dark output pane for streamed text. It follows the bottom while the reader
 * is at the bottom, and stays put once they scroll up to read something.
 */
export const TermPane = forwardRef<
  HTMLDivElement,
  { children: ReactNode; follow?: boolean; tick: number; wrap?: boolean; className?: string }
>(function TermPane({ children, follow = true, tick, wrap = true, className }, outer) {
  const pane = useRef<HTMLDivElement>(null);
  const scroll = useRef<ScrollState>({ atBottom: true, lastScrollTop: 0 });
  useImperativeHandle(outer, () => pane.current!);

  useEffect(() => {
    const el = pane.current;
    if (el && follow && scroll.current.atBottom) el.scrollTop = el.scrollHeight;
  }, [tick, follow]);

  return (
    <div
      ref={pane}
      onScroll={(e) => onPaneScroll(scroll.current, e.currentTarget)}
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
