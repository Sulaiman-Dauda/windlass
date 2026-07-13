import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { useServices } from "../api/deployments";

export default function TerminalTab({ project }: { project: string }) {
  const services = useServices(project);
  const [service, setService] = useState<string | null>(null);

  const running = services.data?.services.filter((s) => s.state === "running") ?? [];
  const active = service ?? running[0]?.service ?? null;

  return (
    <div>
      <div className="mb-3 flex items-center gap-2">
        <span className="text-xs text-zinc-500">Service</span>
        <select
          value={active ?? ""}
          onChange={(e) => setService(e.target.value)}
          className="rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 outline-none"
        >
          {running.map((s) => (
            <option key={s.service} value={s.service}>
              {s.service}
            </option>
          ))}
        </select>
      </div>
      {active ? (
        <TerminalPane key={`${project}/${active}`} project={project} service={active} />
      ) : (
        <p className="text-sm text-zinc-600">
          No running containers. Deploy the project first.
        </p>
      )}
    </div>
  );
}

function TerminalPane({ project, service }: { project: string; service: string }) {
  const holder = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState("connecting…");

  useEffect(() => {
    const el = holder.current;
    if (!el) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      theme: { background: "#000000" },
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
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "input", data }));
      }
    });
    const onResize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", cols, rows }));
      }
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
      <div ref={holder} className="h-96 rounded-md border border-zinc-900 bg-black p-1" />
      <p className="mt-2 text-xs text-zinc-600">{status}</p>
    </div>
  );
}
