// Inherits currentColor, so it reads correctly on a primary button, in a
// status pill or on plain text without a colour prop.
export function Spinner({ size = 16, className }: { size?: number; className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={className}
      style={{
        width: size,
        height: size,
        flex: "none",
        display: "inline-block",
        borderRadius: "50%",
        border: "2px solid color-mix(in oklab, currentColor 22%, transparent)",
        borderTopColor: "currentColor",
        animation: "wl-spin 0.7s linear infinite",
      }}
    />
  );
}
