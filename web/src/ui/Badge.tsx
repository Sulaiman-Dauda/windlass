import type { ReactNode } from "react";
import { cn } from "./cn";
import { Spinner } from "./Spinner";

export type Tone = "ok" | "warn" | "err" | "idle" | "accent";

const tones: Record<Tone, string> = {
  ok: "text-ok bg-ok-soft",
  warn: "text-warn bg-warn-soft",
  err: "text-err bg-err-soft",
  idle: "text-fg2 bg-sunken",
  accent: "text-accent bg-accent-soft",
};

const dotTones: Record<Tone, string> = {
  ok: "bg-ok",
  warn: "bg-warn",
  err: "bg-err",
  idle: "bg-fg3",
  accent: "bg-accent",
};

/** A coloured dot; `live` adds a soft halo that pulses. */
export function StatusDot({ tone, live, className }: { tone: Tone; live?: boolean; className?: string }) {
  return (
    <span className={cn("relative inline-flex h-2 w-2 flex-none", className)} aria-hidden="true">
      {live && (
        <span
          className={cn("absolute inset-0 rounded-full", dotTones[tone])}
          style={{ animation: "wl-ping 1.8s cubic-bezier(0, 0, 0.2, 1) infinite" }}
        />
      )}
      <span className={cn("relative inline-flex h-2 w-2 rounded-full", dotTones[tone])} />
    </span>
  );
}

export function StatusPill({
  tone,
  children,
  live,
  busy,
  className,
}: {
  tone: Tone;
  children: ReactNode;
  /** Pulsing dot: the state is current and being watched. */
  live?: boolean;
  /** Spinner in place of the dot: something is in progress. */
  busy?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex h-[22px] items-center gap-1.5 whitespace-nowrap rounded-full px-2 text-xs font-semibold",
        tones[tone],
        className,
      )}
    >
      {busy ? (
        <Spinner size={10} />
      ) : (
        <span
          className="h-1.5 w-1.5 flex-none rounded-full bg-current"
          style={live ? { animation: "wl-pulse 1.8s var(--ease) infinite" } : undefined}
        />
      )}
      {children}
    </span>
  );
}

/** Neutral label for a kind or role ("App", "admin"). */
export function Tag({ children, tone = "idle", className }: { children: ReactNode; tone?: Tone; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center whitespace-nowrap rounded-[6px] px-1.5 text-2xs font-semibold uppercase tracking-[0.04em]",
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd
      className={cn(
        "inline-flex h-5 min-w-5 items-center justify-center rounded-[5px] border border-edge bg-surface px-1 font-sans text-2xs font-semibold text-fg3 shadow-[0_1px_0_var(--edge)]",
        className,
      )}
    >
      {children}
    </kbd>
  );
}
