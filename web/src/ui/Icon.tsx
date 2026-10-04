import type { ReactNode } from "react";

// Line-icon set (24x24, 1.7 stroke, currentColor). Kept intentionally small:
// only what the app actually uses.
const P = {
  overview: <><rect x="3.5" y="3.5" width="7" height="9" rx="1.6" /><rect x="13.5" y="3.5" width="7" height="5" rx="1.6" /><rect x="13.5" y="11.5" width="7" height="9" rx="1.6" /><rect x="3.5" y="15.5" width="7" height="5" rx="1.6" /></>,
  projects: <><path d="M12 2.5l8.5 4.9v9.2L12 21.5 3.5 16.6V7.4z" /><path d="M3.7 7.4 12 12l8.3-4.6M12 12v9.5" /></>,
  templates: <><path d="M12 3 21 8l-9 5-9-5z" /><path d="m3 12.5 9 5 9-5" /><path d="m3 16.5 9 5 9-5" /></>,
  settings: <><path d="M4 6h10M18 6h2M4 12h2M10 12h10M4 18h6M14 18h6" /><circle cx="16" cy="6" r="2" /><circle cx="8" cy="12" r="2" /><circle cx="12" cy="18" r="2" /></>,
  signout: <><path d="M9 21H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3" /><path d="m16 17 5-5-5-5M21 12H9" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  deploy: <><path d="M12 15V4M7.5 8.5 12 4l4.5 4.5" /><path d="M5 20h14" /></>,
  play: <path d="M7.5 5.2v13.6a.8.8 0 0 0 1.2.7l10.8-6.8a.8.8 0 0 0 0-1.4L8.7 4.5a.8.8 0 0 0-1.2.7z" />,
  stop: <rect x="6" y="6" width="12" height="12" rx="2" />,
  restart: <><path d="M3 12a9 9 0 1 0 2.6-6.3" /><path d="M3 4.5V10h5.5" /></>,
  rollback: <><path d="M9 14 4 9l5-5" /><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11" /></>,
  trash: <path d="M4 7h16M9 7V5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v2M6 7l1 13a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1l1-13" />,
  chevronRight: <path d="m9 6 6 6-6 6" />,
  chevronLeft: <path d="m15 6-6 6 6 6" />,
  chevronDown: <path d="m6 9 6 6 6-6" />,
  chevronsUpDown: <path d="m7 9 5-5 5 5M7 15l5 5 5-5" />,
  arrowLeft: <path d="M19 12H5M11 6l-6 6 6 6" />,
  arrowDown: <path d="M12 5v14M6 13l6 6 6-6" />,
  external: <path d="M14 4h6v6M20 4l-9 9M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" />,
  refresh: <><path d="M21 12a9 9 0 1 1-2.6-6.3" /><path d="M21 4.5V10h-5.5" /></>,
  warning: <><path d="M10.3 3.7 1.8 18a2 2 0 0 0 1.7 3h16.9a2 2 0 0 0 1.7-3L13.7 3.7a2 2 0 0 0-3.4 0z" /><path d="M12 9v4M12 17h.01" /></>,
  info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" /></>,
  check: <path d="M5 12.5l4.5 4.5L19 7.5" />,
  checkCircle: <><circle cx="12" cy="12" r="9" /><path d="m8 12.3 2.8 2.7L16 9.5" /></>,
  xCircle: <><circle cx="12" cy="12" r="9" /><path d="m9 9 6 6M15 9l-6 6" /></>,
  x: <path d="M6 6l12 12M18 6 6 18" />,
  minus: <path d="M5 12h14" />,
  terminal: <><rect x="3" y="4" width="18" height="16" rx="2.5" /><path d="m7 9 3 3-3 3M13 15h4" /></>,
  logs: <path d="M4 6h16M4 10h16M4 14h10M4 18h7" />,
  database: <><ellipse cx="12" cy="5.5" rx="7" ry="3" /><path d="M5 5.5v13c0 1.7 3.1 3 7 3s7-1.3 7-3v-13M5 12c0 1.7 3.1 3 7 3s7-1.3 7-3" /></>,
  globe: <><circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c2.5 2.4 3.9 5.6 4 9-.1 3.4-1.5 6.6-4 9-2.5-2.4-3.9-5.6-4-9 .1-3.4 1.5-6.6 4-9z" /></>,
  clock: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3.5 2" /></>,
  gitBranch: <><circle cx="6" cy="6" r="2.5" /><circle cx="6" cy="18" r="2.5" /><circle cx="18" cy="8" r="2.5" /><path d="M6 8.5v7M18 10.5c0 4-4 3.5-6 5.5" /></>,
  gitCommit: <><circle cx="12" cy="12" r="3.5" /><path d="M3 12h5.5M15.5 12H21" /></>,
  download: <path d="M12 4v11M7 11l5 4 5-4M5 20h14" />,
  archive: <><rect x="3" y="4" width="18" height="5" rx="1.5" /><path d="M5 9v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9M10 13h4" /></>,
  key: <><circle cx="8" cy="14" r="4" /><path d="m10.8 11.2 8-8M17 5l2 2M14 8l2 2" /></>,
  lock: <><rect x="4.5" y="10.5" width="15" height="10.5" rx="2" /><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3" /></>,
  shield: <><path d="M12 3 4.5 6v5.5c0 4.6 3.2 8.3 7.5 9.5 4.3-1.2 7.5-4.9 7.5-9.5V6z" /><path d="m9 12 2.2 2.2L15.5 10" /></>,
  user: <><circle cx="12" cy="8" r="4" /><path d="M4 21a8 8 0 0 1 16 0" /></>,
  users: <><circle cx="9" cy="8" r="3.5" /><path d="M2.5 20a6.5 6.5 0 0 1 13 0" /><path d="M16 4.6a3.5 3.5 0 0 1 0 6.8M21.5 20a6.5 6.5 0 0 0-4-6" /></>,
  link: <><path d="M10 14a4.5 4.5 0 0 0 6.4 0l3.2-3.2a4.5 4.5 0 0 0-6.4-6.4L12 5.6" /><path d="M14 10a4.5 4.5 0 0 0-6.4 0l-3.2 3.2a4.5 4.5 0 0 0 6.4 6.4l1.2-1.2" /></>,
  package: <><path d="M3.5 7.5 12 3l8.5 4.5v9L12 21l-8.5-4.5z" /><path d="M3.5 7.5 12 12l8.5-4.5M12 12v9M7.8 5.2l8.5 4.6" /></>,
  server: <><rect x="3" y="4" width="18" height="7" rx="2" /><rect x="3" y="13" width="18" height="7" rx="2" /><path d="M7 7.5h.01M7 16.5h.01" /></>,
  cpu: <><rect x="6" y="6" width="12" height="12" rx="2" /><rect x="9.5" y="9.5" width="5" height="5" rx=".8" /><path d="M9 2.5V6M15 2.5V6M9 18v3.5M15 18v3.5M2.5 9H6M2.5 15H6M18 9h3.5M18 15h3.5" /></>,
  memory: <><rect x="2.5" y="7" width="19" height="9" rx="1.5" /><path d="M6.5 16v3M10.5 16v3M14.5 16v3M18.5 16v3M6.5 10.5h3M14.5 10.5h3" /></>,
  disk: <><path d="M3 13h18" /><path d="M5.5 5h13l2.5 8v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-5z" /><path d="M7 16.5h.01M10.5 16.5h.01" /></>,
  layers: <><path d="m12 3 9 5-9 5-9-5z" /><path d="m3 13 9 5 9-5" /></>,
  activity: <path d="M3 12h4l3-8 4 16 3-8h4" />,
  bolt: <path d="M13 2.5 4.5 13.5H12L11 21.5l8.5-11H12z" />,
  file: <><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" /><path d="M14 3v5h5" /></>,
  copy: <><rect x="8" y="8" width="12" height="12" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" /></>,
  search: <><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></>,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  more: <><circle cx="5.5" cy="12" r="1.4" fill="currentColor" stroke="none" /><circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none" /><circle cx="18.5" cy="12" r="1.4" fill="currentColor" stroke="none" /></>,
  wrap: <><path d="M3 6h18M3 12h15a3 3 0 0 1 0 6h-4" /><path d="m16 16-2 2 2 2M3 18h7" /></>,
  edit: <><path d="M4 20h4L19 9l-4-4L4 16z" /><path d="m13.5 6.5 4 4" /></>,
  command: <path d="M9 6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3z" />,
  sun: <><circle cx="12" cy="12" r="4.2" /><path d="M12 1.5v2.5M12 20v2.5M4.2 4.2 6 6M18 18l1.8 1.8M1.5 12H4M20 12h2.5M4.2 19.8 6 18M18 6l1.8-1.8" /></>,
  monitor: <><rect x="2.5" y="4" width="19" height="12.5" rx="2" /><path d="M8.5 20.5h7M12 16.5v4" /></>,
  moon: <path d="M20 13.5A8 8 0 1 1 10.5 4a6.2 6.2 0 0 0 9.5 9.5z" />,
  github: <path d="M12 2C6.48 2 2 6.58 2 12.25c0 4.53 2.87 8.37 6.85 9.73.5.1.68-.22.68-.49v-1.7c-2.79.62-3.38-1.22-3.38-1.22-.46-1.18-1.11-1.5-1.11-1.5-.91-.64.07-.62.07-.62 1 .07 1.53 1.06 1.53 1.06.9 1.57 2.36 1.12 2.94.85.09-.66.35-1.12.63-1.37-2.22-.26-4.56-1.14-4.56-5.07 0-1.12.39-2.03 1.03-2.75-.1-.26-.45-1.3.1-2.7 0 0 .84-.28 2.75 1.05a9.3 9.3 0 0 1 5 0c1.91-1.33 2.75-1.05 2.75-1.05.55 1.4.2 2.44.1 2.7.64.72 1.03 1.63 1.03 2.75 0 3.94-2.34 4.8-4.57 5.06.36.32.68.94.68 1.9v2.82c0 .27.18.6.69.49A10.02 10.02 0 0 0 22 12.25C22 6.58 17.52 2 12 2z" />,
} satisfies Record<string, ReactNode>;

// Glyphs drawn as solid shapes rather than strokes.
const FILLED = new Set<string>(["play", "github"]);

export type IconName = keyof typeof P;

export function Icon({
  name,
  size = 18,
  className,
}: {
  name: IconName;
  size?: number;
  className?: string;
}) {
  const filled = FILLED.has(name);
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill={filled ? "currentColor" : "none"}
      stroke={filled ? "none" : "currentColor"}
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className ? `flex-none ${className}` : "flex-none"}
      aria-hidden="true"
    >
      {P[name]}
    </svg>
  );
}
