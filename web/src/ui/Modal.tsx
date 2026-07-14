import { useEffect, type ReactNode } from "react";
import { cn } from "./cn";

export function Modal({
  onClose,
  children,
  labelledBy,
  width = 440,
}: {
  onClose: () => void;
  children: ReactNode;
  labelledBy?: string;
  width?: number;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center p-5"
      style={{ background: "rgba(10,12,15,0.42)", backdropFilter: "blur(6px)", WebkitBackdropFilter: "blur(6px)", animation: "wl-fade 0.2s var(--ease)" }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelledBy}
        className={cn(
          "w-full rounded-[16px] border border-hairline bg-surface p-6 shadow-[var(--shadow-lg)]",
        )}
        style={{ maxWidth: width, animation: "wl-pop 0.26s var(--ease)" }}
      >
        {children}
      </div>
    </div>
  );
}
