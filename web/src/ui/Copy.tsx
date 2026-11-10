import { useEffect, useState } from "react";
import { IconButton, type Size } from "./Button";
import { cn } from "./cn";

/**
 * Copies text. navigator.clipboard only exists in secure contexts, and a
 * fresh panel is often reached over plain http://ip:8080, so fall back to
 * the selection-based copy there.
 */
async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to the legacy path
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  area.remove();
  return ok;
}

export function CopyButton({
  value,
  label = "Copy",
  size = "xs",
  className,
}: {
  value: string;
  label?: string;
  size?: Size;
  className?: string;
}) {
  const [done, setDone] = useState(false);
  useEffect(() => {
    if (!done) return;
    const t = setTimeout(() => setDone(false), 1500);
    return () => clearTimeout(t);
  }, [done]);
  return (
    <IconButton
      icon={done ? "check" : "copy"}
      label={done ? "Copied" : label}
      size={size}
      className={cn(done && "text-ok hover:text-ok", className)}
      onClick={async () => setDone(await copyText(value))}
    />
  );
}

/** Read-only multi-line snippet with a copy button, for config files and commands. */
export function CopyBlock({ value, label = "Copy", className }: { value: string; label?: string; className?: string }) {
  return (
    <div className={cn("relative min-w-0 rounded-control border border-hairline bg-sunken", className)}>
      <pre className="overflow-x-auto py-3 pl-3 pr-11 font-mono text-xs leading-5 text-fg2">
        <code>{value}</code>
      </pre>
      <div className="absolute right-1.5 top-1.5">
        <CopyButton value={value} label={label} />
      </div>
    </div>
  );
}

/** Read-only value with a copy button, for URLs and secrets shown once. */
export function CopyField({ value, className }: { value: string; className?: string }) {
  return (
    <div className={cn("flex h-9 min-w-0 items-center gap-1 rounded-control border border-hairline bg-sunken pl-3 pr-1", className)}>
      <code className="min-w-0 flex-1 truncate font-mono text-xs text-fg2" title={value}>
        {value}
      </code>
      <CopyButton value={value} />
    </div>
  );
}
