import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { useServices } from "../api/deployments";
import { Select } from "../ui/Field";
import { StatusPill, type Tone } from "../ui/Badge";
import { EmptyState } from "../ui/Page";
import { Icon } from "../ui/Icon";

export default function TerminalTab({ project }: { project: string }) {
  const services = useServices(project);
  const [service, setService] = useState<string | null>(null);

  const running = services.data?.services.filter((s) => s.state === "running") ?? [];
  const active = service ?? running[0]?.service ?? null;

  return (
    <div>
      {active && (
        <div className="mb-3 flex items-center gap-2.5">
          <span className="text-xs font-semibold text-fg2">Service</span>
          <Select value={active ?? ""} onChange={(e) => setService(e.target.value)} className="w-auto font-mono">
            {running.map((s) => (
              <option key={s.service} value={s.service}>
                {s.service}
              </option>
            ))}
          </Select>
        </div>
      )}
      {active ? (
        <TerminalPane key={`${project}/${active}`} project={project} service={active} />
      ) : (
        <EmptyState
          icon={<Icon name="terminal" size={26} />}
          title="No running containers"
          desc="Deploy the project first to open a shell."
        />
      )}
    </div>
  );
}

function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

const statusTone: Record<string, Tone> = {
  "connecting…": "idle",
  connected: "ok",
  disconnected: "idle",
  "connection failed": "err",
};

function TerminalPane({ project, service }: { project: string; service: string }) {
  const holder = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState("connecting…");

  useEffect(() => {
    const el = holder.current;
    if (!el) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: '"Windlass Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      theme: {
        background: cssVar("--term") || "#08090b",
        foreground: cssVar("--fg") || "#f3f4f6",
        cursor: cssVar("--accent") || "#3aa9d6",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(
      `${proto}://${window.location.host}/api/v1/projects/${project}/terminal?service=${service}`,
    );
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      setStatus("connected");
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    ws.onclose = () => setStatus("disconnected");
    ws.onerror = () => setStatus("connection failed");
    ws.onmessage = (ev) => {
      term.write(new Uint8Array(ev.data as ArrayBuffer));
    };

    const onData = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "input", data }));
    });
    const onResize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", cols, rows }));
    });
    const resizeObserver = new ResizeObserver(() => fit.fit());
    resizeObserver.observe(el);

    return () => {
      resizeObserver.disconnect();
      onData.dispose();
      onResize.dispose();
      ws.close();
      term.dispose();
    };
  }, [project, service]);

  return (
    <div>
      <div ref={holder} className="h-96 overflow-hidden rounded-[13px] border border-hairline bg-term p-2" />
      <div className="mt-2.5">
        <StatusPill tone={statusTone[status] ?? "idle"} live={status === "connecting…"}>
          {status}
        </StatusPill>
      </div>
    </div>
  );
}
