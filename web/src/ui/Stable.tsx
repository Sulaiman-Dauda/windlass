import type { ReactNode } from "react";

// Labels that change must not move what sits next to them. Both helpers
// stack invisible copies in one grid cell, so the cell always takes the
// width of the widest copy. The copies are hidden from assistive tech and
// from innerText; only the visible label is read.

/**
 * A label that always takes the width of its bold form, so a tab or nav item
 * does not widen when it becomes active and push its neighbours along.
 */
export function StableLabel({ children }: { children: ReactNode }) {
  return (
    <span className="inline-grid text-center">
      <span aria-hidden="true" className="invisible col-start-1 row-start-1 font-semibold">
        {children}
      </span>
      <span className="col-start-1 row-start-1">{children}</span>
    </span>
  );
}

/**
 * A label sized to the widest of the values it can take, for a status or a
 * button whose text changes while the user watches ("Connecting" to "Live").
 */
export function ReserveWidth({ options, children }: { options: readonly string[]; children: ReactNode }) {
  // Start-aligned: a centred label would start at a new x each time it changes,
  // and Chrome counts that as the text moving.
  return (
    <span className="inline-grid">
      {options.map((o) => (
        <span key={o} aria-hidden="true" className="invisible col-start-1 row-start-1">
          {o}
        </span>
      ))}
      <span className="col-start-1 row-start-1">{children}</span>
    </span>
  );
}
