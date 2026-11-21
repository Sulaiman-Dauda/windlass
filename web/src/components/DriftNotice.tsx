import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useDeployments } from "../api/deployments";
import { useProjectDrift, type ServiceDrift } from "../api/projects";
import { Callout } from "../ui/Page";

function reason(r: ServiceDrift): string | null {
  if (r.orphan) return "is no longer in the files, and a deploy removes it";
  if (r.missing) return "is in the files and has no container";
  if (r.config_changed && r.image_changed) return "runs an older configuration and an older image";
  if (r.config_changed) return "runs an older configuration than the files";
  if (r.image_changed) return "runs an older image than its tag now names";
  return null;
}

/**
 * Says which services differ from the project's files, from
 * /projects/{name}/drift. Absent when nothing differs or the check fails:
 * the Compose note above the services table already covers a failure.
 */
export default function DriftNotice({ project }: { project: string }) {
  const drift = useProjectDrift(project).data;
  // A deploy that finishes is the usual fix, so check again at once.
  const qc = useQueryClient();
  const latest = useDeployments(project).data?.[0];
  const stamp = latest ? `${latest.id}-${latest.status}` : "";
  useEffect(() => {
    if (stamp) void qc.invalidateQueries({ queryKey: ["projects", project, "drift"] });
  }, [qc, project, stamp]);
  if (!drift?.drifted) return null;
  const rows = drift.services.flatMap((r) => {
    const why = reason(r);
    return why ? [{ key: `${r.service}-${r.container ?? ""}`, service: r.service, why }] : [];
  });
  return (
    <Callout tone="info" title="The running containers differ from the project files">
      <ul className="mt-1 space-y-0.5">
        {rows.map((r) => (
          <li key={r.key}>
            <span className="font-mono text-xs text-fg">{r.service}</span> {r.why}
          </li>
        ))}
      </ul>
      <p className="mt-1.5">Deploy to bring them in line.</p>
    </Callout>
  );
}
