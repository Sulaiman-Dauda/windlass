import { forwardRef, useImperativeHandle, useLayoutEffect, useRef, type ReactNode } from "react";
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

/** The row at the top of the reader's view, where it sat, and the scrollTop it was measured at. */
type Anchor = { row: Element; y: number; scrollTop: number };

/** Finds the first row that reaches below the pane's top edge. Rows are in document order, so this bisects. */
function firstRowInView(el: HTMLElement): Anchor | null {
  const top = el.getBoundingClientRect().top;
  const rows = el.children;
  let lo = 0;
  let hi = rows.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (rows[mid].getBoundingClientRect().bottom > top) hi = mid;
    else lo = mid + 1;
  }
  if (lo === rows.length) return null;
  return { row: rows[lo], y: rows[lo].getBoundingClientRect().top - top, scrollTop: el.scrollTop };
}

/**
 * Dark output pane for streamed text. It follows the bottom while the reader
 * is at the bottom, and holds the reader's place once they scroll up to read
 * something, even when a capped log drops rows above them.
 *
 * Holding the place needs each row to keep its DOM node between renders, so
 * callers key rows by something stable (a sequence number), never the index.
 */
export const TermPane = forwardRef<
  HTMLDivElement,
  { children: ReactNode; follow?: boolean; tick: number; wrap?: boolean; className?: string }
>(function TermPane({ children, follow = true, tick, wrap = true, className }, outer) {
  const pane = useRef<HTMLDivElement>(null);
  const scroll = useRef<ScrollState>({ atBottom: true, lastScrollTop: 0 });
  const anchor = useRef<Anchor | null>(null);
  useImperativeHandle(outer, () => pane.current!);

  // A layout effect, so the pin lands before paint and the reader never sees
  // a frame of the pane short of the bottom.
  useLayoutEffect(() => {
    const el = pane.current;
    if (el && follow && scroll.current.atBottom) el.scrollTop = el.scrollHeight;
  }, [tick, follow]);

  // Runs after every render. When the pane is not pinned to the bottom, put
  // the row the reader was looking at back where it was. Rows dropped from the
  // top of a capped log would otherwise slide the text up under them. The
  // browser's own scroll anchoring is switched off on the pane (its timing is
  // what stopped the pane following in #109, and support varies by browser),
  // so this is the only thing that moves scrollTop besides the pin and the reader.
  useLayoutEffect(() => {
    const el = pane.current;
    if (!el) return;
    if (follow && scroll.current.atBottom) {
      // The pin moves the text under a fixed scrollTop at the cap, so an
      // anchor kept from before would be stale by the time following stops,
      // and correcting to it jumps the view up. Measure afresh then instead.
      anchor.current = null;
      return;
    }
    const a = anchor.current;
    // Only trust an anchor measured at the current scrollTop. Anything else
    // means the pane moved since (the pin, or a scroll whose event has not
    // arrived yet), and correcting to it would undo that move.
    if (a && a.row.parentElement === el && a.scrollTop === el.scrollTop) {
      const drift = a.row.getBoundingClientRect().top - el.getBoundingClientRect().top - a.y;
      if (Math.abs(drift) >= 0.5) {
        el.scrollTop += drift;
        a.scrollTop = el.scrollTop;
        // Our own move, not the reader's: keep onPaneScroll from reading it as a scroll up.
        scroll.current.lastScrollTop = el.scrollTop;
      }
    } else {
      anchor.current = firstRowInView(el);
    }
  });

  return (
    <div
      ref={pane}
      onScroll={(e) => {
        onPaneScroll(scroll.current, e.currentTarget);
        // A scroll event at the scrollTop the correction above just set is our
        // own move. Re-measuring then would make the rounded position the new
        // baseline, and the fraction of a pixel lost each time adds up (#116).
        // Keeping the anchor lets the next correction take that fraction back.
        const a = anchor.current;
        if (a && a.row.parentElement === e.currentTarget && a.scrollTop === e.currentTarget.scrollTop) return;
        anchor.current = firstRowInView(e.currentTarget);
      }}
      className={cn(
        "wl-term overflow-auto bg-term px-4 py-3 font-mono text-xs leading-[1.7] text-term-fg [overflow-anchor:none]",
        wrap ? "whitespace-pre-wrap break-words" : "whitespace-pre",
        className,
      )}
    >
      {children}
    </div>
  );
});
