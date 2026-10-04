import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { useServices } from "../api/deployments";
import { Select } from "../ui/Field";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { StatusPill, type Tone } from "../ui/Badge";
import { EmptyState } from "../ui/Page";
import { Skeleton } from "../ui/Skeleton";

export default function TerminalTab({ project }: { project: string }) {
  const services = useServices(project);
  const [service, setService] = useState<string | null>(null);
  const [session, setSession] = useState(0);
  const [status, setStatus] = useState("connecting");

  const running = services.data?.services.filter((s) => s.state === "running") ?? [];
  const active = service ?? running[0]?.service ?? null;

  if (services.isLoading) return <Skeleton className="h-[480px] w-full rounded-card" />;
  if (!active) {
    return (
      <EmptyState
        icon="terminal"
        title="No running containers"
        desc="A shell opens inside a running service. Deploy or start the project first."
      />
    );
  }

  const tone: Record<string, Tone> = { connecting: "idle", connected: "ok", disconnected: "idle", failed: "err" };

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-hairline px-4 py-2.5">
        <div className="w-44">
          <Select
            controlSize="sm"
            aria-label="Service"
            value={active}
            onChange={(e) => {
              setService(e.target.value);
              setStatus("connecting");
            }}
          >
            {running.map((s) => (
              <option key={s.service} value={s.service}>
                {s.service}
              </option>
            ))}
          </Select>
        </div>
        <StatusPill tone={tone[status] ?? "idle"} live={status === "connected"}>
          {status === "failed" ? "Connection failed" : status[0].toUpperCase() + status.slice(1)}
        </StatusPill>
        <span className="hidden text-xs text-fg3 sm:inline">Runs the container's shell as its default user.</span>
        <Button
          size="sm"
          icon="refresh"
          className="ml-auto"
          onClick={() => {
            setStatus("connecting");
            setSession((s) => s + 1);
          }}
        >
          Reconnect
        </Button>
      </div>
      <TerminalPane key={`${project}/${active}/${session}`} project={project} service={active} onStatus={setStatus} />
    </Card>
  );
}

function cssVar(name: string, fallback: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

function TerminalPane({
  project,
  service,
  onStatus,
}: {
  project: string;
  service: string;
  onStatus: (s: string) => void;
}) {
  const holder = useRef<HTMLDivElement>(null);
  const report = useRef(onStatus);
  report.current = onStatus;

  useEffect(() => {
    const el = holder.current;
    if (!el) return;

    // The pane is dark in both themes, so it reads the term-* tokens rather
    // than the page's foreground (which is near-black in light mode).
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      lineHeight: 1.25,
      fontFamily: '"Windlass Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      theme: {
        background: cssVar("--term", "#0c0e12"),
        foreground: cssVar("--term-fg", "#d5d9e0"),
        cursor: cssVar("--term-accent", "#5cbce4"),
        cursorAccent: cssVar("--term", "#0c0e12"),
        selectionBackground: "rgba(92, 188, 228, 0.3)",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();
    term.focus();

    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(
      `${proto}://${window.location.host}/api/v1/projects/${project}/terminal?service=${encodeURIComponent(service)}`,
    );
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      report.current("connected");
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    ws.onclose = () => report.current("disconnected");
    ws.onerror = () => report.current("failed");
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
    <div className="wl-term bg-term px-3 py-2">
      <div ref={holder} className="h-[max(380px,calc(100dvh-380px))]" />
    </div>
  );
}
