/**
 * The Windlass wordmark.
 *
 * The name is the logo. The letters are drawn as paths rather than set in a
 * typeface, so the mark belongs to the product and does not change when a font
 * fails to load or a system substitutes something else.
 *
 * A windlass is the winch that hauls a ship's anchor, so the word sits on a
 * taut cable. That rule is the whole ornament, and it is deliberately all that
 * is left: earlier attempts drew the drum with cable wound on it and, at the
 * size a favicon is actually seen, it resolved first into a snail and then into
 * a battery. A mark has to survive being a silhouette.
 *
 * So the small mark is the wordmark's own w with the same rule beneath it. The
 * two are one drawing at two sizes rather than an icon sitting next to some
 * text. `public/favicon.svg` carries the same geometry.
 *
 * Deliberately not a line icon inside a gradient rounded square, which is the
 * house style of every dashboard shipped in the last two years and says nothing
 * about what this is.
 */

/** One stroke weight throughout the letterforms. */
const STROKE = 4.6;

/**
 * The small mark: the w and its cable.
 *
 * Matches public/favicon.svg. Change one and change the other.
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
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <path d="M4.5 7 L10 22.5 L16 12.5 L22 22.5 L27.5 7" strokeWidth={4.2} />
      <path d="M4.5 27.5 H27.5" strokeWidth={2.8} />
    </svg>
  );
}

/**
 * The full wordmark.
 *
 * Monoline geometric letterforms on a 44-unit body: x-height 14 to 32,
 * ascenders to 6, one stroke weight throughout, and the cable at 39.5.
 */
export function Wordmark({
  height = 26,
  className,
}: {
  height?: number;
  className?: string;
}) {
  return (
    <svg
      width={(height / 44) * 152}
      height={height}
      viewBox="0 0 152 44"
      fill="none"
      stroke="currentColor"
      strokeWidth={STROKE}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      role="img"
      aria-label="Windlass"
    >
      {/* w */}
      <path d="M8 14 L13 32 L18 20 L23 32 L28 14" />
      {/* i */}
      <path d="M35 16 V32" />
      <circle cx="35" cy="9.2" r="1.9" fill="currentColor" stroke="none" />
      {/* n */}
      <path d="M42 32 V16.5 M42 22.5 A7.2 7.2 0 0 1 56.4 22.5 V32" />
      {/* d */}
      <circle cx="71" cy="24" r="7.4" />
      <path d="M78.4 6 V32" />
      {/* l */}
      <path d="M85.5 6 V32" />
      {/* a */}
      <circle cx="99" cy="24" r="7.4" />
      <path d="M106.4 17 V32" />
      {/* s */}
      <path d="M124 18.6 C124 14.4 113 14.4 113 19.2 C113 23.4 124 23.4 124 27.6 C124 32.4 113 32.4 113 28.2" />
      {/* s */}
      <path d="M142 18.6 C142 14.4 131 14.4 131 19.2 C131 23.4 142 23.4 142 27.6 C142 32.4 131 32.4 131 28.2" />

      {/* The cable the word rides on, flush with the letters at both ends. */}
      <path d="M8 39.5 H142" strokeWidth={STROKE * 0.62} />
    </svg>
  );
}

/**
 * Logo for the app chrome.
 *
 * `withText` keeps the existing call sites working. The wordmark already
 * contains the name, so the flag chooses between the word and the small mark
 * rather than bolting a text node onto an icon.
 */
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
