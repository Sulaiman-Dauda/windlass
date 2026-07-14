import type { ReactNode } from "react";
import { cn } from "./cn";

export type Tone = "ok" | "warn" | "err" | "idle" | "accent";

const tones: Record<Tone, string> = {
  ok: "text-ok bg-ok-soft",
  warn: "text-warn bg-warn-soft",
  err: "text-err bg-err-soft",
  idle: "text-fg2 bg-sunken",
  accent: "text-accent bg-accent-soft",
};

export function StatusPill({
  tone,
  children,
  live,
  className,
}: {
  tone: Tone;
  children: ReactNode;
  live?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold leading-none",
        tones[tone],
        className,
      )}
    >
      <span
        className="h-1.5 w-1.5 rounded-full bg-current"
        style={live ? { animation: "wl-pulse 1.8s var(--ease) infinite" } : undefined}
      />
      {children}
    </span>
  );
}

export function Chip({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-lg border border-hairline bg-sunken px-2.5 py-1 font-mono text-xs text-fg2",
        className,
      )}
    >
      {children}
    </span>
  );
}
