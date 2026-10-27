import { useState } from "react";
import { useNavigate } from "react-router";
import { useMe } from "../api/auth";
import { useProject } from "../api/projects";
import { useRecreateStaging, type CreateResult, type StagingStatus } from "../api/staging";
import { Modal } from "../ui/Modal";
import { Button } from "../ui/Button";
import { Field, Input, Switch } from "../ui/Field";
import { Callout, FormError } from "../ui/Page";
import { useToast } from "../ui/Toast";
import StagingCreatedDialog from "./StagingCreatedDialog";

export default function RecreateStagingDialog({ st, onClose }: { st: StagingStatus; onClose: () => void }) {
  const me = useMe();
  const live = useProject(st.live).data;
  const recreate = useRecreateStaging(st.staging);
  const navigate = useNavigate();
  const toast = useToast();
  const [password, setPassword] = useState("");
  const [typed, setTyped] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);
  const [created, setCreated] = useState<CreateResult | null>(null);
  const needsPassword = me.data?.has_password ?? true;
  const flagged = st.recreate_flagged_keys ?? [];
  const ready = typed === st.staging && acknowledged && (!needsPassword || password.length > 0);

  if (created) return <StagingCreatedDialog created={created} repo={live?.git_repo} onClose={onClose} />;

  return (
    <Modal
      onClose={onClose}
      tone="danger"
      title={`Recreate ${st.staging}`}
      description={
        <>
          The first data copy was interrupted. This deletes staging's containers, directory and volumes, then starts again
          from {st.live}'s current files and data. It keeps staging's branch, domains, environment values and auto-promote
          setting. Changes to staging's other files are lost. {st.live} keeps running.
        </>
      }
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button type="submit" form="recreate-staging" variant="dangerSolid" disabled={!ready} loading={recreate.isPending}>
            {recreate.isPending ? "Recreating…" : "Recreate staging"}
          </Button>
        </>
      }
    >
      <form id="recreate-staging" className="flex flex-col gap-3.5" onSubmit={(e) => {
        e.preventDefault();
        if (!ready || recreate.isPending) return;
        recreate.mutate({ password: needsPassword ? password : undefined, acknowledged }, {
          onSuccess: (res) => {
            if (res.result.webhook_secret && !res.result.webhook_registered) {
              setCreated(res);
              return;
            }
            toast.info(`Recreated ${res.project.name}`, `Deployment #${res.result.deployment} started.`);
            navigate(`/projects/${res.project.name}/deployments?d=${res.result.deployment}`);
          },
        });
      }}>
        <p className="text-sm text-fg2">The usual creation checks run again after deletion. If they fail, staging stays deleted.</p>
        <Field label={<>Type <span className="font-mono">{st.staging}</span> to confirm</>}>
          <Input autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
        {needsPassword && (
          <Field label="Your password">
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
          </Field>
        )}
        {flagged.length > 0 && (
          <Callout tone="warn" title="Values that may reach real people or services">
            <p>Check these in staging's Environment tab before reusing them.</p>
            <ul className="mt-1 list-inside list-disc">
              {flagged.map((key) => <li key={key}><code className="break-all font-mono">{key}</code></li>)}
            </ul>
          </Callout>
        )}
        <Switch checked={acknowledged} onChange={setAcknowledged} label="Reuse staging's current environment values"
          description="I have checked any values that can reach real people or external services." />
        <FormError error={recreate.error} fallback="Could not recreate staging" />
      </form>
    </Modal>
  );
}
