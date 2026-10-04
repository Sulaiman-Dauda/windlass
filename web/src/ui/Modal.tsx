import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { Button } from "./Button";
import { cn } from "./cn";

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Focus handling for a modal layer: focus moves in on open, Tab cycles inside,
 * Escape closes, the page behind stops scrolling, and focus returns to
 * whatever opened it on close. Shared by Modal and the mobile navigation drawer.
 */
export function useModalFocus(panel: RefObject<HTMLElement>, onClose: () => void) {
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null;
    const el = panel.current;
    if (el && !el.contains(document.activeElement)) {
      const first = el.querySelector<HTMLElement>("[autofocus], [data-autofocus]") ?? el;
      first.focus();
    }
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        closeRef.current();
        return;
      }
      if (e.key !== "Tab" || !el) return;
      const items = Array.from(el.querySelectorAll<HTMLElement>(FOCUSABLE));
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.body.style.overflow = overflow;
      opener?.focus?.();
    };
  }, [panel]);
}

/**
 * Dialog rendered into <body>, so no ancestor's transform or backdrop-filter
 * can trap it. Focus moves in on open, cycles inside, and returns to whatever
 * opened it on close.
 */
export function Modal({
  onClose,
  title,
  description,
  children,
  footer,
  width = 460,
  tone,
}: {
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  width?: number;
  tone?: "danger";
}) {
  const panel = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const descId = useId();
  useModalFocus(panel, onClose);

  return createPortal(
    <div
      className="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-overlay p-4 backdrop-blur-[2px]"
      style={{ animation: "wl-fade 0.16s var(--ease)" }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className="flex w-full flex-col rounded-xl2 border border-hairline bg-surface shadow-[var(--shadow-lg)] outline-none"
        style={{ maxWidth: width, animation: "wl-pop 0.2s var(--ease)" }}
      >
        <div className="px-6 pb-1 pt-5">
          <h2
            id={titleId}
            className={cn("text-lg font-semibold tracking-[-0.012em]", tone === "danger" ? "text-err" : "text-fg")}
          >
            {title}
          </h2>
          {description && (
            <div id={descId} className="mt-1.5 text-sm leading-relaxed text-fg2">
              {description}
            </div>
          )}
        </div>
        {children && <div className="px-6 py-4">{children}</div>}
        {footer && (
          <div className="mt-2 flex flex-wrap items-center justify-end gap-2 rounded-b-xl2 border-t border-hairline bg-surface2 px-6 py-3.5">
            {footer}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}

// ---------------------------------------------------------------------------
// Promise-based confirmation, replacing window.confirm() so destructive
// actions read and behave like the rest of the app.

interface ConfirmOptions {
  title: ReactNode;
  body?: ReactNode;
  confirmLabel?: string;
  tone?: "danger" | "primary";
}

type ConfirmFn = (options: ConfirmOptions) => Promise<boolean>;

const ConfirmCtx = createContext<ConfirmFn>(async () => false);

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<(ConfirmOptions & { resolve: (ok: boolean) => void }) | null>(null);

  const confirm = useCallback<ConfirmFn>(
    (options) => new Promise<boolean>((resolve) => setPending({ ...options, resolve })),
    [],
  );

  const settle = (ok: boolean) => {
    pending?.resolve(ok);
    setPending(null);
  };

  return (
    <ConfirmCtx.Provider value={confirm}>
      {children}
      {pending && (
        <Modal
          onClose={() => settle(false)}
          title={pending.title}
          description={pending.body}
          width={420}
          footer={
            <>
              <Button variant="ghost" onClick={() => settle(false)}>
                Cancel
              </Button>
              <Button
                data-autofocus
                variant={pending.tone === "primary" ? "primary" : "dangerSolid"}
                onClick={() => settle(true)}
              >
                {pending.confirmLabel ?? "Confirm"}
              </Button>
            </>
          }
        />
      )}
    </ConfirmCtx.Provider>
  );
}

export function useConfirm(): ConfirmFn {
  return useContext(ConfirmCtx);
}
