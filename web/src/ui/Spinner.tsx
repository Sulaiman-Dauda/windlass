export function Spinner({ size = 16 }: { size?: number }) {
  return (
    <span
      aria-hidden="true"
      style={{
        width: size,
        height: size,
        display: "inline-block",
        borderRadius: "50%",
        border: `2px solid var(--color-edge)`,
        borderTopColor: "var(--color-accent)",
        animation: "wl-spin 0.7s linear infinite",
      }}
    />
  );
}
