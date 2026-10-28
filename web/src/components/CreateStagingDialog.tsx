import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { useProject } from "../api/projects";
import { useCreateStaging, useStagingPreflight, type CreateResult, type DomainMap, type StagingRequest } from "../api/staging";
import { Modal } from "../ui/Modal";
import { Button } from "../ui/Button";
import { Field, Input, Switch } from "../ui/Field";
import { Callout, FormError } from "../ui/Page";
import StagingCreatedDialog from "./StagingCreatedDialog";
import { Skeleton } from "../ui/Skeleton";
import { useToast } from "../ui/Toast";
import { formatBytes, plural } from "../ui/format";

/** Waits until the value has been still for ms, so typing does not send a preflight per key. */
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

const dataKind = { files: "Project files", volume: "Volume", database: "Database dump" } as const;

export default function CreateStagingDialog({ live, onClose }: { live: string; onClose: () => void }) {
  const project = useProject(live).data;
  const navigate = useNavigate();
  const toast = useToast();
  const create = useCreateStaging(live);

  const [branch, setBranch] = useState<string | null>(null);
  const [domains, setDomains] = useState<Record<string, string>>({});
  const [env, setEnv] = useState<Record<string, string>>({});
  const [copyData, setCopyData] = useState<boolean | null>(null);
  const [autoPromote, setAutoPromote] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false);
  const [created, setCreated] = useState<CreateResult | null>(null);

  const req = useMemo<StagingRequest>(() => {
    const d: DomainMap[] = Object.entries(domains).map(([l, s]) => ({ live: l, staging: s.trim() }));
    return { branch: branch?.trim() || undefined, domains: d.length ? d : undefined, copy_data: copyData ?? undefined };
  }, [branch, domains, copyData]);
  const pf = useStagingPreflight(live, useDebounced(req, 350), !created);
  const p = pf.data;

  // Copy data defaults on when live has something to copy.
  const copy = copyData ?? (p ? p.data.length > 0 : false);
  const dataBytes = p?.data.reduce((n, d) => n + d.bytes, 0) ?? 0;
  const free = p?.projects_free_bytes ?? null;
  const flagged = p?.flagged_env ?? [];
  const edited = Object.fromEntries(Object.entries(env).filter(([, v]) => v !== ""));
  const ready = !!p && p.can_create && (flagged.length === 0 || acknowledged) && !pf.isFetching;

  const submit = () =>
    create.mutate(
      { ...req, copy_data: copy, env: Object.keys(edited).length ? edited : undefined, auto_promote: autoPromote, acknowledged },
      {
        onSuccess: (res) => {
          if (res.result.webhook_secret && !res.result.webhook_registered) {
            setCreated(res);
            return;
          }
          toast.info(`Created ${res.project.name}`, `Deployment #${res.result.deployment} started.`);
          navigate(`/projects/${res.project.name}/deployments?d=${res.result.deployment}`);
        },
      },
    );

  if (created) return <StagingCreatedDialog created={created} repo={project?.git_repo} onClose={onClose} />;

  return (
    <Modal
      onClose={onClose}
      width={640}
      title={`Create a staging for ${live}`}
      description={
        <>
          {p?.staging ?? `${live}-staging`} is a copy of {live} with its own domains, environment and data. Pushes deploy there
          instead of to {live}, and {live} changes only when you promote a staging deployment.
        </>
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" onClick={submit} disabled={!ready} loading={create.isPending}>
            Create staging
          </Button>
        </>
      }
    >
      {!p ? (
        pf.isError ? (
          <FormError error={pf.error} fallback="Could not check the project" />
        ) : (
          <div className="flex flex-col gap-3">
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        )
      ) : (
        <div className="flex flex-col gap-5">
          {p.blockers.map((b, i) => (
            <Callout key={`b${i}`} tone="err" title={b.service ? `${b.service}: ${b.message}` : b.message}>
              {b.fix}
            </Callout>
          ))}

          {p.git && (
            <Field label="Branch" hint={`Pushes to this branch deploy to staging. ${live} stops deploying on push.`}>
              <Input
                value={branch ?? p.branch ?? ""}
                onChange={(e) => setBranch(e.target.value)}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
              />
            </Field>
          )}

          {p.domains.length > 0 && (
            <Section title="Domains" hint={`Staging gets its own certificates and tells search engines not to index it.`}>
              {p.domains.map((d) => (
                <div key={d.live} className="grid grid-cols-1 items-center gap-x-3 gap-y-1 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
                  <span className="truncate font-mono text-sm text-fg2" title={d.live}>
                    {d.live}
                  </span>
                  <Input
                    aria-label={`Staging domain for ${d.live}`}
                    value={domains[d.live] ?? d.staging}
                    onChange={(e) => setDomains((m) => ({ ...m, [d.live]: e.target.value }))}
                    spellCheck={false}
                    autoComplete="off"
                    className="font-mono"
                  />
                </div>
              ))}
              {p.rewrites.length > 0 && (
                <p className="text-xs text-fg3">
                  The copied Compose files and environment use the staging hostnames:{" "}
                  {p.rewrites.map((r, i) => (
                    <span key={r.from}>
                      {i > 0 && ", "}
                      <span className="font-mono">{r.from}</span> becomes <span className="font-mono">{r.to}</span>
                    </span>
                  ))}
                  .
                </p>
              )}
            </Section>
          )}

          {flagged.length > 0 && (
            <Section
              title="Values that may reach real people or services"
              hint="Staging copies these from live. Give staging its own test keys, mail settings or webhooks. Empty keeps live's value."
            >
              {flagged.map((f) => (
                <Field key={f.key} label={<span className="font-mono">{f.key}</span>}>
                  <Input
                    value={env[f.key] ?? ""}
                    placeholder={f.value || "(empty on live)"}
                    onChange={(e) => setEnv((m) => ({ ...m, [f.key]: e.target.value }))}
                    spellCheck={false}
                    autoComplete="off"
                    className="font-mono"
                  />
                </Field>
              ))}
            </Section>
          )}

          <div className="flex flex-col gap-3.5">
            <Switch
              checked={copy}
              disabled={p.data.length === 0}
              onChange={setCopyData}
              label="Copy live's data"
              description={
                p.data.length === 0
                  ? `${live} has no volumes or databases to copy.`
                  : `${formatBytes(dataBytes)} from ${plural(p.data.length, "source")}${free !== null ? `, ${formatBytes(free)} free on the server` : ""}. ${live} keeps running; databases are copied from a dump.`
              }
            />
            {copy && p.data.length > 0 && (
              <ul className="ml-12 flex flex-col gap-1 text-xs text-fg3">
                {p.data.map((d) => (
                  <li key={`${d.kind}:${d.name}`} className="flex gap-2">
                    <span className="w-28 flex-none">{dataKind[d.kind]}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-fg2">
                      {d.kind === "files" ? "" : d.service ? `${d.service}: ${d.name}` : d.name}
                    </span>
                    <span className="tabular-nums">{d.known ? formatBytes(d.bytes) : "size unknown"}</span>
                  </li>
                ))}
              </ul>
            )}
            <Switch
              checked={autoPromote}
              onChange={setAutoPromote}
              label="Promote automatically"
              description={`After each staging deployment succeeds, ${live} deploys the same commit and images. The first one is never promoted.`}
            />
          </div>

          {p.warnings.map((w, i) => (
            <Callout key={`w${i}`} tone="warn" title={w.service ? `${w.service}: ${w.message}` : w.message}>
              {w.fix}
            </Callout>
          ))}

          {flagged.length > 0 && (
            <Switch
              checked={acknowledged}
              onChange={setAcknowledged}
              label={`I have checked the ${plural(flagged.length, "flagged value")}`}
              description="Any I left empty will be the same on staging as on live."
            />
          )}

          <FormError error={create.error} fallback="Could not create the staging" />
        </div>
      )}
    </Modal>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2.5">
      <div>
        <div className="text-xs font-semibold text-fg2">{title}</div>
        {hint && <p className="mt-0.5 text-xs text-fg3">{hint}</p>}
      </div>
      {children}
    </div>
  );
}
