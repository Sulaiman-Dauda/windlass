import { useEffect, useMemo, useState } from "react";
import { useServices } from "../api/deployments";
import { Select, Input } from "../ui/Field";
import { IconButton } from "../ui/Button";
import { StatusPill } from "../ui/Badge";
import { Card } from "../ui/Card";
import { EmptyState } from "../ui/Page";
import { Icon } from "../ui/Icon";
import { Skeleton } from "../ui/Skeleton";
import { cn } from "../ui/cn";
import { TermPane } from "./TermPane";

interface LogLine {
  stream: string;
  text: string;
}

type Conn = "connecting" | "live" | "reconnecting" | "closed";

export default function LogsTab({ project }: { project: string }) {
  const services = useServices(project);
  const [service, setService] = useState<string | null>(null);
  const names = services.data?.services.map((s) => s.service) ?? [];
  const active = service ?? names[0] ?? null;

  if (services.isLoading) return <Skeleton className="h-[480px] w-full rounded-card" />;
  if (!active) {
    return (
      <EmptyState
        icon="logs"
        title="No services to read from"
        desc="Logs stream from the project's containers. Deploy it first."
      />
    );
  }
  return (
    <LogStream
      key={`${project}/${active}`}
      project={project}
      service={active}
      services={names}
      onService={setService}
    />
  );
}

function LogStream({
  project,
  service,
  services,
  onService,
}: {
  project: string;
  service: string;
  services: string[];
  onService: (s: string) => void;
}) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [conn, setConn] = useState<Conn>("connecting");
  const [filter, setFilter] = useState("");
  const [wrap, setWrap] = useState(true);
  const [follow, setFollow] = useState(true);

  useEffect(() => {
    setLines([]);
    const es = new EventSource(`/api/v1/projects/${project}/logs?service=${encodeURIComponent(service)}&tail=200`);
    es.onopen = () => setConn("live");
    es.onerror = () => setConn(es.readyState === EventSource.CLOSED ? "closed" : "reconnecting");
    const push = (ev: MessageEvent) => {
      try {
        const line = JSON.parse(ev.data) as LogLine;
        setLines((ls) => {
          const next = [...ls, line];
          return next.length > 2000 ? next.slice(-2000) : next;
        });
      } catch {
        // ignore frames that are not log lines
      }
    };
    es.addEventListener("log", push);
    es.addEventListener("error", push as EventListener);
    return () => es.close();
  }, [project, service]);

  const q = filter.trim().toLowerCase();
  const shown = useMemo(() => (q ? lines.filter((l) => l.text.toLowerCase().includes(q)) : lines), [lines, q]);

  const download = () => {
    const blob = new Blob([lines.map((l) => l.text).join("\n") + "\n"], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${project}-${service}.log`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const status: Record<Conn, { tone: "ok" | "warn" | "idle" | "err"; label: string }> = {
    connecting: { tone: "idle", label: "Connecting" },
    live: { tone: "ok", label: "Live" },
    reconnecting: { tone: "warn", label: "Reconnecting" },
    closed: { tone: "err", label: "Disconnected" },
  };

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-hairline px-4 py-2.5">
        <div className="w-44">
          <Select controlSize="sm" value={service} onChange={(e) => onService(e.target.value)} aria-label="Service">
            {services.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
        </div>
        <div className="relative w-full max-w-[260px] flex-1">
          <Icon name="search" size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-fg3" />
          <Input
            controlSize="sm"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter lines"
            aria-label="Filter lines"
            className="pl-8"
          />
        </div>
        <StatusPill tone={status[conn].tone} live={conn === "live"}>
          {status[conn].label}
        </StatusPill>
        <span className="text-xs tabular-nums text-fg3">
          {q ? `${shown.length} of ${lines.length} lines` : `${lines.length} lines`}
        </span>
        <div className="ml-auto flex items-center gap-0.5">
          <IconButton
            icon="arrowDown"
            label={follow ? "Stop following new lines" : "Follow new lines"}
            aria-pressed={follow}
            className={cn(follow && "bg-sunken text-fg")}
            onClick={() => setFollow((f) => !f)}
          />
          <IconButton
            icon="wrap"
            label={wrap ? "Don't wrap long lines" : "Wrap long lines"}
            aria-pressed={wrap}
            className={cn(wrap && "bg-sunken text-fg")}
            onClick={() => setWrap((w) => !w)}
          />
          <IconButton icon="download" label="Download these lines" onClick={download} disabled={lines.length === 0} />
          <IconButton icon="x" label="Clear the view" onClick={() => setLines([])} disabled={lines.length === 0} />
        </div>
      </div>
      <TermPane tick={lines.length} follow={follow} wrap={wrap} className="h-[max(360px,calc(100dvh-360px))]">
        {shown.map((l, i) => (
          <div key={i} className={l.stream === "stderr" ? "text-term-warn" : undefined}>
            {l.text || " "}
          </div>
        ))}
        {lines.length === 0 && <span className="text-term-dim">Waiting for logs…</span>}
        {lines.length > 0 && shown.length === 0 && <span className="text-term-dim">No lines match “{filter}”.</span>}
      </TermPane>
    </Card>
  );
}
