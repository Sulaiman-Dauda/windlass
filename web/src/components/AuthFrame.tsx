import type { ReactNode } from "react";
import { Wordmark } from "../ui/Logo";

/** Shared frame for the signed-out screens: wordmark, card, reassurance line. */
export default function AuthFrame({ children, footer }: { children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="relative grid min-h-dvh place-items-center overflow-hidden bg-canvas px-4 py-12">
      <div aria-hidden="true" className="pointer-events-none absolute inset-0">
        <div className="absolute inset-0 [background-image:linear-gradient(var(--hairline)_1px,transparent_1px),linear-gradient(90deg,var(--hairline)_1px,transparent_1px)] [background-size:44px_44px] [mask-image:radial-gradient(ellipse_70%_55%_at_50%_0%,black,transparent)]" />
        <div className="absolute left-1/2 top-[-260px] h-[520px] w-[760px] -translate-x-1/2 rounded-full bg-accent opacity-[0.09] blur-[110px]" />
      </div>

      <div className="relative w-full max-w-[400px]">
        <div className="mb-7 flex justify-center text-accent">
          <Wordmark height={30} />
        </div>
        <div className="rounded-xl2 border border-hairline bg-surface p-7 shadow-[var(--shadow-md)]">{children}</div>
        {footer && <div className="mt-6 text-center text-xs text-fg3">{footer}</div>}
      </div>
    </div>
  );
}
