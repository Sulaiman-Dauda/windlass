/**
 * Windlass's split-drum W.
 *
 * The horizontal void is the windlass axle; the centre boss is the hub that
 * joins the two cable runs. It is intentionally a filled silhouette so the
 * same drawing remains legible in browser chrome at 16px.
 */
export function WindlassMark({
  size = 24,
  className,
}: {
  size?: number;
  className?: string;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="currentColor"
      className={className}
      aria-hidden="true"
    >
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M2.75 5h5.1l3.55 15.9 2.76-10.3h3.68l2.76 10.3L24.15 5h5.1L24.1 27h-5.2L16 17.3 13.1 27H7.9L2.75 5Zm6.08 8.3.61 2.75h13.12l.61-2.75H8.83Z"
      />
      <circle cx="16" cy="14.675" r="1.55" />
    </svg>
  );
}

/**
 * The mark is the capital W, not an icon placed beside the product name.
 * Keeping “indlass” as live type makes the lockup crisp at every UI size.
 */
export function Wordmark({
  height = 26,
  className,
}: {
  height?: number;
  className?: string;
}) {
  return (
    <span
      className={className}
      role="img"
      aria-label="Windlass"
      style={{
        display: "inline-flex",
        height,
        alignItems: "center",
        flex: "none",
        whiteSpace: "nowrap",
      }}
    >
      <span
        aria-hidden="true"
        style={{ display: "inline-flex", marginRight: -height * 0.04 }}
      >
        <WindlassMark size={height} />
      </span>
      <span
        aria-hidden="true"
        style={{
          fontFamily: 'Manrope, ui-sans-serif, system-ui, sans-serif',
          fontSize: height * 0.88,
          fontWeight: 760,
          letterSpacing: "-0.055em",
          lineHeight: 1,
          transform: `translateY(${-height * 0.015}px)`,
        }}
      >
        indlass
      </span>
    </span>
  );
}

export function Logo({
  size = 30,
  withText = false,
  className,
}: {
  size?: number;
  withText?: boolean;
  className?: string;
}) {
  if (withText) {
    return <Wordmark height={Math.round(size * 0.72)} className={className} />;
  }
  return <WindlassMark size={size} className={className} />;
}
