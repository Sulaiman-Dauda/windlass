import { useNavigate } from "react-router";
import type { CreateResult } from "../api/staging";
import { Modal } from "../ui/Modal";
import { Button } from "../ui/Button";
import { Field } from "../ui/Field";
import { CopyField } from "../ui/Copy";

/** A replacement also rotates the webhook secret, which must be shown once. */
export default function StagingCreatedDialog({ created, repo, onClose }: {
  created: CreateResult;
  repo?: string;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const name = created.project.name;
  const provider = /gitlab/i.test(repo ?? "") ? "gitlab" : "github";
  return (
    <Modal
      onClose={onClose}
      width={560}
      title={`Created ${name}`}
      description="Windlass could not add the push webhook to the repository, so add it there by hand. Until then, pushes do not deploy to staging. The secret is shown once."
      footer={
        <Button variant="primary" onClick={() => navigate(`/projects/${name}/deployments?d=${created.result.deployment}`)}>
          Open deployment #{created.result.deployment}
        </Button>
      }
    >
      <div className="flex flex-col gap-3.5">
        <Field as="div" label="Payload URL">
          <CopyField value={`${window.location.origin}/api/v1/webhooks/${provider}/${name}`} />
        </Field>
        <Field as="div" label="Secret" hint="Content type application/json, push events only.">
          <CopyField value={created.result.webhook_secret!} />
        </Field>
        {provider === "github" && (
          <p className="text-xs text-fg3">With the Windlass GitHub App installed on the repository, pushes reach staging without this webhook.</p>
        )}
      </div>
    </Modal>
  );
}
