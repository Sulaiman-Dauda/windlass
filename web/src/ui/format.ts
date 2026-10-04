// Formatting for times, durations and sizes. Dates use the browser's locale;
// relative times are compact English to match the rest of the interface.

/** Parses API timestamps, tolerating SQLite's space-separated UTC form. */
export function parseTime(value?: string | null): Date | null {
  if (!value) return null;
  const iso = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)?$/.test(value) ? value.replace(" ", "T") + "Z" : value;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

export function timeAgo(value?: string | null, now: number = Date.now()): string {
  const d = parseTime(value);
  if (!d) return "";
  const s = Math.max(0, Math.round((now - d.getTime()) / 1000));
  if (s < 45) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.round(s / 3600);
  if (h < 24) return `${h}h ago`;
  const days = Math.round(s / 86400);
  if (days < 30) return `${days}d ago`;
  return formatDate(value);
}

export function formatDate(value?: string | null): string {
  const d = parseTime(value);
  if (!d) return "";
  return d.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

export function formatDateTime(value?: string | null): string {
  const d = parseTime(value);
  if (!d) return "";
  return d.toLocaleString(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function formatClock(value?: string | null): string {
  const d = parseTime(value);
  if (!d) return "";
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${String(m % 60).padStart(2, "0")}m`;
}

/** Milliseconds between two timestamps, or until now when `to` is missing. */
export function elapsed(from?: string | null, to?: string | null, now: number = Date.now()): number | null {
  const a = parseTime(from);
  if (!a) return null;
  const b = parseTime(to);
  return (b ? b.getTime() : now) - a.getTime();
}

const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(v >= 100 ? 0 : 1)} ${UNITS[i]}`;
}

export function plural(n: number, one: string, many = one + "s"): string {
  return `${n} ${n === 1 ? one : many}`;
}

export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}
