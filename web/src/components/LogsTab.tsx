import { useEffect, useRef, useState } from "react";
import { useServices } from "../api/deployments";
import { Select } from "../ui/Field";

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
      <label className="mb-3 flex items-center gap-2.5">
        <span className="text-xs font-semibold text-fg2">Service</span>
        <Select
          value={active ?? ""}
          onChange={(e) => setService(e.target.value)}
          className="w-auto min-w-[10rem]"
        >
          {names.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </Select>
      </label>
      {active ? (
        <LogStream key={`${project}/${active}`} project={project} service={active} />
      ) : (
        <p className="text-sm text-fg3">No services yet.</p>
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
      className="max-h-[26rem] overflow-auto rounded-[13px] border border-hairline bg-term p-4 font-mono text-xs leading-relaxed"
    >
      {lines.map((l, i) => (
        <div key={i} className={l.stream === "stderr" ? "text-warn" : "text-fg2"}>
          {l.text}
        </div>
      ))}
      {lines.length === 0 && <span className="text-fg3">Waiting for logs…</span>}
    </div>
  );
}
