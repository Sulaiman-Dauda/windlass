import { useProjectAction, useServices } from "../api/deployments";

const stateColors: Record<string, string> = {
  running: "text-emerald-400",
  exited: "text-zinc-500",
  restarting: "text-amber-400",
  dead: "text-red-400",
};

export default function OverviewTab({ project }: { project: string }) {
  const services = useServices(project);
  const action = useProjectAction(project);

  return (
    <div>
      <div className="flex items-center gap-2">
        {(["start", "stop", "restart"] as const).map((a) => (
          <button
            key={a}
            onClick={() => action.mutate(a)}
            disabled={action.isPending}
            className="rounded-md border border-zinc-700 px-3 py-1.5 text-sm capitalize hover:bg-zinc-900 disabled:opacity-50"
          >
            {a}
          </button>
        ))}
        {action.isError && (
          <span className="text-sm text-red-400">
            {action.error instanceof Error ? action.error.message : "Action failed"}
          </span>
        )}
      </div>

      <div className="mt-6">
        {services.data?.note && (
          <p className="mb-3 text-sm text-amber-400/80">{services.data.note}</p>
        )}
        <div className="overflow-x-auto rounded-lg border border-zinc-900">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-zinc-900 text-left text-xs uppercase tracking-wide text-zinc-500">
                <th className="px-4 py-2">Service</th>
                <th className="px-4 py-2">Container</th>
                <th className="px-4 py-2">State</th>
                <th className="px-4 py-2">Health</th>
                <th className="px-4 py-2">Image</th>
              </tr>
            </thead>
            <tbody>
              {services.data?.services.map((s) => (
                <tr key={s.name} className="border-b border-zinc-900/60 last:border-0">
                  <td className="px-4 py-2 font-medium">{s.service}</td>
                  <td className="px-4 py-2 text-zinc-400">{s.name}</td>
                  <td className={`px-4 py-2 ${stateColors[s.state] ?? "text-zinc-300"}`}>
                    {s.state}
                  </td>
                  <td className="px-4 py-2 text-zinc-400">{s.health || "—"}</td>
                  <td className="px-4 py-2 text-zinc-500">{s.image}</td>
                </tr>
              ))}
              {services.data?.services.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-zinc-600">
                    No services running. Deploy the project to start them.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
