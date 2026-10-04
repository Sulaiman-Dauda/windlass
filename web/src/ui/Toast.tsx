import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Icon, type IconName } from "./Icon";
import { cn } from "./cn";

type ToastTone = "ok" | "err" | "info";

interface ToastItem {
  id: number;
  tone: ToastTone;
  title: ReactNode;
  description?: ReactNode;
}

type Show = (toast: Omit<ToastItem, "id">) => void;

const ToastCtx = createContext<Show>(() => {});

const toneIcon: Record<ToastTone, { icon: IconName; className: string }> = {
  ok: { icon: "checkCircle", className: "text-ok" },
  err: { icon: "xCircle", className: "text-err" },
  info: { icon: "info", className: "text-accent" },
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const next = useRef(1);

  const dismiss = useCallback((id: number) => setItems((all) => all.filter((t) => t.id !== id)), []);

  const show = useCallback<Show>(
    (toast) => {
      const id = next.current++;
      setItems((all) => [...all.slice(-3), { ...toast, id }]);
      // Errors stay longer: they are the ones somebody needs to read.
      setTimeout(() => dismiss(id), toast.tone === "err" ? 8000 : 4500);
    },
    [dismiss],
  );

  return (
    <ToastCtx.Provider value={show}>
      {children}
      {createPortal(
        <div
          aria-live="polite"
          className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-[360px] max-w-[calc(100vw-32px)] flex-col gap-2"
        >
          {items.map((t) => (
            <div
              key={t.id}
              role="status"
              className="pointer-events-auto flex items-start gap-3 rounded-card border border-hairline bg-surface px-4 py-3 shadow-[var(--shadow-lg)]"
              style={{ animation: "wl-rise 0.22s var(--ease)" }}
            >
              <Icon name={toneIcon[t.tone].icon} size={18} className={cn("mt-px", toneIcon[t.tone].className)} />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-semibold text-fg">{t.title}</div>
                {t.description && <div className="mt-0.5 break-words text-xs text-fg2">{t.description}</div>}
              </div>
              <button
                type="button"
                onClick={() => dismiss(t.id)}
                aria-label="Dismiss"
                className="-mr-1 grid h-6 w-6 place-items-center rounded-[6px] text-fg3 hover:bg-sunken hover:text-fg"
              >
                <Icon name="x" size={14} />
              </button>
            </div>
          ))}
        </div>,
        document.body,
      )}
    </ToastCtx.Provider>
  );
}

export function useToast() {
  const show = useContext(ToastCtx);
  return useMemo(
    () => ({
      ok: (title: ReactNode, description?: ReactNode) => show({ tone: "ok", title, description }),
      err: (title: ReactNode, description?: ReactNode) => show({ tone: "err", title, description }),
      info: (title: ReactNode, description?: ReactNode) => show({ tone: "info", title, description }),
    }),
    [show],
  );
}

/** Message from a failed mutation, or a fallback. */
export function errorText(error: unknown, fallback = "Something went wrong"): string {
  return error instanceof Error && error.message ? error.message : fallback;
}
