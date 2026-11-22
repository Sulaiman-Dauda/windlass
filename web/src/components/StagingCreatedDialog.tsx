import { useNavigate } from "react-router";
import type { CreateResult } from "../api/staging";
import { Modal } from "../ui/Modal";
import { Button } from "../ui/Button";
import { Field } from "../ui/Field";
import { CopyField } from "../ui/Copy";

/**
 * Shows what is shown once: staging's password, and the webhook secret when
 * Windlass could not add the webhook (a replacement also rotates it).
 */
export default function StagingCreatedDialog({ created, repo, onClose }: {
  created: CreateResult;
  repo?: string;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const name = created.project.name;
  const provider = /gitlab/i.test(repo ?? "") ? "gitlab" : "github";
  const { password, password_user, webhook_secret, webhook_registered } = created.result;
  const webhook = !!webhook_secret && !webhook_registered;
  const said = [
    password && "Staging's domains ask for this user name and password.",
    webhook && "Windlass could not add the push webhook to the repository, so add it there by hand. Until then, pushes do not deploy to staging.",
  ].filter(Boolean);
  return (
    <Modal
      onClose={onClose}
      width={560}
      title={`Created ${name}`}
      description={`${said.join(" ")} ${webhook && password ? "Both are" : password ? "The password is" : "The secret is"} shown once.`}
      footer={
        <Button variant="primary" onClick={() => navigate(`/projects/${name}/deployments?d=${created.result.deployment}`)}>
          Open deployment #{created.result.deployment}
        </Button>
      }
    >
      <div className="flex flex-col gap-3.5">
        {password && (
          <>
            <Field as="div" label="User name">
              <CopyField value={password_user ?? "staging"} />
            </Field>
            <Field as="div" label="Password" hint="Windlass keeps only a hash of it. To change it, use New password on the staging card.">
              <CopyField value={password} />
            </Field>
          </>
        )}
        {webhook && (
          <>
            <Field as="div" label="Payload URL">
              <CopyField value={`${window.location.origin}/api/v1/webhooks/${provider}/${name}`} />
            </Field>
            <Field as="div" label="Secret" hint="Content type application/json, push events only.">
              <CopyField value={webhook_secret} />
            </Field>
            {provider === "github" && (
              <p className="text-xs text-fg3">With the Windlass GitHub App installed on the repository, pushes reach staging without this webhook.</p>
            )}
          </>
        )}
      </div>
    </Modal>
  );
}
