import { cn } from "./cn";

export type MeterTone = "accent" | "ok" | "warn" | "err";

/**
 * The colour for a usage reading, where more is worse: CPU, memory, disk.
 * A reading where more is better (containers up) must not use this, or a
 * healthy 100% shows red.
 */
export function usageTone(pct: number): MeterTone {
  if (pct >= 95) return "err";
  if (pct >= 85) return "warn";
  return "accent";
}

const fill: Record<MeterTone, string> = {
  accent: "bg-accent",
  ok: "bg-ok",
  warn: "bg-warn",
  err: "bg-err",
};

/** A thin bar for one reading. The tone is the caller's choice; see usageTone. */
export function Meter({ pct, tone, label, className }: { pct: number; tone: MeterTone; label: string; className?: string }) {
  return (
    <div
      className={cn("h-[5px] overflow-hidden rounded-full bg-[color-mix(in_oklab,var(--fg)_8%,transparent)]", className)}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
    >
      <div
        className={cn("h-full rounded-full transition-[width] duration-700", fill[tone])}
        style={{ width: `${Math.min(100, Math.max(1.5, pct))}%`, transitionTimingFunction: "var(--ease)" }}
      />
    </div>
  );
}

export interface MeterPart {
  label: string;
  bytes: number;
  /** A CSS colour, normally a token. */
  colour: string;
}

/** One bar split into parts of a whole, such as what fills a disk. */
export function StackedMeter({ parts, total, label, className }: { parts: MeterPart[]; total: number; label: string; className?: string }) {
  return (
    <div
      className={cn("flex h-[5px] gap-[2px] overflow-hidden rounded-full bg-[color-mix(in_oklab,var(--fg)_8%,transparent)]", className)}
      role="img"
      aria-label={label}
      title={label}
    >
      {total > 0 &&
        parts
          .filter((p) => p.bytes > 0)
          .map((p) => (
            <div key={p.label} className="h-full flex-none" style={{ width: `${(p.bytes / total) * 100}%`, background: p.colour }} />
          ))}
    </div>
  );
}
