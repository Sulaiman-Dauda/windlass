import { cn } from "./cn";

// cn() does not resolve Tailwind conflicts, and rounded-sm sorts after
// rounded-card, rounded-control and rounded-full in the built CSS, so the
// default radius applies only when the caller does not pass its own.
export function skeletonClass(className?: string): string {
  const radius = className && /(^|\s)rounded-/.test(className) ? "" : "rounded-sm";
  return cn("animate-pulse bg-sunken", radius, className);
}

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden="true" className={skeletonClass(className)} />;
}
