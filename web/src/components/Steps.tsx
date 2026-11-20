import { PIPELINE, isActive, type Deployment, type StageState } from "../api/deployments";
import { cn } from "../ui/cn";

/**
 * "settled" is a step of a finished deployment whose events are not loaded
 * here: the strip says the deployment got past it, never whether it ran or
 * was skipped. The log has that.
 */
export type StepState = StageState | "settled";

const rail: Record<StepState, string> = {
  done: "bg-fg2",
  skipped: "bg-[repeating-linear-gradient(to_right,var(--edge-strong)_0_3px,transparent_3px_6px)]",
  active: "wl-sweep",
  failed: "bg-err",
  pending: "bg-edge",
  settled: "bg-[color-mix(in_oklab,var(--ok)_70%,var(--surface))]",
};

const name: Record<StepState, string> = {
  done: "text-fg",
  skipped: "text-fg3 line-through decoration-fg3/60",
  active: "font-semibold text-accent",
  failed: "font-semibold text-err",
  pending: "text-fg3",
  settled: "text-fg2",
};

const spoken: Record<StepState, string> = {
  done: "done",
  skipped: "skipped",
  active: "running",
  failed: "failed",
  pending: "not started",
  settled: "passed",
};

/** The deploy pipeline as one strip: a 4px rail per step, the step's name under it. */
export function Steps({ states, numbered = true, className }: { states: Record<string, StepState>; numbered?: boolean; className?: string }) {
  return (
    <ol className={cn("relative grid grid-cols-6 gap-1", className)}>
      {PIPELINE.map((s, i) => {
        const st = states[s.key] ?? "pending";
        return (
          <li key={s.key} className="min-w-0" title={`${s.label}: ${spoken[st]}`}>
            <span aria-hidden="true" className={cn("block h-1 rounded-full", rail[st])} />
            <span className={cn("mt-1.5 flex items-baseline gap-1.5 truncate text-xs", name[st])}>
              {numbered && <span className="hidden tabular-nums text-fg3 sm:inline-block">{i + 1}</span>}
              {s.label}
              <span className="sr-only">, {spoken[st]}</span>
            </span>
          </li>
        );
      })}
    </ol>
  );
}

/**
 * The strip for a deployment whose events are not loaded, from its record
 * alone: where a running one is, where a failed one stopped, and nothing
 * claimed about the steps it passed.
 */
export function summaryStates(d: Deployment): Record<string, StepState> {
  const keys = PIPELINE.map((p) => p.key as string);
  const out: Record<string, StepState> = {};
  if (d.status === "succeeded") {
    for (const k of keys) out[k] = "settled";
    return out;
  }
  const at = isActive(d.status) ? keys.indexOf(d.status) : d.status === "failed" && d.failed_step ? keys.indexOf(d.failed_step) : -1;
  keys.forEach((k, i) => {
    if (at < 0) out[k] = "pending";
    else if (i < at) out[k] = "settled";
    else if (i === at) out[k] = isActive(d.status) ? "active" : "failed";
    else out[k] = "pending";
  });
  return out;
}
