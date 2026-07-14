import { useProjectAction, useServices } from "../api/deployments";
import { Link } from "react-router-dom";
import { Button } from "../ui/Button";
import { StatusPill, type Tone } from "../ui/Badge";
import { Icon } from "../ui/Icon";

const actionIcons = { start: "play", stop: "stop", restart: "restart" } as const;

function stateTone(state: string): { tone: Tone; live?: boolean } {
  switch (state) {
    case "running":
      return { tone: "ok", live: true };
    case "restarting":
      return { tone: "warn" };
    case "dead":
      return { tone: "err" };
    default:
      return { tone: "idle" };
  }
}

export default function OverviewTab({ project }: { project: string }) {
  const services = useServices(project);
  const action = useProjectAction(project);

  return (
    <div>
      <div className="flex items-center gap-2">
        {(["start", "stop", "restart"] as const).map((a) => (
          <Button
            key={a}
            size="sm"
            onClick={() => action.mutate(a)}
            disabled={action.isPending}
            className="capitalize"
          >
            <Icon name={actionIcons[a]} size={15} />
            {a}
          </Button>
        ))}
        {action.isError && (
          <span className="text-sm text-err">
            {action.error instanceof Error ? action.error.message : "Action failed"}
          </span>
        )}
      </div>

      <div className="mt-6">
        {services.data?.note && (
          <div className="mb-3 flex items-start gap-2.5 rounded-[10px] bg-warn-soft px-3.5 py-3 text-sm text-warn">
            <Icon name="warning" size={16} className="mt-0.5 flex-none" />
            <span>{services.data.note}</span>
          </div>
        )}
        <div className="overflow-x-auto rounded-[13px] border border-hairline bg-surface">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-hairline bg-surface2 text-left text-2xs font-semibold uppercase tracking-[0.05em] text-fg3">
                <th className="px-4 py-3">Service</th>
                <th className="px-4 py-3">Container</th>
                <th className="px-4 py-3">State</th>
                <th className="px-4 py-3">Health</th>
                <th className="px-4 py-3">Image</th>
                <th className="px-4 py-3">Limits</th>
              </tr>
            </thead>
            <tbody>
              {services.data?.services.map((s) => {
                const st = stateTone(s.state);
                return (
                  <tr
                    key={s.name}
                    className="border-b border-hairline last:[&>td]:border-0 hover:bg-surface2"
                  >
                    <td className="px-4 py-3 border-b border-hairline font-medium text-fg">
                      {s.service}
                    </td>
                    <td className="px-4 py-3 border-b border-hairline font-mono text-fg2">
                      {s.name}
                    </td>
                    <td className="px-4 py-3 border-b border-hairline">
                      <StatusPill tone={st.tone} live={st.live}>
                        {s.state}
                      </StatusPill>
                    </td>
                    <td className="px-4 py-3 border-b border-hairline text-fg2">
                      {s.health || "—"}
                    </td>
                    <td className="px-4 py-3 border-b border-hairline font-mono text-fg3">
                      {s.image}
                    </td>
                    <td className="px-4 py-3 border-b border-hairline text-fg3">
                      {s.memory_limit
                        ? `${Math.round(s.memory_limit / 1024 / 1024)} MiB`
                        : "no memory limit"}
                      {s.cpu_limit ? ` · ${s.cpu_limit} CPU` : ""}
                    </td>
                  </tr>
                );
              })}
              {services.data?.services.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-sm text-fg3">
                    No services running. Deploy the project to start them.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <p className="mt-3 text-xs leading-relaxed text-fg3">
          Resource limits come directly from <code className="font-mono text-fg2">mem_limit</code>{" "}
          and <code className="font-mono text-fg2">cpus</code> in{" "}
          <Link className="text-accent hover:underline" to="files">
            compose.yaml
          </Link>
          . Application readiness can be enforced with the Compose labels{" "}
          <code className="font-mono text-fg2">windlass.health.url</code>,{" "}
          <code className="font-mono text-fg2">windlass.health.status</code>, and{" "}
          <code className="font-mono text-fg2">windlass.health.contains</code>.
        </p>
      </div>
    </div>
  );
}
