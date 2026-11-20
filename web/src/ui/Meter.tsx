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

/**
 * The sounding scale: a 1px ruler with a tick every tenth, a 3px fill and a
 * plumb mark at the reading, so every figure is read against its scale. A
 * notch marks a line the reading is judged by, such as an idle budget.
 */
export function Meter({
  pct,
  tone,
  label,
  valueText,
  notch,
  className,
}: {
  pct: number;
  tone: MeterTone;
  label: string;
  /** What a screen reader hears in place of the percentage. */
  valueText?: string;
  /** Where on the scale, 0 to 100, the notch sits. */
  notch?: number;
  className?: string;
}) {
  const at = Math.min(100, Math.max(0, pct));
  return (
    <div
      className={cn("relative h-[11px]", className)}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(at)}
      aria-valuetext={valueText}
    >
      <Ruler />
      <div
        className={cn("absolute bottom-0 left-0 h-[3px] transition-[width] duration-700", fill[tone])}
        style={{ width: `${Math.max(1, at)}%`, transitionTimingFunction: "var(--ease)" }}
      />
      {notch !== undefined && (
        <span
          aria-hidden="true"
          className="absolute bottom-0 top-0 w-0 border-l border-dashed border-warn"
          style={{ left: `${Math.min(100, Math.max(0, notch))}%` }}
        />
      )}
      <span
        aria-hidden="true"
        className="absolute bottom-0 top-0 w-px -translate-x-1/2 bg-fg transition-[left] duration-700"
        style={{ left: `${Math.max(0.5, at)}%`, transitionTimingFunction: "var(--ease)" }}
      />
    </div>
  );
}

/** The ruler under a meter: a baseline and a tick at every tenth, both ends included. */
function Ruler() {
  return (
    <span
      aria-hidden="true"
      className="absolute inset-x-0 bottom-0 h-[5px] border-x border-b border-edge bg-[repeating-linear-gradient(to_right,var(--edge)_0_1px,transparent_1px_10%)]"
    />
  );
}

export interface MeterPart {
  label: string;
  bytes: number;
  /** A CSS colour, normally a token. */
  colour: string;
}

/** One bar split into parts of a whole, such as what fills a disk, on the same ruler. */
export function StackedMeter({ parts, total, label, className }: { parts: MeterPart[]; total: number; label: string; className?: string }) {
  return (
    <div className={cn("relative h-[11px]", className)} role="img" aria-label={label} title={label}>
      <Ruler />
      <div className="absolute inset-x-0 bottom-0 flex h-[3px] gap-px overflow-hidden">
        {total > 0 &&
          parts
            .filter((p) => p.bytes > 0)
            .map((p) => (
              <div key={p.label} className="h-full flex-none" style={{ width: `${(p.bytes / total) * 100}%`, background: p.colour }} />
            ))}
      </div>
    </div>
  );
}
