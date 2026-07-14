// Windlass mark — a braced winch drum hauling two chain links. A windlass is
// the winch that raises a ship's anchor chain; the drum + brace also reads as a
// control dial, fitting a server cockpit. Deliberately specific to the name.
export function WindlassMark({
  size = 24,
  className,
  strokeWidth = 1.9,
}: {
  size?: number;
  className?: string;
  strokeWidth?: number;
}) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      {/* drum rim */}
      <circle cx="12" cy="10" r="6.3" />
      {/* X-brace spokes */}
      <path d="M13.84 11.84 15.96 13.96M10.16 11.84 8.04 13.96M10.16 8.16 8.04 6.04M13.84 8.16 15.96 6.04" />
      {/* hub */}
      <circle cx="12" cy="10" r="1.55" fill="currentColor" stroke="none" />
      {/* anchor chain: two interlocking links */}
      <rect x="10.2" y="15.2" width="3.6" height="4.5" rx="1.8" />
      <rect x="10.7" y="18.7" width="2.6" height="3.2" rx="1.3" />
    </svg>
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
  const tile = (
    <span
      className={className}
      style={{
        width: size,
        height: size,
        borderRadius: Math.round(size * 0.3),
        display: "grid",
        placeItems: "center",
        flex: "none",
        color: "var(--onaccent)",
        background:
          "linear-gradient(152deg, var(--accent), color-mix(in oklab, var(--accent) 60%, #05303f))",
        boxShadow: "var(--shadow-sm), inset 0 1px 0 rgba(255,255,255,0.22)",
      }}
    >
      <WindlassMark size={Math.round(size * 0.62)} strokeWidth={1.85} />
    </span>
  );

  if (!withText) return tile;

  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 11 }}>
      {tile}
      <span
        style={{ fontWeight: 700, fontSize: size * 0.5, letterSpacing: "-0.02em" }}
        className="text-fg"
      >
        Windlass
      </span>
    </span>
  );
}
