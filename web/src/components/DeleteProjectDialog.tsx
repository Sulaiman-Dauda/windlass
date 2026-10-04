import { useState } from "react";
import { useMe } from "../api/auth";
import { useDeleteProject } from "../api/projects";
import { Modal } from "../ui/Modal";
import { Button } from "../ui/Button";
import { Input, Field } from "../ui/Field";
import { FormError } from "../ui/Page";

export default function DeleteProjectDialog({
  name,
  onClose,
  onDeleted,
}: {
  name: string;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const me = useMe();
  const del = useDeleteProject();
  const [typed, setTyped] = useState("");
  const [password, setPassword] = useState("");

  const needsPassword = me.data?.has_password ?? true;
  const ready = typed === name && (!needsPassword || password.length > 0);

  return (
    <Modal
      onClose={onClose}
      tone="danger"
      title={`Delete ${name}`}
      description="This stops the project's containers and permanently removes its directory, including Compose files and environment values. It can't be undone."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="delete-project" variant="dangerSolid" disabled={!ready} loading={del.isPending}>
            {del.isPending ? "Deleting…" : "Delete project"}
          </Button>
        </>
      }
    >
      <form
        id="delete-project"
        className="flex flex-col gap-3.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (!ready || del.isPending) return;
          del.mutate({ name, password: needsPassword ? password : undefined }, { onSuccess: onDeleted });
        }}
      >
        <Field
          label={
            <>
              Type <span className="font-mono text-fg">{name}</span> to confirm
            </>
          }
        >
          <Input
            autoFocus
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={name}
            spellCheck={false}
            autoComplete="off"
            className="font-mono"
          />
        </Field>

        {needsPassword && (
          <Field label="Your password">
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
          </Field>
        )}

        <FormError error={del.error} fallback="Delete failed" />
      </form>
    </Modal>
  );
}
