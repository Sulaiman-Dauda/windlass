import { useEffect, useRef, useState } from "react";
import { useServices } from "../api/deployments";

interface LogLine {
  stream: string;
  text: string;
}

export default function LogsTab({ project }: { project: string }) {
  const services = useServices(project);
  const [service, setService] = useState<string | null>(null);
  const names = services.data?.services.map((s) => s.service) ?? [];
  const active = service ?? names[0] ?? null;

  return (
    <div>
      <div className="mb-3 flex items-center gap-2">
        <span className="text-xs text-zinc-500">Service</span>
        <select
          value={active ?? ""}
          onChange={(e) => setService(e.target.value)}
          className="rounded-md border border-zinc-800 bg-zinc-900 px-2 py-1 text-sm text-zinc-100 outline-none"
        >
          {names.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </div>
      {active ? (
        <LogStream key={`${project}/${active}`} project={project} service={active} />
      ) : (
        <p className="text-sm text-zinc-600">No services yet.</p>
      )}
    </div>
  );
}

function LogStream({ project, service }: { project: string; service: string }) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const pane = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setLines([]);
    const es = new EventSource(
      `/api/v1/projects/${project}/logs?service=${service}&tail=200`,
    );
    const push = (ev: MessageEvent) => {
      try {
        const line = JSON.parse(ev.data) as LogLine;
        setLines((ls) => {
          const next = [...ls, line];
          return next.length > 2000 ? next.slice(-2000) : next;
        });
      } catch {
        // ignore
      }
    };
    es.addEventListener("log", push);
    es.addEventListener("error", push);
    return () => es.close();
  }, [project, service]);

  useEffect(() => {
    const el = pane.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines.length]);

  return (
    <div
      ref={pane}
      className="h-96 overflow-auto rounded-md border border-zinc-900 bg-black p-3 font-mono text-xs leading-5"
    >
      {lines.map((l, i) => (
        <div key={i} className={l.stream === "stderr" ? "text-amber-300" : "text-zinc-300"}>
          {l.text}
        </div>
      ))}
      {lines.length === 0 && <span className="text-zinc-600">Waiting for logs…</span>}
    </div>
  );
}
